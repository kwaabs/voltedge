package powerflow

import (
	"context"
	"math"

	"github.com/uptrace/bun"
)

// This file is Phase 51 Tier 1: a weakly-meshed distribution load flow built on
// top of the radial forward-backward sweep, closing a small number of loops by
// the compensation method rather than solving a full nodal (Y-bus / Newton)
// power flow. It is deliberately scoped to weak meshing -- a handful of
// normally-closed link boxes or ring mains, not a dense urban secondary grid --
// exactly the tier docs/phase-51-meshed-lv-powerflow.md draws the line at.
//
// The method, at this engine's own linear planning fidelity:
//
//  1. buildTree records each loop-closing edge as a meshBreak and keeps the
//     spanning tree radial (mesh.go never sees a general graph -- it sees a tree
//     plus a list of broken loops). runSweep then solves that radial base case.
//  2. Each broken loop is two tree paths from a lowest common ancestor (LCA) to
//     the two endpoints of the loop-closing edge; closing the (ideal
//     connectivity) edge means the two endpoints must be at the same voltage.
//  3. The base sweep leaves a voltage mismatch M = V(existing) - V(closer)
//     across each still-open loop. A circulating power flow is injected around
//     the loop -- added to the branches toward `closer`, subtracted from the
//     branches toward `existing` -- to drive M to zero.
//
// Linearizations (documented honestly, since this is a planning figure, not a
// full AC solve):
//
//   - Constant-denominator superposition. The drop model is ΔV ≈ (P·R + Q·X)/V;
//     holding V at the loop's own base makes the drop linear in (P, Q), so the
//     circulating flow's effect on each endpoint voltage superposes cleanly and
//     one linear solve per loop closes it (the outer iterations then refine the
//     V denominators and reconcile loops that share branches).
//   - The circulating power is taken at the loop load's own power factor:
//     cQ = k·cP with k = Q/P of the demand crossing the break (the load being
//     redistributed between the two paths). The magnitude-drop continuity the
//     endpoints must satisfy is one scalar equation, cP·R_loop + cQ·X_loop =
//     -V·M -- one equation, two unknowns -- so a second assumption is needed to
//     pin (cP, cQ). Fixing the transferred power at the load's own power factor
//     is the physical one: it reduces to purely real circulation for a real
//     load, and (crucially) reproduces the exact 50/50 split two identical
//     parallel conductors must carry, which the alternative "circulate at the
//     loop-impedance angle" does NOT (it injects spurious reactive circulation
//     and mis-splits even the symmetric case). It is the honest stand-in for the
//     phasor-angle continuity equation this magnitude-only engine cannot form.
//   - Weak coupling. Loops that share branches are reconciled by Gauss-Seidel
//     over the outer iterations, not a full breakpoint-impedance matrix; this
//     converges for weak meshing and is why the endpoint is scoped to it.
//
// A loop whose meeting endpoint is an open device (a normally-open tie the
// radial barrier already halts at) is left open, not closed: its residual
// mismatch is reported and it is not compensated, so meshed-load-flow never
// silently energizes a normally-open tie.

// meshCompIterations is the outer compensation pass cap -- each pass re-solves
// every loop's circulating flow against the latest voltages. A single loop
// closes in one pass under the linearization; the cap only matters for several
// branch-sharing loops reconciling, and weak meshing converges well inside it.
const meshCompIterations = 25

// meshConvergedV is the per-loop mismatch (volts) at or below which a loop is
// treated as closed -- a small fraction of a volt on any real LV/MV base.
const meshConvergedV = 1e-4

// meshedLoadFlowRequest is POST /analysis/meshed-load-flow's body -- the same
// start + nominal as load-flow, plus an optional iteration override for
// pathologically stiff loop sets. It reports the same per-node result as
// load-flow (voltage + loading) with the loops closed, so a caller reads a
// meshed LV feeder exactly as it reads a radial one.
type meshedLoadFlowRequest struct {
	Start           featureRef `json:"start"`
	NominalVoltageV float64    `json:"nominal_voltage_v"`
	MaxIterations   *int       `json:"max_iterations"`
}

// loopResult is one closed (or, for an open-device loop, left-open) loop's
// report: the loop-closing edge's two endpoints, the loop's own series
// impedance, the residual voltage mismatch after compensation (≈ 0 for a
// converged loop), whether every branch on the loop resolved its impedance, and
// whether the loop was actually compensated (false when it meets at an open
// device or the loop impedance is degenerate).
type loopResult struct {
	Closer            featureRef `json:"closer"`
	Existing          featureRef `json:"existing"`
	LoopResistanceOhm float64    `json:"loop_resistance_ohm"`
	LoopReactanceOhm  float64    `json:"loop_reactance_ohm"`
	ResidualMismatchV float64    `json:"residual_mismatch_v"`
	ImpedanceResolved bool       `json:"impedance_resolved"`
	Compensated       bool       `json:"compensated"`
	Note              string     `json:"note,omitempty"`
}

// meshedLoadFlowResponse is the full result: the same node/barrier/overload
// shape as load-flow, plus the closed loops and how many compensation passes ran
// (0 when the network turned out radial -- the endpoint degrades to an ordinary
// load flow with no loops).
type meshedLoadFlowResponse struct {
	Start           featureRef   `json:"start"`
	NominalVoltageV float64      `json:"nominal_voltage_v"`
	Iterations      int          `json:"iterations"`
	CompPasses      int          `json:"compensation_passes"`
	LoopCount       int          `json:"loop_count"`
	Nodes           []nodeResult `json:"nodes"`
	Barriers        []featureRef `json:"barriers"`
	Overloaded      []featureRef `json:"overloaded"`
	Loops           []loopResult `json:"loops"`
}

// loopGeom is one loop's precomputed geometry and working compensation state.
// segA are the branch nodes from `existing` up to (excluding) the LCA -- the
// circulating flow is subtracted from these; segB are from `closer` up to
// (excluding) the LCA -- the circulating flow is added to these. r/x/zSq are the
// loop's own series impedance (segA + segB); k is the load power factor ratio
// Q/P of the demand crossing the break (the circulating power is injected at
// this ratio, cQ = k·cP -- see the file header); refV is the base voltage the
// linear drop is measured against; cP/cQ are the accumulated circulating power.
type loopGeom struct {
	closer   node
	existing node
	segA     []node
	segB     []node
	r        float64
	x        float64
	zSq      float64
	k        float64
	resolved bool
	refV     float64
	cP       float64
	cQ       float64
}

// runMeshLoadFlow is POST /analysis/meshed-load-flow: sweep the radial spanning
// tree with loop-closing edges recorded, then close each real loop by
// compensation and format the same per-node result load-flow returns.
func runMeshLoadFlow(ctx context.Context, db bun.IDB, req meshedLoadFlowRequest) (*meshedLoadFlowResponse, error) {
	var breaks []meshBreak
	swept, err := runSweep(ctx, db, req.Start, req.NominalVoltageV, nil, &breaks)
	if err != nil {
		return nil, err
	}

	maxIter := meshCompIterations
	if req.MaxIterations != nil && *req.MaxIterations > 0 {
		maxIter = *req.MaxIterations
	}

	barrierSet := make(map[node]bool, len(swept.barrierHits))
	for _, n := range swept.barrierHits {
		barrierSet[n] = true
	}

	loops := make([]*loopGeom, 0, len(breaks))
	openLoops := make([]loopResult, 0)
	for _, br := range breaks {
		// A loop that meets at an open device is not actually closed -- leave it
		// open (do not compensate), so a normally-open tie is never energized.
		if barrierSet[br.existing] || barrierSet[br.closer] {
			openLoops = append(openLoops, loopResult{
				Closer:            featureRefOf(br.closer, swept.classCodes),
				Existing:          featureRefOf(br.existing, swept.classCodes),
				ResidualMismatchV: voltageAt(swept.states, br.existing) - voltageAt(swept.states, br.closer),
				Compensated:       false,
				Note:              "loop meets at an open device -- left open, not compensated",
			})
			continue
		}

		segA, segB := loopBranches(swept.tr, br.existing, br.closer)
		r, x, resolved := loopImpedance(swept.states, segA, segB)
		refV := loopRefVoltage(swept.states, br.existing, br.closer, swept.nominalV)
		// k is the load power factor ratio Q/P of the demand crossing the break --
		// the circulating power is injected at this ratio so a real load draws
		// purely real circulation (identical parallel conductors then split 50/50).
		// swept.states still holds the radial BASE cumulative power here (the
		// backward sweep ran inside runSweep; compensate snapshots it only later).
		k := loopLoadPF(swept.states, br.existing)
		loops = append(loops, &loopGeom{
			closer: br.closer, existing: br.existing,
			segA: segA, segB: segB,
			r: r, x: x, zSq: r*r + x*x, k: k, resolved: resolved, refV: refV,
		})
	}

	passes := 0
	if len(loops) > 0 {
		var sourceR, sourceX float64
		if ss := swept.states[swept.start]; ss != nil {
			sourceR, sourceX = resolveSourceImpedance(ss.attrs)
		}
		passes = compensate(swept.tr, swept.states, swept.start, swept.nominalV, sourceR, sourceX, loops, maxIter)
	}

	nodes, overloaded := formatResults(swept.tr, swept.states, swept.classCodes)

	loopResults := make([]loopResult, 0, len(loops)+len(openLoops))
	for _, lp := range loops {
		note := ""
		compensated := true
		if lp.zSq <= 0 {
			// A loop entirely through zero-impedance junctions: the two endpoints
			// are the same electrical point, nothing to circulate. Only honest to
			// flag it rather than divide by zero.
			compensated = false
			note = "loop impedance is zero (ideal wire loop) -- endpoints tied, no circulating flow"
		}
		loopResults = append(loopResults, loopResult{
			Closer:            featureRefOf(lp.closer, swept.classCodes),
			Existing:          featureRefOf(lp.existing, swept.classCodes),
			LoopResistanceOhm: lp.r,
			LoopReactanceOhm:  lp.x,
			ResidualMismatchV: voltageAt(swept.states, lp.existing) - voltageAt(swept.states, lp.closer),
			ImpedanceResolved: lp.resolved,
			Compensated:       compensated,
			Note:              note,
		})
	}
	loopResults = append(loopResults, openLoops...)

	return &meshedLoadFlowResponse{
		Start:           req.Start,
		NominalVoltageV: swept.nominalV, // the base actually used (derived secondary at a transformer start)
		Iterations:      sweepIterations,
		CompPasses:      passes,
		LoopCount:       len(loopResults),
		Nodes:           nodes,
		Barriers:        toFeatureRefs(swept.barrierHits, swept.classCodes),
		Overloaded:      overloaded,
		Loops:           loopResults,
	}, nil
}

// compensate runs the outer compensation loop: reset every node's cumulative
// power to the radial base, apply every loop's accumulated circulating flow,
// forward-sweep, then adjust each loop's circulating flow by the linear step
// that would close its remaining mismatch. Returns the number of passes run.
//
// The base cumulative power is snapshotted once (the backward sweep already
// ran inside runSweep); each pass only re-applies corrections and re-runs the
// forward sweep, so this is pure in-memory arithmetic with no extra queries --
// the same resolve-once / re-sweep pattern hosting.go uses.
func compensate(tr *tree, states map[node]*nodeState, start node, nominalV, sourceR, sourceX float64, loops []*loopGeom, maxIter int) int {
	baseKW := make(map[node]float64, len(states))
	baseKVAR := make(map[node]float64, len(states))
	for n, st := range states {
		baseKW[n] = st.cumKW
		baseKVAR[n] = st.cumKVAR
	}

	passes := 0
	for it := 0; it < maxIter; it++ {
		passes = it + 1

		for n, st := range states {
			st.cumKW = baseKW[n]
			st.cumKVAR = baseKVAR[n]
		}
		for _, lp := range loops {
			applyCorrection(states, lp)
		}
		forwardSweep(tr, states, start, nominalV, sourceR, sourceX)

		maxResidual := 0.0
		for _, lp := range loops {
			// denom = R_loop + k·X_loop is the endpoint-gap sensitivity to a unit
			// of circulating real power injected at the load power factor (cQ = k·cP):
			// the gap moves by (cP·R_loop + cQ·X_loop)/refV = cP·denom/refV. It
			// degenerates only when the loop is (near) zero-impedance in the
			// direction the load pf can drive -- a purely reactive loop fed a real
			// load, or an ideal-wire loop -- both nothing to (or unable to) solve.
			denom := lp.r + lp.k*lp.x
			if lp.zSq <= 0 || math.Abs(denom) < 1e-12 {
				continue
			}
			// Remaining mismatch after the currently-applied correction. Adding
			// dP = -(m·refV/denom) with dQ = k·dP changes the endpoint gap by
			// (dP·R_loop + dQ·X_loop)/refV = dP·denom/refV = -m, closing it in one
			// step under the linearization (see the file header).
			m := voltageAt(states, lp.existing) - voltageAt(states, lp.closer)
			if a := math.Abs(m); a > maxResidual {
				maxResidual = a
			}
			dP := -m * lp.refV / denom
			lp.cP += dP
			lp.cQ += lp.k * dP
		}
		if maxResidual < meshConvergedV {
			break
		}
	}

	// Leave the states holding the final converged correction so formatResults
	// reports the compensated voltages and (loop-adjusted) branch loadings.
	for n, st := range states {
		st.cumKW = baseKW[n]
		st.cumKVAR = baseKVAR[n]
	}
	for _, lp := range loops {
		applyCorrection(states, lp)
	}
	forwardSweep(tr, states, start, nominalV, sourceR, sourceX)
	return passes
}

// applyCorrection adds a loop's accumulated circulating power to the branches
// toward `closer` (segB) and subtracts it from the branches toward `existing`
// (segA) -- the sign convention that raises V(existing) and lowers V(closer)
// toward each other as cP/cQ grow (see the file header's KVL derivation).
func applyCorrection(states map[node]*nodeState, lp *loopGeom) {
	for _, n := range lp.segA {
		if st := states[n]; st != nil {
			st.cumKW -= lp.cP
			st.cumKVAR -= lp.cQ
		}
	}
	for _, n := range lp.segB {
		if st := states[n]; st != nil {
			st.cumKW += lp.cP
			st.cumKVAR += lp.cQ
		}
	}
}

// loopBranches returns the two tree-path segments of a loop: from `a`
// (`existing`) and from `b` (`closer`) each up to but excluding their lowest
// common ancestor. Together with the (ideal) loop-closing edge these are the
// branches the circulating flow traverses.
func loopBranches(tr *tree, a, b node) (segA, segB []node) {
	ancA := pathToRoot(tr, a)
	idxInA := make(map[node]int, len(ancA))
	for i, n := range ancA {
		idxInA[n] = i
	}

	ancB := pathToRoot(tr, b)
	for j, n := range ancB {
		if i, ok := idxInA[n]; ok {
			// n is the LCA: segA is a..(child of LCA), segB is b..(child of LCA).
			return append([]node{}, ancA[:i]...), append([]node{}, ancB[:j]...)
		}
	}
	// No common ancestor (should not happen inside one tree) -- treat both whole
	// paths as the loop, the safe over-inclusive fallback.
	return ancA, ancB
}

// pathToRoot returns n, parent(n), ..., root (the sweep's start) inclusive.
func pathToRoot(tr *tree, n node) []node {
	path := []node{n}
	for {
		p, ok := tr.parent[n]
		if !ok {
			break // start has no parent entry -- it is the root
		}
		path = append(path, p)
		n = p
	}
	return path
}

// loopImpedance sums the series R and X of every branch on the loop -- a
// conductor node's own resolved impedance, or a two-winding transformer's own
// series impedance if the loop crosses one. resolved is false when any
// conductor on the loop failed to resolve its material (the compensation then
// rests on an incomplete loop impedance and is flagged as such).
func loopImpedance(states map[node]*nodeState, segA, segB []node) (r, x float64, resolved bool) {
	resolved = true
	for _, seg := range [][]node{segA, segB} {
		for _, n := range seg {
			st := states[n]
			if st == nil {
				continue
			}
			switch {
			case st.impedance.resolved:
				r += st.impedance.rOhm
				x += st.impedance.xOhm
			case st.xfmr.ok && !st.xfmr.threeWinding:
				r += st.xfmr.rOhm
				x += st.xfmr.xOhm
			case st.loaded != nil && st.loaded.FeatureClass.NetworkRole == "edge":
				// A conductor branch on the loop whose material never resolved --
				// its R/X is missing from the loop impedance.
				resolved = false
			}
		}
	}
	return r, x, resolved
}

// loopRefVoltage is the base voltage a loop's linear drop is measured against --
// the endpoints' own voltage base (LV below a transformer, MV above), so an LV
// ring's circulating flow is not scaled by an MV nominal. Falls back through the
// two endpoints to the run nominal.
func loopRefVoltage(states map[node]*nodeState, existing, closer node, nominalV float64) float64 {
	if st := states[existing]; st != nil && st.voltageBase > 0 {
		return st.voltageBase
	}
	if st := states[closer]; st != nil && st.voltageBase > 0 {
		return st.voltageBase
	}
	return nominalV
}

// loopLoadPF is the load power factor ratio Q/P of the demand crossing a loop's
// break, read from the radial base cumulative power at `existing` (its own
// subtree load, the power the two loop paths share once closed). The circulating
// power is injected at this ratio so a real load draws no spurious reactive
// circulation. Zero (purely real) when there is no real demand to key off.
func loopLoadPF(states map[node]*nodeState, existing node) float64 {
	st := states[existing]
	if st == nil || st.cumKW == 0 {
		return 0
	}
	return st.cumKVAR / st.cumKW
}

// voltageAt is the swept voltage at a node, 0 if it was never resolved.
func voltageAt(states map[node]*nodeState, n node) float64 {
	if st := states[n]; st != nil {
		return st.voltageV
	}
	return 0
}

// featureRefOf resolves one node to its response feature ref.
func featureRefOf(n node, classCodes map[string]string) featureRef {
	return featureRef{FeatureClass: classCodes[n.classID], FeatureID: n.featureID}
}
