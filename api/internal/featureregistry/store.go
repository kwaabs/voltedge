package featureregistry

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/uptrace/bun"
)

// baseSelect reads registry rows and left-joins the class's human name from
// metadata.feature_classes (feature_registry stores only the code). territory_id
// is cast to text so the nullable uuid scans into *string, matching the id
// treatment the faults/oms/wam stores use.
func baseSelect(db bun.IDB, dst any) *bun.SelectQuery {
	return db.NewSelect().Model(dst).
		ColumnExpr("fr.feature_id::text AS feature_id").
		ColumnExpr("fr.feature_class_code").
		ColumnExpr("fc.name AS class_name").
		ColumnExpr("fr.status").
		ColumnExpr("fr.deleted_at").
		ColumnExpr("fr.created_at").
		ColumnExpr("fr.territory_id::text AS territory_id").
		Join("LEFT JOIN metadata.feature_classes AS fc ON fc.code = fr.feature_class_code")
}

// scopeTerritory constrains a registry query to a Phase-58 scoped caller's
// accessible territories, mirroring crud.readScope/enforceReadScope exactly so
// the registry can't surface an id the CRUD read side would hide:
//
//   - a geometry-less class (fc.geometry_type = 'none') is never territory-scoped
//     -- it has no territory_id -- so it stays visible, matching readScope's
//     geometry-less exemption;
//   - every other row must sit in the caller's territory set; a geometry feature
//     with a NULL territory_id is therefore hidden (NULL IN (...) is never true),
//     the same fail-closed answer enforceReadScope gives.
//
// An empty (non-nil) id set is "scoped to nothing": only the geometry-less
// classes remain. Callers pass apply=false to skip scoping (enforcement off,
// unscoped/break-glass user, or an internal no-session caller).
func scopeTerritory(q *bun.SelectQuery, ids []string, apply bool) *bun.SelectQuery {
	if !apply {
		return q
	}
	if len(ids) == 0 {
		return q.Where("fc.geometry_type = 'none'")
	}
	return q.Where("fc.geometry_type = 'none' OR fr.territory_id IN (?)", bun.In(ids))
}

// Lookup resolves a single feature by its exact id -- the "type in an asset id,
// what is it?" path. Returns (nil, nil) when no such id exists (a soft-deleted
// row is still returned; the caller decides whether a tombstone is acceptable),
// so the HTTP layer can answer 404 without treating a miss as an error.
func Lookup(ctx context.Context, db bun.IDB, id string, scope []string, applyScope bool) (*Entry, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, nil
	}
	var e Entry
	q := scopeTerritory(baseSelect(db, &e).Where("fr.feature_id = ?", id), scope, applyScope)
	err := q.Limit(1).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		// Either no such id, or the id exists but is out of the caller's scope --
		// both answer "not found", so an out-of-territory id can't be confirmed.
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &e, nil
}

// SearchFilter drives the typeahead. Q is an id prefix (case-insensitive), the
// natural input as a user types a uuid; Class scopes to one feature class; Live
// hides soft-deleted rows (the picker default). TerritoryIDs, when non-nil,
// scopes to a Phase-58 user's accessible territories -- an empty slice means
// "no accessible territory", which correctly returns nothing.
type SearchFilter struct {
	Q            string
	Class        string
	Live         bool
	TerritoryIDs []string
	Limit        int
}

// Search returns registry entries matching the filter, newest first, capped.
// It backs the asset-id picker: prefix-match on id, optionally within a class
// and the caller's territories.
func Search(ctx context.Context, db bun.IDB, f SearchFilter) ([]Entry, error) {
	limit := f.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	var entries []Entry
	q := baseSelect(db, &entries)

	if class := strings.TrimSpace(f.Class); class != "" {
		q = q.Where("fr.feature_class_code = ?", class)
	}
	if prefix := strings.TrimSpace(f.Q); prefix != "" {
		// Prefix, not substring: the class_idx / PK make a left-anchored
		// match cheap, and a user types an id from the front.
		q = q.Where("fr.feature_id::text ILIKE ?", prefix+"%")
	}
	if f.Live {
		q = q.Where("fr.deleted_at IS NULL")
	}
	// A non-nil TerritoryIDs (even empty) is the Phase-58 apply signal; nil means
	// an unscoped caller. scopeTerritory keeps geometry-less classes visible and
	// hides out-of-territory geometry features, matching crud.enforceReadScope.
	q = scopeTerritory(q, f.TerritoryIDs, f.TerritoryIDs != nil)

	if err := q.OrderExpr("fr.created_at DESC NULLS LAST").Limit(limit).Scan(ctx); err != nil {
		return nil, err
	}
	return entries, nil
}
