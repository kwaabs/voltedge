package crud

import (
	"context"
	"database/sql"
	"fmt"
	"sort"

	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// This file is the read half of the point-on-line split assist: given a POINT
// feature the user just placed, it proposes the existing line features that point
// landed *on* and could split. It writes nothing -- the write is the untouched
// SplitLine / POST /features/{code}/{id}/split path in split.go, which the caller
// (the assistant, api/internal/assistant) surfaces as a "Split here" suggestion
// chip. A candidate is "rule-free" unlike a connection suggestion: any simple
// LineString the point sits on is splittable, no network_rule needed, so this is a
// plain spatial query rather than a rule-oracle walk.

// splitSuggestDefaultToleranceMeters is how close the placed point must be to a
// line to read as "on" it. Deliberately tight -- unlike the connectivity suggest
// radius (hundreds of metres of "nearby"), a split candidate is a line the user
// *deliberately* dropped a point onto, so anything past about a metre is a
// different line, not a split target. Callers may override it.
const splitSuggestDefaultToleranceMeters = 1.0

// splitSuggestLimit caps how many split candidates one call returns before the
// assistant merges and ranks them. A point sitting on a pile of stacked lines is
// pathological, not a menu worth showing in full.
const splitSuggestLimit = 10

// SplitCandidateFeature is the line a placed point could split.
type SplitCandidateFeature struct {
	FeatureClass string `json:"feature_class"`
	FeatureID    string `json:"feature_id"`
	Name         string `json:"name,omitempty"`
	Subtype      *int   `json:"subtype,omitempty"`
}

// SplitCandidate is one ranked proposal: the line to split, how far the point sat
// from it (metres, geography-measured so it's real-world regardless of SRID), and
// the exact point on the line to cut at, in EPSG:4326 lng/lat -- the wire
// convention the frontend's startSplitDraft and POST /features/{code}/{id}/split
// both already speak (see split.go's computeSplitHalves). The cut point is snapped
// onto the line (ST_ClosestPoint), so a point placed a few centimetres off still
// cuts exactly on the line.
type SplitCandidate struct {
	Feature        SplitCandidateFeature
	DistanceMeters float64
	SplitLng       float64
	SplitLat       float64
}

// SplitSuggestInput identifies the just-placed point feature to look under, plus
// an optional override of how close counts as "on" the line.
type SplitSuggestInput struct {
	FeatureClass    string
	FeatureID       string
	ToleranceMeters float64 // <= 0 -> splitSuggestDefaultToleranceMeters
}

// SuggestSplits proposes the lines the given POINT feature lands on and could
// split. It reads only. A candidate is a compiled, active, non-deleted, simple
// LineString whose closest point to the source is within the tolerance AND lies
// strictly in the line's interior -- a point on (or a hair from) an endpoint is no
// split at all, the same guard SplitLine enforces, reusing splitEndpointEpsilon.
// Returns an empty slice (not an error) when the source isn't a point class or has
// no geometry: only a point dropped onto a line reads as "split here".
func SuggestSplits(ctx context.Context, db bun.IDB, in SplitSuggestInput) ([]SplitCandidate, error) {
	tolerance := in.ToleranceMeters
	if tolerance <= 0 {
		tolerance = splitSuggestDefaultToleranceMeters
	}

	source, err := metadata.Load(ctx, db, in.FeatureClass)
	if err != nil {
		return nil, err
	}
	// Only a point splits a line here. A line/polygon/none source has no single
	// "where to cut" location, so there is nothing to propose; a class that was
	// never compiled has no table to read the point's geometry from.
	if source.FeatureClass.GeometryType != "point" || source.FeatureClass.PhysicalTable == nil {
		return []SplitCandidate{}, nil
	}
	sourceTable := *source.FeatureClass.PhysicalTable

	classes, err := metadata.ListFeatureClasses(ctx, db)
	if err != nil {
		return nil, err
	}

	var out []SplitCandidate
	for i := range classes {
		lineClass := classes[i]
		if lineClass.GeometryType != "line" || lineClass.PhysicalTable == nil || lineClass.SRID == nil {
			continue
		}
		// Load the line class for its subtype shape (whether its physical table
		// even has a subtype column), the same targetHasSubtype test the
		// connectivity suggest engine makes before scanning a candidate table.
		loaded, err := metadata.Load(ctx, db, lineClass.Code)
		if err != nil {
			return nil, fmt.Errorf("crud: split suggest: loading line class %q: %w", lineClass.Code, err)
		}
		rows, err := splitCandidatesForClass(ctx, db, sourceTable, in.FeatureID, loaded.FeatureClass, len(loaded.Subtypes) > 0, tolerance)
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
	}

	sortSplitCandidates(out)
	if len(out) > splitSuggestLimit {
		out = out[:splitSuggestLimit]
	}
	if out == nil {
		out = []SplitCandidate{}
	}
	return out, nil
}

// splitCandidatesForClass scans one line class's physical table for simple
// LineStrings the source point sits on within the tolerance and strictly inside
// their interior, ranked nearest-first. The cut point is computed once per row in
// a LATERAL so ST_ClosestPoint and ST_LineLocatePoint don't each re-project the
// source.
func splitCandidatesForClass(ctx context.Context, db bun.IDB, sourceTable, sourceID string, lineClass *metadata.FeatureClass, hasSubtype bool, tolerance float64) ([]SplitCandidate, error) {
	lineTable := *lineClass.PhysicalTable
	lineSRID := *lineClass.SRID

	// subtypeExpr selects the candidate's subtype code when the class has one,
	// else a typed NULL so the scan target is uniform (mirrors nearbyCandidates).
	subtypeExpr := "NULL::int"
	if hasSubtype {
		subtypeExpr = "t.subtype"
	}

	// s.gsrid: the source point transformed into the line's own SRID, so
	// ST_ClosestPoint / ST_LineLocatePoint (both require matching SRIDs) run in
	// native coordinates. s.g4326: geography, for a real-metre distance and
	// tolerance regardless of either class's SRID. The returned cut point is
	// transformed back to 4326 -- the lng/lat the create form and POST .../split
	// speak over the wire.
	query := fmt.Sprintf(`
SELECT t.id, t.name, %s,
       ST_Distance(ST_Transform(t.geom, 4326)::geography, s.g4326) AS dist,
       ST_X(ST_Transform(loc.cp, 4326)) AS lng,
       ST_Y(ST_Transform(loc.cp, 4326)) AS lat
FROM %s t
CROSS JOIN (
  SELECT ST_Transform(geom, %d) AS gsrid,
         ST_Transform(geom, 4326)::geography AS g4326
  FROM %s WHERE id = ?
) s
CROSS JOIN LATERAL (
  SELECT ST_ClosestPoint(t.geom, s.gsrid) AS cp,
         ST_LineLocatePoint(t.geom, s.gsrid) AS frac
) loc
WHERE t.deleted_at IS NULL AND t.geom IS NOT NULL
  AND ST_GeometryType(t.geom) = 'ST_LineString'
  AND ST_DWithin(ST_Transform(t.geom, 4326)::geography, s.g4326, ?)
  AND loc.frac > ? AND loc.frac < ?
ORDER BY dist ASC
LIMIT ?`, subtypeExpr, lineTable, lineSRID, sourceTable)

	rows, err := db.QueryContext(ctx, query, sourceID, tolerance, splitEndpointEpsilon, 1-splitEndpointEpsilon, splitSuggestLimit)
	if err != nil {
		return nil, fmt.Errorf("crud: split suggest: querying %s: %w", lineTable, err)
	}
	defer rows.Close()

	var out []SplitCandidate
	for rows.Next() {
		var (
			id       string
			name     sql.NullString
			subtype  sql.NullInt64
			dist     float64
			lng, lat float64
		)
		if err := rows.Scan(&id, &name, &subtype, &dist, &lng, &lat); err != nil {
			return nil, fmt.Errorf("crud: split suggest: scanning %s: %w", lineTable, err)
		}
		// name is a nullable column -- an unnamed line scans as NULL, not "".
		c := SplitCandidate{
			Feature:        SplitCandidateFeature{FeatureClass: lineClass.Code, FeatureID: id, Name: name.String},
			DistanceMeters: dist,
			SplitLng:       lng,
			SplitLat:       lat,
		}
		if subtype.Valid {
			code := int(subtype.Int64)
			c.Feature.Subtype = &code
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// sortSplitCandidates orders candidates nearest-first, tie-broken by name for a
// stable order.
func sortSplitCandidates(s []SplitCandidate) {
	sort.SliceStable(s, func(i, j int) bool {
		if s[i].DistanceMeters != s[j].DistanceMeters {
			return s[i].DistanceMeters < s[j].DistanceMeters
		}
		return s[i].Feature.Name < s[j].Feature.Name
	})
}
