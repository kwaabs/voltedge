// Package powerflow is the load-flow engine for Phase 8/9: a radial
// (tree-shaped) distribution feeder's per-node voltage and per-branch
// loading, computed from the same substrate the trace engine already
// walks -- runtime.feature_connectivity, barrier-aware via the existing
// DOWNSTREAM_TRACE trace_barrier_rules -- plus two new inputs nothing
// else has needed yet: real demand at a SERVICE_POINT and (optionally)
// a source's own impedance.
//
// Method: forward-backward sweep, the standard technique for a radial
// distribution feeder (Kersting), not full Newton-Raphson AC power
// flow -- unnecessary complexity for a tree with no loops, and this
// project already has loop detection (LOOP_CHECK_TRACE) to keep it
// that way. A backward pass accumulates downstream (P,Q) demand into
// each node; a forward pass applies the standard linearized voltage-
// drop approximation ΔV ≈ (P·R + Q·X) / V per branch, using the
// previous iteration's own voltage as V -- three iterations, refining
// only which V the drop is measured against (demand itself doesn't
// depend on voltage under this pass's constant-power load assumption).
//
// Where a branch's impedance comes from: every graph node -- not just
// the literal connectivity edges -- corresponds to one feature, and a
// feature whose class has network_role = 'edge' (a conductor) is the
// one that carries real R+jX, resolved from whichever of its own
// fields currently holds a material-linked domain value (see
// metadata.MaterialSpec) times its own real geometry length
// (ST_Length(geom::geography)). A junction/container node (a meter, a
// cutout, a link box, a busbar, a chamber) contributes zero impedance
// -- electrically pass-through, exactly as docs/phase-9-lv-service-
// delivery.md already describes a METER. Deliberately not hardcoded to
// a field named "conductor_spec": LV_CONDUCTOR and MV_CONDUCTOR are
// free to name their own material-linked field differently (they
// don't today, but nothing here assumes they won't diverge), so
// resolveConductorImpedance scans a class's own fields for whichever
// one resolves, rather than a fixed name.
//
// Deliberately its own package, not layered onto api/internal/trace --
// same reasoning trace itself gives for staying independent of
// connectivity/relationship: enough surface in common (walking the
// same table, resolving a feature ref) to look shareable, but this
// package's node model tracks a real tree (parent/children, for two
// full sweep passes) and per-node electrical state trace's
// membership-only BFS has no reason to carry, so each keeps its own
// copy of the small graph-walk primitives it actually needs.
package powerflow

// featureRef identifies one specific feature row -- same shape as
// api/internal/trace's own copy, kept as this package's own copy for
// the same reason every package that touches runtime.feature_connectivity
// already does.
type featureRef struct {
	FeatureClass string `json:"feature_class"`
	FeatureID    string `json:"feature_id"`
}

// loadFlowRequest is POST /analysis/load-flow's body. NominalVoltageV is
// required rather than derived from a per-class voltage field: MV and
// LV sit at very different nominal voltages, LV_CONDUCTOR carries no
// voltage-class field at all (only phase), and DIST_XFMR only carries
// its primary side -- so guessing which field means "the sending-end
// voltage at Start" would be fragile in a way an explicit, honest
// request parameter isn't. Phase-to-neutral or line-to-line is the
// caller's own choice as long as it's applied consistently; this
// engine works in per-unit-length R/X and treats every value in this
// request and response as whatever single voltage basis was supplied.
type loadFlowRequest struct {
	Start           featureRef `json:"start"`
	NominalVoltageV float64    `json:"nominal_voltage_v"`
}

// nodeResult is one visited node's resolved electrical state.
// VoltageV/VoltageDropPct are always populated. CurrentAmps/
// AmpacityAmps only apply to a conductor (network_role = 'edge') node;
// RatedKVA only applies to a rated junction node (today, a DIST_XFMR
// with its own rated_kva set) -- a node is never both, so the two
// loading sources share one LoadingPct rather than each needing their
// own: a caller displaying "how loaded is this" doesn't need to know
// which kind of equipment this particular node happens to be.
type nodeResult struct {
	FeatureClass     string   `json:"feature_class"`
	FeatureID        string   `json:"feature_id"`
	VoltageV         float64  `json:"voltage_v"`
	VoltageDropPct   float64  `json:"voltage_drop_pct"`
	CumulativeKW     float64  `json:"cumulative_kw"`
	CumulativeKVAR   float64  `json:"cumulative_kvar"`
	CurrentAmps      *float64 `json:"current_amps,omitempty"`
	AmpacityAmps     *float64 `json:"ampacity_amps,omitempty"`
	RatedKVA         *float64 `json:"rated_kva,omitempty"`
	LoadingPct       *float64 `json:"loading_pct,omitempty"`
	MaterialResolved bool     `json:"material_resolved"`
}

// loadFlowResponse is the full result of one sweep. Barriers mirrors
// api/internal/trace's own traceResponse.Barriers -- the subset of
// Nodes where the walk actually stopped because of a live, open
// device, not just because the network ends there. Overloaded is a
// convenience subset of Nodes whose LoadingPct exceeds 100 -- a
// client shouldn't have to recompute that scan itself.
type loadFlowResponse struct {
	Start           featureRef   `json:"start"`
	NominalVoltageV float64      `json:"nominal_voltage_v"`
	Iterations      int          `json:"iterations"`
	Nodes           []nodeResult `json:"nodes"`
	Barriers        []featureRef `json:"barriers"`
	Overloaded      []featureRef `json:"overloaded"`
}

// faultCurrentRequest is POST /analysis/fault-current's body -- same
// shape and same reasoning as loadFlowRequest's own NominalVoltageV
// (required, caller's own consistent LN/LL basis; see that type's doc
// comment).
type faultCurrentRequest struct {
	Start           featureRef `json:"start"`
	NominalVoltageV float64    `json:"nominal_voltage_v"`
}

// faultNodeResult is the available symmetrical (3-phase bolted) fault
// current at one node, as if a bolted fault occurred right there.
// ROhmTotal/XOhmTotal/ZOhmTotal are the series impedance summed from
// the network's own source down to this node -- always populated,
// zero for the start node itself when no source impedance is set.
// FaultCurrentAmps is omitted (not zero, not infinite) when ZOhmTotal
// is zero: an ideal, zero-impedance path to this point makes the
// fault current mathematically unbounded, not a real number to report
// -- the caller has incomplete impedance data (no source impedance
// set, and/or no conductor between here and the source resolved a
// material spec), not "the fault current here is 0A."
type faultNodeResult struct {
	FeatureClass     string   `json:"feature_class"`
	FeatureID        string   `json:"feature_id"`
	ROhmTotal        float64  `json:"r_ohm_total"`
	XOhmTotal        float64  `json:"x_ohm_total"`
	ZOhmTotal        float64  `json:"z_ohm_total"`
	FaultCurrentAmps *float64 `json:"fault_current_amps,omitempty"`
	MaterialResolved bool     `json:"material_resolved"`
}

// faultCurrentResponse mirrors loadFlowResponse's own shape: one
// reading per downstream node rather than a single target-specific
// figure, so a caller sees available fault current across the whole
// network from one call, same as a load flow's own per-node voltage
// table.
type faultCurrentResponse struct {
	Start           featureRef        `json:"start"`
	NominalVoltageV float64           `json:"nominal_voltage_v"`
	Nodes           []faultNodeResult `json:"nodes"`
	Barriers        []featureRef      `json:"barriers"`
}

// apiError is the JSON body returned for any 4xx/5xx response.
type apiError struct {
	Error string `json:"error"`
}
