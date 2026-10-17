package crud

import (
	"context"
	"fmt"

	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// enforceClassAvailability is the write-time gate for feature-class deactivation
// (Slice 1 global + Slice 2 per-territory). It makes deactivation AUTHORITATIVE,
// not merely hidden in the UI:
//
//   - GLOBAL: a class whose status is not 'active' (deactivated = 'deprecated',
//     or a never-compiled 'draft') accepts no new features at all.
//   - PER-TERRITORY: a class may additionally be disabled for specific territories
//     (metadata.feature_class_territory_disabled). A new feature landing in a
//     disabled territory -- or any DESCENDANT of one, since a disable cascades
//     down the territory tree -- is refused. Geometry-less features (nil
//     territoryID) have no territory and are exempt from the per-territory half.
//
// Existing features are grandfathered: this fires only on create, so turning a
// class off never retroactively removes or rewrites rows captured while it was on.
func enforceClassAvailability(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, territoryID *string) error {
	if loaded.FeatureClass.Status != "active" {
		return invalid("feature class %q is deactivated and cannot accept new features", loaded.FeatureClass.Code)
	}
	if territoryID == nil {
		return nil
	}
	disabled, err := classDisabledForTerritory(ctx, db, loaded.FeatureClass.ID, *territoryID)
	if err != nil {
		return err
	}
	if disabled {
		return invalid("feature class %q is deactivated in this territory", loaded.FeatureClass.Code)
	}
	return nil
}

// classDisabledForTerritory reports whether featureClassID is disabled for
// territoryID or any of its ANCESTORS -- a disable authored on a parent territory
// applies to every territory beneath it. It walks the gis.territories adjacency
// list (migration 0027, self-referential parent_id) up to the root with a
// recursive CTE, then tests the disable set.
func classDisabledForTerritory(ctx context.Context, db bun.IDB, featureClassID, territoryID string) (bool, error) {
	var exists bool
	err := db.NewRaw(`
WITH RECURSIVE ancestors AS (
    SELECT id, parent_id FROM gis.territories WHERE id = ?
    UNION ALL
    SELECT t.id, t.parent_id FROM gis.territories t JOIN ancestors a ON t.id = a.parent_id
)
SELECT EXISTS (
    SELECT 1
    FROM metadata.feature_class_territory_disabled d
    JOIN ancestors a ON a.id = d.territory_id
    WHERE d.feature_class_id = ?
)`, territoryID, featureClassID).Scan(ctx, &exists)
	if err != nil {
		return false, fmt.Errorf("crud: checking class availability for territory: %w", err)
	}
	return exists, nil
}
