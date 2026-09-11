package crud

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/kwaabs/voltedge/api/internal/territory"
	"github.com/uptrace/bun"
)

// UpdateFeatureInput is the exported, transport-free form of updateRequest --
// the fields a partial update of one feature can change. Same shape and JSON
// tags as the REST PATCH body, so an MCP tool (docs/phase-18-mcp.md) and the
// HTTP handler describe an identical request. Every field is optional and only
// what's present is changed: an absent Geometry/TerritoryID means "leave it",
// an absent Name means "not changing it" while an explicit empty string clears
// it, and a nil Attributes is treated as an empty map (see updateRequest for
// each field's meaning). Subtype is deliberately not updatable.
//
// Geometry has a third state beyond absent/replace: an explicit JSON null clears
// the map geometry (Phase 26 Slice 3), demoting a feature to diagram-only. It's
// only allowed on a geometry_optional class -- json.RawMessage preserves the
// literal `null`, so "clear" (the bytes "null") is distinguishable from "leave
// it" (empty) and "replace" (a geometry value).
type UpdateFeatureInput struct {
	Name        *string         `json:"name,omitempty"`
	Geometry    json.RawMessage `json:"geometry,omitempty"`
	TerritoryID *string         `json:"territory_id,omitempty"`
	Attributes  map[string]any  `json:"attributes"`
	// ExpectedUpdatedAt is the optimistic-concurrency token (Phase 40): the
	// row's updated_at as the client last saw it. Present makes the write
	// conditional on the row not having changed since (a lost race -> 409);
	// absent leaves it unconditional (last-write-wins), so the MCP update_feature
	// tool and any caller that doesn't send it are unaffected.
	ExpectedUpdatedAt *string `json:"expected_updated_at,omitempty"`
}

// UpdateFeature runs the full partial-update path -- fetch the current row,
// reject an update to a soft-deleted feature, validate the changed geometry and
// attributes (in update mode, so post-publish-locked fields are enforced against
// the current status), re-check completeness when the feature is published,
// re-resolve the territory only when there's a reason to, and write. It is the
// single source of truth both updateHandler (REST) and the MCP update_feature
// tool call, so the two front doors can never disagree about what an update
// validates or writes (the same reasoning CreateFeature uses for create).
//
// The existing-row fetch lives here, not in the caller, so the deleted-check and
// the published-completeness re-check are shared too. The acting user is read
// from ctx (auth.ActorID, via the updated_by stamp updateFeature applies), so a
// caller must have established an authenticated context -- the HTTP session
// middleware for REST, auth.WithUser after a bearer-token resolve for MCP.
// Errors are this package's own validation/not-found errors, for the caller to
// map to its transport (HTTP status, or an in-band MCP tool error).
func UpdateFeature(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, id string, in UpdateFeatureInput) (map[string]any, error) {
	existing, err := getFeature(ctx, db, loaded, id)
	if err != nil {
		return nil, err
	}
	if existing["deleted_at"] != nil {
		return nil, invalid("feature %s is deleted -- restore it before updating", id)
	}
	currentStatus, _ := existing["status"].(string)

	attrs := in.Attributes
	if attrs == nil {
		attrs = map[string]any{}
	}

	// Phase 26 Slice 3: an explicit JSON null clears the map geometry (demote to
	// diagram-only), which is distinct from an absent geometry ("leave it") and a
	// supplied one ("replace it"). json.RawMessage keeps the literal `null`, so
	// clearGeometry is exactly the 4 bytes "null"; hasGeometry is a real value to
	// write. Both false means the geometry column is left untouched.
	clearGeometry := bytes.Equal(bytes.TrimSpace(in.Geometry), []byte("null"))
	hasGeometry := len(in.Geometry) > 0 && !clearGeometry

	// Clearing is only meaningful for a geometric class that opted into optional
	// geometry -- on a "none" class there's no geom column to clear, and on a
	// mandatory-geometry class the DB's NOT NULL would reject it anyway, so reject
	// cleanly with a 400 rather than surfacing a raw Postgres error. A territory
	// override alongside a clear is contradictory: a feature with no map geometry
	// belongs to no territory.
	if clearGeometry {
		switch {
		case loaded.FeatureClass.GeometryType == "none":
			return nil, invalid("feature class %q has no map geometry to clear", loaded.FeatureClass.Code)
		case !loaded.FeatureClass.GeometryOptional:
			return nil, invalid("geometry is required for feature class %q -- it can't be cleared (enable geometry_optional to allow diagram-only features)", loaded.FeatureClass.Code)
		case in.TerritoryID != nil:
			return nil, invalid("can't set a territory while clearing geometry -- a feature with no map geometry belongs to no territory")
		}
	}

	// Same type-matches-feature-class check create runs -- but skipped entirely
	// when geometry wasn't sent (absence means "not changing it") or is being
	// cleared (handled above), since neither is a geometry to type-check.
	if hasGeometry {
		if err := validateGeometry(loaded, in.Geometry); err != nil {
			return nil, err
		}
	}

	fields, err := validateAttributes(loaded, attrs, writeUpdate, &currentStatus)
	if err != nil {
		return nil, err
	}

	values := make(map[string]any, len(attrs))
	for name, raw := range attrs {
		f := fields[name]
		coerced, err := validateValue(loaded, f, raw)
		if err != nil {
			return nil, err
		}
		values[name] = coerced
	}

	// Enforce any coded-domain state machine (Phase 38 Slice 2): a change to a
	// field like operational_state must follow the legal transition graph
	// (metadata.domain_transitions). No-op for every coded field without an
	// authored graph, and for a request that doesn't touch such a field. Runs
	// against the current row fetched above, so it's inherently update-only.
	if err := validateTransitions(loaded, existing, values); err != nil {
		return nil, err
	}

	if currentStatus == "published" {
		if err := validateUpdateCompleteness(loaded, existing, values); err != nil {
			return nil, err
		}
	}

	// Re-resolve the territory (Phase 11) only when there's a reason to: an
	// explicit override, or a geometry change that could move the feature into a
	// different territory. Absent both, the existing assignment is left untouched
	// (assignTerritory stays false).
	var territoryID *string
	assignTerritory := false
	switch {
	case loaded.FeatureClass.GeometryType == "none":
		if in.TerritoryID != nil {
			return nil, invalid("feature class %q has no geometry, so it can't belong to a territory", loaded.FeatureClass.Code)
		}
	case clearGeometry:
		// Demoting to diagram-only: the feature leaves the map, so it belongs to
		// no territory. Force the column to NULL (assignTerritory true so it's
		// actually written, not left at its stale value).
		assignTerritory = true
		territoryID = nil
	case in.TerritoryID != nil || hasGeometry:
		assignTerritory = true
		territoryID, err = territory.Assign(ctx, db, string(in.Geometry), *loaded.FeatureClass.SRID, in.TerritoryID)
		if err != nil {
			return nil, territoryError(err)
		}
	}

	return updateFeature(ctx, db, loaded, id, in.Geometry, clearGeometry, in.Name, territoryID, assignTerritory, values, in.ExpectedUpdatedAt)
}
