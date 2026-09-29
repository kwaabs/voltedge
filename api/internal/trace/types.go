// Package trace is the network trace engine for Phase 4: walks
// runtime.feature_connectivity edge by edge according to a named
// metadata.trace_configurations profile, the same table
// api/internal/connectivity writes but nothing has read as a graph
// until now (see docs/phase-2-connectivity.md's own "No trace engine
// yet" note).
//
// This pass builds the four raw directions docs/metadata-core.md
// describes (connected, downstream, upstream, shortest_path) with real
// barrier-stopping (trace_barrier_rules, checked live against a
// feature's current attribute value at each hop -- never baked into
// the connectivity graph itself). Isolation and loop/mesh detection
// (which compose two of these primitives) aren't built yet -- see
// docs/phase-4-trace-engine.md's "What's still open". cross_transformers
// is now enforced for downstream traces -- the transformer boundary the
// original stub left open is answered as functional_role='transformer'
// (migration 0091): a downstream walk stops at each transformer unless the
// config's cross_transformers default (overridable per-run via the request's
// cross_transformers, the map's "continue past transformers" checkbox) says to
// continue into the stepped-down LV network. respect_phase is still accepted
// and threaded but not enforced -- how phase capability intersects hop to hop
// remains the open design question this pass deliberately didn't answer.
//
// Deliberately its own package, not layered onto api/internal/connectivity
// -- same reasoning connectivity and relationship already established
// for each other: the two have enough surface in common (resolving a
// feature ref, walking the same table) to look shareable, but a trace's
// read-only, multi-hop, config-driven walk is a different shape of
// problem than a single establish/list/revoke, so keeping them
// independent avoids forcing an awkward shared abstraction.
package trace

// featureRef identifies one specific feature row -- same shape as
// api/internal/connectivity's endpointRef, kept as this package's own
// copy for the same reason connectivity and relationship each keep
// their own instead of sharing one.
type featureRef struct {
	FeatureClass string `json:"feature_class"`
	FeatureID    string `json:"feature_id"`
}

// tracedFeature is one node in a trace's Features list, enriched past the
// bare featureRef with the two attributes a trace reader most often wants
// alongside "what did the walk reach": the feature's stored circuit
// membership (Phase 37) and its nominal operating voltage. All three extras
// are omitempty -- a class carrying neither a circuit_id field nor any
// voltage-class field (a POLE, a plain busbar), or one carrying the field
// with no value set on this particular feature, reports just its ref.
//
// CircuitID is the stored circuit_id uuid, CircuitRef the human circuit name
// (circuit_ref) the Phase 37 assign stamps alongside it. NominalVoltageV is
// the element's own nameplate/operating voltage in volts -- a conductor's
// voltage_class, a transformer's primary (source-side) voltage -- the
// topology-intrinsic voltage a trace can know from stored attributes without
// a load-flow solve. Deliberately NOT a computed per-node voltage: that only
// a power-flow run produces, and this engine stays decoupled from powerflow
// (see this package's own doc), so a client that wants computed voltages
// overlays them from a load-flow result itself.
// ParentFeatureClass/ParentFeatureID are the reached feature's one-hop-upstream
// neighbour in the trace's BFS tree -- the feature the walk arrived here FROM
// (toward the start for a downstream trace, away from it for an upstream one).
// Only the main graph trace stamps them (see runTrace); the start node, and any
// node whose parent fell outside the caller's territory scope, carry neither, so
// both are omitempty pointers. A client uses this to orient direction without a
// second query -- e.g. the map's directional "marching ants" trace animation
// crawls each line from the endpoint nearest its parent's geometry outward.
type tracedFeature struct {
	FeatureClass       string   `json:"feature_class"`
	FeatureID          string   `json:"feature_id"`
	CircuitID          string   `json:"circuit_id,omitempty"`
	CircuitRef         string   `json:"circuit_ref,omitempty"`
	NominalVoltageV    *float64 `json:"nominal_voltage_v,omitempty"`
	ParentFeatureClass *string  `json:"parent_feature_class,omitempty"`
	ParentFeatureID    *string  `json:"parent_feature_id,omitempty"`
	// OnMap is true when the reached feature carries real map geometry (a
	// non-null geom -- crud.formatRow only emits a "geometry" key then). A
	// geometry-less class (a diagram-only conductor stub, an off-map device)
	// reports false: the trace still reached it topologically, but it lives
	// only inside a single-line diagram, not on the map canvas.
	OnMap bool `json:"on_map"`
	// Diagrams lists every single-line diagram this reached feature is placed
	// on (runtime.diagram_placements -> diagram_instances), so a reader can see
	// which results are drawn on the map vs. only inside a diagram, and name the
	// diagram(s). Omitted (nil) when the feature is on no diagram. Name may be
	// empty for an unnamed diagram instance.
	Diagrams []diagramMembership `json:"diagrams,omitempty"`
	// DeviceState is the reached feature's live switch position -- the coded
	// device_state attribute (a DEVICE_STATE domain value, "OPEN"/"CLOSED") that
	// switching devices (MV_SWITCH, MV_BREAKER, MV_ISOLATOR, a bus coupler, ...)
	// carry and everything else omits. It's exactly the value a downstream trace
	// checks live as a barrier (an OPEN device halts the walk), surfaced here so a
	// reader can see the position of every switch the trace reached -- a CLOSED
	// switch it passed through as well as an OPEN one it stopped at -- not just the
	// barriers. Empty (omitted) for any feature whose class has no device_state
	// field, or that carries it unset. Read from the same live row enrichNode
	// already loads, so it costs nothing extra.
	DeviceState string `json:"device_state,omitempty"`
}

// diagramMembership names one single-line diagram a reached feature appears on:
// the diagram_instances row id and its human name (nullable in the table -->
// empty string here when unset).
type diagramMembership struct {
	InstanceID string `json:"instance_id"`
	Name       string `json:"name,omitempty"`
}

// traceRequest is the POST /trace body. Target is only meaningful (and
// required) when the named trace_configurations row's direction is
// SHORTEST_PATH -- validated in runTrace, not here, since knowing that
// requires the configuration to already be loaded.
// CrossTransformers is an optional per-run override of the named
// configuration's stored cross_transformers default. nil (the usual case)
// uses the config's own value; a non-nil pointer wins -- false makes a
// downstream trace stop at each transformer (the boundary between a feeder and
// its stepped-down LV network), true lets it continue past into the LV side.
// It backs the map's "continue past transformers" checkbox and only affects a
// DOWNSTREAM trace; other directions never stop at a transformer regardless.
type traceRequest struct {
	TraceConfiguration string      `json:"trace_configuration"`
	Start              featureRef  `json:"start"`
	Target             *featureRef `json:"target,omitempty"`
	CrossTransformers  *bool       `json:"cross_transformers,omitempty"`
}

// traceResponse is the full result of one trace run. Features includes
// the starting feature itself (a one-node trace is a legal, if
// uninteresting, result -- e.g. an isolated feature with no
// connections at all). Barriers is the subset of Features where
// traversal actually stopped because of a matching, live
// trace_barrier_rules condition -- distinct from a node that simply
// has no further edges, so a caller (or a future map highlight) can
// tell "the trace stopped here because of an open device" apart from
// "the trace stopped here because the network just ends."
//
// Targets/TargetsFound are only ever populated when the named
// trace_configurations row has a target_class_id set (see
// migrations/0018) -- omitted entirely otherwise, same reasoning
// PathFound is only set for a SHORTEST_PATH trace. Targets is the
// subset of Features whose feature class matched, e.g. every
// transformer a "trace to transformer" run reached.
// Features carries the enriched tracedFeature shape (circuit + nominal
// voltage); Barriers/Targets stay bare featureRefs, since both are subsets
// of Features a client cross-references by class:id rather than re-reading
// the enrichment off.
type traceResponse struct {
	TraceConfiguration string          `json:"trace_configuration"`
	Direction          string          `json:"direction"`
	Start              featureRef      `json:"start"`
	Features           []tracedFeature `json:"features"`
	Barriers           []featureRef    `json:"barriers"`
	PathFound          *bool           `json:"path_found,omitempty"`
	Targets            []featureRef    `json:"targets,omitempty"`
	TargetsFound       *bool           `json:"targets_found,omitempty"`
	// Service is the consumption rollup of the SERVICE_POINTs this trace
	// reached (Phase 62): total load + customers, the critical subset, and the
	// per-restoration-tier breakdown. Always present, even on a trace that
	// reached no load (every tier zero), so a client can render the summary
	// unconditionally. See ServiceRollup.
	Service *ServiceRollup `json:"service,omitempty"`
	// TransformerBoundaries is the subset of Features where a DOWNSTREAM walk
	// stopped because it reached a transformer and was not told to cross it (the
	// feeder/LV boundary). Distinct from Barriers (an open device) and Targets (a
	// config's target_class_id) so a client can label "the trace stopped here at a
	// transformer -- tick 'continue past transformers' to include its LV network".
	// Omitted entirely when the trace crossed transformers or reached none.
	TransformerBoundaries []featureRef `json:"transformer_boundaries,omitempty"`
	// CrossTransformers echoes the resolved value the walk actually ran with (the
	// per-run override if the request set one, else the config default) -- only
	// meaningful for a DOWNSTREAM trace. The result UI reads it to render the
	// "Continue past transformers" toggle in the correct checked state so a single
	// click re-runs the same trace with the boundary crossed (or restored).
	CrossTransformers bool `json:"cross_transformers"`
}

// traceConfigurationSummary is one row of GET /trace/configurations --
// enough for a client to discover what's available (and which raw
// direction it drives) instead of a fixed set of hardcoded codes.
type traceConfigurationSummary struct {
	Code      string `json:"code"`
	Name      string `json:"name"`
	Direction string `json:"direction"`
}

// apiError is the JSON body returned for any 4xx/5xx response.
type apiError struct {
	Error string `json:"error"`
}
