package oms

// callbacks.go is the CUSTOMER CALLBACK QUEUE (migration 0180, blocker #5). When
// an outage is restored, OMS enqueues one callback per affected service point —
// "notify this premise that supply is back". The external CIS/IVR pulls the queue,
// maps each service point to its own customer, dials/texts, and marks each
// notified. OMS owns WHICH supplies to notify (the frozen interruption set); the
// customer model and the notification itself stay in the external CIS.

import (
	"context"
	"fmt"
	"time"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/webhookout"
	"github.com/uptrace/bun"
)

const (
	callbackPending   = "pending"
	callbackNotified  = "notified"
	callbackCancelled = "cancelled"

	callbackReasonRestored = "restored"
)

// CustomerCallback mirrors oms.customer_callbacks — one affected supply to notify.
type CustomerCallback struct {
	bun.BaseModel `bun:"table:oms.customer_callbacks,alias:cb"`

	ID             string     `bun:"id,pk" json:"id"`
	IncidentID     string     `bun:"incident_id" json:"incident_id"`
	ServicePointID string     `bun:"service_point_id" json:"service_point_id"`
	AccountRef     *string    `bun:"account_ref" json:"account_ref"`
	Phone          *string    `bun:"phone" json:"phone"`
	Reason         string     `bun:"reason" json:"reason"`
	Status         string     `bun:"status" json:"status"`
	Note           *string    `bun:"note" json:"note"`
	NotifiedBy     *string    `bun:"notified_by" json:"notified_by"`
	CreatedAt      time.Time  `bun:"created_at" json:"created_at"`
	NotifiedAt     *time.Time `bun:"notified_at" json:"notified_at"`
}

// enqueueRestorationCallbacks raises a 'restored' callback for every supply this
// incident interrupted — pulling the premise id / phone from that supply's own
// trouble calls when a caller (an IVR/CIS report) left them. Runs inside the
// restore transaction. Idempotent: ON CONFLICT (incident, service_point) keeps the
// existing row, so a re-restore or retry never duplicates a supply's callback.
// Returns how many were newly enqueued, so the caller fires one callback-due
// webhook only when there is something to notify.
func enqueueRestorationCallbacks(ctx context.Context, tx bun.Tx, incidentID string) (int, error) {
	// One row per interrupted service point, with the most recent account_ref/phone
	// any of that SP's calls for this incident carried. Interruptions are the frozen
	// affected set (the reliability facts), so the notify list is exactly who was out.
	res, err := tx.ExecContext(ctx, `
INSERT INTO oms.customer_callbacks (incident_id, service_point_id, account_ref, phone, reason, status)
SELECT ci.incident_id, ci.service_point_id, c.account_ref, c.phone, 'restored', 'pending'
FROM oms.customer_interruptions ci
LEFT JOIN LATERAL (
    SELECT account_ref, phone
    FROM oms.trouble_calls t
    WHERE t.incident_id = ci.incident_id
      AND t.service_point_id = ci.service_point_id
      AND (t.account_ref IS NOT NULL OR t.phone IS NOT NULL)
    ORDER BY t.reported_at DESC
    LIMIT 1
) c ON true
WHERE ci.incident_id = ?
ON CONFLICT (incident_id, service_point_id) DO NOTHING`, incidentID)
	if err != nil {
		return 0, fmt.Errorf("oms: enqueuing restoration callbacks for incident %s: %w", incidentID, err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// CallbackFilter narrows ListCallbacks. Empty fields are ignored.
type CallbackFilter struct {
	Status     string
	IncidentID string
	Limit      int
	Offset     int
}

// ListCallbacks returns callbacks under the filter, newest-first. The external
// CIS/IVR pulls ?status=pending to get its work list.
func ListCallbacks(ctx context.Context, db bun.IDB, f CallbackFilter) ([]CustomerCallback, error) {
	var out []CustomerCallback
	q := db.NewSelect().Model(&out).Order("cb.created_at DESC")
	if f.Status != "" {
		q = q.Where("cb.status = ?", f.Status)
	}
	if f.IncidentID != "" {
		q = q.Where("cb.incident_id = ?", f.IncidentID)
	}
	if f.Limit > 0 {
		q = q.Limit(f.Limit)
	}
	if f.Offset > 0 {
		q = q.Offset(f.Offset)
	}
	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("oms: listing callbacks: %w", err)
	}
	return out, nil
}

// MarkCallbackNotified marks a callback done once the CIS/IVR (or an operator) has
// reached the customer. Stamps the actor and time. A note (e.g. "left voicemail")
// is optional. Idempotent-ish: re-marking an already-notified one refreshes the
// time; a cancelled one is refused.
func MarkCallbackNotified(ctx context.Context, db bun.IDB, id string, note *string) (*CustomerCallback, error) {
	upd := db.NewUpdate().Model((*CustomerCallback)(nil)).
		Set("status = ?", callbackNotified).
		Set("notified_at = now()").
		Where("id = ?", id).
		Where("status <> ?", callbackCancelled)
	if a := auth.ActorID(ctx); a != "" {
		upd = upd.Set("notified_by = ?", a)
	}
	if note != nil {
		upd = upd.Set("note = ?", trimToNil(note))
	}
	res, err := upd.Exec(ctx)
	if err != nil {
		return nil, fmt.Errorf("oms: marking callback %s notified: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, invalidf("callback %q does not exist or is cancelled", id)
	}
	return getCallback(ctx, db, id)
}

// CancelCallback drops a callback from the queue (e.g. a re-outage before it was
// made, or a bad number). A notified one is left as history.
func CancelCallback(ctx context.Context, db bun.IDB, id string) (*CustomerCallback, error) {
	res, err := db.NewUpdate().Model((*CustomerCallback)(nil)).
		Set("status = ?", callbackCancelled).
		Where("id = ?", id).
		Where("status = ?", callbackPending).Exec(ctx)
	if err != nil {
		return nil, fmt.Errorf("oms: cancelling callback %s: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, invalidf("callback %q does not exist or is not pending", id)
	}
	return getCallback(ctx, db, id)
}

func getCallback(ctx context.Context, db bun.IDB, id string) (*CustomerCallback, error) {
	cb := new(CustomerCallback)
	if err := db.NewSelect().Model(cb).Where("cb.id = ?", id).Scan(ctx); err != nil {
		return nil, fmt.Errorf("oms: loading callback %s: %w", id, err)
	}
	return cb, nil
}

// emitCallbackDue fires the one-per-restore signal telling an external CIS/IVR that
// callbacks are waiting; it then pulls GET /oms/callbacks to do the notifying.
func emitCallbackDue(ctx context.Context, tx bun.Tx, incidentID string, count int) error {
	payload := map[string]any{"incident_id": incidentID, "callback_count": count}
	return webhookout.Emit(ctx, tx, webhookout.EventCallbackDue, webhookout.KindIncident, incidentID, payload)
}
