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
	Subtype  *int            `json:"subtype,omitempty"`
	Name     *string         `json:"name,omitempty"`
	Geometry json.RawMessage `json:"geometry,omitempty"`
	// TerritoryID is an optional manual override of the spatial territory
	// assignment (Phase 11). Absent means "auto-assign from geometry";
	// present forces this territory (still validated as at least a
	// district). Only meaningful for a geometric feature class.
	TerritoryID *string        `json:"territory_id,omitempty"`
	Attributes  map[string]any `json:"attributes"`
}

// updateRequest is the PATCH body for updating a feature. Every field is
// optional -- only what's present gets changed. Subtype is still
// deliberately not updatable (changing a feature's subtype after
// creation raises "does it still satisfy the new subtype's required
// fields" questions this pass doesn't answer yet) -- geometry doesn't
// have that problem (no subtype/required-field interaction at all), so
// it's just a plain optional replace, same validation
// (validateGeometry) as create, only skipped entirely when absent
// rather than required. Geometry additionally accepts an explicit JSON
// null to clear the map geometry (Phase 26 Slice 3, demote to
// diagram-only) -- only allowed on a geometry_optional class. Name is
// the same kind of plain optional replace as geometry -- absent means
// "not changing it", an explicit empty string means "clear it".
type updateRequest struct {
	Name     *string         `json:"name,omitempty"`
	Geometry json.RawMessage `json:"geometry,omitempty"`
	// TerritoryID overrides the territory assignment (Phase 11). Present
	// forces this territory; absent leaves the current assignment alone
	// unless the geometry itself is changing, in which case the territory
	// is re-resolved from the new geometry.
	TerritoryID *string        `json:"territory_id,omitempty"`
	Attributes  map[string]any `json:"attributes"`
	// ExpectedUpdatedAt is the row's updated_at as the client last read it --
	// the optimistic-concurrency token (Phase 40). Present makes the write
	// conditional (it only applies if the row hasn't changed since), so two
	// people editing the same feature can't silently clobber each other; a lost
	// race returns 409 instead. Absent leaves the write unconditional
	// (last-write-wins), which keeps every programmatic caller that doesn't send
	// it behaving exactly as before.
	ExpectedUpdatedAt *string `json:"expected_updated_at,omitempty"`
}

// queryRequest is the POST /{code}/query body -- the structured filter
// the map's query builder (docs/phase-13-query-builder.md) sends in place
// of the list endpoint's flat ?name=/?status= query params, which can't
// express typed operators or an OR. The request is itself the root query
// group: Match ("all" = AND, the default, or "any" = OR) combines its own
// Conditions with any nested Groups. A flat query just fills Conditions and
// leaves Groups empty -- exactly the Slice 1 shape, unchanged; nested groups
// (Slice 4) let a client express `(A AND B) OR C` by nesting. Sort/Order/
// Limit/Offset mirror the list endpoint's own paging exactly.
type queryRequest struct {
	Match      string           `json:"match"`
	Conditions []queryCondition `json:"conditions"`
	Groups     []queryGroup     `json:"groups"`
	Sort       string           `json:"sort"`
	Order      string           `json:"order"`
	Limit      int              `json:"limit"`
	Offset     int              `json:"offset"`
}

// queryGroup is one node of the condition tree: its own Match combinator
// applied across its leaf Conditions and its nested sub-Groups alike, so a
// group evaluates to a single parenthesized SQL fragment. Recursion depth
// and total condition count are bounded (buildGroup / countConditions) so a
// pathological body can't blow the stack or the WHERE clause.
type queryGroup struct {
	Match      string           `json:"match"`
	Conditions []queryCondition `json:"conditions"`
	Groups     []queryGroup     `json:"groups"`
}

// queryCondition is one "field op value" clause. Value carries the operand
// for the single-value operators (eq/ne/lt/lte/gt/gte/contains/starts_with);
// Values carries the operand list for `in` (1+) and `between` (exactly 2);
// is_null/not_null take neither. Every field/op is validated against the
// feature class's real columns and the op is checked against that column's
// data_type before any SQL is built -- see buildCondition.
type queryCondition struct {
	Field  string `json:"field"`
	Op     string `json:"op"`
	Value  any    `json:"value,omitempty"`
	Values []any  `json:"values,omitempty"`
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
