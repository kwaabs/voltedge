package deviceops

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/uptrace/bun"
)

// RecordInput is one device operation to log. DeviceClass/DeviceID/OpType/
// Source/OccurredAt are required; the rest are optional correlations and
// external-feed provenance. The caller (HTTP handler, an internal emitter, or
// the webhook adapter) fills what it knows.
type RecordInput struct {
	DeviceClass     string
	DeviceID        string
	OpType          string
	Source          string
	OccurredAt      time.Time
	Cause           *string
	Notes           *string
	FaultID         *string
	IncidentID      *string
	SwitchingStepID *string
	SourceSystem    *string
	SourceEventID   *string
}

// Record appends one operation to the log. The acting user is stamped from ctx
// (nil for a system/service write, same as crud's created_by). When the input
// carries a source_event_id (an external feed), the write is IDEMPOTENT: a
// re-delivered event with the same (source_system, source_event_id) returns the
// already-stored row instead of double-logging -- SCADA re-sends are common.
func Record(ctx context.Context, db bun.IDB, in RecordInput) (*Operation, error) {
	in.DeviceClass = strings.TrimSpace(in.DeviceClass)
	in.DeviceID = strings.TrimSpace(in.DeviceID)
	if in.DeviceClass == "" || in.DeviceID == "" {
		return nil, invalidf("device_class and device_id are required")
	}
	if !opTypes[in.OpType] {
		return nil, invalidf("op_type %q is not one of trip, lockout, reclose, open, close, earth, alarm, other", in.OpType)
	}
	if !sources[in.Source] {
		return nil, invalidf("source %q is not one of crew, scada, relay, switching_order, derived, telemetry, other", in.Source)
	}
	if in.OccurredAt.IsZero() {
		return nil, invalidf("occurred_at is required")
	}

	op := &Operation{
		DeviceClass:     in.DeviceClass,
		DeviceID:        in.DeviceID,
		OpType:          in.OpType,
		Source:          in.Source,
		OccurredAt:      in.OccurredAt.UTC(),
		Cause:           trimPtr(in.Cause),
		Notes:           trimPtr(in.Notes),
		FaultID:         in.FaultID,
		IncidentID:      in.IncidentID,
		SwitchingStepID: in.SwitchingStepID,
		SourceSystem:    in.SourceSystem,
		SourceEventID:   in.SourceEventID,
		ActorID:         actorArg(ctx),
	}

	// External feed: dedupe on (source_system, source_event_id) via the partial
	// unique index (migration 0147). ON CONFLICT DO NOTHING + RETURNING yields no
	// row on a duplicate, so re-select the stored one to stay idempotent.
	if op.SourceEventID != nil && *op.SourceEventID != "" {
		res, err := db.NewInsert().Model(op).ExcludeColumn("id").
			On("CONFLICT (source_system, source_event_id) WHERE source_event_id IS NOT NULL DO NOTHING").
			Returning("*").Exec(ctx)
		if err != nil {
			return nil, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			existing := new(Operation)
			if err := db.NewSelect().Model(existing).
				Where("source_system IS NOT DISTINCT FROM ?", op.SourceSystem).
				Where("source_event_id = ?", *op.SourceEventID).
				Scan(ctx); err != nil {
				return nil, err
			}
			return existing, nil
		}
		return op, nil
	}

	if _, err := db.NewInsert().Model(op).ExcludeColumn("id").
		Returning("*").Exec(ctx); err != nil {
		return nil, err
	}
	return op, nil
}

// Filter narrows a List query. Empty fields are ignored. Since/Until bound the
// occurred_at window (inclusive). Limit defaults to 200 and is capped at 1000.
type Filter struct {
	DeviceClass string
	DeviceID    string
	OpType      string
	Source      string
	Since       *time.Time
	Until       *time.Time
	Limit       int
}

// List returns operations newest-first for the given filter -- the board (no
// device filter) or one device's timeline (device_class + device_id set).
func List(ctx context.Context, db bun.IDB, f Filter) ([]Operation, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 200
	}
	if limit > 1000 {
		limit = 1000
	}

	ops := []Operation{}
	q := db.NewSelect().Model(&ops).OrderExpr("o.occurred_at DESC").Limit(limit)
	if f.DeviceClass != "" {
		q = q.Where("o.device_class = ?", f.DeviceClass)
	}
	if f.DeviceID != "" {
		q = q.Where("o.device_id = ?", f.DeviceID)
	}
	if f.OpType != "" {
		q = q.Where("o.op_type = ?", f.OpType)
	}
	if f.Source != "" {
		q = q.Where("o.source = ?", f.Source)
	}
	if f.Since != nil {
		q = q.Where("o.occurred_at >= ?", f.Since.UTC())
	}
	if f.Until != nil {
		q = q.Where("o.occurred_at <= ?", f.Until.UTC())
	}
	if err := q.Scan(ctx); err != nil {
		return nil, err
	}
	return ops, nil
}

// Get returns one operation by id, or a nil *Operation (not an error) when
// absent -- the handler maps that to 404.
func Get(ctx context.Context, db bun.IDB, id string) (*Operation, error) {
	op := new(Operation)
	err := db.NewSelect().Model(op).Where("o.id = ?", id).Scan(ctx)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return op, nil
}

// DeviceOpCount is one device's operation tally for an op_type -- the trip-count
// / reliability rollup. LastOccurredAt is the most recent such operation.
type DeviceOpCount struct {
	DeviceClass    string    `bun:"device_class" json:"device_class"`
	DeviceID       string    `bun:"device_id" json:"device_id"`
	OpType         string    `bun:"op_type" json:"op_type"`
	Count          int       `bun:"count" json:"count"`
	LastOccurredAt time.Time `bun:"last_occurred_at" json:"last_occurred_at"`
}

// CountsFilter scopes the rollup: an optional op_type (e.g. only 'trip') and an
// optional window start.
type CountsFilter struct {
	OpType string
	Since  *time.Time
}

// Counts rolls the log up per (device, op_type): how many times each device did
// each thing, and when it last did it -- busiest devices first. This is the
// maintenance/reliability view (which recloser operates most).
func Counts(ctx context.Context, db bun.IDB, f CountsFilter) ([]DeviceOpCount, error) {
	rows := []DeviceOpCount{}
	q := db.NewSelect().
		Model((*Operation)(nil)).
		ColumnExpr("o.device_class, o.device_id, o.op_type").
		ColumnExpr("COUNT(*) AS count").
		ColumnExpr("MAX(o.occurred_at) AS last_occurred_at").
		GroupExpr("o.device_class, o.device_id, o.op_type").
		OrderExpr("count DESC")
	if f.OpType != "" {
		q = q.Where("o.op_type = ?", f.OpType)
	}
	if f.Since != nil {
		q = q.Where("o.occurred_at >= ?", f.Since.UTC())
	}
	if err := q.Scan(ctx, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

// actorArg reads the acting user's id from ctx (the session middleware put it
// there), returning nil -- so the column writes NULL -- for a system / service
// write. The same stamp gis crud and faults use.
func actorArg(ctx context.Context) *string {
	if id := auth.ActorID(ctx); id != "" {
		return &id
	}
	return nil
}

func trimPtr(s *string) *string {
	if s == nil {
		return nil
	}
	t := strings.TrimSpace(*s)
	if t == "" {
		return nil
	}
	return &t
}
