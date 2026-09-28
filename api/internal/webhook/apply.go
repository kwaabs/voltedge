package webhook

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/uptrace/bun"
)

// applyDelivery is the heart of ingestion, shared by the async worker and the
// inline tester: load the delivery's endpoint, normalize the payload into
// changes, assume the endpoint's service-user identity, then apply every change
// atomically through the target adapter. The whole batch runs in ONE transaction
// -- a single bad change rolls back the lot, so a delivery never lands halfway.
//
// The returned []itemResult is the per-change outcome recorded on the delivery
// (and echoed to the tester). A returned error means nothing was committed.
func applyDelivery(ctx context.Context, db *bun.DB, d *Delivery) ([]itemResult, error) {
	ep := new(Endpoint)
	if err := db.NewSelect().Model(ep).Where("id = ?", d.EndpointID).Scan(ctx); err != nil {
		return nil, fmt.Errorf("webhook: apply: load endpoint %q: %w", d.EndpointID, err)
	}
	if !ep.Active {
		return nil, invalid("endpoint %q is inactive", ep.ID)
	}

	ad, ok := adapters[ep.Target]
	if !ok {
		// Every DB-allowed target (feature/reading/work/fault) is registered, so
		// this only fires if a new target column value outran its adapter. Not the
		// sender's fault, but not retryable either.
		return nil, invalid("target %q has no adapter", ep.Target)
	}

	changes, err := parseChanges(ep, d.Payload)
	if err != nil {
		return nil, err
	}
	if len(changes) == 0 {
		return nil, invalid("payload contained no changes")
	}

	// Assume the endpoint's service user: every downstream crud write inherits
	// that user's permissions, territory write-scope, and audit stamping.
	su, err := auth.LoadSessionUser(ctx, db, ep.ServiceUserID)
	if err != nil {
		return nil, fmt.Errorf("webhook: apply: load service user %q: %w", ep.ServiceUserID, err)
	}
	actx := auth.WithUser(ctx, su)

	var results []itemResult
	if err := db.RunInTx(actx, nil, func(ctx context.Context, tx bun.Tx) error {
		results = make([]itemResult, 0, len(changes))
		for _, ch := range changes {
			r, err := ad.apply(ctx, tx, ep, ch)
			if err != nil {
				return err
			}
			results = append(results, r)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return results, nil
}

// applyInline applies a just-enqueued delivery synchronously -- the shared body
// of the REST paste-and-apply tester (handler.test) and the webhook_test agent
// tool. It flips the row to 'running' so the shared complete/fail guards match,
// applies it, then records success or dead-letters on failure (maxAttempts=1: a
// hand test is never requeued for the background worker). Returns the per-item
// result JSON that the delivery row also stores.
func applyInline(ctx context.Context, db *bun.DB, d *Delivery) (json.RawMessage, error) {
	if _, err := db.NewUpdate().Model((*Delivery)(nil)).
		Set("status = 'running'").
		Set("started_at = now()").
		Set("attempts = attempts + 1").
		Where("id = ?", d.ID).
		Where("status = 'queued'").
		Exec(ctx); err != nil {
		return nil, err
	}

	results, err := applyDelivery(ctx, db, d)
	if err != nil {
		// attempts is now 1; maxAttempts=1 forces 'dead' (no requeue for a test).
		_ = failDelivery(ctx, db, d.ID, err.Error(), 1)
		return nil, err
	}
	rj := resultJSON(results)
	if err := completeDelivery(ctx, db, d.ID, rj); err != nil {
		return nil, err
	}
	return rj, nil
}

// resultJSON encodes per-item results for storage on the delivery row. On the
// (unreachable in practice) marshal error it falls back to an empty array so the
// column is always valid JSON.
func resultJSON(results []itemResult) json.RawMessage {
	b, err := json.Marshal(results)
	if err != nil {
		return json.RawMessage(`[]`)
	}
	return b
}
