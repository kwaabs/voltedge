// Package spatial is the geometry-derived spatial engine: it answers "what
// features are inside / overlap / are near this one?" straight from the PostGIS
// geometry, across the compiled per-class gis.<code> tables. It is the companion
// to the land/spatial classes (easement, parcel, corridor, vegetation): "what
// assets fall inside this easement?", "what land covers this pole?", "what
// vegetation is within 5 m of this line?". Unlike the connectivity/relationship
// engines -- which read EXPLICIT, authored links -- this derives the answer from
// the shapes, so nothing has to be wired first.
//
// Read-only. Class codes in a request are resolved to their physical table
// through metadata (a whitelist), so a table name is never taken from the request;
// feature ids are bound parameters. The reference feature's geometry is fetched by
// a subquery, never round-tripped through Go.
package spatial

import (
	"context"
	"fmt"
	"sort"

	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// Spatial relationship modes.
const (
	// ModeIntersects -- any geometric overlap (symmetric). The default.
	ModeIntersects = "intersects"
	// ModeContains -- the reference feature's geometry fully contains the target's
	// (e.g. assets strictly inside an easement polygon).
	ModeContains = "contains"
	// ModeContainedBy -- the target's geometry contains the reference's (e.g. the
	// land parcels / easements that cover an asset).
	ModeContainedBy = "contained_by"
	// ModeWithin -- the target is within DistanceM metres of the reference
	// (geodesic, via ::geography). Returns each match's distance.
	ModeWithin = "within"
)

var validModes = map[string]bool{
	ModeIntersects: true, ModeContains: true, ModeContainedBy: true, ModeWithin: true,
}

// Match is one feature found by a spatial query. DistanceM is set only for a
// ModeWithin query (metres to the reference feature).
type Match struct {
	FeatureClass string   `json:"feature_class"`
	FeatureID    string   `json:"feature_id"`
	DistanceM    *float64 `json:"distance_m,omitempty"`
}

// QueryInput asks for the features spatially related to one reference feature.
// TargetClasses narrows which classes to search (empty = every geometric class).
// DistanceM is required (and only used) for ModeWithin. Limit caps the result
// (default 500, max 5000).
type QueryInput struct {
	RefClass      string
	RefID         string
	TargetClasses []string
	Mode          string
	DistanceM     float64
	Limit         int
}

// geomClass is a resolved geometric class: its code and physical table.
type geomClass struct{ code, table string }

// Query returns the features spatially related to the reference feature under the
// requested mode. The reference feature itself is never included.
func Query(ctx context.Context, db bun.IDB, in QueryInput) ([]Match, error) {
	if in.RefClass == "" || in.RefID == "" {
		return nil, invalidf("feature_class and feature_id are required")
	}
	mode := in.Mode
	if mode == "" {
		mode = ModeIntersects
	}
	if !validModes[mode] {
		return nil, invalidf("mode %q is not one of intersects, contains, contained_by, within", mode)
	}
	if mode == ModeWithin && in.DistanceM <= 0 {
		return nil, invalidf("distance (metres, > 0) is required for a within query")
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 500
	}
	if limit > 5000 {
		limit = 5000
	}

	classes, err := metadata.ListFeatureClasses(ctx, db)
	if err != nil {
		return nil, err
	}
	// code -> physical table, only geometric + compiled classes.
	tableByCode := make(map[string]string, len(classes))
	for _, c := range classes {
		if c.PhysicalTable == nil || *c.PhysicalTable == "" {
			continue
		}
		switch c.GeometryType {
		case "point", "line", "polygon":
			tableByCode[c.Code] = *c.PhysicalTable
		}
	}

	refTable, ok := tableByCode[in.RefClass]
	if !ok {
		return nil, invalidf("feature class %q is not a compiled geometric class", in.RefClass)
	}

	// Resolve the target set: the requested classes (skipping any non-geometric or
	// unknown), or every geometric class when none are named.
	targets := make([]geomClass, 0, len(tableByCode))
	if len(in.TargetClasses) > 0 {
		for _, code := range in.TargetClasses {
			if t, ok := tableByCode[code]; ok {
				targets = append(targets, geomClass{code, t})
			}
		}
		if len(targets) == 0 {
			return nil, invalidf("none of the target classes are compiled geometric classes")
		}
	} else {
		for code, t := range tableByCode {
			targets = append(targets, geomClass{code, t})
		}
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].code < targets[j].code })

	// The reference geometry, fetched by subquery inside each predicate so it never
	// leaves the database. refTable is trusted (resolved from metadata above).
	refGeom := fmt.Sprintf("(SELECT geom FROM %s WHERE id = ?)", refTable)

	out := make([]Match, 0, 32)
	for _, tc := range targets {
		rows, err := queryOneClass(ctx, db, tc, refTable, refGeom, in.RefClass, in.RefID, mode, in.DistanceM, limit-len(out))
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
		if len(out) >= limit {
			out = out[:limit]
			break
		}
	}

	// A within query is most useful nearest-first; otherwise a stable class/id order.
	if mode == ModeWithin {
		sort.SliceStable(out, func(i, j int) bool {
			di, dj := 0.0, 0.0
			if out[i].DistanceM != nil {
				di = *out[i].DistanceM
			}
			if out[j].DistanceM != nil {
				dj = *out[j].DistanceM
			}
			return di < dj
		})
	}
	return out, nil
}

// queryOneClass runs the spatial predicate against one target class's table.
func queryOneClass(ctx context.Context, db bun.IDB, tc geomClass, refTable, refGeom, refClass, refID, mode string, distanceM float64, limit int) ([]Match, error) {
	if limit <= 0 {
		return nil, nil
	}

	var pred, distExpr string
	args := []any{}
	switch mode {
	case ModeIntersects:
		pred = "ST_Intersects(t.geom, " + refGeom + ")"
		distExpr = "NULL::float8"
		args = append(args, refID)
	case ModeContains:
		pred = "ST_Contains(" + refGeom + ", t.geom)"
		distExpr = "NULL::float8"
		args = append(args, refID)
	case ModeContainedBy:
		pred = "ST_Contains(t.geom, " + refGeom + ")"
		distExpr = "NULL::float8"
		args = append(args, refID)
	case ModeWithin:
		// Distance in metres uses the geography cast; the ref geom is referenced in
		// both the distance expression and the predicate, so refID binds twice.
		distExpr = "ST_Distance(t.geom::geography, " + refGeom + "::geography)"
		args = append(args, refID)
		pred = fmt.Sprintf("ST_DWithin(t.geom::geography, %s::geography, %f)", refGeom, distanceM)
		args = append(args, refID)
	}

	query := fmt.Sprintf(
		"SELECT t.id::text AS feature_id, %s AS distance_m FROM %s AS t WHERE t.deleted_at IS NULL AND t.geom IS NOT NULL AND %s",
		distExpr, tc.table, pred,
	)
	// Exclude the reference feature itself when scanning its own class.
	if tc.table == refTable {
		query += " AND t.id <> ?"
		args = append(args, refID)
	}
	query += fmt.Sprintf(" LIMIT %d", limit)

	var rows []struct {
		FeatureID string   `bun:"feature_id"`
		DistanceM *float64 `bun:"distance_m"`
	}
	if err := db.NewRaw(query, args...).Scan(ctx, &rows); err != nil {
		return nil, fmt.Errorf("spatial: querying %s: %w", tc.code, err)
	}

	matches := make([]Match, 0, len(rows))
	for _, r := range rows {
		matches = append(matches, Match{FeatureClass: tc.code, FeatureID: r.FeatureID, DistanceM: r.DistanceM})
	}
	return matches, nil
}
