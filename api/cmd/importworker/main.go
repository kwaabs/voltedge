// Command importworker is the VoltEdge import worker (the admin ETL / import
// engine, docs/etl-import-engine.md). It runs as its own process, separate from
// the API, so streaming and applying a large uploaded artifact never competes
// with request handling for memory or CPU: the API only enqueues jobs into
// ops.import_jobs and hands back a job id to poll; this worker claims those jobs
// (FOR UPDATE SKIP LOCKED), streams the normalized NDJSON artifact out of object
// storage, and applies every row through the validated crud engine (insert or
// natural-key upsert), marking each run succeeded/failed.
//
// It is stateless and horizontally scalable -- run as many as you like against
// the same queue; SKIP LOCKED hands each a distinct job and the per-job lease
// heartbeat lets a survivor reclaim a crashed worker's in-flight jobs (which is
// safe: inserts use deterministic per-row ids and upserts re-match, so a re-run
// never duplicates rows).
//
// Config (all via env, like the server): DATABASE_URL and the RUSTFS_* block the
// storage layer reads, plus IMPORT_WORKER_CONCURRENCY and IMPORT_JOB_LEASE (see
// internal/importer/worker.go for defaults).
package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/kwaabs/voltedge/api/internal/export"
	"github.com/kwaabs/voltedge/api/internal/importer"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
)

func main() {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is not set")
	}

	sqldb := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(databaseURL)))
	db := bun.NewDB(sqldb, pgdialect.New())
	defer db.Close()

	// The import artifacts share the export object store (RustFS); the worker
	// reads them back to stream each run. An unreachable bucket at boot is fatal:
	// there is nothing else for the worker to do.
	store, err := export.NewObjectStoreFromEnv()
	if err != nil {
		log.Fatalf("import object store: %v", err)
	}
	if err := store.EnsureBucket(context.Background()); err != nil {
		log.Fatalf("import worker: %v", err)
	}

	// SIGINT/SIGTERM cancels the loop; RunImportWorker drains its in-flight jobs
	// before returning, so a rolling restart doesn't strand a running import.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := importer.RunImportWorker(ctx, db, store, importer.WorkerConfigFromEnv()); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("import worker: %v", err)
	}
	log.Print("import worker: shut down cleanly")
}
