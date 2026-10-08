package webhookout

// The outbound delivery worker -- a lean twin of the inbound webhook worker
// (internal/webhook/worker.go). It runs two stages against the Slice-1 outbox:
//
//   1. FAN-OUT: turn each new ops.webhook_outbound_events row into one
//      ops.webhook_outbound_deliveries row per matching active subscription.
//   2. DELIVER: claim deliveries (FOR UPDATE SKIP LOCKED + lease), POST each
//      signed envelope through the SSRF-hardened client, retry with exponential
//      backoff, dead-letter past the cap.
//
// Both stages are SKIP-LOCKED and idempotent, so the worker is stateless and
// horizontally scalable -- run as many as you like against the same queue. It
// lives in the webhookout package (not cmd/) so it can drive the unexported
// fan-out + delivery primitives directly, and takes the secretbox cipher so it
// can unseal each subscription's HMAC signing secret.

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"runtime/debug"
	"strconv"
	"sync"
	"time"

	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/logx"
	"github.com/kwaabs/voltedge/api/internal/secretbox"
)

const (
	defaultConcurrency = 2
	defaultLease       = 60 * time.Second
	defaultPoll        = 2 * time.Second
	defaultMaxAttempts = 8

	// baseBackoff/maxBackoff bound the exponential retry schedule: a failed
	// delivery waits baseBackoff, then doubles each attempt, capped at maxBackoff,
	// so a flapping subscriber is retried patiently rather than hammered.
	baseBackoff = 10 * time.Second
	maxBackoff  = 1 * time.Hour
)

// WorkerConfig tunes RunWorker. ID identifies this worker in worker_id.
type WorkerConfig struct {
	ID          string
	Concurrency int
	Lease       time.Duration
	Poll        time.Duration
	MaxAttempts int
}

// WorkerConfigFromEnv reads WEBHOOK_OUT_CONCURRENCY, WEBHOOK_OUT_LEASE, and
// WEBHOOK_OUT_MAX_ATTEMPTS (distinct from the inbound worker's WEBHOOK_* vars so
// the two can be tuned independently when they share a process), falling back to
// sane defaults, and derives a stable-per-process worker id (host + pid).
func WorkerConfigFromEnv() WorkerConfig {
	cfg := WorkerConfig{
		Concurrency: defaultConcurrency,
		Lease:       defaultLease,
		Poll:        defaultPoll,
		MaxAttempts: defaultMaxAttempts,
	}
	if v := os.Getenv("WEBHOOK_OUT_CONCURRENCY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Concurrency = n
		} else {
			slog.Warn("ignoring invalid env var", "worker", "webhook-outbound", "var", "WEBHOOK_OUT_CONCURRENCY", "value", v)
		}
	}
	if v := os.Getenv("WEBHOOK_OUT_LEASE"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.Lease = d
		} else {
			slog.Warn("ignoring invalid env var", "worker", "webhook-outbound", "var", "WEBHOOK_OUT_LEASE", "value", v)
		}
	}
	if v := os.Getenv("WEBHOOK_OUT_MAX_ATTEMPTS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.MaxAttempts = n
		} else {
			slog.Warn("ignoring invalid env var", "worker", "webhook-outbound", "var", "WEBHOOK_OUT_MAX_ATTEMPTS", "value", v)
		}
	}

	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "worker"
	}
	cfg.ID = host + "-" + strconv.Itoa(os.Getpid())
	return cfg
}

// worker bundles the dependencies the delivery loop threads through its stages.
type worker struct {
	db     *bun.DB
	cipher *secretbox.Cipher
	cfg    WorkerConfig
}

// RunWorker drives the fan-out stage, the delivery claim loop, and the stuck-
// delivery reaper until ctx is cancelled, then drains in-flight deliveries before
// returning ctx.Err() (context.Canceled on a clean shutdown). cipher unseals each
// subscription's signing secret; a nil cipher means EXTERNAL_SYSTEMS_ENCRYPTION_KEY
// is unset, in which case every delivery dead-letters with a clear "signing
// unavailable" error rather than sending unsigned.
func RunWorker(ctx context.Context, db *bun.DB, cipher *secretbox.Cipher, cfg WorkerConfig) error {
	lg := slog.Default().With(slog.String("worker", "webhook-outbound"), slog.String("worker_id", cfg.ID))
	ctx = logx.Into(ctx, lg)
	lg.Info("worker started", "concurrency", cfg.Concurrency, "lease", cfg.Lease.String())

	w := &worker{db: db, cipher: cipher, cfg: cfg}

	sem := make(chan struct{}, cfg.Concurrency)
	var wg sync.WaitGroup

	// Fan-out stage: its own goroutine, draining the outbox into deliveries
	// independently of the claim loop.
	wg.Add(1)
	go func() {
		defer wg.Done()
		w.runFanOut(ctx)
	}()

	// Reaper: dead-letter deliveries a hard-killed worker left stuck 'running'
	// past their lease with attempts already at the cap.
	wg.Add(1)
	go func() {
		defer wg.Done()
		w.runReaper(ctx)
	}()

	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			return ctx.Err()
		case sem <- struct{}{}:
		}

		d, err := claimDelivery(ctx, db, cfg.ID, cfg.Lease, cfg.MaxAttempts)
		if err != nil {
			if ctx.Err() != nil {
				<-sem
				wg.Wait()
				return ctx.Err()
			}
			lg.Error("claim failed", "err", err)
			<-sem
			sleepCtx(ctx, cfg.Poll)
			continue
		}
		if d == nil { // empty queue
			<-sem
			sleepCtx(ctx, cfg.Poll)
			continue
		}

		wg.Add(1)
		go func(dv *OutboundDelivery) {
			defer wg.Done()
			defer func() { <-sem }()
			w.runDeliveryWithHeartbeat(ctx, dv)
		}(d)
	}
}

// runFanOut repeatedly turns one outbox event into its per-subscription
// deliveries, looping while there is work and sleeping Poll when the outbox is
// empty. It stops when ctx is cancelled.
func (w *worker) runFanOut(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		worked, err := fanOutOne(ctx, w.db)
		if err != nil {
			if ctx.Err() == nil {
				logx.From(ctx).Error("fan-out failed", "err", err)
			}
			sleepCtx(ctx, w.cfg.Poll)
			continue
		}
		if !worked {
			sleepCtx(ctx, w.cfg.Poll)
		}
	}
}

// runReaper periodically dead-letters deliveries stuck 'running' past their lease
// with attempts already at the cap -- the case failDelivery can't reach because
// the worker holding them was killed before it could record a failure.
func (w *worker) runReaper(ctx context.Context) {
	t := time.NewTicker(w.cfg.Lease)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n, err := reapExhaustedDeliveries(ctx, w.db, w.cfg.MaxAttempts)
			if err != nil {
				if ctx.Err() == nil {
					logx.From(ctx).Error("reaping stuck deliveries failed", "err", err)
				}
				continue
			}
			if n > 0 {
				logx.From(ctx).Warn("dead-lettered stuck deliveries", "count", n)
			}
		}
	}
}

// runDeliveryWithHeartbeat sends one claimed delivery while a ticker keeps its
// lease fresh; a heartbeat that finds the delivery no longer ours cancels the
// send so an in-flight request aborts rather than racing another worker.
func (w *worker) runDeliveryWithHeartbeat(parent context.Context, d *OutboundDelivery) {
	dl := logx.From(parent).With(slog.String("delivery_id", d.ID), slog.String("event_type", d.EventType))
	dCtx, cancel := context.WithCancel(logx.Into(parent, dl))
	defer cancel()

	interval := w.cfg.Lease / 3
	if interval <= 0 {
		interval = time.Second
	}

	var hb sync.WaitGroup
	hb.Add(1)
	go func() {
		defer hb.Done()
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-dCtx.Done():
				return
			case <-t.C:
				ok, err := heartbeatDelivery(dCtx, w.db, d.ID, w.cfg.ID, w.cfg.Lease)
				if err != nil {
					if dCtx.Err() != nil {
						return
					}
					dl.Error("heartbeat failed", "err", err)
					continue
				}
				if !ok {
					dl.Warn("lost lease, aborting delivery")
					cancel()
					return
				}
			}
		}
	}()

	// Recover a panic in the send path so one poison delivery fails just itself
	// instead of crashing the worker (which would halt all delivery and, on
	// restart, reclaim the stuck row and panic again -- a crash loop). The failure
	// is recorded on a non-cancel context so it survives dCtx teardown.
	func() {
		defer func() {
			if rec := recover(); rec != nil {
				dl.Error("delivery send panicked", "err", rec, "stack", string(debug.Stack()))
				if ferr := failDelivery(context.WithoutCancel(parent), w.db, d.ID,
					fmt.Sprintf("send panicked: %v", rec), 0, w.cfg.MaxAttempts, backoffFor(d.Attempts)); ferr != nil {
					dl.Error("recording panic failure failed", "err", ferr)
				}
			}
		}()
		w.runClaimedDelivery(dCtx, d)
	}()
	cancel()
	hb.Wait()
}

// runClaimedDelivery sends one delivery and records its terminal state. A
// permanent error (bad URL, no secret, a 4xx) dead-letters immediately; a
// transient one backs off and requeues until MaxAttempts.
func (w *worker) runClaimedDelivery(ctx context.Context, d *OutboundDelivery) {
	code, err := sendDelivery(ctx, w.db, w.cipher, d)
	if err != nil {
		max := w.cfg.MaxAttempts
		if isPermanent(err) {
			max = 0 // attempts is already >= 1, so failDelivery marks it 'dead'
		}
		if ferr := failDelivery(ctx, w.db, d.ID, err.Error(), code, max, backoffFor(d.Attempts)); ferr != nil {
			logx.From(ctx).Error("recording delivery failure failed", "err", ferr)
		}
		return
	}
	if cerr := completeDelivery(ctx, w.db, d.ID, code); cerr != nil {
		logx.From(ctx).Error("recording delivery success failed", "err", cerr)
	}
}

// backoffFor returns the retry delay after a failure on the given attempt count
// (attempts is already incremented at claim, so the first failure is attempt 1):
// baseBackoff doubling each attempt, capped at maxBackoff.
func backoffFor(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	d := baseBackoff
	for i := 1; i < attempts && d < maxBackoff; i++ {
		d *= 2
	}
	if d > maxBackoff {
		d = maxBackoff
	}
	return d
}

// sleepCtx waits d, or returns early when ctx is cancelled.
func sleepCtx(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
