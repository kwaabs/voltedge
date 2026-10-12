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
// optional -- only what's present gets changed. Subtype is now updatable
// (Phase 60): the "does it still satisfy the new subtype's required
// fields" question the earlier pass punted on is answered by
// validateRequiredForSubtypeChange -- a change is accepted only when the
// row still satisfies the new subtype's required fields (existing values,
// values sent in the same PATCH, or a default_override all count), and
// rejected on a class with no subtypes. Geometry is a plain optional
// replace, same validation (validateGeometry) as create, only skipped
// entirely when absent rather than required; it additionally accepts an
// explicit JSON null to clear the map geometry (Phase 26 Slice 3, demote
// to diagram-only) -- only allowed on a geometry_optional class. Name is
// the same kind of plain optional replace as geometry -- absent means
// "not changing it", an explicit empty string means "clear it".
type updateRequest struct {
	Name     *string         `json:"name,omitempty"`
	Subtype  *int            `json:"subtype,omitempty"`
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
	Match      string             `json:"match"`
	Conditions []queryCondition   `json:"conditions"`
	Groups     []queryGroup       `json:"groups"`
	Scope      *querySpatialScope `json:"scope"`
	Tag        *queryTagFilter    `json:"tag"`
	Sort       string             `json:"sort"`
	Order      string             `json:"order"`
	Limit      int                `json:"limit"`
	Offset     int                `json:"offset"`
}

// queryTagFilter is the optional asset-tag predicate a query can carry, ANDed on
// top of the field conditions and every scope. It restricts the result to the
// features that DO (or, with Present=false, do NOT) carry a matching asset tag
// -- the "find the feature behind a scanned code" and "list every feature still
// missing an external asset number" searches the field conditions alone can't
// express, because a tag lives in runtime.asset_tags, not on the feature table.
//
// It is realised as an EXISTS (or NOT EXISTS) subquery correlated to each
// candidate row by (feature_class_code, id); void tags never count -- they are
// retired, exactly as the resolver 404s on them. Every operand is bound; only
// the fixed EXISTS/NOT EXISTS token and the trusted physical-table correlation
// reference are spliced in, so it adds no injection surface. When a tag filter
// is present, each returned row also carries its primary tag_code
// (primary_tag_code) so a code search shows which record a scan lands on.
type queryTagFilter struct {
	// Code, when set, matches a tag_code -- exact by default, or as a
	// case-insensitive prefix when Match=="prefix" (the partial-scan / typed
	// fragment search). Empty matches any code (a pure presence test).
	Code  string `json:"code"`
	Match string `json:"match"` // "" / "exact" | "prefix"
	// Source, when set, restricts which tags count: "generated" (system-minted)
	// or "external" (a pre-existing utility asset number). Empty counts both.
	Source string `json:"source"`
	// Present flips the predicate: nil / true keeps rows that HAVE a matching
	// tag; false keeps rows that have NONE (e.g. Source="external", Present=false
	// finds plant still missing its external number). Pointer so absent == true.
	Present *bool `json:"present"`
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

// aggregateRequest is the POST /{code}/aggregate body -- the numeric/spatial
// analysis counterpart to /query (docs/phase-13-query-builder.md's guided
// panel). It reuses the exact same filter tree as a query (Match + Conditions +
// nested Groups, built by the shared buildGroup) to narrow the rows, then folds
// what's left into counts instead of returning them:
//
//   - GroupBy (optional): a field to GROUP BY, so the response carries one row
//     per distinct value with its count ("count by subtype/status/..."). Absent
//     yields a single overall Total.
//   - Aggregate (optional): a secondary roll-up beyond the always-present count
//     (sum/avg/min/max over a numeric field), reported per group and overall.
//   - Scope (optional): a spatial constraint ANDed on top of the filter --
//     "in this territory (and its sub-territories)" or "within this radius of a
//     dropped point" -- the map's spatial-query tools (Slice 2/3).
//
// Every field name is whitelisted through columnDataType before it reaches SQL,
// exactly as a query condition is, so the endpoint adds no injection surface.
// Limit caps how many group rows come back (see maxAggregateGroups).
type aggregateRequest struct {
	Match      string             `json:"match"`
	Conditions []queryCondition   `json:"conditions"`
	Groups     []queryGroup       `json:"groups"`
	GroupBy    string             `json:"group_by"`
	Aggregate  *aggregateSpec     `json:"aggregate"`
	Scope      *querySpatialScope `json:"scope"`
	Limit      int                `json:"limit"`
}

// aggregateSpec is the optional secondary roll-up: a function (sum/avg/min/max,
// or count which is a no-op since a count always comes back) over a numeric
// field. The field is whitelisted and its data_type checked (numeric/integer
// only) before the SQL is built -- see aggregateExpr.
type aggregateSpec struct {
	Fn    string `json:"fn"`
	Field string `json:"field"`
}

// querySpatialScope is the optional scope constraint an aggregate or a query can
// carry (the query path uses it to drill from an aggregate's count into the very
// rows behind it, under the same narrowing). Kind selects the shape:
//
//   - "" / "none": no constraint.
//   - "territory": every feature stamped with TerritoryID or any territory
//     beneath it (territory.DescendantIDs, matching the deepest-wins stamping).
//   - "radius": every feature whose geometry lies within RadiusM metres of
//     Center ([longitude, latitude], WGS84).
//   - "circuit": every feature on circuit CircuitID -- a non-spatial, column-based
//     scope over the Phase-37 circuit_id membership. A class that carries no
//     circuit_id column simply matches nothing (so it contributes zero to an
//     all-classes tally rather than erroring the whole query).
//
// The two spatial shapes (territory/radius) are only meaningful for a geometric
// feature class; a geometry-less class 400s on them. The circuit shape needs no
// geometry (see spatialScopeWhere).
type querySpatialScope struct {
	Kind        string    `json:"kind"`
	TerritoryID string    `json:"territory_id"`
	Center      []float64 `json:"center"`
	RadiusM     float64   `json:"radius_m"`
	CircuitID   string    `json:"circuit_id"`
}

// aggregateResponse is the POST /{code}/aggregate result. Total is the row
// count across the whole (filtered, scoped) set -- the true total even when
// Groups is capped by Limit. Agg is the overall secondary aggregate, present
// only when the request asked for one and GroupBy was absent. Groups is one
// entry per distinct GroupBy value (empty when GroupBy was absent).
type aggregateResponse struct {
	Total  int              `json:"total"`
	Agg    *float64         `json:"agg,omitempty"`
	Groups []aggregateGroup `json:"groups"`
}

// aggregateGroup is one GROUP BY bucket: the distinct Value (a raw scalar --
// domain code, status string, subtype int, etc.; the frontend maps it to a
// label from the class schema), its Count, and the secondary Agg when requested.
type aggregateGroup struct {
	Value any      `json:"value"`
	Count int      `json:"count"`
	Agg   *float64 `json:"agg,omitempty"`
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
