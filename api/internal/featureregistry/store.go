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

// Lookup resolves a single feature by its exact id -- the "type in an asset id,
// what is it?" path. Returns (nil, nil) when no such id exists (a soft-deleted
// row is still returned; the caller decides whether a tombstone is acceptable),
// so the HTTP layer can answer 404 without treating a miss as an error.
func Lookup(ctx context.Context, db bun.IDB, id string) (*Entry, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, nil
	}
	var e Entry
	err := baseSelect(db, &e).Where("fr.feature_id = ?", id).Limit(1).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
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
	if f.TerritoryIDs != nil {
		if len(f.TerritoryIDs) == 0 {
			return []Entry{}, nil
		}
		q = q.Where("fr.territory_id IN (?)", bun.In(f.TerritoryIDs))
	}

	if err := q.OrderExpr("fr.created_at DESC NULLS LAST").Limit(limit).Scan(ctx); err != nil {
		return nil, err
	}
	return entries, nil
}
