package crud

import (
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
type UpdateFeatureInput struct {
	Name        *string         `json:"name,omitempty"`
	Geometry    json.RawMessage `json:"geometry,omitempty"`
	TerritoryID *string         `json:"territory_id,omitempty"`
	Attributes  map[string]any  `json:"attributes"`
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

	// Same type-matches-feature-class check create runs -- but skipped entirely
	// when geometry wasn't sent, since absence means "not changing it" here.
	if len(in.Geometry) > 0 {
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
	if loaded.FeatureClass.GeometryType == "none" {
		if in.TerritoryID != nil {
			return nil, invalid("feature class %q has no geometry, so it can't belong to a territory", loaded.FeatureClass.Code)
		}
	} else if in.TerritoryID != nil || len(in.Geometry) > 0 {
		assignTerritory = true
		territoryID, err = territory.Assign(ctx, db, string(in.Geometry), *loaded.FeatureClass.SRID, in.TerritoryID)
		if err != nil {
			return nil, territoryError(err)
		}
	}

	return updateFeature(ctx, db, loaded, id, in.Geometry, in.Name, territoryID, assignTerritory, values)
}
