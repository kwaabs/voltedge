// Package importer is the server side of the admin ETL / import engine
// (docs/etl-import-engine.md): it streams the normalized source artifact a
// browser uploaded, maps each row onto a target feature class's fields per an
// import profile, and applies it through the validated crud engine (the same
// CreateFeature/UpdateFeature path field-pack sync uses) as an insert or a
// natural-key upsert.
//
// This file defines the one thing that crosses the browser/server boundary: the
// NDJSON source artifact contract. Everything else in the package (profile CRUD,
// the dry-run validator, the load worker) builds on it in later slices.
//
// The artifact is newline-delimited GeoJSON ("NDJSON"): one GeoJSON Feature per
// line,
//
//	{"type":"Feature","properties":{...},"geometry":{...}|null}
//
// produced by web/src/lib/etl-import.ts. The browser normalizes every supported
// upload format (CSV, GeoJSON, Shapefile, KML, DXF) into this single shape, so
// the server parses exactly one format regardless of the source. `properties`
// carries the source columns (CSV cells arrive as strings; GeoJSON numbers as
// numbers); `geometry` is a GeoJSON geometry when the source carried one, or
// null when geometry is assembled server-side from the profile's lon/lat or WKT
// columns (or when the target class is geometry-none).
package importer

import (
	"encoding/json"
	"io"
)

// Feature is one row of the NDJSON source artifact. Geometry is kept as raw
// JSON so it passes straight to ST_GeomFromGeoJSON without a decode/re-encode
// round trip (the crud write path takes geometry as GeoJSON); it is nil/`null`
// when the row carries no inline geometry.
type Feature struct {
	Type       string          `json:"type"`
	Properties map[string]any  `json:"properties"`
	Geometry   json.RawMessage `json:"geometry,omitempty"`
}

// HasGeometry reports whether the row carries an inline GeoJSON geometry (i.e.
// geometry_mode = native). A literal JSON `null` counts as absent.
func (f *Feature) HasGeometry() bool {
	if len(f.Geometry) == 0 {
		return false
	}
	// A bare `null` (with optional surrounding space) is "no geometry".
	for _, b := range f.Geometry {
		switch b {
		case ' ', '\t', '\n', '\r':
			continue
		case 'n': // start of `null`
			return false
		default:
			return true
		}
	}
	return false
}

// Reader streams Features from an NDJSON artifact. It uses a json.Decoder rather
// than a line scanner so an individual feature may be arbitrarily large (a dense
// polygon geometry) without a token-size limit, while still tolerating the
// newline separators between records.
type Reader struct {
	dec *json.Decoder
	row int
}

// NewReader wraps an artifact stream (e.g. the object-store object body).
func NewReader(r io.Reader) *Reader {
	return &Reader{dec: json.NewDecoder(r)}
}

// Read returns the next feature and its 1-based row number (the line number in
// the artifact, which the load reports back on a per-row error). It returns
// io.EOF when the stream is exhausted. A malformed line surfaces its decode
// error with the row number already advanced, so the caller can record it
// against that row and decide whether to continue.
func (r *Reader) Read() (*Feature, int, error) {
	if !r.dec.More() {
		return nil, r.row, io.EOF
	}
	r.row++
	var f Feature
	if err := r.dec.Decode(&f); err != nil {
		return nil, r.row, err
	}
	return &f, r.row, nil
}
