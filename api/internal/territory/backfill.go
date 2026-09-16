package territory

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
)

// This file is the Phase 58 rollout aid: the coverage report and the backfill
// sweep that must run BEFORE territory enforcement is switched on. Under
// fail-closed scoping a geometric feature with a NULL territory_id is invisible
// to every scoped user, so flipping the flag while unassigned features exist
// would silently hide them. Coverage lets an admin see how many features are
// unassigned per class; Backfill spatially assigns every one it can (the same
// deepest-containing-territory rule Assign uses on write), leaving only the
// genuinely-uncovered ones for manual attention.
//
// Both enumerate the physical feature tables straight from
// metadata.feature_classes (as migrations 0038/0095 do) rather than importing
// api/internal/metadata, keeping this package's dependency surface unchanged.

// classTable pairs a feature class's display code with its physical table.
type classTable struct {
	Code  string `bun:"code"`
	Table string `bun:"physical_table"`
}

// CoverageRow is one class's territory-assignment status: how many live
// (non-deleted) geometric features it has and how many of those still carry no
// territory_id. Classes with zero features are included so the admin view can
// show a complete picture; the frontend can hide the fully-covered ones.
type CoverageRow struct {
	ClassCode  string `json:"class_code"`
	Total      int    `json:"total"`
	Unassigned int    `json:"unassigned"`
}

// BackfillClassResult reports what one class's sweep did: how many features it
// newly assigned, and how many are STILL unassigned afterward (no territory of
// at least the minimum level contains them -- these need a boundary fix or a
// manual override before the flag is flipped).
type BackfillClassResult struct {
	ClassCode string `json:"class_code"`
	Assigned  int    `json:"assigned"`
	Remaining int    `json:"remaining"`
}

// BackfillResult is the whole sweep's outcome, per class plus totals.
type BackfillResult struct {
	Classes        []BackfillClassResult `json:"classes"`
	TotalAssigned  int                   `json:"total_assigned"`
	TotalRemaining int                   `json:"total_remaining"`
}

// geometricTables returns every compiled geometric feature class's code and
// physical table. Not status-filtered (a retired/draft class's table can still
// hold rows that need assigning), matching migrations 0038/0095's own sweep.
func geometricTables(ctx context.Context, db bun.IDB) ([]classTable, error) {
	var tables []classTable
	err := db.NewSelect().
		ColumnExpr("code").
		ColumnExpr("physical_table").
		TableExpr("metadata.feature_classes").
		Where("physical_table IS NOT NULL").
		Where("geometry_type != ?", "none").
		OrderExpr("code").
		Scan(ctx, &tables)
	if err != nil {
		return nil, fmt.Errorf("territory: listing geometric tables: %w", err)
	}
	return tables, nil
}

// Coverage reports, per geometric class, how many live features exist and how
// many are unassigned -- the data behind the admin "unassigned features"
// review view. Read-only; runs no writes.
func Coverage(ctx context.Context, db bun.IDB) ([]CoverageRow, error) {
	tables, err := geometricTables(ctx, db)
	if err != nil {
		return nil, err
	}
	rows := make([]CoverageRow, 0, len(tables))
	for _, ct := range tables {
		var total, unassigned int
		// One pass: total live rows and, among them, the NULL-territory subset.
		query := fmt.Sprintf(`
			SELECT
				count(*) FILTER (WHERE deleted_at IS NULL),
				count(*) FILTER (WHERE deleted_at IS NULL AND territory_id IS NULL)
			FROM %s`, ct.Table)
		if err := db.QueryRowContext(ctx, query).Scan(&total, &unassigned); err != nil {
			return nil, fmt.Errorf("territory: coverage for %s: %w", ct.Code, err)
		}
		rows = append(rows, CoverageRow{ClassCode: ct.Code, Total: total, Unassigned: unassigned})
	}
	return rows, nil
}

// Backfill spatially assigns a territory to every currently-unassigned live
// geometric feature it can, across all classes, in one transaction. Each
// feature is placed in the deepest territory (at least the minimum feature
// level) whose boundary contains its centroid -- identical to Assign's
// automatic path, expressed set-based so a whole class is done in one UPDATE
// rather than a per-row round trip. A feature no qualifying territory covers is
// left NULL and counted in Remaining. Safe to re-run: it only ever touches rows
// that are still NULL, so a second sweep after drawing a missing boundary picks
// up exactly the stragglers.
func Backfill(ctx context.Context, db *bun.DB) (BackfillResult, error) {
	min, err := MinFeatureLevel(ctx, db)
	if err != nil {
		return BackfillResult{}, err
	}
	tables, err := geometricTables(ctx, db)
	if err != nil {
		return BackfillResult{}, err
	}

	var result BackfillResult
	err = db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		for _, ct := range tables {
			// Assign the deepest containing territory (>= min level) to every
			// live, geometry-bearing, still-unassigned feature. The feature's
			// geom is in the class SRID; ST_Transform(...,4326) matches it to
			// the 4326 territory boundaries, exactly as ResolveContaining does.
			update := fmt.Sprintf(`
				UPDATE %[1]s AS f
				SET territory_id = sub.tid
				FROM (
					SELECT f2.id AS fid, cand.tid
					FROM %[1]s AS f2
					CROSS JOIN LATERAL (
						SELECT t.id AS tid
						FROM gis.territories t
						JOIN gis.territory_levels l ON l.id = t.level_id
						WHERE t.geom IS NOT NULL
						  AND l.rank >= ?
						  AND ST_Contains(t.geom, ST_Centroid(ST_Transform(f2.geom, 4326)))
						ORDER BY l.rank DESC
						LIMIT 1
					) AS cand
					WHERE f2.territory_id IS NULL
					  AND f2.geom IS NOT NULL
					  AND f2.deleted_at IS NULL
				) AS sub
				WHERE f.id = sub.fid`, ct.Table)

			res, err := tx.ExecContext(ctx, update, min.Rank)
			if err != nil {
				return fmt.Errorf("territory: backfill %s: %w", ct.Code, err)
			}
			assigned, _ := res.RowsAffected()

			// Whatever's still NULL after the sweep is genuinely uncovered.
			var remaining int
			countQ := fmt.Sprintf(
				"SELECT count(*) FROM %s WHERE deleted_at IS NULL AND geom IS NOT NULL AND territory_id IS NULL",
				ct.Table)
			if err := tx.QueryRowContext(ctx, countQ).Scan(&remaining); err != nil {
				return fmt.Errorf("territory: backfill remaining for %s: %w", ct.Code, err)
			}

			result.Classes = append(result.Classes, BackfillClassResult{
				ClassCode: ct.Code,
				Assigned:  int(assigned),
				Remaining: remaining,
			})
			result.TotalAssigned += int(assigned)
			result.TotalRemaining += remaining
		}
		return nil
	})
	if err != nil {
		return BackfillResult{}, err
	}
	return result, nil
}

// SetEnforcement flips the territory_enforcement_enabled system setting -- the
// final rollout step, after Backfill and an admin review of Coverage. Upserts
// the iam.system_settings row so it works whether or not the key exists yet.
// This is the one write that turns Phase 58 scoping on for the whole install,
// so it lives behind the same territories:write gate the mutating territory
// verbs use.
func SetEnforcement(ctx context.Context, db bun.IDB, enabled bool) error {
	value := "false"
	if enabled {
		value = "true"
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO iam.system_settings (key, value)
		VALUES (?, ?)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`,
		SettingEnforcementEnabled, value)
	if err != nil {
		return fmt.Errorf("territory: setting enforcement flag: %w", err)
	}
	return nil
}
