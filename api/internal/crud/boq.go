package crud

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/kwaabs/voltedge/api/internal/drafts"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// Bill-of-Quantities scoped scan (Slice 1). The BoQ engine (api/internal/boq)
// owns pricing and assembly; this file owns the one thing that must live in crud
// -- a per-class, fully read-scoped quantity roll-up over the physical table.
// Every narrowing the normal read path applies (soft-delete, Phase 58 territory
// read-enforcement, Phase 61 private-drafts visibility, an optional spatial
// scope) is applied here too, so a scoped user can never price features they
// couldn't list.

// BoQScope is the spatial / identity narrowing for one class's quantity scan.
// Kind is "", "none", "territory", "radius" or "bbox"; the territory/radius
// shapes reuse the aggregate engine's spatialScopeWhere, bbox is a coarse
// envelope intersection. FeatureIDs, when non-empty, restricts the scan to those
// ids (the explicit trace-result / map-selection scope) -- AND-ed with any
// spatial scope. All fields are optional; a zero BoQScope scans the whole class.
type BoQScope struct {
	Kind        string
	TerritoryID string
	Center      []float64 // [lon, lat] for radius
	RadiusM     float64
	BBox        []float64 // [minLon, minLat, maxLon, maxLat]
	FeatureIDs  []string
}

// IsSpatial reports whether the scope narrows by geometry (territory/radius/bbox)
// -- the engine uses it to skip geometry-less classes, which such a scope can
// neither locate nor carry a territory_id for.
func (s BoQScope) IsSpatial() bool {
	switch strings.ToLower(strings.TrimSpace(s.Kind)) {
	case "territory", "radius", "bbox":
		return true
	default:
		return false
	}
}

// BoQGroup is one grouped bucket of a class's scan: a (subtype, material) pair
// with its feature Count and, for a linear class, the summed geodesic LengthKm.
// Subtype is nil for a class without subtypes; MaterialCode is nil for a class
// with no material-backed field (or a feature that left it unset).
type BoQGroup struct {
	Subtype      *int
	MaterialCode *string
	Count        int
	LengthKm     float64
}

// BoQOverrideRow is one overridden feature's own quantity line -- its FeatureID,
// bucket identity (Subtype, MaterialCode) and, for a linear class, its geodesic
// LengthKm (a discrete feature counts as one). The BoQ engine pairs it with the
// feature's override rate (metadata.feature_cost_overrides.unit_cost); it is
// returned per-feature, not grouped, because each override carries its own rate.
type BoQOverrideRow struct {
	FeatureID    string
	Subtype      *int
	MaterialCode *string
	LengthKm     float64
}

// ClassQuantities rolls one feature class's in-scope rows up into (subtype,
// material) buckets. materialField is the class's material-backed coded field
// (the value that resolves to a material_specs cost), or "" when it has none.
// linear asks for the geodesic length sum (true only for a line-geometry class
// priced per km); a discrete class passes false and every bucket's LengthKm is 0.
// excludeIDs drops specific features from the roll-up -- the BoQ engine passes
// the ids that carry a per-feature override so they are priced individually (by
// ClassOverrideRows) instead of folded into a catalog-rated bucket.
func ClassQuantities(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, materialField string, linear bool, scope BoQScope, excludeIDs []string) ([]BoQGroup, error) {
	table, err := physicalTable(loaded.FeatureClass)
	if err != nil {
		return nil, err
	}

	whereSQL, args, err := boqWhere(ctx, db, loaded, scope)
	if err != nil {
		return nil, err
	}
	if len(excludeIDs) > 0 {
		placeholders := make([]string, len(excludeIDs))
		for i, id := range excludeIDs {
			placeholders[i] = "?"
			args = append(args, id)
		}
		whereSQL += " AND id::text NOT IN (" + strings.Join(placeholders, ", ") + ")"
	}

	hasSubtype := len(loaded.Subtypes) > 0
	var selectParts, groupParts []string
	if hasSubtype {
		selectParts = append(selectParts, "subtype")
		groupParts = append(groupParts, "subtype")
	} else {
		selectParts = append(selectParts, "NULL::integer AS subtype")
	}
	if materialField != "" {
		col := quoteIdent(materialField)
		selectParts = append(selectParts, col+"::text AS material_code")
		groupParts = append(groupParts, col)
	} else {
		selectParts = append(selectParts, "NULL::text AS material_code")
	}
	selectParts = append(selectParts, "COUNT(*) AS cnt")
	if linear {
		// Geodesic length in km, matching export's geoLengthMeters/haversine: the
		// geometry (stored SRID) transformed to 4326 geography so ST_Length is true
		// metres. NULL geom (an unplaced geometry_optional row) contributes 0.
		selectParts = append(selectParts, "COALESCE(SUM(ST_Length(ST_Transform(geom, 4326)::geography)), 0)::double precision / 1000 AS length_km")
	} else {
		selectParts = append(selectParts, "0::double precision AS length_km")
	}

	q := fmt.Sprintf("SELECT %s FROM %s WHERE %s", strings.Join(selectParts, ", "), table, whereSQL)
	if len(groupParts) > 0 {
		q += " GROUP BY " + strings.Join(groupParts, ", ")
	}

	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("crud: boq quantities: %w", err)
	}
	defer rows.Close()

	var groups []BoQGroup
	for rows.Next() {
		var (
			subtype  sql.NullInt64
			material sql.NullString
			cnt      int
			lengthKm float64
		)
		if err := rows.Scan(&subtype, &material, &cnt, &lengthKm); err != nil {
			return nil, fmt.Errorf("crud: boq scan: %w", err)
		}
		g := BoQGroup{Count: cnt, LengthKm: lengthKm}
		if subtype.Valid {
			v := int(subtype.Int64)
			g.Subtype = &v
		}
		if material.Valid && material.String != "" {
			v := material.String
			g.MaterialCode = &v
		}
		groups = append(groups, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("crud: boq rows: %w", err)
	}
	return groups, nil
}

// ClassOverrideRows returns one row per in-scope feature whose id is in
// includeIDs -- the features that carry a per-feature cost override -- with its
// (subtype, material) identity and, for a linear class, its own geodesic
// LengthKm (a discrete feature's quantity is 1). Unlike ClassQuantities it does
// NOT group, because each override has its own rate (held in
// metadata.feature_cost_overrides and paired in by the engine). includeIDs must
// be non-empty; the same scope narrowing (soft-delete, spatial, read-scope,
// drafts) still applies, so a scoped user can't price an override they couldn't
// see.
func ClassOverrideRows(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, materialField string, linear bool, scope BoQScope, includeIDs []string) ([]BoQOverrideRow, error) {
	if len(includeIDs) == 0 {
		return nil, nil
	}
	table, err := physicalTable(loaded.FeatureClass)
	if err != nil {
		return nil, err
	}

	whereSQL, args, err := boqWhere(ctx, db, loaded, scope)
	if err != nil {
		return nil, err
	}
	placeholders := make([]string, len(includeIDs))
	for i, id := range includeIDs {
		placeholders[i] = "?"
		args = append(args, id)
	}
	whereSQL += " AND id::text IN (" + strings.Join(placeholders, ", ") + ")"

	selectParts := []string{"id::text AS feature_id"}
	if len(loaded.Subtypes) > 0 {
		selectParts = append(selectParts, "subtype")
	} else {
		selectParts = append(selectParts, "NULL::integer AS subtype")
	}
	if materialField != "" {
		selectParts = append(selectParts, quoteIdent(materialField)+"::text AS material_code")
	} else {
		selectParts = append(selectParts, "NULL::text AS material_code")
	}
	if linear {
		selectParts = append(selectParts, "COALESCE(ST_Length(ST_Transform(geom, 4326)::geography), 0)::double precision / 1000 AS length_km")
	} else {
		selectParts = append(selectParts, "0::double precision AS length_km")
	}

	q := fmt.Sprintf("SELECT %s FROM %s WHERE %s", strings.Join(selectParts, ", "), table, whereSQL)
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("crud: boq override rows: %w", err)
	}
	defer rows.Close()

	var out []BoQOverrideRow
	for rows.Next() {
		var (
			featureID string
			subtype   sql.NullInt64
			material  sql.NullString
			lengthKm  float64
		)
		if err := rows.Scan(&featureID, &subtype, &material, &lengthKm); err != nil {
			return nil, fmt.Errorf("crud: boq override scan: %w", err)
		}
		r := BoQOverrideRow{FeatureID: featureID, LengthKm: lengthKm}
		if subtype.Valid {
			v := int(subtype.Int64)
			r.Subtype = &v
		}
		if material.Valid && material.String != "" {
			v := material.String
			r.MaterialCode = &v
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("crud: boq override rows: %w", err)
	}
	return out, nil
}

// boqWhere assembles the shared WHERE for a quantity scan: soft-delete, the
// optional spatial scope (territory/radius via spatialScopeWhere, bbox here), an
// optional id restriction, then the Phase 58 territory read-enforcement and
// Phase 61 private-drafts predicates -- the same narrowing aggregateWhere applies,
// minus the free-form filter tree a BoQ has no use for.
func boqWhere(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, scope BoQScope) (string, []any, error) {
	where := []string{"deleted_at IS NULL"}
	args := make([]any, 0, 8)

	kind := strings.ToLower(strings.TrimSpace(scope.Kind))
	switch kind {
	case "", "none":
		// no spatial narrowing
	case "territory", "radius":
		spatialSQL, spatialArgs, err := spatialScopeWhere(ctx, db, loaded, &querySpatialScope{
			Kind:        kind,
			TerritoryID: scope.TerritoryID,
			Center:      scope.Center,
			RadiusM:     scope.RadiusM,
		})
		if err != nil {
			return "", nil, err
		}
		if spatialSQL != "" {
			where = append(where, spatialSQL)
			args = append(args, spatialArgs...)
		}
	case "bbox":
		if loaded.FeatureClass.GeometryType == "none" {
			return "", nil, invalid("feature class %q has no geometry, so it can't be filtered by bbox", loaded.FeatureClass.Code)
		}
		if len(scope.BBox) != 4 {
			return "", nil, invalid("a bbox scope needs [min_lon, min_lat, max_lon, max_lat]")
		}
		// Coarse envelope intersection against the geometry transformed to 4326 --
		// enough to scope a BoQ; not the exact containment an analysis would want.
		where = append(where, "geom IS NOT NULL AND ST_Transform(geom, 4326) && ST_MakeEnvelope(?, ?, ?, ?, 4326)")
		args = append(args, scope.BBox[0], scope.BBox[1], scope.BBox[2], scope.BBox[3])
	default:
		return "", nil, invalid("unknown scope kind %q", scope.Kind)
	}

	if len(scope.FeatureIDs) > 0 {
		placeholders := make([]string, len(scope.FeatureIDs))
		for i, id := range scope.FeatureIDs {
			placeholders[i] = "?"
			args = append(args, id)
		}
		where = append(where, "id::text IN ("+strings.Join(placeholders, ", ")+")")
	}

	// Phase 58 spatial read enforcement (no-op unless on and the user is scoped).
	scopeIDs, scopeApply, err := readScope(ctx, db, loaded)
	if err != nil {
		return "", nil, err
	}
	if clause, scopeArgs := scopeWhere(scopeIDs, scopeApply); clause != "" {
		where = append(where, clause)
		args = append(args, scopeArgs...)
	}

	// Private-drafts (Phase 61): hide never-published features from non-authors.
	draftAuthors, draftApply, err := drafts.VisibleAuthors(ctx, db)
	if err != nil {
		return "", nil, err
	}
	if clause, draftArgs := drafts.WhereClause(draftAuthors, draftApply, ""); clause != "" {
		where = append(where, clause)
		args = append(args, draftArgs...)
	}

	return strings.Join(where, " AND "), args, nil
}
