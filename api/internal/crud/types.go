// Package crud is the generic, metadata-driven CRUD engine for Phase 2:
// one set of HTTP handlers that reads a feature class's metadata (via
// api/internal/metadata) and validates/executes create, read, update,
// publish/unpublish, and delete against its compiled physical table
// (api/internal/compiler). No feature-class-specific code exists
// anywhere in this package -- adding a new feature class means compiling
// it, not writing a new handler.
//
// Deliberately not in this pass: filtering/sorting on list, and the
// connectivity/relationship establish flow. See
// docs/phase-2-crud-engine.md for the full list of what's open.
package crud

import "encoding/json"

// createRequest is the POST body for creating a feature. Name is a
// top-level field, not nested under Attributes -- it's the one
// universal, optional, human-assigned label every feature class gets
// identically (api/internal/compiler's buildCreateTable), not a
// metadata-authored field an admin opts a class into.
type createRequest struct {
	Subtype    *int            `json:"subtype,omitempty"`
	Name       *string         `json:"name,omitempty"`
	Geometry   json.RawMessage `json:"geometry,omitempty"`
	Attributes map[string]any  `json:"attributes"`
}

// updateRequest is the PATCH body for updating a feature. Every field is
// optional -- only what's present gets changed. Subtype is still
// deliberately not updatable (changing a feature's subtype after
// creation raises "does it still satisfy the new subtype's required
// fields" questions this pass doesn't answer yet) -- geometry doesn't
// have that problem (no subtype/required-field interaction at all), so
// it's just a plain optional replace, same validation
// (validateGeometry) as create, only skipped entirely when absent
// rather than required. Name is the same kind of plain optional replace
// as geometry -- absent means "not changing it", an explicit empty
// string means "clear it".
type updateRequest struct {
	Name       *string         `json:"name,omitempty"`
	Geometry   json.RawMessage `json:"geometry,omitempty"`
	Attributes map[string]any  `json:"attributes"`
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
