// Command validationworker runs VoltEdge's incremental data-quality validation
// (api/internal/connectivity, sweep.go) as its own process, separate from the API so
// re-validating edits never competes with request handling. It tails
// gis.feature_history as the dirty-feature log: every feature write is re-validated
// (the scoped connectivity audit + the self-pruning issue queue), so a fixed problem
// leaves ops.validation_issues with zero human action, and operators triage a small
// live queue instead of auditing every change.
//
// Run a SINGLE instance (unlike the horizontally-scalable webhook workers): the sweep
// is a lightweight tail of one cursor, and re-processing a batch is idempotent, so no
// lease is needed. A periodic whole-DB reconcile (POST /connectivity/audit/reconcile
// with no body) is the backstop for anything a concurrent-commit gap skips.
//
// Config (all via env, like the server): DATABASE_URL, and the optional
// VALIDATION_SWEEP_INTERVAL (a Go duration, default 15s) / VALIDATION_SWEEP_BATCH (int,
// default 500).
package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"

	"github.com/kwaabs/voltedge/api/internal/connectivity"
	"github.com/kwaabs/voltedge/api/internal/logx"
)

func main() {
	// Structured (JSON) logging, like the API and the other workers.
	logx.Init()

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is not set")
	}

	sqldb := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(databaseURL)))
	db := bun.NewDB(sqldb, pgdialect.New())
	defer db.Close()

	// SIGINT/SIGTERM cancels the loop; RunWorker returns after finishing its current
	// sweep, so a rolling restart never strands work (the cursor just resumes).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := connectivity.RunWorker(ctx, db, connectivity.WorkerConfigFromEnv()); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("validation worker: %v", err)
	}
	log.Print("validation worker: shut down cleanly")
}
