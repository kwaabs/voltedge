package faults

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/trace"
)

// This file backs the fault MAP OVERLAY (Slice 3). A fault is not a GIS feature,
// so it has no geometry of its own; a marker is placed either at the fault's own
// location pin or -- for an asset-anchored fault -- at the asset's centroid,
// resolved here from the asset's compiled gis table. The impact area (the
// de-energized downstream section) is a live trace from the fault's protective
// device, in the same shape the OMS affected-area overlay already draws.

// serviceClass is the feature class a fault's downstream impact is counted
// against -- the customer supply points that go dark below the protective
// device. Kept in step with oms.ServicePointClass (faults deliberately does not
// import oms; the coupling stays at the DB/const level, as with incident reads).
const serviceClass = "SERVICE_POINT"

// classTable looks up a feature class's compiled table (e.g. gis.pole) and
// whether it carries geometry. A class that is uncompiled (no physical_table) or
// non-geometric returns ok=false -- its faults simply get no marker.
func classTable(ctx context.Context, db bun.IDB, code string) (table string, ok bool, err error) {
	var row struct {
		PhysicalTable sql.NullString `bun:"physical_table"`
		GeometryType  string         `bun:"geometry_type"`
	}
	err = db.NewSelect().
		ColumnExpr("physical_table, geometry_type").
		TableExpr("metadata.feature_classes").
		Where("code = ?", code).
		Limit(1).
		Scan(ctx, &row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("faults: looking up table for class %s: %w", code, err)
	}
	if !row.PhysicalTable.Valid || row.PhysicalTable.String == "" || row.GeometryType == "none" {
		return "", false, nil
	}
	return row.PhysicalTable.String, true, nil
}

// assetGeom is an asset feature's map projection for the overlay: a marker point
// guaranteed to lie ON the geometry, plus the full geometry as GeoJSON for the
// status-colored highlight.
type assetGeom struct {
	Lon     float64
	Lat     float64
	GeoJSON json.RawMessage
}

// assetGeoms resolves each live feature's marker point + GeoJSON geometry in the
// given compiled table, keyed by id. The marker point is ST_ClosestPoint(geom,
// centroid) -- a point guaranteed to sit ON the geometry for ANY type (a line's
// on-line midpoint, a polygon's edge, a point itself), unlike a bare centroid,
// which for a curved or multipart line lands off the feature. The GeoJSON is the
// whole geometry, so a line/polygon fault highlights the actual asset rather than
// a single derived point. Deleted / geometry-less rows are skipped.
func assetGeoms(ctx context.Context, db bun.IDB, table string, ids []string) (map[string]assetGeom, error) {
	out := map[string]assetGeom{}
	if len(ids) == 0 {
		return out, nil
	}
	var rows []struct {
		ID      string  `bun:"id"`
		Lon     float64 `bun:"lon"`
		Lat     float64 `bun:"lat"`
		GeoJSON string  `bun:"geojson"`
	}
	err := db.NewSelect().
		ColumnExpr("id::text AS id").
		ColumnExpr("ST_X(ST_ClosestPoint(geom, ST_Centroid(geom))) AS lon").
		ColumnExpr("ST_Y(ST_ClosestPoint(geom, ST_Centroid(geom))) AS lat").
		ColumnExpr("ST_AsGeoJSON(geom) AS geojson").
		TableExpr(table).
		Where("id IN (?)", bun.In(ids)).
		Where("deleted_at IS NULL").
		Where("geom IS NOT NULL").
		Scan(ctx, &rows)
	if err != nil {
		return nil, fmt.Errorf("faults: resolving geometries in %s: %w", table, err)
	}
	for _, r := range rows {
		out[r.ID] = assetGeom{Lon: r.Lon, Lat: r.Lat, GeoJSON: json.RawMessage(r.GeoJSON)}
	}
	return out, nil
}

// FaultMapMarker is one fault projected onto the map: the fault event plus,
// for an asset-anchored fault, the asset's GeoJSON geometry. Longitude/Latitude
// (promoted from the embedded fault) are the MARKER point -- the location pin for
// a location-anchored fault, or a point on the asset geometry for an asset-anchored
// one. Geometry is the asset's full shape (line / polygon / point) so the overlay
// can highlight the actual asset, not just drop a pin; it is null for a
// location-only fault (there is no asset to highlight).
type FaultMapMarker struct {
	FaultEvent
	Geometry json.RawMessage `json:"geometry,omitempty"`
}

// ListFaultsForMap returns faults with a placeable map point. An asset-anchored
// fault is drawn on its asset: an on-geometry marker point plus the asset's full
// geometry for highlighting. A location-anchored fault keeps its own pin (no
// asset geometry). A fault with no resolvable point (incident-only, or an asset
// that's uncompiled / deleted / geometry-less and carries no pin of its own) is
// omitted -- it has nothing to draw. Asset geometry is preferred over a bare
// location pin when both are present, since the highlight is the richer view.
// The filter is the board's (open-only is applied client-side via the legend, so
// the overlay fetches every status once).
func ListFaultsForMap(ctx context.Context, db bun.IDB, f FaultFilter) ([]FaultMapMarker, error) {
	faults, err := ListFaults(ctx, db, f)
	if err != nil {
		return nil, err
	}

	// Resolve asset geometry (marker point + GeoJSON highlight) once per class.
	byClass := map[string][]string{}
	for i := range faults {
		ft := &faults[i]
		if ft.AssetClass == nil || ft.AssetID == nil || *ft.AssetClass == "" || *ft.AssetID == "" {
			continue
		}
		byClass[*ft.AssetClass] = append(byClass[*ft.AssetClass], *ft.AssetID)
	}
	resolved := map[string]map[string]assetGeom{} // class -> (asset id -> geometry)
	for class, ids := range byClass {
		table, ok, err := classTable(ctx, db, class)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		geoms, err := assetGeoms(ctx, db, table, ids)
		if err != nil {
			return nil, err
		}
		resolved[class] = geoms
	}

	out := make([]FaultMapMarker, 0, len(faults))
	for i := range faults {
		marker := FaultMapMarker{FaultEvent: faults[i]}
		// Prefer the asset's own geometry (on-geometry marker + highlight) when the
		// fault names a resolvable asset; fall back to the fault's location pin.
		if marker.AssetClass != nil && marker.AssetID != nil {
			if g, ok := resolved[*marker.AssetClass][*marker.AssetID]; ok {
				lon, lat := g.Lon, g.Lat
				marker.Longitude = &lon
				marker.Latitude = &lat
				marker.Geometry = g.GeoJSON
				out = append(out, marker)
				continue
			}
		}
		if marker.Longitude != nil && marker.Latitude != nil {
			out = append(out, marker) // location pin, no asset to highlight
			continue
		}
		// No resolvable point -- nothing to draw, omit.
	}
	return out, nil
}

// FaultImpactPoint is one customer supply point in a fault's downstream impact,
// with the WGS84 point the overlay draws.
type FaultImpactPoint struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Longitude float64 `json:"longitude"`
	Latitude  float64 `json:"latitude"`
}

// FaultImpact is a fault's de-energized downstream section: the protective
// device that operated plus every service point below it. Its shape mirrors
// oms.AffectedArea so the map reuses the OMS affected-area drawing.
type FaultImpact struct {
	FaultID       string             `json:"fault_id"`
	DeviceClass   *string            `json:"device_class"`
	DeviceID      *string            `json:"device_id"`
	ServicePoints []FaultImpactPoint `json:"service_points"`
}

// FaultImpactArea traces the downstream service points fed through a fault's
// protective device -- the section it de-energized when it operated. A fault
// with no protective device (never assigned one) yields an empty but non-null
// set. Barrier-free/topological, the same conservative basis the OMS impact
// trace uses. Reuses the shared trace engine directly (a lower layer; no oms
// import), keeping the faults->oms decoupling intact.
func FaultImpactArea(ctx context.Context, db bun.IDB, faultID string) (*FaultImpact, error) {
	f, err := GetFault(ctx, db, faultID)
	if err != nil {
		return nil, err
	}
	impact := &FaultImpact{
		FaultID:       f.ID,
		DeviceClass:   f.ProtectiveDeviceClass,
		DeviceID:      f.ProtectiveDeviceID,
		ServicePoints: []FaultImpactPoint{},
	}
	if f.ProtectiveDeviceClass == nil || f.ProtectiveDeviceID == nil ||
		*f.ProtectiveDeviceClass == "" || *f.ProtectiveDeviceID == "" {
		return impact, nil
	}

	device := trace.FeatureRef{FeatureClass: *f.ProtectiveDeviceClass, FeatureID: *f.ProtectiveDeviceID}
	sps, err := trace.DownstreamFeaturesOfClass(ctx, db, device, []string{serviceClass})
	if err != nil {
		return nil, err
	}
	if len(sps) == 0 {
		return impact, nil
	}
	ids := make([]string, len(sps))
	for i, sp := range sps {
		ids[i] = sp.FeatureID
	}

	var rows []FaultImpactPoint
	err = db.NewSelect().
		ColumnExpr("id::text AS id, name").
		ColumnExpr("ST_X(geom) AS longitude").
		ColumnExpr("ST_Y(geom) AS latitude").
		TableExpr("gis.service_point").
		Where("id IN (?)", bun.In(ids)).
		Where("deleted_at IS NULL").
		Where("geom IS NOT NULL").
		OrderExpr("name").
		Scan(ctx, &rows)
	if err != nil {
		return nil, fmt.Errorf("faults: resolving impact points for fault %s: %w", faultID, err)
	}
	if rows != nil {
		impact.ServicePoints = rows
	}
	return impact, nil
}
