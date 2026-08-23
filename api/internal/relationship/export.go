package relationship

// Exported, non-HTTP entrypoint for the Phase 18 MCP server
// (docs/phase-18-mcp.md), whose establish_relationship tool calls the
// explicit-confirm establish path directly rather than through the chi
// handler. Parallel to connectivity/export.go and the trace/export.go pattern:
// the HTTP handler keeps its unexported request/response types, and this thin
// wrapper exposes an exported surface over the same engine function
// (establishRelationship). No relationship logic lives here -- only the
// mapping between exported DTOs and the internal ones.

import (
	"context"
	"time"

	"github.com/uptrace/bun"
)

// FeatureRef is the exported form of featureRef: one specific feature row.
type FeatureRef struct {
	FeatureClass string `json:"feature_class"`
	FeatureID    string `json:"feature_id"`
}

// EstablishRelationshipInput is the exported input to EstablishRelationship,
// mirroring the POST /relationships body. Kind is optional -- only needed to
// disambiguate when more than one relationship_classes row exists for the same
// origin/destination feature-class pairing.
type EstablishRelationshipInput struct {
	Origin      FeatureRef
	Destination FeatureRef
	Kind        *string
}

// RelationshipResult is the exported form of relationshipResponse -- the
// committed relationship, plus an optional plausibility warning when the two
// features' geometries are further apart than expected for the class.
type RelationshipResult struct {
	ID                    string     `json:"id"`
	RelationshipClassID   string     `json:"relationship_class_id"`
	RelationshipClassName string     `json:"relationship_class_name"`
	Kind                  string     `json:"kind"`
	Cardinality           string     `json:"cardinality"`
	Origin                FeatureRef `json:"origin"`
	Destination           FeatureRef `json:"destination"`
	EstablishedAt         time.Time  `json:"established_at"`
	PlausibilityWarning   *string    `json:"plausibility_warning,omitempty"`
}

// EstablishRelationship commits one structural/spatial relationship through the
// engine's explicit-confirm establishRelationship entry point and returns it in
// exported form. This WRITES: it only commits when a matching
// relationship_classes row exists for the origin/destination pairing (and the
// optional kind) and that class's cardinality isn't already exhausted;
// otherwise it errors. Re-establishing an identical relationship is a no-op (ON
// CONFLICT keeps the original, so no plausibility warning recomputes). The
// acting user is read from ctx (auth.ActorID) and stamped as established_by.
// Errors are the engine's own (validation / not-found / cardinality), for the
// caller to map to its transport.
func EstablishRelationship(ctx context.Context, db bun.IDB, in EstablishRelationshipInput) (*RelationshipResult, error) {
	resp, err := establishRelationship(ctx, db, establishRequest{
		Origin:      featureRef{FeatureClass: in.Origin.FeatureClass, FeatureID: in.Origin.FeatureID},
		Destination: featureRef{FeatureClass: in.Destination.FeatureClass, FeatureID: in.Destination.FeatureID},
		Kind:        in.Kind,
	})
	if err != nil {
		return nil, err
	}
	return &RelationshipResult{
		ID:                    resp.ID,
		RelationshipClassID:   resp.RelationshipClassID,
		RelationshipClassName: resp.RelationshipClassName,
		Kind:                  resp.Kind,
		Cardinality:           resp.Cardinality,
		Origin:                FeatureRef{FeatureClass: resp.Origin.FeatureClass, FeatureID: resp.Origin.FeatureID},
		Destination:           FeatureRef{FeatureClass: resp.Destination.FeatureClass, FeatureID: resp.Destination.FeatureID},
		EstablishedAt:         resp.EstablishedAt,
		PlausibilityWarning:   resp.PlausibilityWarning,
	}, nil
}
