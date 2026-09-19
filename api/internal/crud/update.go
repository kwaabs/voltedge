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
// each field's meaning).
//
// Subtype (Phase 60) is now changeable: an absent (nil) Subtype leaves it, a
// present one switches the feature to that subtype -- but only if the row then
// still satisfies the new subtype's required fields (existing values, values in
// this same request, or a default_override all count; see
// validateRequiredForSubtypeChange). Requesting the subtype it already has is a
// no-op. It stays forbidden on a class with no subtypes.
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
	// Subtype changes the feature's subtype (Phase 60). Absent leaves it; a
	// present code switches to that subtype, gated on the new subtype's
	// required fields (validateRequiredForSubtypeChange). Only meaningful for a
	// class that defines subtypes.
	Subtype *int `json:"subtype,omitempty"`
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
	// Phase 58 spatial write enforcement (part 1): a scoped user may not edit a
	// feature outside their territories. Treated as not-found, exactly as a GET
	// of the same feature would be, so an edit reveals no more than a read.
	if err := enforceReadScope(ctx, db, loaded, existing); err != nil {
		return nil, err
	}
	currentStatus, _ := existing["status"].(string)

	attrs := in.Attributes
	if attrs == nil {
		attrs = map[string]any{}
	}

	// Subtype change (Phase 60): resolve the target subtype (nil = subtype not
	// changing), then enforce the new subtype's required fields against the
	// row-plus-request merge before anything is coerced -- the check the update
	// path used to punt on. Runs before validateAttributes so any default_override
	// this fills into attrs is validated and coerced by the same path as an
	// explicitly-sent value.
	subtypeChange, err := resolveSubtypeChange(loaded, existing, in.Subtype)
	if err != nil {
		return nil, err
	}
	if subtypeChange != nil {
		existingAttrs, _ := existing["attributes"].(map[string]any)
		if err := validateRequiredForSubtypeChange(loaded, subtypeChange, existingAttrs, attrs); err != nil {
			return nil, err
		}
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

	// A published row must stay complete on every write. When subtype is also
	// changing, the completeness that matters is the NEW subtype's, which
	// validateRequiredForSubtypeChange above already enforced (merge-aware and
	// stricter -- it also fills defaults); validateUpdateCompleteness would check
	// the old subtype's rules, which no longer apply, so it's skipped in that case.
	if currentStatus == "published" && subtypeChange == nil {
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

	// Phase 58 spatial write enforcement (part 2): when the edit moves the
	// feature (new geometry or explicit override), the DESTINATION territory
	// must also be one the user holds -- a scoped user can't push a feature into
	// a territory they don't own. A denial here is a 403, not a 404: the user
	// demonstrably owns the feature (it passed the read check above) and is
	// being refused the destination. Clearing geometry (territoryID nil) is
	// exempt.
	if assignTerritory {
		if err := enforceWriteScope(ctx, db, loaded, territoryID); err != nil {
			return nil, err
		}
	}

	var subtypeCode *int
	if subtypeChange != nil {
		subtypeCode = &subtypeChange.Code
	}

	return updateFeature(ctx, db, loaded, id, in.Geometry, clearGeometry, in.Name, territoryID, assignTerritory, subtypeCode, values, in.ExpectedUpdatedAt)
}
