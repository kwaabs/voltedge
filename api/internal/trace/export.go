package trace

// Exported, non-HTTP entrypoints for callers that reach the trace engine
// without going through the chi handlers -- specifically the Phase 18 MCP
// server (docs/phase-18-mcp.md): its run_trace/list_trace_configurations
// tools (Slice 2) and its isolation/feeder/section trace tools (Slice 5)
// need to call the engine directly. This mirrors the crud/search.go pattern
// from MCP Slice 1: the HTTP handlers keep their own unexported
// request/response types, and these thin wrappers expose an exported surface
// over the same engine functions (runTrace, listConfigurations, isolate,
// feederTrace, sectionTrace) rather than renaming the internal types across
// the whole package. No trace logic lives here -- only the mapping between
// exported DTOs and the internal ones.

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

// TracedFeature is the exported form of tracedFeature: a reached feature plus
// its stored circuit membership and nominal operating voltage. Same wire shape
// as the REST trace API's own Features entries, so an MCP consumer sees the
// same circuit_id/circuit_ref/nominal_voltage_v a REST caller does. See
// tracedFeature for the field meanings.
type TracedFeature struct {
	FeatureClass    string   `json:"feature_class"`
	FeatureID       string   `json:"feature_id"`
	CircuitID       string   `json:"circuit_id,omitempty"`
	CircuitRef      string   `json:"circuit_ref,omitempty"`
	NominalVoltageV *float64 `json:"nominal_voltage_v,omitempty"`
	// OnMap is true when the reached feature carries real map geometry; false
	// when it lives only inside a single-line diagram. See tracedFeature.OnMap.
	OnMap bool `json:"on_map"`
	// Diagrams lists the single-line diagram(s) this feature is placed on (id +
	// name), omitted when it's on none. See tracedFeature.Diagrams.
	Diagrams []DiagramMembership `json:"diagrams,omitempty"`
	// DeviceState is the reached switching device's live position ("OPEN"/
	// "CLOSED"), omitted for non-switch features. See tracedFeature.DeviceState.
	DeviceState string `json:"device_state,omitempty"`
}

// DiagramMembership is the exported form of diagramMembership: one diagram a
// reached feature appears on, by instance id and human name.
type DiagramMembership struct {
	InstanceID string `json:"instance_id"`
	Name       string `json:"name,omitempty"`
}

// RunTraceInput is the exported input to RunTrace, mirroring the POST /trace
// body. Target is required only when the named trace configuration's
// direction is SHORTEST_PATH (validated inside the engine, not here).
type RunTraceInput struct {
	TraceConfiguration string
	Start              FeatureRef
	Target             *FeatureRef
	// CrossTransformers optionally overrides the configuration's stored
	// cross_transformers default for this one run: nil uses the config default,
	// false stops a downstream trace at each transformer (feeder/LV boundary),
	// true continues past into the LV side. See traceRequest.CrossTransformers.
	CrossTransformers *bool
}

// RunTraceResult is the exported form of traceResponse -- the full result of
// one trace run. See traceResponse for the meaning of each field.
type RunTraceResult struct {
	TraceConfiguration string          `json:"trace_configuration"`
	Direction          string          `json:"direction"`
	Start              FeatureRef      `json:"start"`
	Features           []TracedFeature `json:"features"`
	Barriers           []FeatureRef    `json:"barriers"`
	PathFound          *bool           `json:"path_found,omitempty"`
	Targets            []FeatureRef    `json:"targets,omitempty"`
	TargetsFound       *bool           `json:"targets_found,omitempty"`
	// Service is the Phase 62 consumption rollup -- same shape and meaning a
	// REST caller sees. See ServiceRollup.
	Service *ServiceRollup `json:"service,omitempty"`
	// TransformerBoundaries is the subset of Features where a downstream walk
	// stopped at a transformer it was not told to cross -- same shape and meaning
	// a REST caller sees. See traceResponse.TransformerBoundaries.
	TransformerBoundaries []FeatureRef `json:"transformer_boundaries,omitempty"`
	// CrossTransformers echoes the value the walk ran with (only meaningful for a
	// downstream trace). See traceResponse.CrossTransformers.
	CrossTransformers bool `json:"cross_transformers"`
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
	req.CrossTransformers = in.CrossTransformers

	resp, err := runTrace(ctx, db, req)
	if err != nil {
		return nil, err
	}

	return &RunTraceResult{
		TraceConfiguration:    resp.TraceConfiguration,
		Direction:             resp.Direction,
		Start:                 toExportedRef(resp.Start),
		Features:              toExportedTracedFeatures(resp.Features),
		Barriers:              toExportedRefs(resp.Barriers),
		PathFound:             resp.PathFound,
		Targets:               toExportedRefs(resp.Targets),
		TargetsFound:          resp.TargetsFound,
		Service:               resp.Service,
		TransformerBoundaries: toExportedRefs(resp.TransformerBoundaries),
		CrossTransformers:     resp.CrossTransformers,
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

// IsolationInput is the exported input to Isolate, mirroring the POST
// /trace/isolate body. TraceConfiguration must name a DOWNSTREAM-direction
// row; BoundaryClass is optional (defaults, inside the engine, to Device's
// own feature class). See isolationRequest for the field meanings.
type IsolationInput struct {
	TraceConfiguration string
	Device             FeatureRef
	BoundaryClass      string
}

// IsolationResult is the exported form of isolationResponse -- the full
// result of one isolation run. See isolationResponse for what each field
// does and doesn't claim (in particular UpstreamBoundary and
// PossibleBackfeedPaths).
type IsolationResult struct {
	Device                FeatureRef   `json:"device"`
	DeEnergized           []FeatureRef `json:"de_energized"`
	UpstreamBoundary      *FeatureRef  `json:"upstream_boundary,omitempty"`
	CriticalFacilities    []FeatureRef `json:"critical_facilities,omitempty"`
	PossibleBackfeedPaths []FeatureRef `json:"possible_backfeed_paths,omitempty"`
	Service               *ServiceRollup `json:"service,omitempty"`
}

// Isolate runs one isolation trace through the engine's isolate entry point
// and returns the result in exported form. Read-only -- it computes what
// *would* de-energize if the device were opened, without writing anything.
// Errors are the engine's own (validation / not-found), for a caller to
// render as it likes.
func Isolate(ctx context.Context, db bun.IDB, in IsolationInput) (*IsolationResult, error) {
	resp, err := isolate(ctx, db, isolationRequest{
		TraceConfiguration: in.TraceConfiguration,
		Device:             featureRef{FeatureClass: in.Device.FeatureClass, FeatureID: in.Device.FeatureID},
		BoundaryClass:      in.BoundaryClass,
	})
	if err != nil {
		return nil, err
	}
	return &IsolationResult{
		Device:                toExportedRef(resp.Device),
		DeEnergized:           toExportedRefs(resp.DeEnergized),
		UpstreamBoundary:      toExportedRefPtr(resp.UpstreamBoundary),
		CriticalFacilities:    toExportedRefs(resp.CriticalFacilities),
		PossibleBackfeedPaths: toExportedRefs(resp.PossibleBackfeedPaths),
		Service:               resp.Service,
	}, nil
}

// FeederInput is the exported input to FeederTrace, mirroring the POST
// /trace/feeder body. TraceConfiguration must name a DOWNSTREAM-direction
// row. See feederRequest for the field meanings.
type FeederInput struct {
	TraceConfiguration string
	Start              FeatureRef
}

// FeederResult is the exported form of feederResponse -- the whole feeder a
// feature belongs to. See feederResponse for why Heads is a list.
type FeederResult struct {
	Start    FeatureRef      `json:"start"`
	Heads    []FeatureRef    `json:"heads"`
	Features []TracedFeature `json:"features"`
	Service  *ServiceRollup  `json:"service,omitempty"`
}

// FeederTrace runs one feeder trace through the engine's feederTrace entry
// point and returns the result in exported form. Read-only. Errors are the
// engine's own (validation / not-found).
func FeederTrace(ctx context.Context, db bun.IDB, in FeederInput) (*FeederResult, error) {
	resp, err := feederTrace(ctx, db, feederRequest{
		TraceConfiguration: in.TraceConfiguration,
		Start:              featureRef{FeatureClass: in.Start.FeatureClass, FeatureID: in.Start.FeatureID},
	})
	if err != nil {
		return nil, err
	}
	return &FeederResult{
		Start:    toExportedRef(resp.Start),
		Heads:    toExportedRefs(resp.Heads),
		Features: toExportedTracedFeatures(resp.Features),
		Service:  resp.Service,
	}, nil
}

// SectionInput is the exported input to SectionTrace, mirroring the POST
// /trace/section body. BoundaryClass is optional (defaults, inside the
// engine, to DIST_SWITCH). See sectionRequest for the field meanings.
type SectionInput struct {
	TraceConfiguration string
	Start              FeatureRef
	BoundaryClass      string
}

// SectionResult is the exported form of sectionResponse -- everything
// between the nearest bounding device upstream and downstream of Start.
// Either boundary may be absent (nil) when the section runs to the edge of
// the modeled network in that direction.
type SectionResult struct {
	Start              FeatureRef      `json:"start"`
	Features           []TracedFeature `json:"features"`
	UpstreamBoundary   *FeatureRef     `json:"upstream_boundary,omitempty"`
	DownstreamBoundary *FeatureRef     `json:"downstream_boundary,omitempty"`
	Service            *ServiceRollup  `json:"service,omitempty"`
}

// SectionTrace runs one section trace through the engine's sectionTrace
// entry point and returns the result in exported form. Read-only. Errors
// are the engine's own (validation / not-found).
func SectionTrace(ctx context.Context, db bun.IDB, in SectionInput) (*SectionResult, error) {
	resp, err := sectionTrace(ctx, db, sectionRequest{
		TraceConfiguration: in.TraceConfiguration,
		Start:              featureRef{FeatureClass: in.Start.FeatureClass, FeatureID: in.Start.FeatureID},
		BoundaryClass:      in.BoundaryClass,
	})
	if err != nil {
		return nil, err
	}
	return &SectionResult{
		Start:              toExportedRef(resp.Start),
		Features:           toExportedTracedFeatures(resp.Features),
		UpstreamBoundary:   toExportedRefPtr(resp.UpstreamBoundary),
		DownstreamBoundary: toExportedRefPtr(resp.DownstreamBoundary),
		Service:            resp.Service,
	}, nil
}

// ServiceInput is the exported input to ServiceTrace, mirroring the POST
// /trace/service body. TraceConfiguration must name a DOWNSTREAM-direction row.
type ServiceInput struct {
	TraceConfiguration string
	Start              FeatureRef
}

// ServiceResult is the exported form of serviceResponse -- the consumption below
// a feature: the rollup by load/customers/tier plus the per-SERVICE_POINT
// breakdown. See serviceResponse and ServiceRollup.
type ServiceResult struct {
	Start         FeatureRef     `json:"start"`
	Service       *ServiceRollup `json:"service"`
	ServicePoints []ServicePoint `json:"service_points"`
}

// ServiceTrace runs one dedicated service trace through the engine's serviceTrace
// entry point and returns the result in exported form. Read-only. Errors are the
// engine's own (validation / not-found).
func ServiceTrace(ctx context.Context, db bun.IDB, in ServiceInput) (*ServiceResult, error) {
	resp, err := serviceTrace(ctx, db, serviceRequest{
		TraceConfiguration: in.TraceConfiguration,
		Start:              featureRef{FeatureClass: in.Start.FeatureClass, FeatureID: in.Start.FeatureID},
	})
	if err != nil {
		return nil, err
	}
	return &ServiceResult{
		Start:         toExportedRef(resp.Start),
		Service:       resp.Service,
		ServicePoints: resp.ServicePoints,
	}, nil
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

func toExportedRefPtr(r *featureRef) *FeatureRef {
	if r == nil {
		return nil
	}
	out := toExportedRef(*r)
	return &out
}

func toExportedTracedFeatures(fs []tracedFeature) []TracedFeature {
	if fs == nil {
		return nil
	}
	out := make([]TracedFeature, len(fs))
	for i, f := range fs {
		out[i] = TracedFeature{
			FeatureClass:    f.FeatureClass,
			FeatureID:       f.FeatureID,
			CircuitID:       f.CircuitID,
			CircuitRef:      f.CircuitRef,
			NominalVoltageV: f.NominalVoltageV,
			OnMap:           f.OnMap,
			Diagrams:        toExportedDiagramMemberships(f.Diagrams),
			DeviceState:     f.DeviceState,
		}
	}
	return out
}

// toExportedDiagramMemberships maps the internal diagram membership list to its
// exported form, preserving nil (a feature on no diagram) so the field omits.
func toExportedDiagramMemberships(ms []diagramMembership) []DiagramMembership {
	if ms == nil {
		return nil
	}
	out := make([]DiagramMembership, len(ms))
	for i, m := range ms {
		out[i] = DiagramMembership{InstanceID: m.InstanceID, Name: m.Name}
	}
	return out
}
