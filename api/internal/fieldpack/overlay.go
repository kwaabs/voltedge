package fieldpack

// The per-user OVERLAY source (S2b, docs/shared-field-packs-design.md). A shared
// cell base is the published, non-personal layer many crews share; the overlay is
// the thin PERSONAL layer each crew member lays over it:
//
//   * their WAM work board -- their own open work orders (wherever the asset sits); and
//   * their OWN private drafts over the cell -- the unpublished features they authored
//     that the base (published-only) deliberately excluded.
//
// Unlike the base, the overlay is NOT a built artifact. It is tiny and personal, and
// the crew is online at pickup and at each weekly sync, so it is served as live JSON
// the device fetches and caches (design §13). The device composes base + overlay +
// its own edits.sqlite (overlay beats base, edits beat both) -- that compose is S3.
//
// Everything here is the requesting user's OWN data, so there is no cross-user leak:
// the work board is filtered to assigned_to = the user, the drafts to created_by =
// the user. Pickup gating (cellCoveredBy) still applies, so the overlay is only ever
// served for a cell the crew may pull.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// overlayWorkOrder is one of the crew member's open work orders over the wire -- the
// JSON shape of a wam.work_orders row, the same columns the pack's wam_work_orders
// table carries, but delivered live instead of baked into SQLite. Nullable columns
// are pointers so a NULL omits rather than serializing an empty string/zero time.
type overlayWorkOrder struct {
	ID              string     `json:"id"`
	Reference       *string    `json:"reference,omitempty"`
	WorkType        string     `json:"work_type"`
	Status          string     `json:"status"`
	Priority        *string    `json:"priority,omitempty"`
	Title           *string    `json:"title,omitempty"`
	Description     *string    `json:"description,omitempty"`
	AssetClass      *string    `json:"asset_class,omitempty"`
	AssetID         *string    `json:"asset_id,omitempty"`
	DefectID        *string    `json:"defect_id,omitempty"`
	AssignedTo      *string    `json:"assigned_to,omitempty"`
	ScheduledFor    *time.Time `json:"scheduled_for,omitempty"`
	DueBy           *time.Time `json:"due_by,omitempty"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	CompletionNotes *string    `json:"completion_notes,omitempty"`
}

// overlayDraft is one of the crew member's own private drafts over the cell -- the
// same feature shape the pack's features table carries. WKB is OGC binary (the
// on-device edit truth), which Go's JSON encoder emits as base64; GeoJSON rides
// alongside for display without a device-side WKB decode. The device layers these
// over the base's features (overlay wins) and treats them as its own editable drafts.
type overlayDraft struct {
	ID           string         `json:"id"`
	FeatureClass string         `json:"feature_class"`
	Name         *string        `json:"name,omitempty"`
	Status       *string        `json:"status,omitempty"`
	Subtype      *int64         `json:"subtype,omitempty"`
	TerritoryID  *string        `json:"territory_id,omitempty"`
	WKB          []byte         `json:"wkb"`
	GeoJSON      string         `json:"geojson"`
	Attributes   map[string]any `json:"attributes"`
}

// overlayResult is the whole overlay payload for one (cell, user): the work board,
// the drafts, and the snapshot instant both were read under.
type overlayResult struct {
	CellID     string             `json:"cell_id"`
	SnapshotAt time.Time          `json:"snapshot_at"`
	WorkOrders []overlayWorkOrder `json:"work_orders"`
	Drafts     []overlayDraft     `json:"drafts"`
}

// buildOverlay reads a user's overlay for a cell under ONE read-only REPEATABLE READ
// snapshot, so the work board and the drafts are a consistent point-in-time view (an
// edit landing mid-read can't show a draft in one list and not the other). includeWAM
// gates the work board on the caller's wam:read, resolved by the handler from the live
// session (the same freeze the base build does for its WAM tables); drafts need only
// features:read, already gated by the route.
func buildOverlay(ctx context.Context, db *bun.DB, cell *Cell, userID string, includeWAM bool) (overlayResult, error) {
	res := overlayResult{CellID: cell.ID, WorkOrders: []overlayWorkOrder{}, Drafts: []overlayDraft{}}

	bbox, err := scopeBBox(cell.Bbox)
	if err != nil {
		return overlayResult{}, err
	}
	territoryIDs, err := cellTerritoryIDs(ctx, db, cell)
	if err != nil {
		return overlayResult{}, err
	}

	txErr := db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true},
		func(ctx context.Context, tx bun.Tx) error {
			if err := tx.NewRaw("SELECT now()").Scan(ctx, &res.SnapshotAt); err != nil {
				return fmt.Errorf("reading overlay snapshot time: %w", err)
			}

			if includeWAM {
				res.WorkOrders, err = loadOverlayWorkOrders(ctx, tx, userID)
				if err != nil {
					return fmt.Errorf("loading work board: %w", err)
				}
			}

			res.Drafts, err = loadOverlayDrafts(ctx, tx, cell, bbox, territoryIDs, userID)
			if err != nil {
				return fmt.Errorf("loading drafts: %w", err)
			}
			return nil
		})
	if txErr != nil {
		return overlayResult{}, txErr
	}
	return res, nil
}

// loadOverlayWorkOrders reads the user's own open work orders -- their field work
// board. Same query + predicate as the pack builder's loadPackWorkOrders (assigned to
// the user, not completed/cancelled, soonest-due first), but scanned into the JSON
// shape instead of the SQLite statement.
func loadOverlayWorkOrders(ctx context.Context, db bun.IDB, userID string) ([]overlayWorkOrder, error) {
	if userID == "" {
		return []overlayWorkOrder{}, nil
	}
	const q = `
SELECT id::text, reference, work_type, status, priority, title, description,
       asset_class, asset_id::text, defect_id::text, assigned_to_user_id::text,
       scheduled_for, due_by, started_at, completion_notes
FROM wam.work_orders
WHERE assigned_to_user_id::text = ?
  AND status NOT IN ('completed', 'cancelled')
ORDER BY COALESCE(scheduled_for, due_by, created_at)`
	rows, err := db.QueryContext(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []overlayWorkOrder{}
	for rows.Next() {
		var id, workType, status string
		var reference, priority, title, description, assetClass, assetID, defectID, assignedTo, completionNotes sql.NullString
		var scheduledFor, dueBy, startedAt sql.NullTime
		if err := rows.Scan(&id, &reference, &workType, &status, &priority, &title, &description,
			&assetClass, &assetID, &defectID, &assignedTo, &scheduledFor, &dueBy, &startedAt, &completionNotes); err != nil {
			return nil, err
		}
		out = append(out, overlayWorkOrder{
			ID:              id,
			Reference:       nullStr(reference),
			WorkType:        workType,
			Status:          status,
			Priority:        nullStr(priority),
			Title:           nullStr(title),
			Description:     nullStr(description),
			AssetClass:      nullStr(assetClass),
			AssetID:         nullStr(assetID),
			DefectID:        nullStr(defectID),
			AssignedTo:      nullStr(assignedTo),
			ScheduledFor:    nullTime(scheduledFor),
			DueBy:           nullTime(dueBy),
			StartedAt:       nullTime(startedAt),
			CompletionNotes: nullStr(completionNotes),
		})
	}
	return out, rows.Err()
}

// loadOverlayDrafts reads the user's OWN private drafts across the cell's classes,
// scoped to the cell's bbox + territory exactly as the base build is, via the
// readClassFeatures privateDraftsOf filter (not-public AND created_by = user). A
// non-geometric cell class is skipped rather than failing the whole overlay (a
// geomless class carries no map drafts anyway). The total is capped at the same
// maxPackFeatures bound as a pack -- a user's own drafts over one cell are naturally
// few, but an unbounded live response is refused loudly rather than streamed.
func loadOverlayDrafts(ctx context.Context, tx bun.Tx, cell *Cell, bbox *crud.ExportBBox, territoryIDs []string, userID string) ([]overlayDraft, error) {
	out := []overlayDraft{}
	for _, code := range cell.FeatureClasses {
		loaded, err := metadata.Load(ctx, tx, code)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue // a class that vanished from the catalog: nothing to overlay
			}
			return nil, err
		}
		if loaded.FeatureClass.GeometryType == "none" {
			continue
		}
		_, err = readClassFeatures(ctx, tx, loaded, bbox, territoryIDs, nil, false, &userID, func(row packRow) error {
			out = append(out, overlayDraft{
				ID:           row.ID,
				FeatureClass: code,
				Name:         row.Name,
				Status:       row.Status,
				Subtype:      row.Subtype,
				TerritoryID:  row.TerritoryID,
				WKB:          row.WKB,
				GeoJSON:      row.GeoJSON,
				Attributes:   row.Attributes,
			})
			if len(out) > maxPackFeatures {
				return fmt.Errorf(
					"overlay exceeds the %d-draft limit -- publish or prune your drafts in this area (or raise FIELDSYNC_MAX_FEATURES)",
					maxPackFeatures)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// nullStr / nullTime unwrap a sql.Null* into the pointer the JSON shape uses: a
// present value points at it, a NULL stays nil (and omitempty drops the key).
func nullStr(ns sql.NullString) *string {
	if !ns.Valid {
		return nil
	}
	return &ns.String
}

func nullTime(nt sql.NullTime) *time.Time {
	if !nt.Valid {
		return nil
	}
	return &nt.Time
}
