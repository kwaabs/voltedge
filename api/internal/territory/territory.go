package territory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/uptrace/bun"
)

// InvalidError is a client-fixable problem (unknown level, a child whose
// level isn't one below its parent's, a boundary outside its parent) --
// distinct from a plain error so an HTTP caller can map it to a 400
// rather than a 500, the same split api/internal/crud draws with its own
// validationError. Callers test with IsInvalid.
type InvalidError struct{ msg string }

func (e *InvalidError) Error() string { return e.msg }

func invalidf(format string, args ...any) error {
	return &InvalidError{msg: fmt.Sprintf(format, args...)}
}

// IsInvalid reports whether err is (or wraps) an InvalidError.
func IsInvalid(err error) bool {
	var ie *InvalidError
	return errors.As(err, &ie)
}

// selectColumns is the read shape shared by ListTerritories/GetTerritory:
// every scalar column plus the boundary rendered as GeoJSON text (geom
// itself never scans cleanly into a Go value, so it's only ever read back
// through ST_AsGeoJSON, aliased to the scanonly geom_geo_json field).
const selectColumns = "id, parent_id, level_id, name, created_at, updated_at"

// ListLevels returns the level catalog ordered top-first (rank ascending)
// -- a small, global set, fetched whole for a picker.
func ListLevels(ctx context.Context, db bun.IDB) ([]Level, error) {
	var levels []Level
	if err := db.NewSelect().Model(&levels).Order("rank").Scan(ctx); err != nil {
		return nil, fmt.Errorf("territory: listing levels: %w", err)
	}
	return levels, nil
}

// GetLevel fetches one level by id.
func GetLevel(ctx context.Context, db bun.IDB, id string) (*Level, error) {
	l := new(Level)
	if err := db.NewSelect().Model(l).Where("id = ?", id).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, invalidf("territory level %q does not exist", id)
		}
		return nil, fmt.Errorf("territory: loading level %s: %w", id, err)
	}
	return l, nil
}

// MinFeatureLevel returns the single level flagged is_min_feature_level --
// the shallowest a geographically-placed feature is required to reach
// ("at least a district"). The seed guarantees exactly one exists; a
// missing flag is a real misconfiguration, surfaced as an error rather
// than silently disabling the invariant.
func MinFeatureLevel(ctx context.Context, db bun.IDB) (*Level, error) {
	l := new(Level)
	if err := db.NewSelect().Model(l).Where("is_min_feature_level").Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("territory: no level is flagged is_min_feature_level -- the level catalog is misconfigured")
		}
		return nil, fmt.Errorf("territory: loading min feature level: %w", err)
	}
	return l, nil
}

// ListTerritories returns the whole tree (with boundaries as GeoJSON),
// ordered by name -- a client assembles the hierarchy from parent_id
// itself. The set is small (administrative areas, not features), so it's
// fetched whole rather than paged.
func ListTerritories(ctx context.Context, db bun.IDB) ([]Territory, error) {
	var territories []Territory
	if err := db.NewSelect().Model(&territories).
		ColumnExpr(selectColumns).
		ColumnExpr("ST_AsGeoJSON(geom) AS geom_geo_json").
		Order("name").
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("territory: listing territories: %w", err)
	}
	return territories, nil
}

// GetTerritory fetches one territory by id, boundary included.
func GetTerritory(ctx context.Context, db bun.IDB, id string) (*Territory, error) {
	t := new(Territory)
	if err := db.NewSelect().Model(t).
		ColumnExpr(selectColumns).
		ColumnExpr("ST_AsGeoJSON(geom) AS geom_geo_json").
		Where("id = ?", id).
		Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, invalidf("territory %q does not exist", id)
		}
		return nil, fmt.Errorf("territory: loading territory %s: %w", id, err)
	}
	return t, nil
}

// CreateParams is the input to CreateTerritory. GeomGeoJSON is optional
// (a territory can exist before its boundary is drawn) and may be either a
// GeoJSON Polygon or MultiPolygon -- ST_Multi coerces a bare polygon into
// the MultiPolygon the column stores.
type CreateParams struct {
	ParentID    *string
	LevelID     string
	Name        string
	GeomGeoJSON *string
}

// CreateTerritory inserts a territory after enforcing the two structural
// invariants a single-row CHECK can't: the child's level must sit exactly
// one rank below its parent's (or be rank 0 at the root), and -- when both
// the child and its parent have a boundary -- the child's must fall within
// the parent's.
func CreateTerritory(ctx context.Context, db bun.IDB, p CreateParams) (*Territory, error) {
	if strings.TrimSpace(p.Name) == "" {
		return nil, invalidf("territory name is required")
	}
	level, err := GetLevel(ctx, db, p.LevelID)
	if err != nil {
		return nil, err
	}
	if err := validatePlacement(ctx, db, p.ParentID, level, p.GeomGeoJSON); err != nil {
		return nil, err
	}

	cols := []string{"parent_id", "level_id", "name"}
	placeholders := []string{"?", "?", "?"}
	args := []any{p.ParentID, p.LevelID, strings.TrimSpace(p.Name)}
	if p.GeomGeoJSON != nil {
		cols = append(cols, "geom")
		placeholders = append(placeholders, "ST_Multi(ST_SetSRID(ST_GeomFromGeoJSON(?), 4326))")
		args = append(args, *p.GeomGeoJSON)
	}

	query := fmt.Sprintf("INSERT INTO gis.territories (%s) VALUES (%s) RETURNING id",
		strings.Join(cols, ", "), strings.Join(placeholders, ", "))
	var id string
	if err := db.QueryRowContext(ctx, query, args...).Scan(&id); err != nil {
		return nil, fmt.Errorf("territory: inserting territory: %w", err)
	}
	return GetTerritory(ctx, db, id)
}

// UpdateParams is the input to UpdateTerritory. Every field is optional --
// only what's set is changed. GeomGeoJSON replaces the boundary; ClearGeom
// removes it (the two are mutually exclusive -- ClearGeom wins if both are
// set). ReParent moves the node under a new parent (ParentID, nil = make it
// a root); the node's level is kept, so the move is only legal to a parent
// exactly one rank above -- re-leveling is still not offered (it would
// re-open every descendant's rank).
type UpdateParams struct {
	Name        *string
	GeomGeoJSON *string
	ClearGeom   bool
	ReParent    bool
	ParentID    *string
}

// UpdateTerritory edits a territory's name, boundary, and/or parent. Any
// structural change (a new boundary or a new parent) is re-checked against
// the parent: the node's level must remain exactly one below the parent's
// and its boundary must fall within the parent's. A re-parent additionally
// rejects a move under the node's own descendant (which would create a
// cycle). Note: this does NOT re-validate that existing child territories
// still fall within an edited boundary, nor does it re-assign any features
// -- boundary edits never silently relabel placed data, per the phase's
// design (docs/phase-11-territories.md). Because the level is unchanged, a
// re-parent leaves the moved subtree's internal ranks intact; only the
// node's own containment within its new parent is re-checked (descendants
// remain within the node, hence transitively within the new parent).
func UpdateTerritory(ctx context.Context, db bun.IDB, id string, p UpdateParams) (*Territory, error) {
	existing, err := GetTerritory(ctx, db, id)
	if err != nil {
		return nil, err
	}

	var sets []string
	var args []any

	if p.Name != nil {
		if strings.TrimSpace(*p.Name) == "" {
			return nil, invalidf("territory name cannot be empty")
		}
		sets = append(sets, "name = ?")
		args = append(args, strings.TrimSpace(*p.Name))
	}

	// The parent and boundary after this update -- both feed one placement
	// check (rank one-below-parent + containment). Re-parenting to nil
	// makes a root; a not-yet-drawn boundary skips the containment half.
	targetParentID := existing.ParentID
	if p.ReParent {
		targetParentID = p.ParentID
	}
	var effectiveGeom *string
	switch {
	case p.ClearGeom:
		effectiveGeom = nil
	case p.GeomGeoJSON != nil:
		effectiveGeom = p.GeomGeoJSON
	default:
		effectiveGeom = existing.GeomGeoJSON
	}

	if p.ReParent || p.GeomGeoJSON != nil {
		if p.ReParent {
			cycles, err := wouldCreateCycle(ctx, db, id, p.ParentID)
			if err != nil {
				return nil, err
			}
			if cycles {
				return nil, invalidf("can't move a territory under itself or one of its own descendants")
			}
		}
		level, err := GetLevel(ctx, db, existing.LevelID)
		if err != nil {
			return nil, err
		}
		if err := validatePlacement(ctx, db, targetParentID, level, effectiveGeom); err != nil {
			return nil, err
		}
	}

	if p.ReParent {
		sets = append(sets, "parent_id = ?")
		args = append(args, p.ParentID)
	}

	switch {
	case p.ClearGeom:
		sets = append(sets, "geom = NULL")
	case p.GeomGeoJSON != nil:
		sets = append(sets, "geom = ST_Multi(ST_SetSRID(ST_GeomFromGeoJSON(?), 4326))")
		args = append(args, *p.GeomGeoJSON)
	}

	if len(sets) == 0 {
		return nil, invalidf("update body has nothing to change")
	}
	sets = append(sets, "updated_at = now()")
	args = append(args, id)

	query := fmt.Sprintf("UPDATE gis.territories SET %s WHERE id = ?", strings.Join(sets, ", "))
	if _, err := db.ExecContext(ctx, query, args...); err != nil {
		return nil, fmt.Errorf("territory: updating territory %s: %w", id, err)
	}
	return GetTerritory(ctx, db, id)
}

// wouldCreateCycle reports whether re-parenting nodeID under newParentID
// would form a cycle -- i.e. newParentID is nodeID itself or one of its
// descendants. It walks the ancestor chain of the proposed parent upward
// and looks for nodeID: if the node we're moving is already an ancestor of
// its intended parent, the move would close a loop. A nil newParentID
// (making the node a root) can never cycle.
func wouldCreateCycle(ctx context.Context, db bun.IDB, nodeID string, newParentID *string) (bool, error) {
	if newParentID == nil {
		return false, nil
	}
	const query = `
WITH RECURSIVE ancestors AS (
    SELECT id, parent_id FROM gis.territories WHERE id = ?
    UNION ALL
    SELECT t.id, t.parent_id FROM gis.territories t
    JOIN ancestors a ON t.id = a.parent_id
)
SELECT EXISTS (SELECT 1 FROM ancestors WHERE id = ?)`
	var cycles bool
	if err := db.QueryRowContext(ctx, query, *newParentID, nodeID).Scan(&cycles); err != nil {
		return false, fmt.Errorf("territory: checking for re-parent cycle: %w", err)
	}
	return cycles, nil
}

// DeleteTerritory removes a leaf territory. A territory with children is
// refused with a clear message rather than letting the parent_id foreign
// key raise a raw 500 -- delete bottom-up. (Features that reference this
// territory via their territory_id column are not checked here; that guard
// arrives with write-time enforcement, see docs/phase-11-territories.md.)
func DeleteTerritory(ctx context.Context, db bun.IDB, id string) error {
	if _, err := GetTerritory(ctx, db, id); err != nil {
		return err
	}
	childCount, err := db.NewSelect().Model((*Territory)(nil)).Where("parent_id = ?", id).Count(ctx)
	if err != nil {
		return fmt.Errorf("territory: counting children of %s: %w", id, err)
	}
	if childCount > 0 {
		return invalidf("territory has %d child territories -- delete or move them first", childCount)
	}
	if _, err := db.NewDelete().Model((*Territory)(nil)).Where("id = ?", id).Exec(ctx); err != nil {
		return fmt.Errorf("territory: deleting territory %s: %w", id, err)
	}
	return nil
}

// validatePlacement enforces the rank and containment rules for a
// territory about to be written, given its intended parent, its level, and
// (optionally) its boundary.
func validatePlacement(ctx context.Context, db bun.IDB, parentID *string, level *Level, geomGeoJSON *string) error {
	if parentID == nil {
		if level.Rank != 0 {
			return invalidf("a root territory (no parent) must be at the top level (rank 0), but %q is rank %d", level.Code, level.Rank)
		}
		return nil
	}

	parent, err := GetTerritory(ctx, db, *parentID)
	if err != nil {
		return err
	}
	parentLevel, err := GetLevel(ctx, db, parent.LevelID)
	if err != nil {
		return err
	}
	if level.Rank != parentLevel.Rank+1 {
		return invalidf("a %q (rank %d) can't sit directly under a %q (rank %d) -- its level must be exactly one below its parent's",
			level.Code, level.Rank, parentLevel.Code, parentLevel.Rank)
	}

	// Boundary containment only when both sides actually have geometry --
	// a not-yet-drawn parent or child boundary is nothing to check.
	if geomGeoJSON == nil {
		return nil
	}
	query := `SELECT ST_Contains(geom, ST_Multi(ST_SetSRID(ST_GeomFromGeoJSON(?), 4326)))
	          FROM gis.territories WHERE id = ? AND geom IS NOT NULL`
	var contained bool
	err = db.QueryRowContext(ctx, query, *geomGeoJSON, *parentID).Scan(&contained)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // parent has no boundary yet -- nothing to contain within
	}
	if err != nil {
		return fmt.Errorf("territory: checking parent containment: %w", err)
	}
	if !contained {
		return invalidf("this boundary falls outside its parent territory %q -- a child must lie within its parent", parent.Name)
	}
	return nil
}
