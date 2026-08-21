package export

// The export worker loop (Phase 17 Slice 3b, docs/phase-17-large-export.md).
//
// RunWorker is the standalone cmd/exportworker's whole body: claim jobs off the
// ops.export_jobs queue with FOR UPDATE SKIP LOCKED, run up to Concurrency of
// them at once, and heartbeat each one's lease so a job that legitimately takes
// minutes is never mistaken for a dead worker and reclaimed underneath itself.
// It lives in the export package (not cmd/) so it can drive the unexported
// queue + execution primitives directly; the command is just wiring.

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

	// defaultLease is short on purpose: a crashed worker's jobs become
	// reclaimable this soon after its last heartbeat. It must comfortably exceed
	// the heartbeat interval (a job heartbeats at lease/3) so a healthy but busy
	// worker never lets its own lease lapse.
	defaultLease = 60 * time.Second

	// defaultPoll is how long the loop waits before re-checking an empty queue.
	defaultPoll = 2 * time.Second
)

// WorkerConfig tunes RunWorker. ID identifies this worker in worker_id (for
// debugging and to scope its own heartbeats); the rest bound concurrency, the
// lease/poll timing, and the retention sweep (Slice 6).
type WorkerConfig struct {
	ID          string
	Concurrency int
	Lease       time.Duration
	Poll        time.Duration
	// Retention is how long a finished export's row + artifact are kept before
	// the sweep reaps them; 0 disables the sweep. SweepInterval is how often it
	// runs.
	Retention     time.Duration
	SweepInterval time.Duration
}

// WorkerConfigFromEnv reads EXPORT_WORKER_CONCURRENCY and EXPORT_JOB_LEASE,
// falling back to sane defaults, and derives a stable-per-process worker id
// (host + pid) so two workers on one host don't collide.
func WorkerConfigFromEnv() WorkerConfig {
	cfg := WorkerConfig{
		Concurrency:   defaultConcurrency,
		Lease:         defaultLease,
		Poll:          defaultPoll,
		Retention:     defaultRetention,
		SweepInterval: defaultSweepInterval,
	}
	if v := os.Getenv("EXPORT_WORKER_CONCURRENCY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Concurrency = n
		} else {
			log.Printf("export worker: ignoring invalid EXPORT_WORKER_CONCURRENCY %q", v)
		}
	}
	if v := os.Getenv("EXPORT_JOB_LEASE"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.Lease = d
		} else {
			log.Printf("export worker: ignoring invalid EXPORT_JOB_LEASE %q", v)
		}
	}
	// EXPORT_RETENTION accepts 0 to disable the sweep, so >= 0 is valid here
	// (unlike the strictly-positive durations above). Note time.ParseDuration has
	// no day unit -- a week is "168h", not "7d".
	if v := os.Getenv("EXPORT_RETENTION"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d >= 0 {
			cfg.Retention = d
		} else {
			log.Printf("export worker: ignoring invalid EXPORT_RETENTION %q", v)
		}
	}
	if v := os.Getenv("EXPORT_SWEEP_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.SweepInterval = d
		} else {
			log.Printf("export worker: ignoring invalid EXPORT_SWEEP_INTERVAL %q", v)
		}
	}

	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "worker"
	}
	cfg.ID = host + "-" + strconv.Itoa(os.Getpid())
	return cfg
}

// RunWorker drives the claim loop until ctx is cancelled, then drains the jobs
// already in flight before returning. A slot is acquired before each claim, so
// the worker never holds more claimed jobs than it can run -- a job it can't
// start immediately stays 'queued' for another worker rather than being locked
// idle. It returns ctx.Err() (context.Canceled on a clean shutdown).
func RunWorker(ctx context.Context, db *bun.DB, store *ObjectStore, cfg WorkerConfig) error {
	log.Printf("export worker %s started (concurrency=%d, lease=%s)", cfg.ID, cfg.Concurrency, cfg.Lease)

	// The retention sweep runs alongside the claim loop, sharing ctx so it stops
	// on the same shutdown signal. Disabled when Retention is 0.
	if cfg.Retention > 0 {
		log.Printf("export worker %s: retention sweep every %s, reaping exports older than %s",
			cfg.ID, cfg.SweepInterval, cfg.Retention)
		go runRetentionSweep(ctx, db, store, cfg.Retention, cfg.SweepInterval)
	}

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

		job, err := claimExport(ctx, db, cfg.ID, cfg.Lease)
		if err != nil {
			if ctx.Err() != nil { // shutdown races the claim -- expected, not an error
				<-sem
				wg.Wait()
				return ctx.Err()
			}
			log.Printf("export worker: claim failed: %v", err)
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
		go func(j *ExportJob) {
			defer wg.Done()
			defer func() { <-sem }()
			runJobWithHeartbeat(ctx, db, store, cfg, j)
		}(job)
	}
}

// runJobWithHeartbeat runs one claimed job while a background ticker keeps its
// lease fresh. If a heartbeat finds the job no longer ours (reclaimed after a
// missed beat, or cancelled), it cancels the job's context so the in-flight
// encode/upload aborts promptly instead of racing another worker to write the
// same object.
func runJobWithHeartbeat(parent context.Context, db *bun.DB, store *ObjectStore, cfg WorkerConfig, job *ExportJob) {
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
				ok, err := heartbeatExport(jobCtx, db, job.ID, cfg.ID, cfg.Lease)
				if err != nil {
					if jobCtx.Err() != nil {
						return // the job finished; this beat just lost the race
					}
					log.Printf("export worker: heartbeat for job %s failed: %v", job.ID, err)
					continue
				}
				if !ok {
					log.Printf("export worker: lost lease on job %s -- aborting", job.ID)
					cancel()
					return
				}
			}
		}
	}()

	runClaimedJob(jobCtx, db, store, job)
	cancel()  // stop the heartbeat now the job is done (and its status changed)
	hb.Wait() // don't leak the goroutine past this job
}
