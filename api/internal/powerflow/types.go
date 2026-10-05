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

// unbalancedRequest is POST /analysis/unbalanced-load-flow's body. Start and
// NominalVoltageV carry the same meaning (and basis freedom) as loadFlowRequest.
// Coupled selects the solver fidelity: false (default) is the Tier 1 decoupled
// per-phase sweep; true is the Tier 2 Carson-coupled complex sweep, which reads
// each conductor's line geometry (metadata.MaterialSpec, migrations/0117) to
// build a full 3x3 phase-impedance matrix with mutual terms. FrequencyHz and
// EarthResistivityOhmM are the two Carson environmental inputs -- both optional,
// defaulting to 50 Hz and 100 ohm-m (defaultFrequencyHz / defaultEarthResistivity
// OhmM); both ignored on a decoupled run.
type unbalancedRequest struct {
	Start                featureRef `json:"start"`
	NominalVoltageV      float64    `json:"nominal_voltage_v"`
	Coupled              bool       `json:"coupled"`
	FrequencyHz          float64    `json:"frequency_hz"`
	EarthResistivityOhmM float64    `json:"earth_resistivity_ohm_m"`
}

// nodeResult is one visited node's resolved electrical state.
// VoltageV/VoltageDropPct are always populated. NominalBaseV is the
// voltage base VoltageV and VoltageDropPct are expressed on: the run's
// nominal_voltage_v for nodes on the source side of every transformer,
// and a transformer's own secondary_voltage for nodes below it -- so a
// single result set spanning MV -> DIST_XFMR -> LV reports each node's
// drop against its own side's nominal (LV nodes near their LV base, not
// near the MV nominal), and a client can tell the two voltage levels
// apart. CurrentAmps/AmpacityAmps only apply to a conductor
// (network_role = 'edge') node; RatedKVA only applies to a rated
// junction node (today, a DIST_XFMR with its own rated_kva set) -- a
// node is never both, so the two loading sources share one LoadingPct
// rather than each needing their own: a caller displaying "how loaded
// is this" doesn't need to know which kind of equipment this particular
// node happens to be.
type nodeResult struct {
	FeatureClass string `json:"feature_class"`
	FeatureID    string `json:"feature_id"`
	// ParentFeatureClass / ParentFeatureID point at the node one hop closer to the
	// sweep's start in buildTree's radial spanning tree -- the feature that feeds
	// this one. They let a client render the ladder result as the tree it actually
	// is (voltage drops down a branch, loading accumulates up it) instead of a flat
	// list, the same parent stamping api/internal/trace's traceResponse carries.
	// Both nil for the start (it has no parent) and for any node whose parent fell
	// outside the result set, so a client can always resolve a stamped parent.
	ParentFeatureClass *string `json:"parent_feature_class,omitempty"`
	ParentFeatureID    *string `json:"parent_feature_id,omitempty"`
	// CircuitID / CircuitRef are the node's stored circuit membership (Phase 37),
	// the same display extras an enriched trace and a coordination device carry;
	// both empty when the node's class has no circuit field or it's unset. The
	// per-node operating voltage is already reported as VoltageV / NominalBaseV.
	CircuitID  string `json:"circuit_id,omitempty"`
	CircuitRef string `json:"circuit_ref,omitempty"`
	// Ownership is "private" for an owner's plant (the asset-ownership axis),
	// empty for utility-owned/unset -- so the results panel can flag which part
	// of a run is the customer's own network. The load flow crosses the boundary
	// rather than stopping at it; this is purely a result label.
	Ownership        string   `json:"ownership,omitempty"`
	VoltageV         float64  `json:"voltage_v"`
	VoltageDropPct   float64  `json:"voltage_drop_pct"`
	NominalBaseV     float64  `json:"nominal_base_v"`
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

// faultCurrentRequest is POST /analysis/fault-current's body -- the same
// shape as loadFlowRequest, and for the same reasons. Start is the source
// (the fault-current calc walks the same radial tree away from it);
// NominalVoltageV is the pre-fault sending-end voltage the bolted-fault
// current is measured against, required rather than derived for exactly
// the reasons loadFlowRequest.NominalVoltageV documents. The two requests
// are kept as separate types rather than one shared struct so each can
// grow its own fields later without disturbing the other.
type faultCurrentRequest struct {
	Start           featureRef `json:"start"`
	NominalVoltageV float64    `json:"nominal_voltage_v"`

	// FaultType selects the fault modelled: "three_phase" (default -- the
	// symmetrical bolted fault this endpoint has always computed) or "slg"
	// (single-line-to-ground, which adds the zero-sequence network). An SLG run
	// still reports the three-phase figures too, plus its own ground-fault
	// fields per node. FaultImpedanceOhm is the fault's own impedance Zf (the
	// arc/earth resistance of the fault itself), 0 for a bolted fault; it enters
	// the SLG result as the standard 3·Zf term and is ignored by three_phase.
	FaultType         string  `json:"fault_type"`
	FaultImpedanceOhm float64 `json:"fault_impedance_ohm"`

	// VoltageBasis declares whether NominalVoltageV (and every transformer
	// secondary base derived from it) is a line-to-line or a line-to-neutral
	// figure, so the engine can turn an accumulated per-phase impedance into the
	// STANDARD per-phase symmetrical fault current I = V_LN / |Z| rather than the
	// √3-inflated V_LL / |Z|. "line_to_line" (the default -- the basis a
	// distribution nominal is almost always entered on, e.g. 11000 / 400) divides
	// the driving voltage by √3; "line_to_neutral" uses it as-is. The choice
	// scales the grid infeed, the SLG current, and the embedded-generation infeed
	// together (they share one V²/S convention), so a result is never a mix of
	// bases. Empty = line_to_line.
	VoltageBasis string `json:"voltage_basis"`
}

// faultNodeResult is one node's symmetrical (3-phase bolted) fault level:
// the total series impedance from the source down to this node, and the
// prospective fault current a bolted fault at this point would draw,
// I = V / |Z_total|. ResistanceOhm/ReactanceOhm are the accumulated
// R and X of that path (source impedance + every conductor branch between
// source and here, plus any transformer's own series impedance), referred
// to NominalBaseV -- the voltage base this node sits on: the run's nominal
// above every transformer, a transformer's secondary below it, so an
// MV-side and an LV-side fault level are each measured on their own base
// and a client can tell them apart. ImpedanceOhm is their magnitude.
// FaultCurrentAmps is omitted only when |Z_total| is zero -- an ideal,
// zero-impedance source with no conductor between it and this node, where a
// bolted fault current is unbounded and no finite figure is honest. It is the
// TOTAL prospective current: grid infeed PLUS any embedded-generation
// contribution (see GridContributionAmps / GeneratorContributionAmps) -- the
// figure switchgear duty is checked against.
// MaterialResolved is false when any conductor branch on the path lacked
// resolved R/X, or a transformer on the path contributed no series
// impedance (no nameplate %Z/rating) -- the reported current is then an
// optimistic upper bound, since a missing series impedance can only make
// the true |Z_total| larger and the true fault current smaller.
type faultNodeResult struct {
	FeatureClass string `json:"feature_class"`
	FeatureID    string `json:"feature_id"`
	// CircuitID / CircuitRef are the node's stored circuit membership (Phase 37),
	// the same display extras an enriched trace and a coordination device carry;
	// both empty when the node's class has no circuit field or it's unset. The
	// per-node fault voltage base is already reported as NominalBaseV.
	CircuitID        string   `json:"circuit_id,omitempty"`
	CircuitRef       string   `json:"circuit_ref,omitempty"`
	ResistanceOhm    float64  `json:"resistance_ohm"`
	ReactanceOhm     float64  `json:"reactance_ohm"`
	ImpedanceOhm     float64  `json:"impedance_ohm"`
	NominalBaseV     float64  `json:"nominal_base_v"`
	FaultCurrentAmps *float64 `json:"fault_current_amps,omitempty"`
	MaterialResolved bool     `json:"material_resolved"`

	// GridContributionAmps / GeneratorContributionAmps split FaultCurrentAmps
	// into its grid-infeed and embedded-generation parts. Both are populated only
	// when one or more GENERATORs actually contribute to the fault at this node
	// (by superposition, genfault.go); both are omitted on a network with no
	// embedded generation, where FaultCurrentAmps is entirely grid infeed and
	// reads exactly as it always has. The generator part is a conservative
	// superposition estimate. NOTE: the SLG (ground-fault) figures below do NOT
	// include generator infeed -- a documented Tier A limit.
	GridContributionAmps      *float64 `json:"grid_contribution_amps,omitempty"`
	GeneratorContributionAmps *float64 `json:"generator_contribution_amps,omitempty"`

	// Single-line-to-ground fields -- populated only on an SLG run
	// (fault_type = "slg"), nil on the default three-phase run. ZeroSeq* are the
	// accumulated zero-sequence R/X of the path from the nearest zero-sequence
	// ground source down to this node (referred to NominalBaseV, the same base
	// as the positive-sequence R/X above). Grounded is false when no
	// zero-sequence return path reaches this node (an ungrounded section behind a
	// delta / ungrounded-wye winding with no earthing transformer) -- an SLG
	// fault there draws essentially no current, so GroundFaultCurrentAmps is then
	// omitted. GroundFaultCurrentAmps is the prospective SLG current
	// I = 3·V / |Z1 + Z2 + Z0 + 3·Zf|, with Z2 taken equal to Z1.
	// GroundPathResolved is the SLG analog of MaterialResolved -- false when any
	// element on the positive- OR zero-sequence path lacked resolved impedance,
	// so the figure is an optimistic upper bound. ZeroSeqEstimated is a separate,
	// independent signal: true when any element on the zero-sequence path fell
	// back to a default (line Z0 = 3× its positive-sequence value, transformer
	// Z0 = Z1, or source Z0 = its positive-sequence impedance) rather than
	// measured zero-sequence data -- so a planner can tell an SLG figure resting
	// on real r0/x0/z0_pct from one resting on planning estimates. A fully
	// resolved path can still be estimated.
	ZeroSeqResistanceOhm   *float64 `json:"zero_seq_resistance_ohm,omitempty"`
	ZeroSeqReactanceOhm    *float64 `json:"zero_seq_reactance_ohm,omitempty"`
	Grounded               *bool    `json:"grounded,omitempty"`
	GroundFaultCurrentAmps *float64 `json:"ground_fault_current_amps,omitempty"`
	GroundPathResolved     *bool    `json:"ground_path_resolved,omitempty"`
	ZeroSeqEstimated       *bool    `json:"zero_seq_estimated,omitempty"`
}

// faultCurrentResponse is the full result of one fault-current walk.
// Barriers mirrors loadFlowResponse.Barriers exactly -- an open device
// stops the walk, because a bolted fault downstream of an open point draws
// no current from this source, same as a de-energized branch carries no
// load.
type faultCurrentResponse struct {
	Start           featureRef        `json:"start"`
	NominalVoltageV float64           `json:"nominal_voltage_v"`
	FaultType       string            `json:"fault_type"`
	Nodes           []faultNodeResult `json:"nodes"`
	Barriers        []featureRef      `json:"barriers"`
}

// phaseResult is one phase (A, B, or C) of an unbalanced-load-flow node. It is
// the per-phase analog of nodeResult's scalar voltage/current fields: only the
// phases actually energized at the node appear (a single-phase lateral reports
// one). CurrentAmps/AmpacityAmps/LoadingPct are populated only on a conductor
// (network_role = 'edge') node, exactly as in the balanced result.
type phaseResult struct {
	Phase          string   `json:"phase"`
	VoltageV       float64  `json:"voltage_v"`
	VoltageDropPct float64  `json:"voltage_drop_pct"`
	CumulativeKW   float64  `json:"cumulative_kw"`
	CumulativeKVAR float64  `json:"cumulative_kvar"`
	CurrentAmps    *float64 `json:"current_amps,omitempty"`
	AmpacityAmps   *float64 `json:"ampacity_amps,omitempty"`
	LoadingPct     *float64 `json:"loading_pct,omitempty"`
}

// unbalancedNodeResult is one node's three-phase state from the decoupled
// per-phase sweep (unbalanced.go). Phases holds only the energized phases.
// NeutralCurrentAmps and VoltageUnbalancePct are populated only when all three
// phases are present at the node -- a neutral return or an unbalance figure over
// a single- or two-phase node is not meaningful. NominalBaseV is the voltage base
// the phase voltages are on (rebased below a transformer), same convention as
// nodeResult.
type unbalancedNodeResult struct {
	FeatureClass        string        `json:"feature_class"`
	FeatureID           string        `json:"feature_id"`
	CircuitID           string        `json:"circuit_id,omitempty"`
	CircuitRef          string        `json:"circuit_ref,omitempty"`
	NominalBaseV        float64       `json:"nominal_base_v"`
	Phases              []phaseResult `json:"phases"`
	NeutralCurrentAmps  *float64      `json:"neutral_current_amps,omitempty"`
	VoltageUnbalancePct *float64      `json:"voltage_unbalance_pct,omitempty"`
	MaterialResolved    bool          `json:"material_resolved"`

	// MutualModeled is true only on a Tier 2 (coupled) run, and only for a
	// conductor node whose spec carried enough line geometry to build a Carson
	// 3x3 -- i.e. this branch's drop actually included the phase-to-phase mutual
	// terms. False on every decoupled run, every junction, and any conductor
	// that fell back to its decoupled diagonal impedance for want of geometry;
	// a client can tell which segments genuinely coupled.
	MutualModeled bool `json:"mutual_modeled"`
}

// unbalancedResponse is the full result of one unbalanced (three-phase) sweep.
// Unbalanced is a convenience subset of Nodes whose voltage unbalance exceeds the
// planning limit (unbalanceLimitPct) -- the per-phase analog of loadFlowResponse's
// Overloaded, so a client need not re-scan.
type unbalancedResponse struct {
	Start           featureRef `json:"start"`
	NominalVoltageV float64    `json:"nominal_voltage_v"`
	Iterations      int        `json:"iterations"`
	// CouplingModel is which solver ran: "decoupled" (Tier 1, three independent
	// per-phase ladders) or "carson" (Tier 2, complex sweep over each conductor's
	// Carson 3x3). It lets a client label the result without inferring it from
	// the request.
	CouplingModel string                 `json:"coupling_model"`
	Nodes         []unbalancedNodeResult `json:"nodes"`
	Barriers      []featureRef           `json:"barriers"`
	Unbalanced    []featureRef           `json:"unbalanced"`
}

// apiError is the JSON body returned for any 4xx/5xx response.
type apiError struct {
	Error string `json:"error"`
}
