package oms

// events.go is the OMS lifecycle EVENT LOG (oms.events, migration 0167): an
// append-only timeline of every call/incident transition, so clicking a trouble
// call shows "received -> located -> attributed -> confirmed -> dispatched ->
// restored" with who did each and when. Every transition in store.go records one
// row in its own transaction, so the log commits (or rolls back) with the change
// it describes -- the same atomicity the webhook outbox keeps.

import (
	"context"
	"fmt"
	"time"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/uptrace/bun"
)

// Entity kinds and event types. event_type is free text (no DB enum) so a new
// transition never needs a migration; these are the canonical set the writers use.
const (
	EntityCall     = "call"
	EntityIncident = "incident"

	EventCallReceived   = "call.received"
	EventCallLocated    = "call.located"
	EventCallAttributed = "call.attributed"

	EventIncidentPredicted  = "incident.predicted"
	EventIncidentPlanned    = "incident.planned"
	EventIncidentScadaRaise = "incident.scada_raised"
	EventIncidentConfirmed  = "incident.confirmed"
	EventIncidentDispatched = "incident.dispatched"
	EventIncidentRestoredE  = "incident.restored"
	EventIncidentCancelled  = "incident.cancelled"
	EventIncidentCause      = "incident.cause"      // cause / detection-source re-attributed
	EventIncidentFromSpot   = "incident.from_spot"  // raised from a geographic trouble spot
	EventIncidentAttributed = "incident.attributed" // a device was attributed/changed on the incident
)

// Event mirrors oms.events. ActorName is scan-only, filled by a LEFT JOIN on the
// timeline read so the panel shows "by Ama" without the viewer needing admin:users.
type Event struct {
	bun.BaseModel `bun:"table:oms.events,alias:ev"`

	ID          string    `bun:"id,pk" json:"id"`
	EntityKind  string    `bun:"entity_kind" json:"entity_kind"`
	EntityID    string    `bun:"entity_id" json:"entity_id"`
	EventType   string    `bun:"event_type" json:"event_type"`
	ActorUserID *string   `bun:"actor_user_id" json:"actor_user_id"`
	ActorName   *string   `bun:"actor_name,scanonly" json:"actor_name"`
	Note        *string   `bun:"note" json:"note"`
	OccurredAt  time.Time `bun:"occurred_at" json:"occurred_at"`
	CreatedAt   time.Time `bun:"created_at" json:"created_at"`
}

// recordEvent appends one lifecycle event stamped NOW, attributed to the ctx's
// user (nil for an engine/system step). It MUST be called in the same tx as the
// change it describes. A nil note is omitted.
func recordEvent(ctx context.Context, tx bun.IDB, kind, entityID, eventType string, note *string) error {
	return recordEventAt(ctx, tx, kind, entityID, eventType, time.Now(), note)
}

// recordEventAt is recordEvent with an explicit occurred_at -- for a transition
// whose real time differs from the insert (a SCADA trip's reported time, a call's
// reported_at).
func recordEventAt(ctx context.Context, tx bun.IDB, kind, entityID, eventType string, at time.Time, note *string) error {
	ev := &Event{EntityKind: kind, EntityID: entityID, EventType: eventType, OccurredAt: at, Note: note}
	if actor := auth.ActorID(ctx); actor != "" {
		ev.ActorUserID = &actor
	}
	// Insert the writable columns explicitly. NB: actor_name is a scanonly field
	// (resolved by a JOIN on read), so it must NOT be named here -- bun can't
	// exclude a column that isn't in the insert set and errors "can't find column".
	if _, err := tx.NewInsert().Model(ev).
		Column("entity_kind", "entity_id", "event_type", "actor_user_id", "note", "occurred_at").
		Exec(ctx); err != nil {
		return fmt.Errorf("oms: recording %s event for %s %s: %w", eventType, kind, entityID, err)
	}
	return nil
}

// noteOf wraps a non-empty string as a *string note, else nil.
func noteOf(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// eventsFor loads the timeline for a set of (kind, id) pairs, oldest first, with
// the actor display name resolved. The pairs are OR-ed so one query serves both a
// call and the incident it joined.
func eventsFor(ctx context.Context, db bun.IDB, pairs [][2]string) ([]Event, error) {
	out := []Event{}
	if len(pairs) == 0 {
		return out, nil
	}
	q := db.NewSelect().Model(&out).
		ColumnExpr("ev.*").
		ColumnExpr("actor.display_name AS actor_name").
		Join("LEFT JOIN iam.users AS actor ON actor.id::text = ev.actor_user_id").
		OrderExpr("ev.occurred_at ASC, ev.created_at ASC")
	q = q.WhereGroup(" AND ", func(q *bun.SelectQuery) *bun.SelectQuery {
		for _, p := range pairs {
			q = q.WhereOr("ev.entity_kind = ? AND ev.entity_id = ?", p[0], p[1])
		}
		return q
	})
	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("oms: loading timeline: %w", err)
	}
	return out, nil
}

// CallTimeline is a trouble call's full story: its own events plus the lifecycle
// of the incident it was rolled into (if any), merged chronologically -- so the
// panel reads straight from "received" through the outage's restoration.
func CallTimeline(ctx context.Context, db bun.IDB, callID string) ([]Event, error) {
	call, err := GetTroubleCall(ctx, db, callID)
	if err != nil {
		return nil, err
	}
	pairs := [][2]string{{EntityCall, call.ID}}
	if call.IncidentID != nil && *call.IncidentID != "" {
		pairs = append(pairs, [2]string{EntityIncident, *call.IncidentID})
	}
	return eventsFor(ctx, db, pairs)
}

// IncidentTimeline is an incident's lifecycle plus the "received" events of every
// call that fed it, merged chronologically -- so an operator sees the calls
// arriving and the outage advancing in one thread.
func IncidentTimeline(ctx context.Context, db bun.IDB, incidentID string) ([]Event, error) {
	if _, err := GetIncident(ctx, db, incidentID); err != nil {
		return nil, err
	}
	var callIDs []string
	if err := db.NewSelect().
		ColumnExpr("id").
		TableExpr("oms.trouble_calls").
		Where("incident_id = ?", incidentID).
		Scan(ctx, &callIDs); err != nil {
		return nil, fmt.Errorf("oms: loading incident %s calls: %w", incidentID, err)
	}
	pairs := [][2]string{{EntityIncident, incidentID}}
	for _, cid := range callIDs {
		pairs = append(pairs, [2]string{EntityCall, cid})
	}
	return eventsFor(ctx, db, pairs)
}
