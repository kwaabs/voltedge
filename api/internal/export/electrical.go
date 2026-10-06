package export

// CIM electrical detail -- Phase 16 Slice 3, docs/phase-16-cim-export.md.
//
// The equipment/topology from Slices 1-2 is electrically inert: an ACLineSegment
// with no length or impedance, a PowerTransformer with no ratings. This file
// adds the electrical values a power-system tool actually solves against:
//
//   - Conductor.length, computed from the LineString geometry (the from_geometry
//     transform).
//   - ACLineSegment.r / .x, resolved from the conductor's material spec
//     (per-km resistance/reactance x length) -- the material_spec transform.
//
// It resolves material specs the export read-path doesn't carry: a conductor
// field holds a coded value (e.g. "ABC_95") whose metadata.domain_values row
// links to a metadata.material_specs row (r/x/ampacity). loadMaterialSpecs
// builds that value-code -> spec map once per export.

import (
	"context"
	"fmt"
	"math"

	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// loadMaterialSpecs builds the map from a coded conductor-spec value (as stored
// on a feature, e.g. "ABC_95") to its material spec, by joining every
// material-spec-bearing domain value to the material_specs catalog. It follows
// domain_values.material_spec_id (the real FK), not a code-equality assumption.
// The whole catalog is small (a picker-sized global list), so it's fetched in
// full rather than scoped to the exported classes.
func loadMaterialSpecs(ctx context.Context, db bun.IDB) (map[string]*metadata.MaterialSpec, error) {
	specs, err := metadata.ListMaterialSpecs(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("export: loading material specs: %w", err)
	}
	byID := make(map[string]*metadata.MaterialSpec, len(specs))
	for i := range specs {
		byID[specs[i].ID] = &specs[i]
	}

	values, err := metadata.ListDomainValues(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("export: loading domain values: %w", err)
	}
	byCode := map[string]*metadata.MaterialSpec{}
	for _, v := range values {
		if v.MaterialSpecID == nil {
			continue
		}
		if spec, ok := byID[*v.MaterialSpecID]; ok {
			byCode[v.Code] = spec
		}
	}
	return byCode, nil
}

// writeConductorDetail emits a conductor's length and series impedance as
// literal children of the ACLineSegment. It is a no-op for non-edge equipment
// (only conductors have a length/impedance) and for any attribute whose value
// or material spec doesn't resolve, so a class without the mapping or the data
// (e.g. MV_CONDUCTOR, which has no seeded material spec) simply gets nothing.
func (cw *cimWriter) writeConductorDetail(cm metadata.CimClassMapping, f Feature, attrs []metadata.CimAttributeMapping, pts []coord) {
	if cm.CimKind != "conducting_edge" {
		return
	}
	lengthM := cw.conductorLengthMeters(pts)

	for _, a := range attrs {
		switch a.Transform {
		case "from_geometry":
			// The only geometry-derived value in Slice 3 is conductor length; the
			// mapping's field is a placeholder (the value comes from the geometry).
			if a.CimAttribute == "Conductor.length" && lengthM > 0 {
				cw.lit("Conductor.length", formatFloat(round3(lengthM)))
			}
		case "material_spec":
			perKm := cw.specPerKm(f, a)
			if perKm == nil || lengthM <= 0 {
				continue
			}
			// CIM ACLineSegment.r/.x are total series ohms for the whole segment:
			// the per-km catalog figure x the segment length in km.
			cw.lit(a.CimAttribute, formatFloat(round3(*perKm*lengthM/1000)))
		}
	}
}

// specPerKm resolves the per-km material property a material_spec attribute
// mapping targets (ACLineSegment.r -> resistance, .x -> reactance) for a
// feature, or nil if the feature's coded spec value or that property is absent.
func (cw *cimWriter) specPerKm(f Feature, a metadata.CimAttributeMapping) *float64 {
	code, ok := f.Attributes[a.FieldName].(string)
	if !ok || code == "" {
		return nil
	}
	spec := cw.specs[code]
	if spec == nil {
		return nil
	}
	switch a.CimAttribute {
	case "ACLineSegment.r":
		return spec.ResistanceOhmPerKm
	case "ACLineSegment.x":
		return spec.ReactanceOhmPerKm
	}
	return nil
}

// conductorLengthMeters is the conductor's physical length in metres, for CIM
// Conductor.length and the impedance derivation. The vertices are in whatever CRS
// the read path emitted: EPSG:4326 lon/lat by default -> a geodesic (haversine)
// sum; or the reprojection target CRS when a target SRID was requested -> a planar
// sum, which IS the length in metres for the metre-based projected CRS a
// reprojected export realistically targets (UTM, a national grid). Running
// haversine on projected easting/northing values would be nonsense, corrupting
// both the length and the impedance derived from it. (Reprojecting CIM to a
// non-metre CRS is out of scope -- its length would then be off, but that is an
// unrealistic target for a CIM export.)
func (cw *cimWriter) conductorLengthMeters(pts []coord) float64 {
	if cw.targetSRID != nil {
		return planarLengthMeters(pts)
	}
	return geoLengthMeters(pts)
}

// planarLengthMeters sums the Euclidean segment lengths of a polyline whose
// vertices are already in a projected, metre-based CRS. Fewer than two vertices
// yields 0.
func planarLengthMeters(pts []coord) float64 {
	if len(pts) < 2 {
		return 0
	}
	total := 0.0
	for i := 1; i < len(pts); i++ {
		total += math.Hypot(pts[i].x-pts[i-1].x, pts[i].y-pts[i-1].y)
	}
	return total
}

// geoLengthMeters is the geodesic length of a polyline given as ordered lon/lat
// vertices (EPSG:4326), summing the great-circle distance of each segment. Fewer
// than two vertices yields 0.
func geoLengthMeters(pts []coord) float64 {
	if len(pts) < 2 {
		return 0
	}
	total := 0.0
	for i := 1; i < len(pts); i++ {
		total += haversine(pts[i-1], pts[i])
	}
	return total
}

// haversine is the great-circle distance in metres between two lon/lat points.
func haversine(a, b coord) float64 {
	const earthRadiusM = 6371000.0
	lat1 := a.y * math.Pi / 180
	lat2 := b.y * math.Pi / 180
	dLat := (b.y - a.y) * math.Pi / 180
	dLon := (b.x - a.x) * math.Pi / 180
	h := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1)*math.Cos(lat2)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * earthRadiusM * math.Asin(math.Min(1, math.Sqrt(h)))
}

// round3 rounds to 3 decimals -- length to the millimetre, impedance to the
// milliohm -- so the output isn't cluttered with float noise.
func round3(v float64) float64 {
	return math.Round(v*1000) / 1000
}
