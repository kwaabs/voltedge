package admin

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// setFeatureClassActive flips a compiled class between 'active' (available) and
// 'deprecated' (globally deactivated) WITHOUT touching its physical table or its
// rows -- the non-destructive opposite of Uncompile.
//
// A deactivated class drops out of the map, the New-Feature menu, the Features
// browser and every create/edit form, because the map-facing catalog
// (metadata.ListFeatureClasses) only returns status = 'active'. But its gis.<code>
// table and every row in it are kept, so reactivating is instant and lossless --
// the schema is unchanged, no recompile is needed. Use this when a utility simply
// doesn't use a feature class and wants it out of the way, as opposed to Uncompile
// (which DROPS the table to rebuild it) or Delete (draft only).
//
// Only a compiled class participates, and the two states are each other's only
// legal transition: a draft has nothing to deactivate, and an active class can't
// be "reactivated". The status column is a coded lifecycle (draft/active/
// deprecated), so the enum stays valid either way.
func setFeatureClassActive(ctx context.Context, db *bun.DB, code string, active bool) error {
	return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		fc := new(metadata.FeatureClass)
		if err := tx.NewSelect().Model(fc).Where("code = ?", code).Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				return notFound("feature class %q does not exist", code)
			}
			return fmt.Errorf("loading feature class %q: %w", code, err)
		}

		if active {
			if fc.Status != "deprecated" {
				return invalid("only a deactivated class can be reactivated -- %q is %s", code, fc.Status)
			}
		} else {
			if fc.Status != "active" {
				return invalid("only an active (compiled) class can be deactivated -- %q is %s; compile it first", code, fc.Status)
			}
		}

		next := "deprecated"
		if active {
			next = "active"
		}
		if _, err := tx.NewUpdate().
			Model((*metadata.FeatureClass)(nil)).
			Set("status = ?", next).
			Where("code = ?", code).
			Exec(ctx); err != nil {
			return fmt.Errorf("updating status of %q: %w", code, err)
		}
		return nil
	})
}

// featureClassTerritoryDisabled mirrors metadata.feature_class_territory_disabled
// (migration 0141) -- one row per (class, territory) the class is turned off for.
type featureClassTerritoryDisabled struct {
	bun.BaseModel `bun:"table:metadata.feature_class_territory_disabled"`

	FeatureClassID string `bun:"feature_class_id"`
	TerritoryID    string `bun:"territory_id"`
}

// listDisabledTerritories returns the territory ids a class is EXPLICITLY disabled
// for -- the authored rows, not the cascade-expanded descendant set, so the admin
// UI shows and edits exactly what was set.
func listDisabledTerritories(ctx context.Context, db *bun.DB, code string) ([]string, error) {
	ids := []string{}
	err := db.NewRaw(`
SELECT d.territory_id::text
FROM metadata.feature_class_territory_disabled d
JOIN metadata.feature_classes fc ON fc.id = d.feature_class_id
WHERE fc.code = ?
ORDER BY d.territory_id`, code).Scan(ctx, &ids)
	if err != nil {
		return nil, fmt.Errorf("listing disabled territories for %q: %w", code, err)
	}
	return ids, nil
}

// setDisabledTerritories replaces the set of territories a class is disabled for
// with exactly territoryIDs, in one transaction (clear-then-insert). An empty
// slice re-enables the class everywhere. Unknown class -> 404; an unknown
// territory id surfaces as the FK's own error.
func setDisabledTerritories(ctx context.Context, db *bun.DB, code string, territoryIDs []string) error {
	return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		fc := new(metadata.FeatureClass)
		if err := tx.NewSelect().Model(fc).Where("code = ?", code).Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				return notFound("feature class %q does not exist", code)
			}
			return fmt.Errorf("loading feature class %q: %w", code, err)
		}
		if _, err := tx.NewDelete().
			Model((*featureClassTerritoryDisabled)(nil)).
			Where("feature_class_id = ?", fc.ID).
			Exec(ctx); err != nil {
			return fmt.Errorf("clearing disabled territories for %q: %w", code, err)
		}
		if len(territoryIDs) == 0 {
			return nil
		}
		rows := make([]featureClassTerritoryDisabled, 0, len(territoryIDs))
		seen := map[string]bool{}
		for _, tid := range territoryIDs {
			if tid == "" || seen[tid] {
				continue
			}
			seen[tid] = true
			rows = append(rows, featureClassTerritoryDisabled{FeatureClassID: fc.ID, TerritoryID: tid})
		}
		if len(rows) == 0 {
			return nil
		}
		if _, err := tx.NewInsert().Model(&rows).Exec(ctx); err != nil {
			return fmt.Errorf("setting disabled territories for %q: %w", code, err)
		}
		return nil
	})
}
