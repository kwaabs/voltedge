package wam

// mirror.go is the READ-ONLY WAM MIRROR (migration 0182, external-mirror-integration
// design): an authoritative external WAM that OWNS its work orders pushes them to
// VoltEdge, which reflects them beside its own locally-raised ones. A mirrored work
// order is display-only here -- its lifecycle is theirs -- so:
//
//   - MirrorWorkOrder upserts the shadow by (origin_system, external_id). It emits NO
//     outbound webhook (echo suppression) and never touches crews / field packs (a
//     mirrored order can't be assigned here).
//   - assertLocalWorkOrder gates UpdateWorkOrder (the one local-edit path): a mirrored
//     order refuses every VoltEdge-side change. The status translation (their state ->
//     our status) is done by the ingest adapter via its per-endpoint status_map; this
//     engine takes an already-resolved WAM status.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/uptrace/bun"
)

// MirrorWorkOrderParams is one authoritative external work order to reflect. Status
// is an ALREADY-TRANSLATED WAM status (the adapter maps the vendor's state).
// AssetClass/AssetID are the VoltEdge feature the external asset resolved to via
// external_ref (optional). No crew/subcontractor/defect -- those are local concepts.
type MirrorWorkOrderParams struct {
	OriginSystem string
	ExternalID   string
	Reference    *string
	WorkType     string
	Status       string
	Priority     string
	Title        string
	Description  *string
	AssetClass   *string
	AssetID      *string
	ScheduledFor *time.Time
	DueBy        *time.Time
}

// MirrorWorkOrder upserts a read-only mirrored work order keyed by (origin_system,
// external_id). It does NOT emit a webhook and never engages crews/field packs, so a
// mirrored order never echoes back to its source or draws on local dispatch. Returns
// the shadow order.
func MirrorWorkOrder(ctx context.Context, db bun.IDB, p MirrorWorkOrderParams) (*WorkOrder, error) {
	origin := strings.TrimSpace(p.OriginSystem)
	ext := strings.TrimSpace(p.ExternalID)
	if origin == "" || ext == "" {
		return nil, invalidf("a mirrored work order needs origin_system and external_id")
	}
	workType := strings.TrimSpace(p.WorkType)
	if err := validateWorkType(workType); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.Title) == "" {
		return nil, invalidf("a mirrored work order needs a title")
	}
	status := strings.TrimSpace(p.Status)
	if err := oneOf("status", status, WorkStatusDraft, WorkStatusScheduled, WorkStatusAssigned, WorkStatusInProgress, WorkStatusCompleted, WorkStatusCancelled); err != nil {
		return nil, invalidf("mirrored work order %q: %v (map the external state to a VoltEdge work status)", ext, err)
	}
	priority := strings.TrimSpace(p.Priority)
	if priority == "" {
		priority = PriorityMedium
	}
	if err := validatePriority(priority); err != nil {
		return nil, err
	}

	wo := &WorkOrder{
		OriginSystem: &origin,
		ExternalID:   &ext,
		Reference:    p.Reference,
		WorkType:     workType,
		Status:       status,
		Priority:     priority,
		Title:        strings.TrimSpace(p.Title),
		Description:  p.Description,
		AssetClass:   p.AssetClass,
		AssetID:      p.AssetID,
		ScheduledFor: p.ScheduledFor,
		DueBy:        p.DueBy,
	}
	_, err := db.NewInsert().Model(wo).
		Column("origin_system", "external_id", "reference", "work_type", "status",
			"priority", "title", "description", "asset_class", "asset_id",
			"scheduled_for", "due_by").
		On("CONFLICT (origin_system, external_id) WHERE origin_system IS NOT NULL AND external_id IS NOT NULL DO UPDATE").
		Set("work_type = EXCLUDED.work_type").
		Set("status = EXCLUDED.status").
		Set("priority = EXCLUDED.priority").
		Set("title = EXCLUDED.title").
		Set("description = EXCLUDED.description").
		Set("reference = EXCLUDED.reference").
		Set("asset_class = EXCLUDED.asset_class").
		Set("asset_id = EXCLUDED.asset_id").
		Set("scheduled_for = EXCLUDED.scheduled_for").
		Set("due_by = EXCLUDED.due_by").
		Set("updated_at = now()").
		Returning("id").Exec(ctx)
	if err != nil {
		return nil, fmt.Errorf("wam: mirroring work order %s/%s: %w", origin, ext, err)
	}
	return GetWorkOrder(ctx, db, wo.ID)
}

// assertLocalWorkOrder refuses a local edit on a mirrored (read-only) work order. A
// cheap origin probe: an unknown id (no row) is treated as local so UpdateWorkOrder's
// own not-found handling reports it.
func assertLocalWorkOrder(ctx context.Context, db bun.IDB, id string) error {
	var origin sql.NullString
	err := db.NewSelect().
		ColumnExpr("origin_system").
		TableExpr("wam.work_orders").
		Where("id = ?", id).
		Scan(ctx, &origin)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("wam: checking work order %s origin: %w", id, err)
	}
	if origin.Valid && origin.String != "" {
		return invalidf("work order %q is mirrored from %q and is read-only in VoltEdge (its lifecycle is owned by that system)", id, origin.String)
	}
	return nil
}
