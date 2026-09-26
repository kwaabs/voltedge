package oms

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/uptrace/bun"
)

// This file backs the incident MAP OVERLAY -- the twin of the fault overlay
// (api/internal/faults/geometry.go). An incident is not a GIS feature, so it has
// no geometry of its own; it is anchored to its predicted protective device. A
// marker is placed on that device's compiled gis geometry, and the device's full
// shape rides along for a status-colored highlight, exactly as a fault is drawn
// on its asset. The two click-through cones (the frozen affected area and the
// live de-energized subset) already have endpoints -- /incidents/{id}/affected
// and /incidents/{id}/energization -- so the overlay reuses those for the cone
// and this file only resolves the marker.

// classTable looks up a feature class's compiled table (e.g. gis.mv_switch) and
// whether it carries geometry. A class that is uncompiled (no physical_table) or
// non-geometric returns ok=false -- an incident on such a device simply gets no
// marker. Mirrors faults.classTable (oms does not import faults; the small helper
// is duplicated rather than exported across the package boundary, as the two
// modules stay decoupled).
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
		return "", false, fmt.Errorf("oms: looking up table for class %s: %w", code, err)
	}
	if !row.PhysicalTable.Valid || row.PhysicalTable.String == "" || row.GeometryType == "none" {
		return "", false, nil
	}
	return row.PhysicalTable.String, true, nil
}

// deviceGeom is a predicted device's map projection for the overlay: a marker
// point guaranteed to lie ON the geometry, plus the full geometry as GeoJSON for
// the status-colored highlight.
type deviceGeom struct {
	Lon     float64
	Lat     float64
	GeoJSON json.RawMessage
}

// deviceGeoms resolves each device's marker point + GeoJSON geometry in the given
// compiled table, keyed by id. The marker point is ST_ClosestPoint(geom,
// centroid) -- a point guaranteed to sit ON the geometry for ANY type (a line's
// on-line midpoint, a polygon's edge, a point itself), unlike a bare centroid,
// which for a curved or multipart line lands off the feature. The GeoJSON is the
// whole geometry so a line/polygon device highlights the actual asset rather than
// a single derived point. Deleted / geometry-less rows are skipped. Mirrors
// faults.assetGeoms.
func deviceGeoms(ctx context.Context, db bun.IDB, table string, ids []string) (map[string]deviceGeom, error) {
	out := map[string]deviceGeom{}
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
		return nil, fmt.Errorf("oms: resolving geometries in %s: %w", table, err)
	}
	for _, r := range rows {
		out[r.ID] = deviceGeom{Lon: r.Lon, Lat: r.Lat, GeoJSON: json.RawMessage(r.GeoJSON)}
	}
	return out, nil
}

// IncidentMapMarker is one incident projected onto the map: the incident plus its
// predicted device's GeoJSON geometry. Longitude/Latitude are the MARKER point --
// a point on the device geometry (ST_ClosestPoint). Geometry is the device's full
// shape (line / polygon / point) so the overlay can highlight the actual device,
// not just drop a pin. Both are always populated: an incident with no resolvable
// device marker is omitted from the list (there is nothing to draw), the same way
// a location-less fault is omitted from ListFaultsForMap.
type IncidentMapMarker struct {
	Incident
	Longitude float64         `json:"longitude"`
	Latitude  float64         `json:"latitude"`
	Geometry  json.RawMessage `json:"geometry,omitempty"`
}

// ListIncidentsForMap returns incidents with a placeable map point -- each drawn
// on its predicted protective device (an on-geometry marker point plus the
// device's full geometry for highlighting). An incident whose device is
// unattributed, uncompiled, deleted, or geometry-less is omitted: it has nothing
// to anchor to (unlike a fault, an incident never carries a location pin of its
// own -- prediction and planned outages both name a device). The click-through
// cones are drawn separately from /incidents/{id}/affected (frozen) and
// /incidents/{id}/energization (live), so this endpoint only resolves the marker.
//
// The filter mirrors the board's (?status=, ?open=true); the overlay fetches
// every status once and the legend hides restored/cancelled client-side, exactly
// as the fault overlay does.
func ListIncidentsForMap(ctx context.Context, db bun.IDB, status string, openOnly bool) ([]IncidentMapMarker, error) {
	incidents, err := ListIncidents(ctx, db, status, openOnly)
	if err != nil {
		return nil, err
	}

	// Resolve device geometry (marker point + GeoJSON highlight) once per class.
	byClass := map[string][]string{}
	for i := range incidents {
		inc := &incidents[i]
		if inc.PredictedDeviceClass == nil || inc.PredictedDeviceID == nil ||
			*inc.PredictedDeviceClass == "" || *inc.PredictedDeviceID == "" {
			continue
		}
		byClass[*inc.PredictedDeviceClass] = append(byClass[*inc.PredictedDeviceClass], *inc.PredictedDeviceID)
	}
	resolved := map[string]map[string]deviceGeom{} // class -> (device id -> geometry)
	for class, ids := range byClass {
		table, ok, err := classTable(ctx, db, class)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		geoms, err := deviceGeoms(ctx, db, table, ids)
		if err != nil {
			return nil, err
		}
		resolved[class] = geoms
	}

	out := make([]IncidentMapMarker, 0, len(incidents))
	for i := range incidents {
		inc := incidents[i]
		if inc.PredictedDeviceClass == nil || inc.PredictedDeviceID == nil {
			continue
		}
		g, ok := resolved[*inc.PredictedDeviceClass][*inc.PredictedDeviceID]
		if !ok {
			continue // device uncompiled / deleted / geometry-less -- nothing to draw
		}
		out = append(out, IncidentMapMarker{
			Incident:  inc,
			Longitude: g.Lon,
			Latitude:  g.Lat,
			Geometry:  g.GeoJSON,
		})
	}
	return out, nil
}
