// Package crud is the generic, metadata-driven CRUD engine for Phase 2:
// one set of HTTP handlers that reads a feature class's metadata (via
// api/internal/metadata) and validates/executes create, read, and update
// against its compiled physical table (api/internal/compiler). No
// feature-class-specific code exists anywhere in this package -- adding
// a new feature class means compiling it, not writing a new handler.
//
// Deliberately not in this pass: publish/unpublish (status stays
// 'draft' on create -- there's no endpoint to flip it yet), delete,
// filtering/sorting on list, and the connectivity/relationship establish
// flow. See docs/phase-2-crud-engine.md for the full list of what's open.
package crud

import "encoding/json"

// createRequest is the POST body for creating a feature.
type createRequest struct {
	Subtype    *int            `json:"subtype,omitempty"`
	Geometry   json.RawMessage `json:"geometry,omitempty"`
	Attributes map[string]any  `json:"attributes"`
}

// updateRequest is the PATCH body for updating a feature. Every field is
// optional -- only what's present gets changed. Subtype and geometry are
// deliberately not updatable in this pass (changing a feature's subtype
// after creation raises "does it still satisfy the new subtype's
// required fields" questions this pass doesn't answer yet).
type updateRequest struct {
	Attributes map[string]any `json:"attributes"`
}

// geoJSONGeometry is enough of a GeoJSON Geometry object to check its
// "type" matches the feature class's geometry_type. Coordinate
// correctness is left to PostGIS's ST_GeomFromGeoJSON.
type geoJSONGeometry struct {
	Type string `json:"type"`
}

// apiError is the JSON body returned for any 4xx/5xx response.
type apiError struct {
	Error string `json:"error"`
}
