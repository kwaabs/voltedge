package crud

import (
	"context"
	"encoding/json"

	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/kwaabs/voltedge/api/internal/territory"
	"github.com/uptrace/bun"
)

// CreateFeatureInput is the exported, transport-free form of createRequest --
// the fields needed to create one feature. Same shape and JSON tags as the
// REST create body, so an MCP tool (docs/phase-18-mcp.md) and the HTTP handler
// describe an identical request. Name/Subtype/Geometry/TerritoryID are optional
// exactly as they are on the REST path (see createRequest for each field's
// meaning); a nil Attributes is treated as an empty map.
type CreateFeatureInput struct {
	Subtype     *int            `json:"subtype,omitempty"`
	Name        *string         `json:"name,omitempty"`
	Geometry    json.RawMessage `json:"geometry,omitempty"`
	TerritoryID *string         `json:"territory_id,omitempty"`
	Attributes  map[string]any  `json:"attributes"`
}

// CreateFeature runs the full create path -- metadata validation, territory
// resolution, and the insert -- for one feature and returns the created row's
// envelope. It is the single source of truth both createHandler (REST) and the
// MCP create_feature tool call, so the two front doors can never disagree about
// what a create validates, how a territory is assigned, or what gets written
// (the same reasoning validateForCreate uses for the validation step alone).
//
// The acting user is read from ctx (auth.ActorID, via the same
// created_by/updated_by stamp insertFeature always applies), so a caller must
// have established an authenticated context -- the HTTP session middleware for
// REST, auth.WithUser after a bearer-token resolve for MCP. Errors are this
// package's own validation/not-found errors, for the caller to map to its
// transport (HTTP status, or an in-band MCP tool error).
func CreateFeature(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, in CreateFeatureInput) (map[string]any, error) {
	attrs := in.Attributes
	if attrs == nil {
		attrs = map[string]any{}
	}

	subtype, values, err := validateForCreate(loaded, in.Subtype, in.Geometry, attrs)
	if err != nil {
		return nil, err
	}

	// Resolve the feature's territory (Phase 11). Only geometric classes have a
	// territory_id column, so a supplied territory_id on a non-geometric class
	// is a client error, and a geometric class always runs Assign -- override
	// if given, else spatial auto-assign. Identical to the REST create path.
	var territoryID *string
	if loaded.FeatureClass.GeometryType == "none" {
		if in.TerritoryID != nil {
			return nil, invalid("feature class %q has no geometry, so it can't belong to a territory", loaded.FeatureClass.Code)
		}
	} else {
		territoryID, err = territory.Assign(ctx, db, string(in.Geometry), *loaded.FeatureClass.SRID, in.TerritoryID)
		if err != nil {
			return nil, territoryError(err)
		}
	}

	return insertFeature(ctx, db, loaded, subtype, in.Geometry, in.Name, territoryID, values)
}
