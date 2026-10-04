// Command exportworker is the VoltEdge export worker (Phase 17,
// docs/phase-17-large-export.md). It runs as its own process, separate from the
// API, so a 10M-feature export never competes with request handling for memory
// or CPU: the API only enqueues jobs into ops.export_jobs and hands out
// download URLs; this worker claims those jobs (FOR UPDATE SKIP LOCKED), streams
// the encoder straight into RustFS, and marks each succeeded/failed.
//
// It is stateless and horizontally scalable -- run as many as you like against
// the same queue; SKIP LOCKED hands each a distinct job and the per-job lease
// heartbeat lets a survivor reclaim a crashed worker's in-flight jobs.
//
// Config (all via env, like the server): DATABASE_URL and the RUSTFS_* /
// EXPORT_URL_TTL block the storage layer reads, plus EXPORT_WORKER_CONCURRENCY
// and EXPORT_JOB_LEASE (see internal/export/worker.go for defaults).
package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/kwaabs/voltedge/api/internal/attachment"
	"github.com/kwaabs/voltedge/api/internal/export"
	"github.com/kwaabs/voltedge/api/internal/logx"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
)

func main() {
	// Install the structured (JSON) handler as slog.Default so the worker loop's
	// log lines (internal/export, internal/attachment) are machine-parseable like
	// the API's.
	logx.Init()

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is not set")
	}

	sqldb := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(databaseURL)))
	db := bun.NewDB(sqldb, pgdialect.New())
	defer db.Close()

	store, err := export.NewObjectStoreFromEnv()
	if err != nil {
		log.Fatalf("export object store: %v", err)
	}
	// Unlike the API (for which storage is one feature among many), a worker
	// exists only to produce artifacts, so an unreachable bucket at boot is
	// fatal: there is nothing else for it to do.
	if err := store.EnsureBucket(context.Background()); err != nil {
		log.Fatalf("export worker: %v", err)
	}

	// SIGINT/SIGTERM cancels the loop; RunWorker drains its in-flight jobs
	// before returning, so a rolling restart doesn't strand a running export.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The soft-deleted attachment sweep (Phase 22 Slice 6) rides this worker too:
	// attachments reuse the export bucket, so the same store reaps them. It shares
	// ctx (stops on the same shutdown signal). Disabled when retention is 0.
	if retention, interval := attachment.SweepConfigFromEnv(); retention > 0 {
		slog.Info("attachment sweep enabled", "worker", "attachment-sweep",
			"interval", interval.String(), "retention", retention.String())
		go attachment.RunDeletedSweep(ctx, db, store, retention, interval)
	}

	if err := export.RunWorker(ctx, db, store, export.WorkerConfigFromEnv()); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("export worker: %v", err)
	}
	log.Print("export worker: shut down cleanly")
}
