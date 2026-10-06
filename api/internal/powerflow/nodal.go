package powerflow

import (
	"context"
	"fmt"
	"math"
	"math/cmplx"
	"sort"

	"github.com/uptrace/bun"
)

// Phase 52 Tier 2 -- nodal (Y-bus) balanced power flow. This is a DISTINCT
// solver from the radial forward-backward sweep (sweep.go / mesh.go): where that
// engine walks a tree and rejects (or, in Tier 1, compensates) any loop, this
// one assembles a nodal admittance matrix over the GENERAL graph and solves it
// directly -- so a dense/meshed, multi-path LV network is solved, not refused.
//
// Slices built here (S1 + S2 of docs/phase-52-nodal-powerflow.md):
//   S1  the complex layer is Go's native complex128 (no third-party math), plus
//       bus contraction and per-unit Y-bus assembly (buildBuses / assembleYBus).
//   S2  a Gauss-Seidel iterative solve, SINGLE slack, PQ buses only (loads, and
//       generation as negative-demand PQ -- the same Tier A netting resolveNodeState
//       already does), reconciled back into the per-node result shape so it reads
//       like /load-flow. Multi-slack / PV buses (grid-forming generation) and the
//       Newton-Raphson core are later slices (S3 / S5) -- see the phase doc.
//
// Modelling, deliberately and honestly a first cut:
//   * Per-unit on a fixed system base (baseVA), with a per-bus voltage base
//     propagated from the slack across transformer turns ratios. Choosing the
//     voltage bases in the ratio of each transformer's nominal turns ratio makes
//     an at-nominal transformer a 1:1 branch in per-unit -- so a conductor and a
//     transformer stamp into Y-bus identically (just a series admittance between
//     two buses); the only transformer-specific work is the base change and using
//     its secondary base for its per-unit impedance. Off-nominal taps therefore
//     are NOT yet modelled (deferred with the Newton core); every seed this slice
//     targets is at-nominal.
//   * A "bus" is a maximal set of features shorted together through zero-impedance
//     elements (junctions, meters, busbars, containers, and conductors/transformers
//     whose impedance did not resolve). Only a conductor with resolved R+jX, or a
//     transformer with a resolved ratio, is a BRANCH; everything else contracts
//     into the buses at its ends. Two conductors in parallel between the same pair
//     of buses (a ring's two legs) are two branches and split by inverse impedance
//     -- exactly the meshed case the radial engine cannot do.
//   * Balanced positive-sequence only. The current convention matches the sweep's
//     (I = |ΔV|/|Z|, i.e. S = V·I not √3·V·I) so loading %/ampacity read on the
//     same basis as /load-flow.
//
// Read-only, like every other analysis in this package.

// baseVA is the system apparent-power base for the per-unit conversion (1 MVA).
// Any positive value works -- it cancels out of the reported volts/amps -- 1 MVA
// keeps the per-unit numbers near order 1 for a distribution feeder.
const baseVA = 1_000_000.0

// Gauss-Seidel controls. gsMaxIter is a generous cap (GS converges slowly but
// monotonically on a well-conditioned distribution network); gsTolerancePU is the
// per-unit voltage change below which the solve is declared converged.
const (
	gsMaxIter     = 5000
	gsTolerancePU = 1e-9
)

// Newton-Raphson controls (newton.go). Newton converges quadratically -- a
// well-conditioned distribution network reaches machine-tolerance power balance in
// a handful of iterations, so the cap is small. newtonTolerancePU is the largest
// per-unit power mismatch (real or reactive injection residual) below which the
// solve is declared converged -- a different, and stricter, criterion than the GS
// voltage-change tolerance (a power mismatch is the physical residual Newton drives
// to zero).
const (
	newtonMaxIter     = 40
	newtonTolerancePU = 1e-9
)

// idealXfmrXPU is the tiny per-unit series reactance stamped for a transformer
// that resolved a turns ratio but no nameplate %Z (an un-surveyed unit). With the
// voltage bases chosen in the turns ratio an ideal transformer is 1:1 in per-unit,
// so its two windings sit at ~equal per-unit voltage; a real Y-bus can't stamp an
// infinite admittance, so a negligible reactance stands in -- small enough to add
// no meaningful drop, large enough to keep the matrix non-singular and Gauss-
// Seidel conditioned. A transformer WITH a resolved %Z uses its real impedance and
// never touches this.
const idealXfmrXPU = 1e-4

// nodalLoadFlowRequest is POST /analysis/nodal-load-flow's body -- the same
// Start + NominalVoltageV shape every analysis takes (see loadFlowRequest for why
// nominal voltage is an explicit, required input rather than a guessed field). It
// is the slack-bus voltage base; every other bus's base is derived from it across
// the transformer ratios on the way down.
type nodalLoadFlowRequest struct {
	Start           featureRef `json:"start"`
	NominalVoltageV float64    `json:"nominal_voltage_v"`

	// Sources are additional grid-infeed slack buses beyond Start (S3 multi-
	// slack). Each is held at the run nominal on its own propagated base, with a
	// synthetic ideal bus behind any source impedance exactly as Start gets. Each
	// must be a feature the walk from Start actually reaches (same energised,
	// connected network). Empty => the single-Start slack of S2, numerically
	// bit-identical.
	Sources []featureRef `json:"sources,omitempty"`

	// Regulated designates or overrides grid-forming (PV) generators for this run
	// (the "Both" model). A generator is already PV if its own voltage_setpoint_pu
	// asset field is set; an entry here forces PV even without the field and/or
	// overrides the per-unit setpoint and reactive limits per-run. VoltagePU
	// defaults to the asset value, else 1.0.
	Regulated []regulatedGen `json:"regulated,omitempty"`

	// Solver selects the numerical method: "gauss-seidel" (default -- proven,
	// slow-but-monotonic) or "newton" (Newton-Raphson, newton.go -- quadratic
	// convergence, the standard method for a meshed AC power flow). Both solve the
	// same assembled Y-bus model and report the same result shape; the response
	// echoes which ran. "" defaults to gauss-seidel so an existing caller's numbers
	// are unchanged until it opts into Newton.
	Solver string `json:"solver,omitempty"`
}

// Solver identifiers for nodalLoadFlowRequest.Solver and the response's Solver
// field.
const (
	solverGaussSeidel = "gauss-seidel"
	solverNewton      = "newton"
)

// regulatedGen is one per-run PV designation/override. Feature is the generator
// node; VoltagePU is the per-unit magnitude it regulates its bus to (0 = fall
// back to the asset field, else 1.0). QMinKVAR/QMaxKVAR, when both set, bound the
// reactive output (a bound that binds reverts the bus to PQ at the clamped Q).
type regulatedGen struct {
	Feature   featureRef `json:"feature"`
	VoltagePU float64    `json:"voltage_pu,omitempty"`
	QMinKVAR  *float64   `json:"q_min_kvar,omitempty"`
	QMaxKVAR  *float64   `json:"q_max_kvar,omitempty"`
}

// nodalNodeResult is one feature's solved electrical state. Role distinguishes
// how to read it: a "bus" node (junction/meter/load/source) reports the solved
// voltage of the bus it sits on; a "conductor"/"transformer" node reports the
// branch current/through-power flowing through it and its loading. VoltageV /
// NominalBaseV are always on the node's own voltage base (the run nominal above
// every transformer, a transformer's secondary below it), same convention as
// /load-flow's nodeResult.
type nodalNodeResult struct {
	FeatureClass   string   `json:"feature_class"`
	FeatureID      string   `json:"feature_id"`
	Role           string   `json:"role"`
	VoltageV       float64  `json:"voltage_v"`
	VoltageDropPct float64  `json:"voltage_drop_pct"`
	NominalBaseV   float64  `json:"nominal_base_v"`
	CurrentAmps    *float64 `json:"current_amps,omitempty"`
	AmpacityAmps   *float64 `json:"ampacity_amps,omitempty"`
	RatedKVA       *float64 `json:"rated_kva,omitempty"`
	LoadingPct     *float64 `json:"loading_pct,omitempty"`
	ThroughKW      *float64 `json:"through_kw,omitempty"`
	ThroughKVAR    *float64 `json:"through_kvar,omitempty"`
	// SuppliedKW / ReactiveKVAR are the net power INJECTED into the network at a
	// source (slack) or grid-forming (PV) bus -- how much real/reactive power that
	// infeed is pushing at the solved operating point, so a multi-source run shows
	// how the load splits across the infeeds. Nil for an ordinary bus or a branch.
	// A PV bus's ReactiveKVAR is the reactive output the solver found to hold its
	// voltage setpoint (clamped to its limit if it has one).
	SuppliedKW       *float64 `json:"supplied_kw,omitempty"`
	ReactiveKVAR     *float64 `json:"reactive_kvar,omitempty"`
	MaterialResolved bool     `json:"material_resolved"`
}

// nodalLoadFlowResponse is the full result of one nodal solve. Converged /
// Iterations / MaxMismatch expose the solver's own honesty (a non-converged
// solve is returned flagged, not silently trusted). BusCount / BranchCount report
// the contracted electrical model actually solved.
type nodalLoadFlowResponse struct {
	Start           featureRef        `json:"start"`
	NominalVoltageV float64           `json:"nominal_voltage_v"`
	Solver          string            `json:"solver"`
	Converged       bool              `json:"converged"`
	Iterations      int               `json:"iterations"`
	MaxMismatch     float64           `json:"max_mismatch_pu"`
	BusCount        int               `json:"bus_count"`
	BranchCount     int               `json:"branch_count"`
	SlackCount      int               `json:"slack_count"`
	Nodes           []nodalNodeResult `json:"nodes"`
	Barriers        []featureRef      `json:"barriers"`
	Overloaded      []featureRef      `json:"overloaded"`
}

// PV (voltage-regulating generation) field names -- optional, asset-driven. A
// generator carrying voltage_setpoint_pu (> 0) is a PV bus: it holds its terminal
// voltage at that per-unit magnitude, injects its scheduled real power, and the
// solver finds the reactive power. reactive_limit_kvar, when present, bounds that
// reactive output symmetrically (+/-); a PV bus that would need more reactive
// power than its limit reverts to a PQ bus at the clamped value for that pass
// (standard PV->PQ switching). Absent => grid-following (Tier A negative-demand
// PQ), unchanged.
const (
	genFieldVoltageSetpointPU = "voltage_setpoint_pu"
	genFieldReactiveLimitKVAR = "reactive_limit_kvar"
)

// pvSpec is a resolved grid-forming generator's voltage-control setpoint: the
// per-unit magnitude it regulates its bus to, plus an optional symmetric reactive
// limit (kvar). Asset-driven from the fields above, with per-run request
// overrides merged on top (see runNodalLoadFlow).
type pvSpec struct {
	vSetPU   float64
	qMinKVAR float64
	qMaxKVAR float64
	qLimited bool
}

// resolvePVSpec reads a node's asset-driven PV setpoint. ok = false for any node
// that is not a voltage-regulating generator: no voltage_setpoint_pu field (or a
// non-positive one), or a node that carries the field but no generation at all (a
// load cannot hold a bus voltage -- it has no reactive source), so the field is
// ignored there.
func resolvePVSpec(attrs map[string]any) (pvSpec, bool) {
	vset, ok := readFloatAttr(attrs, genFieldVoltageSetpointPU)
	if !ok || vset <= 0 {
		return pvSpec{}, false
	}
	if gkw, _ := resolveGeneration(attrs); gkw <= 0 {
		return pvSpec{}, false
	}
	spec := pvSpec{vSetPU: vset}
	if qlim, ok := readFloatAttr(attrs, genFieldReactiveLimitKVAR); ok && qlim > 0 {
		spec.qMaxKVAR = qlim
		spec.qMinKVAR = -qlim
		spec.qLimited = true
	}
	return spec, true
}

// nodalEdge is one undirected connectivity edge with the terminal id presented at
// each end -- the sweep's edgeRow projects only from_terminal_id (it only ever
// walks from->to), but the nodal contraction needs to know which winding of a
// transformer an edge lands on regardless of the stored edge direction, so this
// projection carries to_terminal_id too.
type nodalEdge struct {
	bun.BaseModel `bun:"table:runtime.feature_connectivity,alias:feature_connectivity"`

	FromFeatureClassID string  `bun:"from_feature_class_id"`
	FromFeatureID      string  `bun:"from_feature_id"`
	FromTerminalID     *string `bun:"from_terminal_id"`
	ToFeatureClassID   string  `bun:"to_feature_class_id"`
	ToFeatureID        string  `bun:"to_feature_id"`
	ToTerminalID       *string `bun:"to_terminal_id"`
}

// graphEdge is one resolved undirected edge between two reachable nodes, carrying
// the terminal id each node presents on this edge (for transformer winding
// classification). a/b are unordered.
type graphEdge struct {
	a, b  node
	aTerm string
	bTerm string
}

// runNodalLoadFlow is POST /analysis/nodal-load-flow: walk the general graph from
// the start (barrier-aware, undirected, meshes allowed), resolve every node's
// electrical inputs, contract to buses + branches, assemble a per-unit Y-bus,
// solve it by Gauss-Seidel with the start as the single slack, and format the
// solved buses/branches back to per-node results.
func runNodalLoadFlow(ctx context.Context, db bun.IDB, req nodalLoadFlowRequest) (*nodalLoadFlowResponse, error) {
	if req.Start.FeatureClass == "" || req.Start.FeatureID == "" {
		return nil, invalid("start.feature_class and start.feature_id are required")
	}
	if req.NominalVoltageV <= 0 {
		return nil, invalid("nominal_voltage_v must be a positive number")
	}

	start, err := resolveNode(ctx, db, req.Start)
	if err != nil {
		return nil, err
	}

	barriers, err := loadFlowBarriers(ctx, db)
	if err != nil {
		return nil, err
	}
	loader := newFeatureClassLoader()

	nodes, edges, barrierHits, err := buildElectricalGraph(ctx, db, start, barriers, loader)
	if err != nil {
		return nil, err
	}

	states := make(map[node]*nodeState, len(nodes))
	for _, n := range nodes {
		st, err := resolveNodeState(ctx, db, loader, n)
		if err != nil {
			return nil, err
		}
		if st != nil {
			states[n] = st
		}
	}

	// S3: additional grid-infeed sources (slack buses) beyond the start, and
	// grid-forming (PV) generators. Both are resolved against the reached node set
	// -- a requested source or regulated generator that the walk never reached (a
	// different network, or behind an open device) is an error, not a silent drop.
	reached := make(map[node]bool, len(nodes))
	for _, n := range nodes {
		reached[n] = true
	}

	sources := make([]node, 0, len(req.Sources))
	seenSource := map[node]bool{start: true}
	for _, ref := range req.Sources {
		sn, err := resolveNode(ctx, db, ref)
		if err != nil {
			return nil, err
		}
		if seenSource[sn] {
			continue // start, or a duplicate, is already a slack
		}
		if !reached[sn] {
			return nil, invalid("source %s/%s is not reachable from start (a different network, or behind an open device)", ref.FeatureClass, ref.FeatureID)
		}
		seenSource[sn] = true
		sources = append(sources, sn)
	}

	// Asset-driven PV first (any reached generator carrying voltage_setpoint_pu),
	// then request entries force/override per-run ("Both").
	regulated := map[node]pvSpec{}
	for _, n := range nodes {
		st := states[n]
		if st == nil {
			continue
		}
		if spec, ok := resolvePVSpec(st.attrs); ok {
			regulated[n] = spec
		}
	}
	for _, r := range req.Regulated {
		rn, err := resolveNode(ctx, db, r.Feature)
		if err != nil {
			return nil, err
		}
		if !reached[rn] {
			return nil, invalid("regulated generator %s/%s is not reachable from start", r.Feature.FeatureClass, r.Feature.FeatureID)
		}
		spec := regulated[rn] // asset value if any, else zero
		if r.VoltagePU > 0 {
			spec.vSetPU = r.VoltagePU
		}
		if spec.vSetPU <= 0 {
			spec.vSetPU = 1.0
		}
		if r.QMinKVAR != nil && r.QMaxKVAR != nil {
			spec.qMinKVAR = *r.QMinKVAR
			spec.qMaxKVAR = *r.QMaxKVAR
			spec.qLimited = true
		}
		regulated[rn] = spec
	}

	model, err := buildBuses(ctx, db, start, sources, regulated, nodes, edges, states, req.NominalVoltageV)
	if err != nil {
		return nil, err
	}

	// Solver selection (S5). Default and any unrecognised value fall back to the
	// proven Gauss-Seidel so an existing caller's numbers are unchanged; "newton"
	// opts into the Newton-Raphson solver (newton.go). Both consume the same model
	// and return the same nodalSolution shape.
	solverName := solverGaussSeidel
	var sol nodalSolution
	switch req.Solver {
	case solverNewton:
		solverName = solverNewton
		sol = solveNewton(model)
	default:
		sol = solveGaussSeidel(model)
	}

	classCodes, err := nodeClassCodes(ctx, db, append(append([]node{}, nodes...), barrierHits...))
	if err != nil {
		return nil, err
	}

	results, overloaded := formatNodalResults(model, states, sol, classCodes)

	return &nodalLoadFlowResponse{
		Start:           req.Start,
		NominalVoltageV: req.NominalVoltageV,
		Solver:          solverName,
		Converged:       sol.converged,
		Iterations:      sol.iterations,
		MaxMismatch:     sol.maxMismatch,
		BusCount:        len(model.buses),
		BranchCount:     len(model.branches),
		SlackCount:      len(model.slacks),
		Nodes:           results,
		Barriers:        toFeatureRefs(barrierHits, classCodes),
		Overloaded:      overloaded,
	}, nil
}

// buildElectricalGraph walks the connectivity graph outward from start, UNDIRECTED
// (an electrical branch conducts both ways, so unlike the sweep's downstream-only
// walk this follows an edge whichever way it is stored) and barrier-aware (an open
// device stops the walk exactly as it does for the sweep -- a de-energized branch
// carries no flow, so it is neither traversed nor returned as a live bus; it is
// recorded as a barrier hit). Returns the reachable node set, the de-duplicated
// undirected edges among them (edges touching a barrier are dropped), and the
// barrier hits.
func buildElectricalGraph(ctx context.Context, db bun.IDB, start node, barriers map[string]barrierCheck, loader *featureClassLoader) ([]node, []graphEdge, []node, error) {
	visited := map[node]bool{start: true}
	order := []node{start}
	queue := []node{start}
	edgeSet := map[string]graphEdge{}
	var barrierHits []node

	for len(queue) > 0 && len(order) < maxSweepNodes {
		current := queue[0]
		queue = queue[1:]

		isBarrier, err := checkBarrier(ctx, db, current, barriers, loader, nil)
		if err != nil {
			return nil, nil, nil, err
		}
		if isBarrier {
			barrierHits = append(barrierHits, current)
			continue
		}

		neighbors, err := undirectedNeighbors(ctx, db, current)
		if err != nil {
			return nil, nil, nil, err
		}
		for _, ne := range neighbors {
			// Record the undirected edge once, keyed on the sorted endpoint pair
			// so the from->to row and any to->from row collapse to one branch.
			key, ge := canonicalEdge(current, ne.other, ne.selfTerm, ne.otherTerm)
			if _, ok := edgeSet[key]; !ok {
				edgeSet[key] = ge
			}
			if !visited[ne.other] {
				visited[ne.other] = true
				order = append(order, ne.other)
				queue = append(queue, ne.other)
			}
		}
	}

	// Cap tripped with work still queued: the reachable set is truncated, and a
	// nodal (Y-bus) solve over a truncated network is numerically WRONG, not just
	// partial -- missing buses and branches give a wrong admittance matrix and so
	// a wrong solution. Fail loudly, the same contract buildTree applies to the
	// ladder sweep, rather than solve an optimistic, incomplete network. Raise
	// POWERFLOW_MAX_NODES (or narrow the study) for a genuinely larger network.
	if len(queue) > 0 && len(order) >= maxSweepNodes {
		return nil, nil, nil, invalid(
			"the network reachable from the start feature exceeds the %d-node analysis cap -- the nodal load flow would be solved over a truncated, incomplete network, so it is refused; narrow the study or raise POWERFLOW_MAX_NODES",
			maxSweepNodes,
		)
	}

	// Drop any edge that lands on a barrier node (discovered as a neighbor but
	// never admitted as a bus) -- the open device breaks that branch.
	// isBarrierHit (contingency.go) takes (hits, n).
	edges := make([]graphEdge, 0, len(edgeSet))
	for _, ge := range edgeSet {
		if visited[ge.a] && visited[ge.b] && !isBarrierHit(barrierHits, ge.a) && !isBarrierHit(barrierHits, ge.b) {
			edges = append(edges, ge)
		}
	}
	// Reachable buses are the visited set minus the barrier hits.
	nodes := make([]node, 0, len(order))
	for _, n := range order {
		if !isBarrierHit(barrierHits, n) {
			nodes = append(nodes, n)
		}
	}
	return nodes, edges, barrierHits, nil
}

// neighborEdge is one undirected step: the reached node plus the terminal id this
// (self) node and the other node each present on the connecting edge.
type neighborEdge struct {
	other     node
	selfTerm  string
	otherTerm string
}

// undirectedNeighbors returns every node directly connected to n by a connectivity
// edge, in EITHER stored direction, with the terminal ids on both ends resolved so
// a transformer's winding can be identified whichever way the edge was stored.
func undirectedNeighbors(ctx context.Context, db bun.IDB, n node) ([]neighborEdge, error) {
	var rows []nodalEdge
	if err := db.NewSelect().Model(&rows).
		Where("(from_feature_id = ? AND from_feature_class_id = ?) OR (to_feature_id = ? AND to_feature_class_id = ?)",
			n.featureID, n.classID, n.featureID, n.classID).
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("powerflow: loading nodal neighbors: %w", err)
	}
	out := make([]neighborEdge, 0, len(rows))
	for _, r := range rows {
		from := node{classID: r.FromFeatureClassID, featureID: r.FromFeatureID}
		to := node{classID: r.ToFeatureClassID, featureID: r.ToFeatureID}
		fromTerm, toTerm := deref(r.FromTerminalID), deref(r.ToTerminalID)
		switch n {
		case from:
			out = append(out, neighborEdge{other: to, selfTerm: fromTerm, otherTerm: toTerm})
		case to:
			out = append(out, neighborEdge{other: from, selfTerm: toTerm, otherTerm: fromTerm})
		}
	}
	return out, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// canonicalEdge builds the de-dup key and an orientation-stable graphEdge for an
// undirected edge between self and other. The pair is sorted so (self,other) and
// (other,self) map to one entry, with each node's own terminal kept alongside it.
func canonicalEdge(self, other node, selfTerm, otherTerm string) (string, graphEdge) {
	sk := self.classID + "|" + self.featureID
	ok := other.classID + "|" + other.featureID
	if sk <= ok {
		return sk + "~" + ok, graphEdge{a: self, b: other, aTerm: selfTerm, bTerm: otherTerm}
	}
	return ok + "~" + sk, graphEdge{a: other, b: self, aTerm: otherTerm, bTerm: selfTerm}
}

// branchKind is what a series element does electrically.
type branchKind int

const (
	branchConductor branchKind = iota
	branchTransformer
)

// nodalBranch is one series element (a conductor with resolved R+jX, or a
// transformer with a resolved ratio) as an edge between two buses. For a
// conductor, busA/busB are its two ends (symmetric) and ratio is 1. For a
// transformer, busA is the primary bus and busB the secondary bus, ratio is
// secondary/primary, and zOhm is referred to the secondary base (as
// resolveTransformerModel returns it). yPU is the per-unit series admittance
// (identical viewed from either side, so it stamps like any branch).
type nodalBranch struct {
	kind      branchKind
	owner     node
	busA      int
	busB      int
	ratio     float64
	zOhm      complex128
	yPU       complex128
	resolved  bool // false for a transformer contributing no series impedance (ratio only)
	synthetic bool // true for the Thevenin source-impedance branch -- not a real feature edge, so not reported as a node result
}

// busKind is how the solver treats a bus: PQ (injection fixed, voltage solved),
// PV (magnitude + real injection fixed, reactive + angle solved -- a grid-forming
// generator), or Slack (voltage fixed, injection solved -- a grid infeed).
type busKind int

const (
	busPQ busKind = iota
	busPV
	busSlack
)

// nodalModel is the assembled electrical problem: the buses (with their voltage
// base, net injected power and bus type), the branches, the admittance matrix,
// and which buses are held fixed. slacks are the buses the solver holds at a
// fixed voltage (there may be several -- multiple grid infeeds); sourceSlackOf
// maps each source FEATURE node to the slack bus that stands in for its infeed
// (its own bus, or the synthetic ideal bus behind its source impedance), so the
// supplied power can be reported against the feature.
type nodalModel struct {
	buses         []busInfo
	branches      []nodalBranch
	yBus          [][]complex128
	slacks        map[int]bool
	sourceSlackOf map[node]int
	busOfNode     map[node]int
}

// busInfo is one solved bus: its voltage base (volts), the net complex power
// INJECTED at it in per-unit (generation positive, load negative), the member
// feature nodes contracted into it, and -- for a non-PQ bus -- its held voltage
// setpoint and (PV only) real injection and reactive limits.
type busInfo struct {
	vBase   float64
	sInjPU  complex128
	members []node

	kind     busKind
	vSetPU   float64 // slack: held |V| in pu (default 1); PV: regulated |V| in pu
	pInjPU   float64 // PV: fixed real-power injection (pu); the reactive part is solved
	qMinPU   float64 // PV reactive lower limit (pu), when qLimited
	qMaxPU   float64 // PV reactive upper limit (pu), when qLimited
	qLimited bool
}

// buildBuses contracts the graph into buses + branches and assembles the per-unit
// Y-bus. Contraction: every conductor with resolved impedance and every
// transformer with a resolved ratio (except the start, which is always the slack
// bus regardless of its class) is a SERIES element split into two ports;
// everything else -- and every unresolved conductor/transformer -- is a
// zero-impedance short. A union-find over the edges (shorting the non-series ends
// together, keeping each series element's two ports apart) yields the buses; each
// series element then spans the two buses its ports fell into.
func buildBuses(ctx context.Context, db bun.IDB, start node, sources []node, regulated map[node]pvSpec, nodes []node, edges []graphEdge, states map[node]*nodeState, nominalV float64) (*nodalModel, error) {
	// Every source (the start and any S3 extra infeed) is a slack: held at a fixed
	// voltage, never a series element, its own transformer ratio not applied
	// (matching the forward sweep's treatment of the start). Collect them once.
	sourceSet := map[node]bool{start: true}
	for _, s := range sources {
		sourceSet[s] = true
	}

	// Classify each node as a series element (branch) or not. A conductor is a
	// branch only if its impedance resolved; a transformer only if its ratio did.
	// A source node is never a series element (see sourceSet above).
	isSeries := map[node]bool{}
	for _, n := range nodes {
		if sourceSet[n] {
			continue
		}
		st := states[n]
		if st == nil {
			continue
		}
		// A conductor is a branch when its impedance resolved; a transformer when
		// its turns ratio resolved (xfmr.ok) -- even with no nameplate %Z, since it
		// still rebases voltage and must span two buses (an ideal per-unit tie).
		// Conductor and transformer are mutually exclusive here: only a network_role
		// == "edge" node resolves impedance, and only a node carrying primary+
		// secondary voltage resolves xfmr.ok.
		if st.impedance.resolved || st.xfmr.ok {
			isSeries[n] = true
		}
	}

	// Point ids for the union-find: one point per non-series node, two ports per
	// series node. portOf[n] = [primary/end0, secondary/end1].
	dsu := newDSU()
	pointOf := map[node]int{}   // non-series node -> its single point
	portOf := map[node][2]int{} // series node -> its two ports
	for _, n := range nodes {
		if isSeries[n] {
			portOf[n] = [2]int{dsu.add(), dsu.add()}
		} else {
			pointOf[n] = dsu.add()
		}
	}

	// Terminal roles for transformer winding classification (which port an edge
	// lands on). Resolve every terminal id seen on an edge touching a transformer.
	termRole, err := resolveEdgeTerminalRoles(ctx, db, nodes, edges, isSeries, states)
	if err != nil {
		return nil, err
	}

	// For conductor port assignment we consume the two ends in a stable order: an
	// edge landing on a conductor takes end0 the first time, end1 thereafter, so
	// its two distinct neighbours fall on opposite ends (the impedance separates
	// them). Sort edges for determinism.
	sort.Slice(edges, func(i, j int) bool {
		return edgeKey(edges[i]) < edgeKey(edges[j])
	})
	condEndUsed := map[node]int{}

	endpointPoint := func(n node, term string) int {
		if !isSeries[n] {
			return pointOf[n]
		}
		ports := portOf[n]
		st := states[n]
		if st.xfmr.ok && !st.impedance.resolved {
			// Transformer: secondary/tertiary terminal -> secondary port (index 1),
			// anything else (primary / unknown / the upstream feed) -> primary (0).
			role := termRole[term]
			if role == windingSecondary || role == windingTertiary {
				return ports[1]
			}
			return ports[0]
		}
		// Conductor: first incidence -> end0, subsequent -> end1.
		if condEndUsed[n] == 0 {
			condEndUsed[n] = 1
			return ports[0]
		}
		return ports[1]
	}

	for _, e := range edges {
		pa := endpointPoint(e.a, e.aTerm)
		pb := endpointPoint(e.b, e.bTerm)
		dsu.union(pa, pb)
	}

	// Collapse union-find roots into contiguous bus indices.
	busIndex := map[int]int{}
	var buses []busInfo
	busOf := func(point int) int {
		root := dsu.find(point)
		if idx, ok := busIndex[root]; ok {
			return idx
		}
		idx := len(buses)
		busIndex[root] = idx
		buses = append(buses, busInfo{})
		return idx
	}

	busOfNode := map[node]int{}
	for _, n := range nodes {
		if isSeries[n] {
			continue // a series element sits on no single bus; assigned via its ports below
		}
		bi := busOf(pointOf[n])
		busOfNode[n] = bi
		buses[bi].members = append(buses[bi].members, n)
	}

	// Net injected power per bus (per-unit): generation positive, load negative.
	// resolveNodeState already netted generation into ownKW/ownKVAR as demand, so
	// the injection is the negative of that.
	for _, n := range nodes {
		if isSeries[n] {
			continue
		}
		st := states[n]
		if st == nil {
			continue
		}
		bi := busOfNode[n]
		buses[bi].sInjPU += complex(-st.ownKW*1000/baseVA, -st.ownKVAR*1000/baseVA)
	}

	// Build the branches from the series elements' two ports.
	var branches []nodalBranch
	for _, n := range nodes {
		if !isSeries[n] {
			continue
		}
		st := states[n]
		ports := portOf[n]
		a := busOf(ports[0])
		b := busOf(ports[1])
		if a == b {
			continue // both ends fell on the same bus -> no branch (self-loop)
		}
		br := nodalBranch{owner: n, busA: a, busB: b}
		if st.impedance.resolved {
			br.kind = branchConductor
			br.ratio = 1
			br.zOhm = complex(st.impedance.rOhm, st.impedance.xOhm)
			br.resolved = true
		} else {
			br.kind = branchTransformer
			br.ratio = st.xfmr.ratio
			br.zOhm = complex(st.xfmr.rOhm, st.xfmr.xOhm)
			br.resolved = st.xfmr.rOhm != 0 || st.xfmr.xOhm != 0
		}
		branches = append(branches, br)
	}

	// Slack buses: one per source (the start plus any S3 extra infeed). A source
	// with a Thevenin source impedance gets a synthetic ideal bus (held at nominal)
	// behind that impedance, so its own bus sags under load exactly as the sweep's
	// source-impedance drop makes it; the slack is then the ideal bus. With no
	// source impedance the source's own bus is the slack directly. sourceSlackOf
	// maps each source FEATURE node to the slack bus index that stands in for it, so
	// its supplied power can be reported against the feature.
	slacks := map[int]bool{}
	sourceSlackOf := map[node]int{}
	orderedSources := append([]node{start}, sources...)
	slackList := make([]int, 0, len(orderedSources))
	for _, src := range orderedSources {
		bi, ok := busOfNode[src]
		if !ok {
			if src == start {
				return nil, invalid("start feature resolved to no electrical bus")
			}
			return nil, invalid("source feature resolved to no electrical bus")
		}
		slackBi := bi
		if st := states[src]; st != nil {
			if sr, sx := resolveSourceImpedance(st.attrs); sr > 0 || sx > 0 {
				ideal := len(buses)
				buses = append(buses, busInfo{})
				branches = append(branches, nodalBranch{
					kind: branchConductor, owner: src, busA: ideal, busB: bi,
					ratio: 1, zOhm: complex(sr, sx), resolved: true, synthetic: true,
				})
				slackBi = ideal
			}
		}
		buses[slackBi].kind = busSlack
		buses[slackBi].vSetPU = 1.0
		slacks[slackBi] = true
		sourceSlackOf[src] = slackBi
		slackList = append(slackList, slackBi)
	}

	// PV (grid-forming) buses: hold a member generator's terminal at its per-unit
	// setpoint, fix that bus's real injection, solve its reactive. A bus already a
	// slack wins (a source outranks a PV designation on the same bus). The fixed
	// real injection is the bus's net (generation minus any co-located load).
	for n, spec := range regulated {
		bi, ok := busOfNode[n]
		if !ok || slacks[bi] {
			continue
		}
		buses[bi].kind = busPV
		buses[bi].vSetPU = spec.vSetPU
		buses[bi].pInjPU = real(buses[bi].sInjPU)
		if spec.qLimited {
			buses[bi].qMinPU = spec.qMinKVAR * 1000 / baseVA
			buses[bi].qMaxPU = spec.qMaxKVAR * 1000 / baseVA
			buses[bi].qLimited = true
		}
	}

	// Voltage bases: BFS from every slack across the branches. A conductor keeps the
	// base; a transformer multiplies by its ratio going primary(busA)->secondary
	// (busB) and divides going the other way.
	propagateVoltageBases(buses, branches, slackList, nominalV)

	// Per-unit admittance for each branch, referred to the voltage base on its
	// impedance's own side (secondary for a transformer, the shared base for a
	// conductor), then assemble Y-bus.
	yBus := make([][]complex128, len(buses))
	for i := range yBus {
		yBus[i] = make([]complex128, len(buses))
	}
	for bi := range branches {
		br := &branches[bi]
		vbaseRef := buses[br.busB].vBase // impedance referred to the B (secondary) side
		if vbaseRef <= 0 {
			vbaseRef = nominalV
		}
		switch {
		case br.zOhm != 0:
			zBase := vbaseRef * vbaseRef / baseVA
			br.yPU = complex(zBase, 0) / br.zOhm
		case br.kind == branchTransformer:
			// Ideal (un-surveyed %Z) transformer: stamp a negligible per-unit
			// reactance so the two windings tie at ~equal per-unit voltage (1:1 in
			// per-unit) without a singular matrix. See idealXfmrXPU.
			br.yPU = complex(1, 0) / complex(0, idealXfmrXPU)
		default:
			continue // a zero-impedance conductor (never happens once resolved) contributes nothing
		}
		yBus[br.busA][br.busA] += br.yPU
		yBus[br.busB][br.busB] += br.yPU
		yBus[br.busA][br.busB] -= br.yPU
		yBus[br.busB][br.busA] -= br.yPU
	}

	return &nodalModel{
		buses: buses, branches: branches, yBus: yBus,
		slacks: slacks, sourceSlackOf: sourceSlackOf, busOfNode: busOfNode,
	}, nil
}

// propagateVoltageBases sets each bus's voltage base by BFS from the slacks: a
// conductor branch carries the base unchanged, a transformer branch rebases by its
// turns ratio (secondary = primary × ratio). Every slack seeds at the run nominal
// (parallel infeeds share a level); a bus never reached (an island no slack feeds)
// keeps base 0 and is reported as such. Where two slacks would disagree on a bus's
// base (e.g. sources straddling a transformer), the first slack to reach it wins.
func propagateVoltageBases(buses []busInfo, branches []nodalBranch, slacks []int, nominalV float64) {
	adj := make([][]int, len(buses)) // bus -> branch indices incident to it
	for i := range branches {
		adj[branches[i].busA] = append(adj[branches[i].busA], i)
		adj[branches[i].busB] = append(adj[branches[i].busB], i)
	}
	seen := map[int]bool{}
	var queue []int
	for _, slack := range slacks {
		if seen[slack] {
			continue
		}
		buses[slack].vBase = nominalV
		seen[slack] = true
		queue = append(queue, slack)
	}
	for len(queue) > 0 {
		b := queue[0]
		queue = queue[1:]
		for _, bi := range adj[b] {
			br := branches[bi]
			var next int
			var base float64
			switch {
			case br.busA == b:
				next = br.busB
				base = buses[b].vBase * br.ratio // A(primary)->B(secondary)
			default:
				next = br.busA
				base = buses[b].vBase / br.ratio // B(secondary)->A(primary)
			}
			if br.ratio == 0 {
				base = buses[b].vBase
			}
			if !seen[next] {
				seen[next] = true
				buses[next].vBase = base
				queue = append(queue, next)
			}
		}
	}
}

// resolveEdgeTerminalRoles resolves the winding role of each terminal id that
// appears on an edge touching a transformer series node, so buildBuses can route
// each edge to the transformer's primary vs secondary port. Non-transformer edges
// need no role.
func resolveEdgeTerminalRoles(ctx context.Context, db bun.IDB, nodes []node, edges []graphEdge, isSeries map[node]bool, states map[node]*nodeState) (map[string]string, error) {
	isXfmr := func(n node) bool {
		st := states[n]
		return isSeries[n] && st != nil && st.xfmr.ok && !st.impedance.resolved
	}
	idSet := map[string]struct{}{}
	for _, e := range edges {
		if isXfmr(e.a) && e.aTerm != "" {
			idSet[e.aTerm] = struct{}{}
		}
		if isXfmr(e.b) && e.bTerm != "" {
			idSet[e.bTerm] = struct{}{}
		}
	}
	roles := make(map[string]string, len(idSet))
	if len(idSet) == 0 {
		return roles, nil
	}
	ids := make([]string, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}
	var rows []terminalRoleRow
	if err := db.NewSelect().Model(&rows).Where("id IN (?)", bun.In(ids)).Scan(ctx); err != nil {
		return nil, fmt.Errorf("powerflow: resolving nodal terminal roles: %w", err)
	}
	for _, r := range rows {
		roles[r.ID] = r.Role
	}
	return roles, nil
}

func edgeKey(e graphEdge) string {
	return e.a.classID + "|" + e.a.featureID + "~" + e.b.classID + "|" + e.b.featureID
}

// nodalSolution is the outcome of an iterative solve -- the per-unit bus voltages
// and the convergence telemetry -- shared by both solvers (Gauss-Seidel below and
// Newton-Raphson in newton.go), so formatNodalResults / busInjectionPower read
// whichever ran without caring which. maxMismatch is the solver's own residual:
// the largest per-unit voltage change on the final GS pass, or the largest
// per-unit power mismatch on the final Newton iteration (both -> 0 as the solve
// converges).
type nodalSolution struct {
	vPU         []complex128
	converged   bool
	iterations  int
	maxMismatch float64
}

// solveGaussSeidel runs the Gauss-Seidel iteration on the per-unit Y-bus with each
// slack held at its setpoint∠0 (skipped in the sweep). Every other bus is a PQ bus
// (voltage updated from its injected power and neighbours) or a PV bus (magnitude
// and real injection held, reactive + angle solved -- see below). The pass repeats
// until the largest voltage change falls below gsTolerancePU (converged) or
// gsMaxIter passes elapse (returned flagged not converged). An isolated bus (no
// admittance to it, a zero-base island) is left at its start -- it carries no
// branch so its exact voltage is meaningless, and dividing by its zero diagonal is
// avoided.
func solveGaussSeidel(model *nodalModel) nodalSolution {
	n := len(model.buses)
	v := make([]complex128, n)
	for i := range v {
		v[i] = complex(1, 0) // flat start (per-unit)
	}
	for i := range model.buses {
		if model.slacks[i] {
			vset := model.buses[i].vSetPU
			if vset <= 0 {
				vset = 1
			}
			v[i] = complex(vset, 0) // slack held fixed at its setpoint angle 0
		}
	}
	y := model.yBus

	var iter int
	var maxDelta float64
	converged := false
	for iter = 0; iter < gsMaxIter; iter++ {
		maxDelta = 0
		for i := 0; i < n; i++ {
			if model.slacks[i] {
				continue // slack voltage is fixed
			}
			yii := y[i][i]
			if yii == 0 {
				continue // isolated bus -- nothing to solve
			}
			// sum of Y_ij * V_j for j != i (Gauss-Seidel uses already-updated V_j)
			var sum complex128
			for j := 0; j < n; j++ {
				if j != i {
					sum += y[i][j] * v[j]
				}
			}
			bus := &model.buses[i]
			var newV complex128
			if bus.kind == busPV {
				// PV bus: fix real injection P and hold |V| at the setpoint, solving
				// the reactive Q. Q = Im(V_i · conj(Σ_j Y_ij V_j)) at the current
				// estimate; if a reactive limit binds, clamp Q and let |V| float for
				// this pass (standard PV->PQ switching). Then update V with S =
				// P + jQ and reset the magnitude to the setpoint holding the angle.
				inj := sum + yii*v[i]
				qInj := imag(v[i] * cmplx.Conj(inj))
				holdMag := true
				if bus.qLimited {
					if qInj < bus.qMinPU {
						qInj = bus.qMinPU
						holdMag = false
					} else if qInj > bus.qMaxPU {
						qInj = bus.qMaxPU
						holdMag = false
					}
				}
				s := complex(bus.pInjPU, qInj)
				newV = (cmplx.Conj(s/v[i]) - sum) / yii
				if holdMag {
					if mag := cmplx.Abs(newV); mag > 0 {
						newV = complex(bus.vSetPU/mag, 0) * newV
					}
				}
			} else {
				// PQ bus: I_i = conj(S_i / V_i); V_i = (I_i - sum) / Y_ii
				iInj := cmplx.Conj(bus.sInjPU / v[i])
				newV = (iInj - sum) / yii
			}
			if d := cmplx.Abs(newV - v[i]); d > maxDelta {
				maxDelta = d
			}
			v[i] = newV
		}
		if maxDelta < gsTolerancePU {
			converged = true
			iter++ // count the converging pass
			break
		}
	}

	return nodalSolution{vPU: v, converged: converged, iterations: iter, maxMismatch: maxDelta}
}

// formatNodalResults maps the solved buses/branches back to one result per feature
// node: a bus member reports its bus voltage; a branch (conductor/transformer)
// reports its through-current/power and loading. Overloaded collects any branch
// over 100% of its ampacity (conductor) or rated kVA (transformer).
func formatNodalResults(model *nodalModel, states map[node]*nodeState, sol nodalSolution, classCodes map[string]string) ([]nodalNodeResult, []featureRef) {
	results := make([]nodalNodeResult, 0, len(model.busOfNode)+len(model.branches))
	overloaded := make([]featureRef, 0)

	// Bus members (junctions, meters, loads, sources, grid-forming generators).
	// busOfNode is the authoritative node->bus map (buses[bi].members carries the
	// same set). A source feature is labelled "slack" and a grid-forming generator
	// "pv"; both report the net power they inject into the network (a source's is
	// measured at its slack bus -- the synthetic ideal bus behind any source
	// impedance -- so a multi-source run shows how the load splits across infeeds).
	for n, bi := range model.busOfNode {
		bus := model.buses[bi]
		vMag := cmplx.Abs(sol.vPU[bi]) * bus.vBase
		var dropPct float64
		if bus.vBase > 0 {
			dropPct = (bus.vBase - vMag) / bus.vBase * 100
		}
		st := states[n]
		role := "bus"
		var suppliedKW, reactiveKVAR *float64
		if slackBi, ok := model.sourceSlackOf[n]; ok {
			role = "slack"
			kw, kvar := busInjectionPower(model, sol, slackBi)
			suppliedKW, reactiveKVAR = &kw, &kvar
		} else if bus.kind == busPV {
			role = "pv"
			kw, kvar := busInjectionPower(model, sol, bi)
			suppliedKW, reactiveKVAR = &kw, &kvar
		}
		res := nodalNodeResult{
			FeatureClass:     classCodes[n.classID],
			FeatureID:        n.featureID,
			Role:             role,
			VoltageV:         vMag,
			VoltageDropPct:   dropPct,
			NominalBaseV:     bus.vBase,
			SuppliedKW:       suppliedKW,
			ReactiveKVAR:     reactiveKVAR,
			MaterialResolved: true,
		}
		// A rated junction (a DIST_XFMR contracted into a bus because its series
		// impedance was zero, or any rated non-series junction) still reports its
		// loading against the net power it carries -- but in the nodal model the
		// through-power belongs to its branch, so a bus member has no loading here.
		if st != nil && st.hasRating {
			rated := st.ratedKVA
			res.RatedKVA = &rated
		}
		results = append(results, res)
	}

	// Branches (conductors, transformers). The synthetic source-impedance branch
	// is internal (its owner is the start feature, already reported as the slack
	// bus) -- skip it so the start isn't listed twice.
	for _, br := range model.branches {
		if br.synthetic || br.owner == (node{}) {
			continue
		}
		st := states[br.owner]
		va := sol.vPU[br.busA]
		vb := sol.vPU[br.busB]
		vbaseRef := model.buses[br.busB].vBase
		if vbaseRef <= 0 {
			vbaseRef = model.buses[br.busA].vBase
		}
		res := nodalNodeResult{
			FeatureClass:     classCodes[br.owner.classID],
			FeatureID:        br.owner.featureID,
			NominalBaseV:     vbaseRef,
			VoltageV:         cmplx.Abs(vb) * model.buses[br.busB].vBase,
			MaterialResolved: br.resolved,
		}
		if br.kind == branchTransformer {
			res.Role = "transformer"
		} else {
			res.Role = "conductor"
		}
		if br.yPU != 0 && vbaseRef > 0 {
			iPU := (va - vb) * br.yPU
			iBase := baseVA / vbaseRef
			currentAmps := cmplx.Abs(iPU) * iBase
			// through complex power at the B end, S = V_b * conj(I), per-unit -> kW/kVAR
			sPU := vb * cmplx.Conj(iPU)
			throughKW := real(sPU) * baseVA / 1000
			throughKVAR := imag(sPU) * baseVA / 1000
			res.CurrentAmps = &currentAmps
			res.ThroughKW = &throughKW
			res.ThroughKVAR = &throughKVAR

			if br.kind == branchConductor && st != nil && st.impedance.ampacityAmps != nil {
				ampacity := *st.impedance.ampacityAmps
				res.AmpacityAmps = &ampacity
				loadingPct := currentAmps / ampacity * 100
				res.LoadingPct = &loadingPct
				if loadingPct > 100 {
					overloaded = append(overloaded, featureRef{FeatureClass: res.FeatureClass, FeatureID: res.FeatureID})
				}
			} else if br.kind == branchTransformer && st != nil && st.hasRating {
				rated := st.ratedKVA
				res.RatedKVA = &rated
				sKVA := math.Hypot(throughKW, throughKVAR)
				loadingPct := sKVA / rated * 100
				res.LoadingPct = &loadingPct
				if loadingPct > 100 {
					overloaded = append(overloaded, featureRef{FeatureClass: res.FeatureClass, FeatureID: res.FeatureID})
				}
			}
		}
		results = append(results, res)
	}

	return results, overloaded
}

// busInjectionPower is the net complex power injected INTO the network at one bus
// at the solved operating point: S = V_i · conj(Σ_j Y_ij V_j), converted to
// kW/kVAR. For a slack it is the power that infeed supplies; for a PV bus, its
// generation (real) and the reactive the solver found to hold its setpoint.
func busInjectionPower(model *nodalModel, sol nodalSolution, bi int) (kw, kvar float64) {
	var inj complex128
	for j := range model.yBus[bi] {
		inj += model.yBus[bi][j] * sol.vPU[j]
	}
	s := sol.vPU[bi] * cmplx.Conj(inj)
	return real(s) * baseVA / 1000, imag(s) * baseVA / 1000
}

// --- disjoint-set (union-find) over integer points ---------------------------

type dsu struct {
	parent []int
	rank   []int
}

func newDSU() *dsu { return &dsu{} }

func (d *dsu) add() int {
	id := len(d.parent)
	d.parent = append(d.parent, id)
	d.rank = append(d.rank, 0)
	return id
}

func (d *dsu) find(x int) int {
	for d.parent[x] != x {
		d.parent[x] = d.parent[d.parent[x]] // path halving
		x = d.parent[x]
	}
	return x
}

func (d *dsu) union(a, b int) {
	ra, rb := d.find(a), d.find(b)
	if ra == rb {
		return
	}
	if d.rank[ra] < d.rank[rb] {
		ra, rb = rb, ra
	}
	d.parent[rb] = ra
	if d.rank[ra] == d.rank[rb] {
		d.rank[ra]++
	}
}
