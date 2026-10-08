// Package webhookout is the OUTBOUND event emitter (docs/outbound-webhooks-design.md)
// -- the counterpart to api/internal/webhook (inbound ingestion). Where that engine
// brings data INTO VoltEdge, this pushes VoltEdge's own changes OUT to subscribers
// (CIS / work-management) when something happens here.
//
// Slice 1 is this file: the transactional OUTBOX. A lifecycle write (an OMS incident,
// a WAM work order or defect, a Faults event) calls Emit INSIDE its own transaction,
// which inserts one ops.webhook_outbound_events row. Because the insert shares the
// change's tx, an event is never emitted for a change that rolled back, and a committed
// change always leaves its event -- at-least-once capture, with no lost events.
//
// Slice 2 adds the subscriptions, the fan-out (event x subscription -> deliveries), and
// the delivery worker that drains this table and POSTs each event through the existing
// SSRF-hardened outbound client. Nothing reads this table yet.
//
// This package is imported BY oms/wam/faults; it imports only auth + bun, so there is
// no import cycle with the inbound webhook engine.
package webhookout

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/uptrace/bun"
)

// Entity kinds -- the `entity_kind` an event is about.
const (
	KindIncident  = "incident"
	KindWorkOrder = "work_order"
	KindDefect    = "defect"
	KindFault     = "fault"
)

// Event types -- the semantic `event_type` a write path emits. Namespaced by module so
// a subscription can filter precisely (e.g. "all wam.* events" or just completions).
const (
	EventIncidentRaised   = "oms.incident.raised"
	EventIncidentRestored = "oms.incident.restored"
	EventIncidentUpdated  = "oms.incident.updated"

	EventWorkOrderCreated   = "wam.workorder.created"
	EventWorkOrderCompleted = "wam.workorder.completed"
	EventWorkOrderUpdated   = "wam.workorder.updated"
	EventDefectCreated      = "wam.defect.created"

	EventFaultRaised  = "faults.event.raised"
	EventFaultCleared = "faults.event.cleared"
	EventFaultUpdated = "faults.event.updated"
)

// OutboundEvent is one row of ops.webhook_outbound_events (migration 0133). Seq is
// DB-assigned (GENERATED ALWAYS AS IDENTITY) so it is scan-only -- never written on
// insert. FannedOutAt stays NULL until Slice 2's fan-out processes the event.
type OutboundEvent struct {
	bun.BaseModel `bun:"table:ops.webhook_outbound_events,alias:webhook_outbound_events"`

	ID          string          `bun:"id,pk,nullzero"`
	Seq         int64           `bun:"seq,scanonly"`
	EventType   string          `bun:"event_type"`
	EntityKind  string          `bun:"entity_kind"`
	EntityID    string          `bun:"entity_id"`
	Payload     json.RawMessage `bun:"payload,type:jsonb"`
	ActorID     *string         `bun:"actor_id"`
	OccurredAt  time.Time       `bun:"occurred_at,nullzero"`
	FannedOutAt *time.Time      `bun:"fanned_out_at"`
}

// Emit records one outbound event in the OUTBOX. It MUST be called with the same
// transaction (db) as the change it describes, so the event and the change commit or
// roll back together -- that atomicity is the whole point of the outbox. payload is the
// event's `data` (the changed record or its salient fields), marshaled to the row's
// jsonb; the acting user (auth.ActorID) is captured for the audit/envelope.
//
// An Emit failure is returned to the caller and so FAILS the surrounding transaction:
// a change must not silently commit without its event. (The caller decides whether that
// is acceptable; for a lifecycle write it is -- a missed event is worse than a retry.)
func Emit(ctx context.Context, db bun.IDB, eventType, entityKind, entityID string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("webhookout: marshaling payload for %s: %w", eventType, err)
	}
	ev := &OutboundEvent{
		EventType:  eventType,
		EntityKind: entityKind,
		EntityID:   entityID,
		Payload:    raw,
	}
	if actor := auth.ActorID(ctx); actor != "" {
		ev.ActorID = &actor
	}
	if _, err := db.NewInsert().Model(ev).Exec(ctx); err != nil {
		return fmt.Errorf("webhookout: inserting outbound event %s for %s/%s: %w", eventType, entityKind, entityID, err)
	}
	return nil
}
