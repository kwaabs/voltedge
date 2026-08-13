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
// and respect_phase are accepted on a trace_configurations row and
// threaded through, but not enforced this pass either -- both need real
// design questions answered first (what marks a transformer boundary?
// how does phase capability intersect hop to hop?) that this pass
// deliberately didn't try to answer under time pressure.
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

// traceRequest is the POST /trace body. Target is only meaningful (and
// required) when the named trace_configurations row's direction is
// SHORTEST_PATH -- validated in runTrace, not here, since knowing that
// requires the configuration to already be loaded.
type traceRequest struct {
	TraceConfiguration string      `json:"trace_configuration"`
	Start              featureRef  `json:"start"`
	Target             *featureRef `json:"target,omitempty"`
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
type traceResponse struct {
	TraceConfiguration string       `json:"trace_configuration"`
	Direction          string       `json:"direction"`
	Start              featureRef   `json:"start"`
	Features           []featureRef `json:"features"`
	Barriers           []featureRef `json:"barriers"`
	PathFound          *bool        `json:"path_found,omitempty"`
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
