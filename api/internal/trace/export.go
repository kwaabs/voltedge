package trace

// Exported, non-HTTP entrypoints for callers that reach the trace engine
// without going through the chi handlers -- specifically the Phase 18 MCP
// server (docs/phase-18-mcp.md), whose read_trace/list_trace_configurations
// tools need to call the engine directly. This mirrors the crud/search.go
// pattern from MCP Slice 1: the HTTP handlers keep their own unexported
// request/response types, and these thin wrappers expose an exported surface
// over the same engine functions (runTrace, listConfigurations) rather than
// renaming the internal types across the whole package. No trace logic lives
// here -- only the mapping between exported DTOs and the internal ones.

import (
	"context"

	"github.com/uptrace/bun"
)

// FeatureRef is the exported form of featureRef: a feature class code plus a
// feature id, identifying one feature row. Same wire shape (feature_class /
// feature_id) as the REST trace API.
type FeatureRef struct {
	FeatureClass string `json:"feature_class"`
	FeatureID    string `json:"feature_id"`
}

// RunTraceInput is the exported input to RunTrace, mirroring the POST /trace
// body. Target is required only when the named trace configuration's
// direction is SHORTEST_PATH (validated inside the engine, not here).
type RunTraceInput struct {
	TraceConfiguration string
	Start              FeatureRef
	Target             *FeatureRef
}

// RunTraceResult is the exported form of traceResponse -- the full result of
// one trace run. See traceResponse for the meaning of each field.
type RunTraceResult struct {
	TraceConfiguration string       `json:"trace_configuration"`
	Direction          string       `json:"direction"`
	Start              FeatureRef   `json:"start"`
	Features           []FeatureRef `json:"features"`
	Barriers           []FeatureRef `json:"barriers"`
	PathFound          *bool        `json:"path_found,omitempty"`
	Targets            []FeatureRef `json:"targets,omitempty"`
	TargetsFound       *bool        `json:"targets_found,omitempty"`
}

// RunTrace runs one trace through the engine's single entry point (runTrace)
// and returns the result in exported form. Errors are the engine's own --
// validation errors (isValidationError) for a bad request and not-found
// errors (isNotFoundError) for a missing configuration or start feature --
// which a caller can render however it likes (the MCP server surfaces them
// as an in-band tool error).
func RunTrace(ctx context.Context, db bun.IDB, in RunTraceInput) (*RunTraceResult, error) {
	req := traceRequest{
		TraceConfiguration: in.TraceConfiguration,
		Start:              featureRef{FeatureClass: in.Start.FeatureClass, FeatureID: in.Start.FeatureID},
	}
	if in.Target != nil {
		req.Target = &featureRef{FeatureClass: in.Target.FeatureClass, FeatureID: in.Target.FeatureID}
	}

	resp, err := runTrace(ctx, db, req)
	if err != nil {
		return nil, err
	}

	return &RunTraceResult{
		TraceConfiguration: resp.TraceConfiguration,
		Direction:          resp.Direction,
		Start:              toExportedRef(resp.Start),
		Features:           toExportedRefs(resp.Features),
		Barriers:           toExportedRefs(resp.Barriers),
		PathFound:          resp.PathFound,
		Targets:            toExportedRefs(resp.Targets),
		TargetsFound:       resp.TargetsFound,
	}, nil
}

// TraceConfigurationSummary is the exported form of traceConfigurationSummary
// -- one available trace configuration: its code (pass as TraceConfiguration
// to RunTrace), display name, and the raw direction it drives.
type TraceConfigurationSummary struct {
	Code      string `json:"code"`
	Name      string `json:"name"`
	Direction string `json:"direction"`
}

// ListConfigurations returns every available trace configuration, so a caller
// can discover valid codes instead of guessing. Wraps the engine's
// listConfigurations (which backs GET /trace/configurations).
func ListConfigurations(ctx context.Context, db bun.IDB) ([]TraceConfigurationSummary, error) {
	configs, err := listConfigurations(ctx, db)
	if err != nil {
		return nil, err
	}
	out := make([]TraceConfigurationSummary, len(configs))
	for i, c := range configs {
		out[i] = TraceConfigurationSummary{Code: c.Code, Name: c.Name, Direction: c.Direction}
	}
	return out, nil
}

func toExportedRef(r featureRef) FeatureRef {
	return FeatureRef{FeatureClass: r.FeatureClass, FeatureID: r.FeatureID}
}

func toExportedRefs(rs []featureRef) []FeatureRef {
	if rs == nil {
		return nil
	}
	out := make([]FeatureRef, len(rs))
	for i, r := range rs {
		out[i] = toExportedRef(r)
	}
	return out
}
