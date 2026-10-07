// Command fieldsync is the VoltEdge field-tool engine (Phase 21,
// docs/phase-21-field-tool.md). It runs as its own process (its own
// FIELDSYNC_PORT), a sibling of server / exportworker / mcpserver in the same
// module, so building a region pack (async, CPU-heavy -- tippecanoe) never
// competes with API request handling.
//
// One process runs three jobs:
//   - a build WORKER (fieldpack.RunWorker) claims queued packs off ops.field_packs
//     FOR UPDATE SKIP LOCKED, builds the two artifacts (MBTiles + SQLite pack), and
//     uploads them to RustFS;
//   - a SYNC-APPLY WORKER (fieldpack.RunSyncWorker, Phase 23) drains ops.sync_inbox,
//     committing each captured device op to production through the shared engines; and
//   - an HTTP SURFACE (fieldpack.Routes) a device uses to enqueue a build, poll it,
//     fetch the manifest, and push/poll its offline edits -- every route behind an
//     iam.api_tokens bearer.
//
// Config (all via env, like the other binaries): DATABASE_URL and the RUSTFS_* /
// EXPORT_URL_TTL block the storage layer reads, plus FIELDSYNC_PORT,
// FIELDSYNC_WORKER_CONCURRENCY / FIELDSYNC_JOB_LEASE (see internal/fieldpack), and
// TIPPECANOE_BIN / FIELDPACK_MIN_ZOOM / FIELDPACK_MAX_ZOOM for the tile build.
package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kwaabs/voltedge/api/internal/export"
	"github.com/kwaabs/voltedge/api/internal/fieldpack"
	"github.com/kwaabs/voltedge/api/internal/logx"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
)

func main() {
	// Install the structured (JSON) handler as slog.Default so the build/sync
	// worker loops (internal/fieldpack) are machine-parseable like the API's.
	logx.Init()

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is not set")
	}

	sqldb := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(databaseURL)))
	db := bun.NewDB(sqldb, pgdialect.New())
	defer db.Close()

	// The pack artifacts (MBTiles + SQLite) share the RustFS store the export
	// path uses -- namespaced under packs/<id>/ so keys never collide.
	store, err := export.NewObjectStoreFromEnv()
	if err != nil {
		log.Fatalf("fieldsync object store: %v", err)
	}
	if err := store.EnsureBucket(context.Background()); err != nil {
		log.Fatalf("fieldsync: %v", err)
	}

	// SIGINT/SIGTERM cancels the worker loop (it drains in-flight builds) and
	// triggers a graceful HTTP shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		if err := fieldpack.RunWorker(ctx, db, store, fieldpack.WorkerConfigFromEnv()); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("fieldsync worker: %v", err)
		}
	}()

	// The sync-apply worker (Phase 23 Slice 1) drains ops.sync_inbox to production.
	// Co-located with POST /sync's capture in this process so a just-received op
	// applies moments later (worker-immediate mode); it drives its own queue off the
	// same DB and drains in-flight ops on shutdown, exactly like the build worker.
	syncWorkerDone := make(chan struct{})
	go func() {
		defer close(syncWorkerDone)
		if err := fieldpack.RunSyncWorker(ctx, db, fieldpack.SyncWorkerConfigFromEnv()); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("fieldsync sync worker: %v", err)
		}
	}()

	// The maintenance loop (shared packs S4 + S6): on a timer it rebuilds scheduled
	// cells inside the off-peak window and reaps old cell versions no crew still holds.
	// Self-skipping on an in-flight build, so running it in several fieldsync replicas
	// at worst double-enqueues a scheduled rebuild on a tick race -- harmless, since
	// retention reaps the redundant version.
	maintenanceDone := make(chan struct{})
	go func() {
		defer close(maintenanceDone)
		if err := fieldpack.RunMaintenance(ctx, db, store, fieldpack.MaintenanceConfigFromEnv()); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("fieldsync maintenance: %v", err)
		}
	}()

	port := os.Getenv("FIELDSYNC_PORT")
	if port == "" {
		port = "8091"
	}
	srv := &http.Server{Addr: ":" + port, Handler: fieldpack.Routes(db, store)}

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutCtx); err != nil {
			log.Printf("fieldsync: http shutdown: %v", err)
		}
	}()

	log.Printf("fieldsync listening on :%s", port)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("fieldsync: %v", err)
	}

	// Let both workers finish draining before exiting, so a rolling restart doesn't
	// strand a running build or a mid-apply sync op.
	<-workerDone
	<-syncWorkerDone
	<-maintenanceDone
	log.Print("fieldsync: shut down cleanly")
}
