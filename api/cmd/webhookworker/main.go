// Command webhookworker is the VoltEdge inbound-webhook worker (0124,
// internal/webhook). It runs as its own process, separate from the API, so
// applying a burst of ingested changes never competes with request handling: the
// public receiver only authenticates and enqueues deliveries into
// ops.webhook_deliveries, and this worker claims them (FOR UPDATE SKIP LOCKED),
// applies each through the normal crud path as the endpoint's service user, and
// marks it succeeded/failed/dead.
//
// It is stateless and horizontally scalable -- run as many as you like against
// the same queue; SKIP LOCKED hands each a distinct delivery and the per-delivery
// lease heartbeat lets a survivor reclaim a crashed worker's in-flight work.
//
// Config (all via env, like the server): DATABASE_URL, the optional
// EXTERNAL_SYSTEMS_ENCRYPTION_KEY (needed to verify HMAC secrets -- unused by the
// worker itself, which only applies already-authenticated deliveries), plus
// WEBHOOK_WORKER_CONCURRENCY / WEBHOOK_DELIVERY_LEASE / WEBHOOK_MAX_ATTEMPTS (see
// internal/webhook/worker.go for defaults).
package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/kwaabs/voltedge/api/internal/webhook"
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

	// SIGINT/SIGTERM cancels the loop; RunWorker drains its in-flight deliveries
	// before returning, so a rolling restart doesn't strand a running apply.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := webhook.RunWorker(ctx, db, webhook.WorkerConfigFromEnv()); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("webhook worker: %v", err)
	}
	log.Print("webhook worker: shut down cleanly")
}
