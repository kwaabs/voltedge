package webhookout

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/uptrace/bun"
)

// OutboundDelivery is one row of ops.webhook_outbound_deliveries (migration 0134)
// -- one subscription's copy of one outbox event, delivered independently with
// its own retries. The outbound twin of the inbound Delivery.
type OutboundDelivery struct {
	bun.BaseModel `bun:"table:ops.webhook_outbound_deliveries,alias:webhook_outbound_deliveries"`

	ID             string          `bun:"id,pk,nullzero"`
	SubscriptionID string          `bun:"subscription_id"`
	EventID        string          `bun:"event_id"`
	EventSeq       int64           `bun:"event_seq"`
	EntityKind     string          `bun:"entity_kind"`
	EntityID       string          `bun:"entity_id"`
	EventType      string          `bun:"event_type"`
	Payload        json.RawMessage `bun:"payload,type:jsonb"`
	Status         string          `bun:"status"`
	Attempts       int             `bun:"attempts"`
	WorkerID       *string         `bun:"worker_id"`
	LeaseExpiresAt *time.Time      `bun:"lease_expires_at"`
	NextAttemptAt  time.Time       `bun:"next_attempt_at,nullzero"`
	ResponseCode   *int            `bun:"response_code"`
	Error          *string         `bun:"error"`
	CreatedAt      time.Time       `bun:"created_at,nullzero"`
	StartedAt      *time.Time      `bun:"started_at"`
	FinishedAt     *time.Time      `bun:"finished_at"`
}

// insertDelivery writes one queued delivery (called by the fan-out, inside the
// fan-out's transaction). The unique (event_id, subscription_id) index makes a
// re-run after a crash a no-op rather than a double-send: ON CONFLICT DO NOTHING
// returns no row, which the caller treats as "already fanned out".
func insertDelivery(ctx context.Context, db bun.IDB, d *OutboundDelivery) error {
	_, err := db.NewInsert().Model(d).
		On("CONFLICT (event_id, subscription_id) DO NOTHING").
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("webhookout: inserting delivery: %w", err)
	}
	return nil
}

// claimDelivery atomically claims the oldest due delivery for workerID -- the
// inbound FOR UPDATE SKIP LOCKED lease pattern, with two outbound additions:
//
//  1. Backoff gate: a 'queued' row is only claimable once next_attempt_at has
//     passed, so a backing-off retry waits its turn instead of spinning.
//  2. Per-entity ordering: a 'queued' row is held while any EARLIER-seq delivery
//     for the same (subscription, entity) is still in flight ('queued' or
//     'running'), so a subscriber sees one entity's events in order. The guard
//     self-heals -- an earlier delivery that succeeds OR dead-letters stops
//     blocking, so a permanently-failing event never wedges the entity forever.
//
// A lease-expired 'running' row is reclaimable only while attempts < maxAttempts
// (a hard-killed worker's in-flight delivery), exactly as inbound; past the cap
// the reaper dead-letters it. Returns (nil, nil) on an empty queue.
func claimDelivery(ctx context.Context, db bun.IDB, workerID string, lease time.Duration, maxAttempts int) (*OutboundDelivery, error) {
	const q = `
UPDATE ops.webhook_outbound_deliveries SET
    status = 'running',
    worker_id = ?,
    started_at = now(),
    lease_expires_at = now() + (? * interval '1 second'),
    attempts = attempts + 1
WHERE id = (
    SELECT d.id FROM ops.webhook_outbound_deliveries d
    WHERE (
        (d.status = 'queued' AND d.next_attempt_at <= now()
         AND NOT EXISTS (
             SELECT 1 FROM ops.webhook_outbound_deliveries b
             WHERE b.subscription_id = d.subscription_id
               AND b.entity_kind = d.entity_kind
               AND b.entity_id = d.entity_id
               AND b.event_seq < d.event_seq
               AND b.status IN ('queued', 'running')
         ))
        OR (d.status = 'running' AND d.lease_expires_at < now() AND d.attempts < ?)
    )
    ORDER BY d.event_seq
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
RETURNING *`

	d := new(OutboundDelivery)
	if err := db.NewRaw(q, workerID, int(lease.Seconds()), maxAttempts).Scan(ctx, d); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return d, nil
}

// heartbeatDelivery extends a running delivery's lease; false means it was
// reclaimed or moved out of 'running' underneath us and the worker should stop.
func heartbeatDelivery(ctx context.Context, db bun.IDB, id, workerID string, lease time.Duration) (bool, error) {
	res, err := db.NewUpdate().Model((*OutboundDelivery)(nil)).
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

// completeDelivery marks a claimed delivery succeeded with the subscriber's HTTP
// status. Guarded on status = 'running' so a row requeued/reclaimed out from
// under a late-finishing send isn't resurrected to 'succeeded'.
func completeDelivery(ctx context.Context, db bun.IDB, id string, responseCode int) error {
	_, err := db.NewUpdate().Model((*OutboundDelivery)(nil)).
		Set("status = 'succeeded'").
		Set("response_code = ?", responseCode).
		Set("error = NULL").
		Set("finished_at = now()").
		Set("lease_expires_at = NULL").
		Set("worker_id = NULL").
		Where("id = ?", id).
		Where("status = 'running'").
		Exec(ctx)
	return err
}

// failDelivery records a send failure. With attempts left it returns to 'queued'
// with next_attempt_at pushed backoff into the future (worker_id/lease cleared,
// no finished_at); once attempts reach maxAttempts it becomes 'dead' -- a
// dead-letter an admin can inspect or replay. responseCode is recorded when the
// subscriber answered (0 for a network/transport failure). Guarded on 'running'.
func failDelivery(ctx context.Context, db bun.IDB, id, msg string, responseCode, maxAttempts int, backoff time.Duration) error {
	const q = `
UPDATE ops.webhook_outbound_deliveries SET
    status = CASE WHEN attempts >= ? THEN 'dead' ELSE 'queued' END,
    error = ?,
    response_code = ?,
    worker_id = NULL,
    lease_expires_at = NULL,
    next_attempt_at = CASE WHEN attempts >= ? THEN next_attempt_at
                           ELSE now() + (? * interval '1 second') END,
    finished_at = CASE WHEN attempts >= ? THEN now() ELSE NULL END
WHERE id = ? AND status = 'running'`
	var code any
	if responseCode > 0 {
		code = responseCode
	}
	_, err := db.NewRaw(q, maxAttempts, msg, code, maxAttempts, int(backoff.Seconds()), maxAttempts, id).Exec(ctx)
	return err
}

// reapExhaustedDeliveries dead-letters deliveries left stuck 'running' past their
// lease with attempts already at the cap -- the terminal state for a delivery
// whose worker was killed before it could call failDelivery, which claimDelivery
// refuses to reclaim. Returns how many were reaped.
func reapExhaustedDeliveries(ctx context.Context, db bun.IDB, maxAttempts int) (int, error) {
	res, err := db.NewUpdate().Model((*OutboundDelivery)(nil)).
		Set("status = 'dead'").
		Set("error = ?", "dead-lettered: the worker delivering this stopped before it finished, and its retries are exhausted").
		Set("finished_at = now()").
		Set("worker_id = NULL").
		Set("lease_expires_at = NULL").
		Where("status = 'running'").
		Where("lease_expires_at < now()").
		Where("attempts >= ?", maxAttempts).
		Exec(ctx)
	if err != nil {
		return 0, fmt.Errorf("webhookout: reap stuck deliveries: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("webhookout: reap stuck deliveries: %w", err)
	}
	return int(n), nil
}
