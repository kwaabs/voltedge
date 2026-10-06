package webhook

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/uptrace/bun"
)

// enqueueDelivery inserts a queued delivery and returns its id. When an
// idempotency key is supplied and a delivery already exists for
// (endpoint, key), it returns that existing id with deduped=true and inserts
// nothing -- a retried push is a no-op the receiver answers 200 to. The unique
// partial index webhook_deliveries_idem_idx is the hard guarantee behind the
// pre-check (it also closes the check-then-insert race).
func enqueueDelivery(ctx context.Context, db bun.IDB, endpointID, source string, idempotencyKey *string, payload, headers json.RawMessage) (id string, deduped bool, err error) {
	if idempotencyKey != nil {
		var existing Delivery
		e := db.NewSelect().Model(&existing).
			Where("endpoint_id = ?", endpointID).
			Where("idempotency_key = ?", *idempotencyKey).
			Scan(ctx)
		if e == nil {
			return existing.ID, true, nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return "", false, fmt.Errorf("webhook: enqueue: idempotency check: %w", e)
		}
	}

	d := &Delivery{
		EndpointID:     endpointID,
		Status:         "queued",
		Source:         source,
		IdempotencyKey: idempotencyKey,
		Payload:        payload,
		Headers:        headers,
	}
	if _, e := db.NewInsert().Model(d).Returning("id").Exec(ctx); e != nil {
		// Lost the race on the unique index -- fetch the winner.
		if idempotencyKey != nil && strings.Contains(e.Error(), "webhook_deliveries_idem_idx") {
			var existing Delivery
			if e2 := db.NewSelect().Model(&existing).
				Where("endpoint_id = ?", endpointID).
				Where("idempotency_key = ?", *idempotencyKey).
				Scan(ctx); e2 == nil {
				return existing.ID, true, nil
			}
		}
		return "", false, fmt.Errorf("webhook: enqueue delivery: %w", e)
	}
	return d.ID, false, nil
}

// claimDelivery atomically claims the oldest queued (or lease-expired running)
// delivery for workerID -- the exact FOR UPDATE SKIP LOCKED lease pattern
// ops.export_jobs uses (job.go). Returns (nil, nil) on an empty queue.
//
// A lease-expired 'running' delivery is only reclaimed while its attempts are
// still below maxAttempts: without that guard, a delivery that hard-kills its
// worker (OOM/SIGKILL, so failDelivery never records the failure) would be
// reclaimed and re-applied forever. Once it hits the cap, claim leaves it alone
// and runStuckDeliveryReaper dead-letters it. A 'queued' delivery is always
// claimable -- failDelivery only requeues while attempts remain, else it goes
// 'dead' -- so the cap never strands a legitimately retryable delivery.
func claimDelivery(ctx context.Context, db bun.IDB, workerID string, lease time.Duration, maxAttempts int) (*Delivery, error) {
	const q = `
UPDATE ops.webhook_deliveries SET
    status = 'running',
    worker_id = ?,
    started_at = now(),
    lease_expires_at = now() + (? * interval '1 second'),
    attempts = attempts + 1
WHERE id = (
    SELECT id FROM ops.webhook_deliveries
    WHERE status = 'queued'
       OR (status = 'running' AND lease_expires_at < now() AND attempts < ?)
    ORDER BY created_at
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
RETURNING *`

	d := new(Delivery)
	if err := db.NewRaw(q, workerID, int(lease.Seconds()), maxAttempts).Scan(ctx, d); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return d, nil
}

// reapExhaustedDeliveries dead-letters deliveries left stuck 'running' past their
// lease with attempts already at the cap -- the terminal state for a delivery
// whose worker was killed (OOM/SIGKILL) before it could call failDelivery, which
// claimDelivery now refuses to reclaim. Returns how many were reaped. Run on a
// timer by runStuckDeliveryReaper; safe to run from every worker concurrently
// (each UPDATE only touches rows no one holds).
func reapExhaustedDeliveries(ctx context.Context, db bun.IDB, maxAttempts int) (int, error) {
	res, err := db.NewUpdate().Model((*Delivery)(nil)).
		Set("status = 'dead'").
		Set("error = ?", "dead-lettered: the worker applying this delivery stopped before it finished, and its retries are exhausted").
		Set("finished_at = now()").
		Set("worker_id = NULL").
		Set("lease_expires_at = NULL").
		Where("status = 'running'").
		Where("lease_expires_at < now()").
		Where("attempts >= ?", maxAttempts).
		Exec(ctx)
	if err != nil {
		return 0, fmt.Errorf("webhook: reap stuck deliveries: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("webhook: reap stuck deliveries: %w", err)
	}
	return int(n), nil
}

// heartbeatDelivery extends a running delivery's lease; false means it was
// reclaimed or moved out of 'running' underneath us and the worker should stop.
func heartbeatDelivery(ctx context.Context, db bun.IDB, id, workerID string, lease time.Duration) (bool, error) {
	res, err := db.NewUpdate().Model((*Delivery)(nil)).
		Set("lease_expires_at = now() + (? * interval '1 second')", int(lease.Seconds())).
		Where("id = ?", id).
		Where("worker_id = ?", workerID).
		Where("status = 'running'").
		Exec(ctx)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// completeDelivery marks a claimed delivery succeeded with its per-item result.
// Guarded on status = 'running' so a delivery that was requeued/reclaimed out
// from under a late-finishing apply isn't resurrected to 'succeeded'.
func completeDelivery(ctx context.Context, db bun.IDB, id string, result json.RawMessage) error {
	_, err := db.NewUpdate().Model((*Delivery)(nil)).
		Set("status = 'succeeded'").
		Set("result = ?", result).
		Set("error = NULL").
		Set("finished_at = now()").
		Set("lease_expires_at = NULL").
		Set("worker_id = NULL").
		Where("id = ?", id).
		Where("status = 'running'").
		Exec(ctx)
	return err
}

// failDelivery records an apply failure. If the delivery still has attempts left
// it goes back to 'queued' for another worker to retry (worker_id/lease
// cleared, no finished_at); once attempts reach maxAttempts it becomes 'dead' --
// a dead-letter held for an admin to inspect or replay. Guarded on
// status = 'running'.
func failDelivery(ctx context.Context, db bun.IDB, id, msg string, maxAttempts int) error {
	const q = `
UPDATE ops.webhook_deliveries SET
    status = CASE WHEN attempts >= ? THEN 'dead' ELSE 'queued' END,
    error = ?,
    worker_id = NULL,
    lease_expires_at = NULL,
    finished_at = CASE WHEN attempts >= ? THEN now() ELSE NULL END
WHERE id = ? AND status = 'running'`
	_, err := db.NewRaw(q, maxAttempts, msg, maxAttempts, id).Exec(ctx)
	return err
}

// getDelivery loads one delivery by id (admin/replay path -- endpoint scoping is
// applied by the handler that already resolved the endpoint).
func getDelivery(ctx context.Context, db bun.IDB, endpointID, id string) (*Delivery, error) {
	d := new(Delivery)
	err := db.NewSelect().Model(d).
		Where("id = ?", id).
		Where("endpoint_id = ?", endpointID).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound("delivery %q not found on this endpoint", id)
	}
	if err != nil {
		return nil, fmt.Errorf("webhook: load delivery: %w", err)
	}
	return d, nil
}

// listDeliveries returns an endpoint's most recent deliveries, newest first.
func listDeliveries(ctx context.Context, db bun.IDB, endpointID string, limit int) ([]Delivery, error) {
	var rows []Delivery
	err := db.NewSelect().Model(&rows).
		Where("endpoint_id = ?", endpointID).
		OrderExpr("created_at DESC").
		Limit(limit).
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("webhook: list deliveries: %w", err)
	}
	return rows, nil
}
