package connectivity

// Exported, non-HTTP entrypoint for callers that reach the connectivity engine
// without going through the chi handlers -- specifically the Phase 18 MCP
// server (docs/phase-18-mcp.md), whose establish_connection tool needs to call
// the zero-trust establish path directly. This mirrors the trace/export.go
// pattern: the HTTP handler keeps its own unexported request/response types,
// and this thin wrapper exposes an exported surface over the same engine
// function (establishConnection) rather than renaming the internal types across
// the package. No connectivity logic lives here -- only the mapping between
// exported DTOs and the internal ones.

import (
	"context"
	"time"

	"github.com/uptrace/bun"
)

// Endpoint is the exported form of endpointRef: one side of a connection -- a
// feature, optionally at a specific named terminal. Same wire shape
// (feature_class / feature_id / terminal) as the REST connectivity API.
type Endpoint struct {
	FeatureClass string  `json:"feature_class"`
	FeatureID    string  `json:"feature_id"`
	Terminal     *string `json:"terminal,omitempty"`
}

// EstablishConnectionInput is the exported input to EstablishConnection,
// mirroring the POST /connectivity body.
type EstablishConnectionInput struct {
	From Endpoint
	To   Endpoint
}

// ConnectionResult is the exported form of connectionResponse -- the committed
// connection, including the network rule that authorized it.
type ConnectionResult struct {
	ID              string    `json:"id"`
	NetworkRuleID   string    `json:"network_rule_id"`
	NetworkRuleName string    `json:"network_rule_name"`
	From            Endpoint  `json:"from"`
	To              Endpoint  `json:"to"`
	EstablishedAt   time.Time `json:"established_at"`
}

// EstablishConnection commits one connection through the engine's zero-trust
// establishConnection entry point and returns it in exported form. This
// WRITES: it only commits when an active network_rules row authorizes the
// from/to classes, subtypes, and terminals (absence, or a matching is_valid =
// false rule, rejects it). Re-establishing an identical connection is a no-op
// (ON CONFLICT keeps the original). The acting user is read from ctx
// (auth.ActorID) and stamped as established_by, so a caller must have
// established an authenticated context. Errors are the engine's own
// (validation / not-found), for the caller to map to its transport.
func EstablishConnection(ctx context.Context, db bun.IDB, in EstablishConnectionInput) (*ConnectionResult, error) {
	resp, err := establishConnection(ctx, db, establishRequest{
		From: endpointRef{FeatureClass: in.From.FeatureClass, FeatureID: in.From.FeatureID, Terminal: in.From.Terminal},
		To:   endpointRef{FeatureClass: in.To.FeatureClass, FeatureID: in.To.FeatureID, Terminal: in.To.Terminal},
	})
	if err != nil {
		return nil, err
	}
	return &ConnectionResult{
		ID:              resp.ID,
		NetworkRuleID:   resp.NetworkRuleID,
		NetworkRuleName: resp.NetworkRuleName,
		From:            toExportedEndpoint(resp.From),
		To:              toExportedEndpoint(resp.To),
		EstablishedAt:   resp.EstablishedAt,
	}, nil
}

func toExportedEndpoint(r endpointRef) Endpoint {
	return Endpoint{FeatureClass: r.FeatureClass, FeatureID: r.FeatureID, Terminal: r.Terminal}
}
