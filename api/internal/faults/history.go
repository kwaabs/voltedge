package faults

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/auth"
)

// The action verbs written to faults.fault_event_history.action -- one per
// distinct write the faults engine performs, so a reader can tell a transition
// from an attribute edit from a defect-booking without diffing snapshots. A
// fault raised from an OMS incident is a `created` row like any other (the
// snapshot already carries the incident_id that shows its origin).
const (
	historyCreated       = "created"
	historyStatusChanged = "status_changed"
	historyUpdated       = "updated"
	historyDefectBooked  = "defect_booked"
)

// faultHistory mirrors faults.fault_event_history (migration 0109): one
// append-only row per fault write, storing the full post-change state as a jsonb
// snapshot. Written on the same bun.IDB the fault write used -- so inside a
// composite's transaction (RaiseFaultFromIncident, BookDefectFromFault) the
// audit row commits atomically with the change, and on the plain REST path it's
// a second statement on the same connection, the same arrangement
// gis.feature_history uses.
type faultHistory struct {
	bun.BaseModel `bun:"table:faults.fault_event_history"`

	ID       int64           `bun:"id,pk,autoincrement"`
	FaultID  string          `bun:"fault_id"`
	Action   string          `bun:"action"`
	ActorID  any             `bun:"actor_id"` // uuid string, or nil for a system write
	Snapshot json.RawMessage `bun:"snapshot,type:jsonb"`
}

// actorArg reads the acting user's id from ctx (the session middleware put it
// there), returning nil -- so the column writes NULL -- for a system / CLI /
// unauthenticated write. The same stamp gis crud uses for created_by/updated_by.
func actorArg(ctx context.Context) any {
	if id := auth.ActorID(ctx); id != "" {
		return id
	}
	return nil
}

// recordFaultHistory appends one immutable audit row for a fault write. The
// snapshot is the full post-change fault (the same shape a GET returns). An
// error here fails the enclosing write -- a silent gap would defeat the point of
// an audit trail, and inside a transaction it rolls the write back.
func recordFaultHistory(ctx context.Context, db bun.IDB, action string, fault *FaultEvent) error {
	raw, err := json.Marshal(fault)
	if err != nil {
		return fmt.Errorf("faults: marshal history snapshot: %w", err)
	}
	if _, err := db.NewInsert().Model(&faultHistory{
		FaultID:  fault.ID,
		Action:   action,
		ActorID:  actorArg(ctx),
		Snapshot: raw,
	}).Exec(ctx); err != nil {
		return fmt.Errorf("faults: record history: %w", err)
	}
	return nil
}

// HistoryEntry is one row of a fault's timeline as the API returns it. The actor
// is resolved to a display name / email via a LEFT JOIN to iam.users (both nil
// for a system write or a since-deleted user), so the client shows "who" without
// a second lookup. Snapshot is the full post-change fault state, passed straight
// through as the stored jsonb.
type HistoryEntry struct {
	ID         int64           `bun:"id" json:"id"`
	Action     string          `bun:"action" json:"action"`
	ActorID    *string         `bun:"actor_id" json:"actor_id"`
	ActorName  *string         `bun:"actor_name" json:"actor_name"`
	ActorEmail *string         `bun:"actor_email" json:"actor_email"`
	At         time.Time       `bun:"at" json:"at"`
	Snapshot   json.RawMessage `bun:"snapshot" json:"snapshot"`
}

// ListFaultHistory returns one fault's change timeline, newest first.
func ListFaultHistory(ctx context.Context, db bun.IDB, faultID string) ([]HistoryEntry, error) {
	var entries []HistoryEntry
	err := db.NewSelect().
		ColumnExpr("h.id, h.action, h.actor_id, u.display_name AS actor_name, u.email AS actor_email, h.at, h.snapshot").
		TableExpr("faults.fault_event_history AS h").
		Join("LEFT JOIN iam.users AS u ON u.id = h.actor_id").
		Where("h.fault_id = ?", faultID).
		OrderExpr("h.at DESC, h.id DESC").
		Scan(ctx, &entries)
	if err != nil {
		return nil, fmt.Errorf("faults: list history: %w", err)
	}
	return entries, nil
}
