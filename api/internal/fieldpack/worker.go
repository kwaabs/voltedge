package fieldpack

// The pack-build worker loop (Phase 21 Slice 1, docs/phase-21-field-tool.md).
//
// A structural twin of export.RunWorker: claim jobs off ops.field_packs with FOR
// UPDATE SKIP LOCKED, run up to Concurrency at once, and heartbeat each one's
// lease so a build that legitimately takes a while is never mistaken for a dead
// worker and reclaimed underneath itself. It lives in the fieldpack package (not
// cmd/) so it can drive the unexported queue + build primitives directly; the
// command is just wiring. No retention sweep in this slice (the pack artifacts
// are kept until a later slice adds one, exactly as the export queue started).

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/kwaabs/voltedge/api/internal/export"
	"github.com/kwaabs/voltedge/api/internal/logx"
	"github.com/uptrace/bun"
)

const (
	defaultConcurrency = 2

	// defaultLease is short on purpose: a crashed worker's builds become
	// reclaimable this soon after its last heartbeat. It must comfortably exceed
	// the heartbeat interval (a build heartbeats at lease/3) so a healthy but busy
	// worker never lets its own lease lapse.
	defaultLease = 60 * time.Second

	// defaultPoll is how long the loop waits before re-checking an empty queue.
	defaultPoll = 2 * time.Second
)

// WorkerConfig tunes RunWorker. ID identifies this worker in worker_id (for
// debugging and to scope its own heartbeats); the rest bound concurrency and the
// lease/poll timing.
type WorkerConfig struct {
	ID          string
	Concurrency int
	Lease       time.Duration
	Poll        time.Duration
}

// WorkerConfigFromEnv reads FIELDSYNC_WORKER_CONCURRENCY and FIELDSYNC_JOB_LEASE,
// falling back to sane defaults, and derives a stable-per-process worker id (host
// + pid) so two workers on one host don't collide.
func WorkerConfigFromEnv() WorkerConfig {
	cfg := WorkerConfig{Concurrency: defaultConcurrency, Lease: defaultLease, Poll: defaultPoll}
	if v := os.Getenv("FIELDSYNC_WORKER_CONCURRENCY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Concurrency = n
		} else {
			slog.Warn("ignoring invalid env var", "worker", "fieldsync", "var", "FIELDSYNC_WORKER_CONCURRENCY", "value", v)
		}
	}
	if v := os.Getenv("FIELDSYNC_JOB_LEASE"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.Lease = d
		} else {
			slog.Warn("ignoring invalid env var", "worker", "fieldsync", "var", "FIELDSYNC_JOB_LEASE", "value", v)
		}
	}
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "fieldsync"
	}
	cfg.ID = host + "-" + strconv.Itoa(os.Getpid())
	return cfg
}

// RunWorker drives the claim loop until ctx is cancelled, then drains the builds
// already in flight before returning. A slot is acquired before each claim, so
// the worker never holds more claimed builds than it can run. It returns ctx.Err()
// (context.Canceled on a clean shutdown).
func RunWorker(ctx context.Context, db *bun.DB, store *export.ObjectStore, cfg WorkerConfig) error {
	lg := slog.Default().With(slog.String("worker", "fieldsync"), slog.String("worker_id", cfg.ID))
	ctx = logx.Into(ctx, lg)
	lg.Info("worker started", "concurrency", cfg.Concurrency, "lease", cfg.Lease.String())

	sem := make(chan struct{}, cfg.Concurrency)
	var wg sync.WaitGroup

	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			return ctx.Err()
		case sem <- struct{}{}:
		}

		pack, err := claimPack(ctx, db, cfg.ID, cfg.Lease)
		if err != nil {
			if ctx.Err() != nil { // shutdown races the claim -- expected, not an error
				<-sem
				wg.Wait()
				return ctx.Err()
			}
			lg.Error("claim failed", "err", err)
			<-sem
			sleep(ctx, cfg.Poll)
			continue
		}
		if pack == nil { // empty queue
			<-sem
			sleep(ctx, cfg.Poll)
			continue
		}

		wg.Add(1)
		go func(p *FieldPack) {
			defer wg.Done()
			defer func() { <-sem }()
			buildPackWithHeartbeat(ctx, db, store, cfg, p)
		}(pack)
	}
}

// buildPackWithHeartbeat builds one claimed pack while a background ticker keeps
// its lease fresh. If a heartbeat finds the pack no longer ours (reclaimed after
// a missed beat, or cancelled), it cancels the build's context so the in-flight
// read/tippecanoe/upload aborts promptly instead of racing another worker.
func buildPackWithHeartbeat(parent context.Context, db *bun.DB, store *export.ObjectStore, cfg WorkerConfig, pack *FieldPack) {
	pl := logx.From(parent).With(slog.String("pack_id", pack.ID))
	buildCtx, cancel := context.WithCancel(logx.Into(parent, pl))
	defer cancel()

	// Beat at a third of the lease: two beats can be missed before the lease
	// lapses, tolerating a transient DB hiccup without a false reclaim.
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
			case <-buildCtx.Done():
				return
			case <-t.C:
				ok, err := heartbeatPack(buildCtx, db, pack.ID, cfg.ID, cfg.Lease)
				if err != nil {
					if buildCtx.Err() != nil {
						return // the build finished; this beat just lost the race
					}
					pl.Error("heartbeat failed", "err", err)
					continue
				}
				if !ok {
					pl.Warn("lost lease, aborting build")
					cancel()
					return
				}
			}
		}
	}()

	buildPack(buildCtx, db, store, pack)
	cancel()  // stop the heartbeat now the build is done (and its status changed)
	hb.Wait() // don't leak the goroutine past this build
}

// sleep waits d, or returns early if ctx is cancelled.
func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
