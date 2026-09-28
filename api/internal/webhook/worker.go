package webhook

// The webhook ingestion worker loop, a lean twin of cmd/exportworker's
// (export/worker.go). RunWorker is the standalone cmd/webhookworker's whole
// body: claim deliveries off ops.webhook_deliveries with FOR UPDATE SKIP LOCKED,
// apply up to Concurrency of them at once, and heartbeat each lease so a
// delivery that legitimately takes a moment is never mistaken for a dead worker
// and reclaimed underneath itself. It lives in the webhook package (not cmd/) so
// it can drive the unexported queue + apply primitives directly.

import (
	"context"
	"log"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/uptrace/bun"
)

const (
	defaultConcurrency = 2

	// defaultLease is short on purpose: a crashed worker's deliveries become
	// reclaimable this soon after its last heartbeat. It must comfortably exceed
	// the heartbeat interval (a delivery heartbeats at lease/3) so a healthy but
	// busy worker never lets its own lease lapse.
	defaultLease = 60 * time.Second

	// defaultPoll is how long the loop waits before re-checking an empty queue.
	defaultPoll = 2 * time.Second

	// defaultMaxAttempts caps transient retries before a delivery is dead-lettered
	// for an admin to inspect or replay. A permanent error (bad payload, rejected
	// credential) dead-letters immediately regardless of this.
	defaultMaxAttempts = 5
)

// WorkerConfig tunes RunWorker. ID identifies this worker in worker_id (for
// debugging and to scope its own heartbeats).
type WorkerConfig struct {
	ID          string
	Concurrency int
	Lease       time.Duration
	Poll        time.Duration
	MaxAttempts int
}

// WorkerConfigFromEnv reads WEBHOOK_WORKER_CONCURRENCY, WEBHOOK_DELIVERY_LEASE,
// and WEBHOOK_MAX_ATTEMPTS, falling back to sane defaults, and derives a
// stable-per-process worker id (host + pid) so two workers on one host don't
// collide.
func WorkerConfigFromEnv() WorkerConfig {
	cfg := WorkerConfig{
		Concurrency: defaultConcurrency,
		Lease:       defaultLease,
		Poll:        defaultPoll,
		MaxAttempts: defaultMaxAttempts,
	}
	if v := os.Getenv("WEBHOOK_WORKER_CONCURRENCY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Concurrency = n
		} else {
			log.Printf("webhook worker: ignoring invalid WEBHOOK_WORKER_CONCURRENCY %q", v)
		}
	}
	if v := os.Getenv("WEBHOOK_DELIVERY_LEASE"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.Lease = d
		} else {
			log.Printf("webhook worker: ignoring invalid WEBHOOK_DELIVERY_LEASE %q", v)
		}
	}
	if v := os.Getenv("WEBHOOK_MAX_ATTEMPTS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.MaxAttempts = n
		} else {
			log.Printf("webhook worker: ignoring invalid WEBHOOK_MAX_ATTEMPTS %q", v)
		}
	}

	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "worker"
	}
	cfg.ID = host + "-" + strconv.Itoa(os.Getpid())
	return cfg
}

// RunWorker drives the claim loop until ctx is cancelled, then drains the
// deliveries already in flight before returning. A slot is acquired before each
// claim, so the worker never holds more claimed deliveries than it can run. It
// returns ctx.Err() (context.Canceled on a clean shutdown).
func RunWorker(ctx context.Context, db *bun.DB, cfg WorkerConfig) error {
	log.Printf("webhook worker %s started (concurrency=%d, lease=%s)", cfg.ID, cfg.Concurrency, cfg.Lease)

	sem := make(chan struct{}, cfg.Concurrency)
	var wg sync.WaitGroup

	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			return ctx.Err()
		case sem <- struct{}{}:
		}

		d, err := claimDelivery(ctx, db, cfg.ID, cfg.Lease)
		if err != nil {
			if ctx.Err() != nil { // shutdown races the claim -- expected
				<-sem
				wg.Wait()
				return ctx.Err()
			}
			log.Printf("webhook worker: claim failed: %v", err)
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
		go func(dv *Delivery) {
			defer wg.Done()
			defer func() { <-sem }()
			runDeliveryWithHeartbeat(ctx, db, cfg, dv)
		}(d)
	}
}

// runDeliveryWithHeartbeat applies one claimed delivery while a background ticker
// keeps its lease fresh. A heartbeat that finds the delivery no longer ours
// cancels the apply context so an in-flight transaction aborts rather than
// racing another worker.
func runDeliveryWithHeartbeat(parent context.Context, db *bun.DB, cfg WorkerConfig, d *Delivery) {
	dCtx, cancel := context.WithCancel(parent)
	defer cancel()

	interval := cfg.Lease / 3
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
				ok, err := heartbeatDelivery(dCtx, db, d.ID, cfg.ID, cfg.Lease)
				if err != nil {
					if dCtx.Err() != nil {
						return // the delivery finished; this beat lost the race
					}
					log.Printf("webhook worker: heartbeat for delivery %s failed: %v", d.ID, err)
					continue
				}
				if !ok {
					log.Printf("webhook worker: lost lease on delivery %s -- aborting", d.ID)
					cancel()
					return
				}
			}
		}
	}()

	runClaimedDelivery(dCtx, db, cfg, d)
	cancel()
	hb.Wait()
}

// runClaimedDelivery applies one delivery and records its terminal state. A
// permanent error (bad payload, rejected credential, no adapter) dead-letters
// immediately -- retrying it would only fail the same way -- while a transient
// error requeues until MaxAttempts.
func runClaimedDelivery(ctx context.Context, db *bun.DB, cfg WorkerConfig, d *Delivery) {
	results, err := applyDelivery(ctx, db, d)
	if err != nil {
		max := cfg.MaxAttempts
		if permanent(err) {
			max = 0 // attempts is already >= 1, so failDelivery marks it 'dead'
		}
		if ferr := failDelivery(ctx, db, d.ID, err.Error(), max); ferr != nil {
			log.Printf("webhook worker: recording failure for delivery %s: %v", d.ID, ferr)
		}
		return
	}
	if cerr := completeDelivery(ctx, db, d.ID, resultJSON(results)); cerr != nil {
		log.Printf("webhook worker: recording success for delivery %s: %v", d.ID, cerr)
	}
}

// permanent reports whether an apply error will never succeed on retry, so the
// worker should dead-letter rather than requeue.
func permanent(err error) bool {
	return isValidationError(err) || isAuthError(err)
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
