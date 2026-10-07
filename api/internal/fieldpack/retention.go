package fieldpack

// Shared-cell version RETENTION (S6, docs/shared-field-packs-design.md).
//
// A cell is rebuilt over time (manual or scheduled), each build a new version
// (an ops.field_packs row, kind='shared_base'). Without reaping, old versions pile
// up in RustFS forever. Retention is per-cell and configurable (migration 0131's
// retention column), and -- critically -- is NEVER a blind auto-delete: a version is
// a reap candidate only once NO CREW STILL HOLDS IT. "Holds" is read from the
// download ledger: a version is held while it is some crew member's LATEST pickup of
// the cell. A crew that re-pulled a newer version no longer holds the old one; a crew
// still on the old one keeps it alive no matter how old it is (they may be weeks into
// a job against it). The newest N versions are also always kept, so a just-built
// version with no holders yet is never reaped out from under an imminent pickup.
//
// Policies (retention column, defaultRetention normalizes the rest to 'manual'):
//   - manual : never auto-reap (the admin deletes versions by hand).
//   - pinned : never auto-reap (explicitly protected).
//   - keep_n : keep the newest N succeeded versions; reap older ones with no holder.
//
// N is FIELDSYNC_KEEP_VERSIONS (default 3). Reaping removes both RustFS artifacts and
// the field_packs row; its ledger rows cascade (migration 0132) -- correct, since a
// reaped version by definition has no holder left to account for.

import (
	"context"
	"os"
	"strconv"

	"github.com/kwaabs/voltedge/api/internal/export"
	"github.com/kwaabs/voltedge/api/internal/logx"
	"github.com/uptrace/bun"
)

// keepVersions is the newest-N a keep_n cell always retains, read once at init.
var keepVersions = keepVersionsFromEnv()

func keepVersionsFromEnv() int {
	if v := os.Getenv("FIELDSYNC_KEEP_VERSIONS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 {
			return n
		}
	}
	return 3
}

// reapRow is a reap-candidate version: just the id + its two artifact keys, so the
// reaper can delete the objects and the row without loading the whole pack.
type reapRow struct {
	ID             string  `bun:"id"`
	TilesObjectKey *string `bun:"tiles_object_key"`
	DataObjectKey  *string `bun:"data_object_key"`
}

// reapableVersions returns a cell's succeeded versions that are both OLDER than the
// newest keepN AND held by no crew (not any user's latest pickup of the cell). The
// newest keepN are protected regardless of holders; a held older version is protected
// regardless of age.
func reapableVersions(ctx context.Context, db bun.IDB, cellID string, keepN int) ([]reapRow, error) {
	const q = `
WITH versions AS (
    SELECT id, tiles_object_key, data_object_key,
           row_number() OVER (ORDER BY created_at DESC) AS rn
    FROM ops.field_packs
    WHERE cell_id = ? AND status = 'succeeded'
),
held AS (
    SELECT DISTINCT ON (user_id) pack_id
    FROM ops.field_pack_downloads
    WHERE cell_id = ?
    ORDER BY user_id, downloaded_at DESC
)
SELECT v.id::text, v.tiles_object_key, v.data_object_key
FROM versions v
WHERE v.rn > ?
  AND v.id NOT IN (SELECT pack_id FROM held WHERE pack_id IS NOT NULL)
ORDER BY v.rn`
	var rows []reapRow
	err := db.NewRaw(q, cellID, cellID, keepN).Scan(ctx, &rows)
	return rows, err
}

// reapCellVersions applies a cell's retention policy, reaping every eligible old
// version (artifacts + row). Only keep_n reaps; manual and pinned are no-ops (0). The
// row delete is best-effort per version -- a failure on one is logged and the sweep
// moves on, so one stuck object never blocks the rest. Returns how many were reaped.
func reapCellVersions(ctx context.Context, db *bun.DB, store *export.ObjectStore, cell *Cell) (int, error) {
	if cell.Retention != "keep_n" {
		return 0, nil // manual / pinned: never auto-reap
	}
	candidates, err := reapableVersions(ctx, db, cell.ID, keepVersions)
	if err != nil {
		return 0, err
	}
	reaped := 0
	for i := range candidates {
		c := &candidates[i]
		reapArtifacts(ctx, store, &FieldPack{TilesObjectKey: c.TilesObjectKey, DataObjectKey: c.DataObjectKey})
		if _, err := db.NewDelete().Model((*FieldPack)(nil)).Where("id = ?", c.ID).Exec(ctx); err != nil {
			logx.From(ctx).Error("retention: reaping version failed", "pack", c.ID, "cell", cell.ID, "err", err)
			continue
		}
		reaped++
	}
	return reaped, nil
}

// reapAllCells runs retention across every ACTIVE keep_n cell -- the periodic sweep's
// unit of work (the cmd/fieldsync worker fires it on a timer). Returns the total
// versions reaped. A per-cell failure is logged and skipped so one bad cell never
// stalls the fleet sweep.
func reapAllCells(ctx context.Context, db *bun.DB, store *export.ObjectStore) (int, error) {
	cells, err := listCells(ctx, db)
	if err != nil {
		return 0, err
	}
	total := 0
	for i := range cells {
		cell := &cells[i]
		if !cell.Active || cell.Retention != "keep_n" {
			continue
		}
		n, err := reapCellVersions(ctx, db, store, cell)
		if err != nil {
			logx.From(ctx).Error("retention: sweeping cell failed", "cell", cell.ID, "err", err)
			continue
		}
		total += n
	}
	return total, nil
}
