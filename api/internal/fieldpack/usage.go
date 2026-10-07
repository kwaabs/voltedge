package fieldpack

// The shared-cell USAGE / reconciliation report (S5, docs/shared-field-packs-design.md).
//
// A shared cell base is pulled by many crews and reused for weeks, so the real
// operational need the product owner called out is ACCOUNTING, not freshness: per
// cell, who is holding which version, who has synced work back, and who has gone
// quiet. This file answers that by reconciling the download LEDGER
// (ops.field_pack_downloads, migration 0132) against the sync INBOX
// (ops.sync_inbox), which already stamps every device op with the pack snapshot it
// was made against (base_version) and the submitting user (submitted_by).
//
// The link between a holding and its syncs is the exact VERSION: a pickup references
// an ops.field_packs row whose snapshot_at is the point-in-time that base built from,
// and the device stamps that same snapshot onto base_version (as ISO text) on every
// op. Comparing them as INSTANTS (base_version::timestamptz = fp.snapshot_at) is
// robust to text-format differences between Go's RFC3339 and the device's ISO. A
// base_version is always either that snapshot ISO or NULL (older op), so the cast is
// safe; the IS NOT NULL guard keeps NULLs out of it.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"
)

// usageHolderRow is one crew member's CURRENT holding of a cell (their latest pickup)
// plus the reconciliation of their syncs against that exact version.
type usageHolderRow struct {
	UserID       string     `bun:"user_id"`
	Email        string     `bun:"email"`
	DisplayName  string     `bun:"display_name"`
	PackID       string     `bun:"pack_id"`
	SnapshotAt   *time.Time `bun:"snapshot_at"`
	DeviceID     *string    `bun:"device_id"`
	DownloadedAt time.Time  `bun:"downloaded_at"`
	LastSyncedAt *time.Time `bun:"last_synced_at"`
	SyncCount    int        `bun:"sync_count"`
}

// usageVersionRow is one built version of a cell and how many distinct crews pulled
// it -- the supervisor's "which versions are out in the field" view, which also tells
// retention which versions still have holders.
type usageVersionRow struct {
	PackID       string     `bun:"pack_id"`
	SnapshotAt   *time.Time `bun:"snapshot_at"`
	FeatureCount *int       `bun:"feature_count"`
	CreatedAt    time.Time  `bun:"created_at"`
	HolderCount  int        `bun:"holder_count"`
}

// cellHolders reconciles the download ledger for a cell against the sync inbox,
// returning one row per DISTINCT crew member (their latest pickup of this cell) with
// their sync activity against the exact version they hold. A holder who has never
// synced against their version comes back with last_synced_at NULL + sync_count 0 --
// the "gone quiet" signal.
func cellHolders(ctx context.Context, db bun.IDB, cellID string) ([]usageHolderRow, error) {
	const q = `
SELECT DISTINCT ON (dl.user_id)
       dl.user_id::text,
       u.email,
       u.display_name,
       dl.pack_id::text    AS pack_id,
       fp.snapshot_at,
       dl.device_id,
       dl.downloaded_at,
       si.last_synced_at,
       COALESCE(si.sync_count, 0) AS sync_count
FROM ops.field_pack_downloads dl
JOIN iam.users u       ON u.id = dl.user_id
JOIN ops.field_packs fp ON fp.id = dl.pack_id
LEFT JOIN LATERAL (
    SELECT max(s.received_at) AS last_synced_at, count(*) AS sync_count
    FROM ops.sync_inbox s
    WHERE s.submitted_by = dl.user_id
      AND s.base_version IS NOT NULL
      AND s.base_version::timestamptz = fp.snapshot_at
) si ON true
WHERE dl.cell_id = ?
ORDER BY dl.user_id, dl.downloaded_at DESC`
	var rows []usageHolderRow
	err := db.NewRaw(q, cellID).Scan(ctx, &rows)
	return rows, err
}

// cellVersions lists a cell's succeeded builds, newest first, each with the count of
// distinct crews that pulled it.
func cellVersions(ctx context.Context, db bun.IDB, cellID string) ([]usageVersionRow, error) {
	const q = `
SELECT fp.id::text AS pack_id,
       fp.snapshot_at,
       fp.feature_count,
       fp.created_at,
       (SELECT count(DISTINCT d.user_id)
          FROM ops.field_pack_downloads d
         WHERE d.pack_id = fp.id) AS holder_count
FROM ops.field_packs fp
WHERE fp.cell_id = ? AND fp.status = 'succeeded'
ORDER BY fp.created_at DESC`
	var rows []usageVersionRow
	err := db.NewRaw(q, cellID).Scan(ctx, &rows)
	return rows, err
}

// ---- Views + handler --------------------------------------------------------

type cellUsageVersionView struct {
	PackID       string     `json:"pack_id"`
	SnapshotAt   *time.Time `json:"snapshot_at,omitempty"`
	FeatureCount *int       `json:"feature_count,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	HolderCount  int        `json:"holder_count"`
}

type cellUsageHolderView struct {
	UserID            string     `json:"user_id"`
	Email             string     `json:"email"`
	DisplayName       string     `json:"display_name"`
	PackID            string     `json:"pack_id"`
	VersionSnapshotAt *time.Time `json:"version_snapshot_at,omitempty"`
	DeviceID          *string    `json:"device_id,omitempty"`
	DownloadedAt      time.Time  `json:"downloaded_at"`
	LastSyncedAt      *time.Time `json:"last_synced_at,omitempty"`
	SyncCount         int        `json:"sync_count"`
	// Quiet is the "gone quiet" flag: the crew picked this version up but has not
	// synced any work against it. It is exactly last_synced_at IS NULL, surfaced so
	// the dashboard can highlight a holder who may be sitting on un-pushed field work.
	Quiet bool `json:"quiet"`
}

type cellUsageView struct {
	CellID   string                 `json:"cell_id"`
	CellName string                 `json:"cell_name"`
	Versions []cellUsageVersionView `json:"versions"`
	Holders  []cellUsageHolderView  `json:"holders"`
}

// adminCellUsageHandler serves the per-cell usage report: every built version with
// its holder count, and every current holder with their sync reconciliation. Mounted
// at GET /field-packs/cells/{id}/usage under the same admin:field-packs gate as the
// rest of the cell admin.
func adminCellUsageHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		cell, err := getCell(r.Context(), db, id)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeErr(w, http.StatusNotFound, "cell not found")
				return
			}
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}

		versions, err := cellVersions(r.Context(), db, id)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		holders, err := cellHolders(r.Context(), db, id)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}

		view := cellUsageView{
			CellID:   cell.ID,
			CellName: cell.Name,
			Versions: make([]cellUsageVersionView, len(versions)),
			Holders:  make([]cellUsageHolderView, len(holders)),
		}
		for i := range versions {
			view.Versions[i] = cellUsageVersionView{
				PackID:       versions[i].PackID,
				SnapshotAt:   versions[i].SnapshotAt,
				FeatureCount: versions[i].FeatureCount,
				CreatedAt:    versions[i].CreatedAt,
				HolderCount:  versions[i].HolderCount,
			}
		}
		for i := range holders {
			view.Holders[i] = cellUsageHolderView{
				UserID:            holders[i].UserID,
				Email:             holders[i].Email,
				DisplayName:       holders[i].DisplayName,
				PackID:            holders[i].PackID,
				VersionSnapshotAt: holders[i].SnapshotAt,
				DeviceID:          holders[i].DeviceID,
				DownloadedAt:      holders[i].DownloadedAt,
				LastSyncedAt:      holders[i].LastSyncedAt,
				SyncCount:         holders[i].SyncCount,
				Quiet:             holders[i].LastSyncedAt == nil,
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(view)
	}
}
