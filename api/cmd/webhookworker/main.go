// Command webhookworker runs VoltEdge's webhook workers (0124/0134,
// internal/webhook + internal/webhookout) as their own process, separate from
// the API, so applying ingested changes and delivering outbound events never
// compete with request handling. Two loops share this process:
//
//   - INBOUND: the public receiver authenticates + enqueues deliveries into
//     ops.webhook_deliveries; this worker applies each through the crud path as
//     the endpoint's service user (internal/webhook).
//   - OUTBOUND: the OMS/WAM/Faults write paths capture events into the
//     ops.webhook_outbound_events outbox; this worker fans each out to its
//     subscriptions and POSTs the signed envelope to each subscriber
//     (internal/webhookout).
//
// Both are stateless and horizontally scalable -- run as many as you like against
// the same queues; FOR UPDATE SKIP LOCKED hands each process distinct work and
// per-delivery lease heartbeats let a survivor reclaim a crashed worker's.
//
// Config (all via env, like the server): DATABASE_URL, the optional
// EXTERNAL_SYSTEMS_ENCRYPTION_KEY (the OUTBOUND worker unseals each subscription's
// HMAC signing secret with it; a missing key makes outbound deliveries
// dead-letter with a clear error), the inbound WEBHOOK_WORKER_CONCURRENCY /
// WEBHOOK_DELIVERY_LEASE / WEBHOOK_MAX_ATTEMPTS, and the outbound
// WEBHOOK_OUT_CONCURRENCY / WEBHOOK_OUT_LEASE / WEBHOOK_OUT_MAX_ATTEMPTS.
package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"

	"github.com/kwaabs/voltedge/api/internal/logx"
	"github.com/kwaabs/voltedge/api/internal/secretbox"
	"github.com/kwaabs/voltedge/api/internal/webhook"
	"github.com/kwaabs/voltedge/api/internal/webhookout"
)

func main() {
	// Install the structured (JSON) handler as slog.Default so both worker loops'
	// log lines are machine-parseable like the API's.
	logx.Init()

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is not set")
	}

	// The outbound worker signs each delivery with the subscription's secret,
	// sealed under the same key as the external-systems credentials. A malformed
	// key is a misconfiguration worth failing boot on; a missing key is allowed
	// (outbound deliveries then dead-letter with a clear "signing unavailable").
	cipher, err := secretbox.NewFromEnv()
	if err != nil {
		log.Fatalf("webhook worker: %v", err)
	}

	sqldb := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(databaseURL)))
	db := bun.NewDB(sqldb, pgdialect.New())
	defer db.Close()

	// SIGINT/SIGTERM cancels both loops; each RunWorker drains its in-flight work
	// before returning, so a rolling restart doesn't strand a running apply/send.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		if err := webhook.RunWorker(ctx, db, webhook.WorkerConfigFromEnv()); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("inbound webhook worker: %v", err)
			stop() // bring the whole process down so the orchestrator restarts it
		}
	}()
	go func() {
		defer wg.Done()
		if err := webhookout.RunWorker(ctx, db, cipher, webhookout.WorkerConfigFromEnv()); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("outbound webhook worker: %v", err)
			stop()
		}
	}()

	wg.Wait()
	log.Print("webhook workers: shut down cleanly")
}
