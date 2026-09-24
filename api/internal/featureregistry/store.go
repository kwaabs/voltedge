package featureregistry

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/kwaabs/voltedge/api/internal/drafts"
	"github.com/uptrace/bun"
)

// Scope carries the per-request visibility constraints the registry read path
// applies, resolved from the caller's session so the registry hides exactly what
// the CRUD read side hides: a Phase-58 territory set and a Phase-61 draft-author
// set. Each has its own Apply flag because an empty slice is meaningful --
// TerritoryIDs empty+apply means "scoped to nothing", and DraftAuthors is always
// non-empty when it applies (it holds at least the caller's own id). A zero Scope
// (both Apply false) is an unscoped caller: enforcement off, unscoped/break-glass,
// or an internal no-session call.
type Scope struct {
	TerritoryIDs   []string
	ApplyTerritory bool
	DraftAuthors   []string
	ApplyDrafts    bool
}

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

// scopeDrafts constrains a registry query to the Phase-61 private-drafts filter,
// reusing the exact predicate the CRUD read side AND-s in (drafts.WhereClause with
// the fr. alias): a row survives if it is public (currently published, or ever
// published via the sticky published_at marker) OR authored by someone in the
// caller's visible set (self + subordinates). Registry rows now carry created_by /
// published_at (migration 0118), populated generically by the sync trigger, so an
// out-of-scope peer draft is hidden by id here just as its attributes are hidden
// elsewhere. apply is false -- no draft predicate -- when drafts are off, or the
// caller is unscoped/break-glass, or an internal no-session call.
func scopeDrafts(q *bun.SelectQuery, authors []string, apply bool) *bun.SelectQuery {
	clause, args := drafts.WhereClause(authors, apply, "fr.")
	if clause == "" {
		return q
	}
	return q.Where(clause, args...)
}

// applyScope AND-s both visibility filters (territory + drafts) onto a query.
func applyScope(q *bun.SelectQuery, sc Scope) *bun.SelectQuery {
	q = scopeTerritory(q, sc.TerritoryIDs, sc.ApplyTerritory)
	q = scopeDrafts(q, sc.DraftAuthors, sc.ApplyDrafts)
	return q
}

// Lookup resolves a single feature by its exact id -- the "type in an asset id,
// what is it?" path. Returns (nil, nil) when no such id exists (a soft-deleted
// row is still returned; the caller decides whether a tombstone is acceptable),
// so the HTTP layer can answer 404 without treating a miss as an error.
func Lookup(ctx context.Context, db bun.IDB, id string, sc Scope) (*Entry, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, nil
	}
	var e Entry
	q := applyScope(baseSelect(db, &e).Where("fr.feature_id = ?", id), sc)
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
// hides soft-deleted rows (the picker default). Scope carries the Phase-58
// territory + Phase-61 draft visibility constraints, applied identically to the
// CRUD read side so the picker can't surface an id the reader couldn't open.
type SearchFilter struct {
	Q     string
	Class string
	Live  bool
	Scope Scope
	Limit int
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
	// AND in the same territory (Phase 58) + draft (Phase 61) visibility filters
	// the CRUD read side applies, so the picker never surfaces an id its owner
	// couldn't open.
	q = applyScope(q, f.Scope)

	if err := q.OrderExpr("fr.created_at DESC NULLS LAST").Limit(limit).Scan(ctx); err != nil {
		return nil, err
	}
	return entries, nil
}
