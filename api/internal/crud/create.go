package crud

import (
	"context"
	"encoding/json"
	"strings"

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
//
// ID is normally nil -- the server mints the primary key. It exists for the
// field-tool sync path (Phase 21 Slice 3e, docs/phase-21-field-tool.md): a
// device creates a feature offline with a client-minted UUID and later replays
// it, so the supplied id is honoured and the insert is made idempotent
// (ON CONFLICT (id) DO NOTHING) -- replaying the same op twice is a no-op that
// returns the existing row, not a duplicate or an error. The REST create body
// leaves it unset.
type CreateFeatureInput struct {
	ID          *string         `json:"id,omitempty"`
	Subtype     *int            `json:"subtype,omitempty"`
	Name        *string         `json:"name,omitempty"`
	Geometry    json.RawMessage `json:"geometry,omitempty"`
	TerritoryID *string         `json:"territory_id,omitempty"`
	// LifecycleStatus creates the feature directly in a life state other than the
	// default IN_SERVICE (e.g. PLANNED / UNDER_CONSTRUCTION for an asset still being
	// designed or built). Nil leaves the column's IN_SERVICE default. Validated
	// against the coded set; commission/retire dates are set later via the dedicated
	// lifecycle transition, not on create.
	LifecycleStatus *string        `json:"lifecycle_status,omitempty"`
	Attributes      map[string]any `json:"attributes"`
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

	// Resolve the feature's territory (Phase 11). Territory assignment keys on
	// whether THIS feature carries geometry, not just on the class: a
	// geometry-less class ('none') never has the column, and a geometry_optional
	// class (Phase 26) has it but this particular feature may have been created
	// without geometry (diagram-only) -- in both cases there's nothing to place
	// in a territory and a supplied territory_id is a client error. A feature
	// with geometry always runs Assign -- override if given, else spatial
	// auto-assign. Identical to the REST create path.
	var territoryID *string
	switch {
	case loaded.FeatureClass.GeometryType == "none" || len(in.Geometry) == 0:
		if in.TerritoryID != nil {
			return nil, invalid("feature class %q has no geometry for this feature, so it can't belong to a territory", loaded.FeatureClass.Code)
		}
	default:
		territoryID, err = territory.Assign(ctx, db, string(in.Geometry), *loaded.FeatureClass.SRID, in.TerritoryID)
		if err != nil {
			return nil, territoryError(err)
		}
	}

	// Feature-class deactivation gate: a globally-deactivated class, or one
	// disabled for this feature's territory, accepts no new features (authoritative,
	// not just hidden in the UI). Runs before the user-scope check -- "is this class
	// usable here at all" is a more fundamental refusal than "do YOU have access".
	if err := enforceClassAvailability(ctx, db, loaded, territoryID); err != nil {
		return nil, err
	}

	// Phase 58 spatial write enforcement: a scoped user may only create a
	// feature inside a territory they hold. A no-op unless enforcement is on and
	// the user is scoped; geometry-less features (nil territoryID) are exempt.
	if err := enforceWriteScope(ctx, db, loaded, territoryID); err != nil {
		return nil, err
	}

	// Optional direct-to-non-default life state (e.g. create as PLANNED). Empty
	// leaves the DB's IN_SERVICE default.
	lifecycle := ""
	if in.LifecycleStatus != nil {
		if lifecycle = strings.TrimSpace(*in.LifecycleStatus); lifecycle != "" && !isLifecycleStatus(lifecycle) {
			return nil, invalid("lifecycle_status must be one of %s", strings.Join(lifecycleStatuses, ", "))
		}
	}

	return insertFeature(ctx, db, loaded, in.ID, subtype, in.Geometry, in.Name, territoryID, values, lifecycle)
}
