package importer

// The import worker loop (S4, docs/etl-import-engine.md), the mirror of
// api/internal/export/worker.go. RunImportWorker is the whole body of the
// standalone cmd/importworker: claim jobs off ops.import_jobs with FOR UPDATE
// SKIP LOCKED, run up to Concurrency of them at once, and heartbeat each one's
// lease so a job that legitimately takes a while (a large artifact) is never
// mistaken for a dead worker and reclaimed underneath itself. It lives in the
// importer package (not cmd/) so it drives the unexported queue + load
// primitives directly; the command is just wiring.

import (
	"context"
	"log"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/export"
	"github.com/kwaabs/voltedge/api/internal/secretbox"
)

const (
	defaultConcurrency = 2

	// defaultLease is short on purpose: a crashed worker's jobs become
	// reclaimable this soon after its last heartbeat. It must comfortably exceed
	// the heartbeat interval (a job heartbeats at lease/3) so a healthy but busy
	// worker never lets its own lease lapse.
	defaultLease = 60 * time.Second

	// defaultPoll is how long the loop waits before re-checking an empty queue.
	defaultPoll = 2 * time.Second
)

// WorkerConfig tunes RunImportWorker. ID identifies this worker in worker_id;
// the rest bound concurrency and the lease/poll timing. Cipher unseals a live-DB
// profile's stored password (v2-S3); it is nil when credential encryption is not
// configured, in which case a job whose profile carries a sealed password fails
// with a clear error rather than connecting without auth.
type WorkerConfig struct {
	ID          string
	Concurrency int
	Lease       time.Duration
	Poll        time.Duration
	Cipher      *secretbox.Cipher
}

// WorkerConfigFromEnv reads IMPORT_WORKER_CONCURRENCY and IMPORT_JOB_LEASE,
// falling back to sane defaults, and derives a stable-per-process worker id
// (host + pid) so two workers on one host don't collide.
func WorkerConfigFromEnv() WorkerConfig {
	cfg := WorkerConfig{
		Concurrency: defaultConcurrency,
		Lease:       defaultLease,
		Poll:        defaultPoll,
	}
	if v := os.Getenv("IMPORT_WORKER_CONCURRENCY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Concurrency = n
		} else {
			log.Printf("import worker: ignoring invalid IMPORT_WORKER_CONCURRENCY %q", v)
		}
	}
	if v := os.Getenv("IMPORT_JOB_LEASE"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.Lease = d
		} else {
			log.Printf("import worker: ignoring invalid IMPORT_JOB_LEASE %q", v)
		}
	}

	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "worker"
	}
	cfg.ID = host + "-" + strconv.Itoa(os.Getpid())
	return cfg
}

// RunImportWorker drives the claim loop until ctx is cancelled, then drains the
// jobs already in flight before returning. A slot is acquired before each claim,
// so the worker never holds more claimed jobs than it can run. It returns
// ctx.Err() (context.Canceled on a clean shutdown).
func RunImportWorker(ctx context.Context, db *bun.DB, store *export.ObjectStore, cfg WorkerConfig) error {
	log.Printf("import worker %s started (concurrency=%d, lease=%s)", cfg.ID, cfg.Concurrency, cfg.Lease)

	sem := make(chan struct{}, cfg.Concurrency)
	var wg sync.WaitGroup

	for {
		// Acquire a slot first; block here (not on a claimed job) when full.
		select {
		case <-ctx.Done():
			wg.Wait()
			return ctx.Err()
		case sem <- struct{}{}:
		}

		job, err := claimImport(ctx, db, cfg.ID, cfg.Lease)
		if err != nil {
			if ctx.Err() != nil { // shutdown races the claim -- expected, not an error
				<-sem
				wg.Wait()
				return ctx.Err()
			}
			log.Printf("import worker: claim failed: %v", err)
			<-sem
			sleep(ctx, cfg.Poll)
			continue
		}
		if job == nil { // empty queue
			<-sem
			sleep(ctx, cfg.Poll)
			continue
		}

		wg.Add(1)
		go func(j *ImportJob) {
			defer wg.Done()
			defer func() { <-sem }()
			runJobWithHeartbeat(ctx, db, store, cfg, j)
		}(job)
	}
}

// runJobWithHeartbeat runs one claimed job while a background ticker keeps its
// lease fresh. If a heartbeat finds the job no longer ours (reclaimed after a
// missed beat, or cancelled), it cancels the job's context so the in-flight load
// aborts promptly instead of racing another worker to apply the same rows.
func runJobWithHeartbeat(parent context.Context, db *bun.DB, store *export.ObjectStore, cfg WorkerConfig, job *ImportJob) {
	jobCtx, cancel := context.WithCancel(parent)
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
			case <-jobCtx.Done():
				return
			case <-t.C:
				ok, err := heartbeatImport(jobCtx, db, job.ID, cfg.ID, cfg.Lease)
				if err != nil {
					if jobCtx.Err() != nil {
						return // the job finished; this beat just lost the race
					}
					log.Printf("import worker: heartbeat for job %s failed: %v", job.ID, err)
					continue
				}
				if !ok {
					log.Printf("import worker: lost lease on job %s -- aborting", job.ID)
					cancel()
					return
				}
			}
		}
	}()

	runClaimedImport(jobCtx, db, store, cfg.Cipher, job)
	cancel()  // stop the heartbeat now the job is done (and its status changed)
	hb.Wait() // don't leak the goroutine past this job
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
