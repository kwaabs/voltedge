package powerflow

import "testing"

// sweep_integration_test.go is the second Bet 2 validation slice (Book of Tests
// 8.1). Where sweep_test.go pins the pure kernels in ISOLATION, these tests drive
// the REAL assembled sweep passes -- backwardSweep then forwardSweep, the exact
// two calls runSweep makes after resolving a network from the database -- over
// small, hand-built radial networks, and check every node's accumulated demand
// and derived voltage against an independent by-hand calculation.
//
// This is the layer a kernel test structurally cannot reach: a pile of correct
// formulas wired together wrong still gives wrong answers. The networks here
// exercise how the passes COMPOSE -- backward accumulation up a tree and across
// sibling laterals, forward voltage propagation, source-impedance at the slack
// bus, and transformer voltage rebasing (an LV node reporting drop from its own
// secondary base, not a spurious ~96% drop from the MV nominal). No database:
// the tree and node states are constructed directly, the same structs runSweep
// hands the passes once it has resolved them from the DB.

// newLoadNode builds a plain pass-through demand node: own kW/kVAR, no branch
// impedance, no transformer -- the forward sweep's "default" inherited-voltage
// case. A source bus is just this with zero demand.
func newLoadNode(kW, kVAR float64) *nodeState {
	return &nodeState{ownKW: kW, ownKVAR: kVAR}
}

// newEdgeNode builds a conductor node carrying a resolved branch impedance (the
// forward sweep's st.impedance.resolved case).
func newEdgeNode(rOhm, xOhm float64) *nodeState {
	return &nodeState{impedance: conductorImpedance{rOhm: rOhm, xOhm: xOhm, resolved: true}}
}

// newXfmrNode builds a two-winding transformer junction: turns ratio
// secondaryV/primaryV, series impedance referred to the secondary, and the LV
// base it hands down (the forward sweep's st.xfmr.ok case).
func newXfmrNode(ratio, rOhm, xOhm, secondaryV float64) *nodeState {
	return &nodeState{xfmr: transformerModel{ok: true, ratio: ratio, rOhm: rOhm, xOhm: xOhm, secondaryV: secondaryV}}
}

// runToySweep runs the two real sweep passes over a constructed tree exactly as
// runSweep does: backward to accumulate demand, forward to derive voltage from
// the start's supplied nominal and (optional) Thevenin source impedance.
func runToySweep(tr *tree, states map[node]*nodeState, start node, nominalV, sourceR, sourceX float64) {
	backwardSweep(tr, states)
	forwardSweep(tr, states, start, nominalV, sourceR, sourceX)
}

// TestSweepRadialChain: source --[conductor R=0.5Ω X=0.3Ω]--> load(100kW,50kVAR),
// 400 V nominal, ideal source (no Thevenin impedance). Validates forward voltage
// propagation and that a pass-through load sits at its conductor's receiving end.
func TestSweepRadialChain(t *testing.T) {
	src := node{classID: "src", featureID: "s"}
	cond := node{classID: "edge", featureID: "c"}
	load := node{classID: "load", featureID: "l"}

	tr := &tree{
		order:        []node{src, cond, load},
		parent:       map[node]node{cond: src, load: cond},
		children:     map[node][]node{src: {cond}, cond: {load}},
		childWinding: map[node]string{},
	}
	states := map[node]*nodeState{
		src:  newLoadNode(0, 0),
		cond: newEdgeNode(0.5, 0.3),
		load: newLoadNode(100, 50),
	}
	runToySweep(tr, states, src, 400, 0, 0)

	// Backward: the conductor (and source) carry the whole 100 kW + 50 kVAR load.
	if got := states[cond].cumKW; got != 100 {
		t.Fatalf("cond cumKW = %v, want 100", got)
	}
	if got := states[cond].cumKVAR; got != 50 {
		t.Fatalf("cond cumKVAR = %v, want 50", got)
	}

	// Forward: ideal source holds the nominal.
	if got := states[src].voltageV; got != 400 {
		t.Fatalf("src voltageV = %v, want 400", got)
	}
	// Conductor drop = (100 kW·0.5Ω + 50 kVAR·0.3Ω)·1000 / 400 V
	//               = (50000 + 15000) / 400 = 162.5 V  →  400 − 162.5 = 237.5 V.
	if got := states[cond].voltageV; !approxEqual(got, 237.5, 1e-9) {
		t.Fatalf("cond voltageV = %v, want 237.5", got)
	}
	// The load is pass-through: it sits at the conductor's receiving-end voltage,
	// still on the 400 V base (no transformer crossed).
	if got := states[load].voltageV; !approxEqual(got, 237.5, 1e-9) {
		t.Fatalf("load voltageV = %v, want 237.5", got)
	}
	if got := states[load].voltageBase; got != 400 {
		t.Fatalf("load voltageBase = %v, want 400", got)
	}
}

// TestSweepSourceImpedance: an ideal-nominal source carrying a Thevenin source
// impedance R=0.1Ω X=0.2Ω and serving 100 kW + 50 kVAR directly. Validates the
// start-node branch, whose own voltage absorbs the source drop against the whole
// network's demand before anything downstream.
func TestSweepSourceImpedance(t *testing.T) {
	src := node{classID: "src", featureID: "s"}
	load := node{classID: "load", featureID: "l"}

	tr := &tree{
		order:        []node{src, load},
		parent:       map[node]node{load: src},
		children:     map[node][]node{src: {load}},
		childWinding: map[node]string{},
	}
	states := map[node]*nodeState{
		src:  newLoadNode(0, 0),
		load: newLoadNode(100, 50),
	}
	runToySweep(tr, states, src, 400, 0.1, 0.2)

	// Source drop = (100 kW·0.1Ω + 50 kVAR·0.2Ω)·1000 / 400 V
	//            = (10000 + 10000) / 400 = 50 V  →  400 − 50 = 350 V at the bus.
	if got := states[src].voltageV; !approxEqual(got, 350, 1e-9) {
		t.Fatalf("src voltageV = %v, want 350", got)
	}
	// The directly-served load inherits the depressed source-bus voltage.
	if got := states[load].voltageV; !approxEqual(got, 350, 1e-9) {
		t.Fatalf("load voltageV = %v, want 350", got)
	}
}

// TestSweepTransformerRebase: source(11 kV) --[11 kV/400 V transformer, secondary
// Z: R=0.01Ω X=0.02Ω]--> load(100kW,50kVAR). The prize of this suite: the forward
// sweep must rebase the LV node onto the 400 V secondary, reporting a small real
// drop from that base rather than a spurious ~96% drop from the 11 kV nominal.
func TestSweepTransformerRebase(t *testing.T) {
	src := node{classID: "src", featureID: "s"}
	xf := node{classID: "xfmr", featureID: "t"}
	load := node{classID: "load", featureID: "l"}

	tr := &tree{
		order:        []node{src, xf, load},
		parent:       map[node]node{xf: src, load: xf},
		children:     map[node][]node{src: {xf}, xf: {load}},
		childWinding: map[node]string{},
	}
	states := map[node]*nodeState{
		src:  newLoadNode(0, 0),
		xf:   newXfmrNode(400.0/11000.0, 0.01, 0.02, 400),
		load: newLoadNode(100, 50),
	}
	runToySweep(tr, states, src, 11000, 0, 0)

	// Ideal open-circuit secondary = 11000 · (400/11000) = 400 V; minus the
	// transformer's own drop on the LV base:
	//   (100 kW·0.01Ω + 50 kVAR·0.02Ω)·1000 / 400 V = (1000 + 1000) / 400 = 5 V.
	// → 395 V on a 400 V base.
	if got := states[xf].voltageBase; got != 400 {
		t.Fatalf("xfmr voltageBase = %v, want 400", got)
	}
	if got := states[xf].voltageV; !approxEqual(got, 395, 1e-9) {
		t.Fatalf("xfmr voltageV = %v, want 395", got)
	}
	// The LV load inherits the secondary voltage and base -- a 1.25% drop from its
	// OWN base, not a bogus drop measured against 11 kV.
	if got := states[load].voltageV; !approxEqual(got, 395, 1e-9) {
		t.Fatalf("load voltageV = %v, want 395", got)
	}
	if got := states[load].voltageBase; got != 400 {
		t.Fatalf("load voltageBase = %v, want 400", got)
	}
}

// TestSweepBranchingAccumulation: source --[conductor]--> {loadA(60kW,30kVAR),
// loadB(40kW,20kVAR)}. Validates the backward pass summing BOTH laterals onto the
// shared conductor -- the composition a single-load chain never exercises.
func TestSweepBranchingAccumulation(t *testing.T) {
	src := node{classID: "src", featureID: "s"}
	cond := node{classID: "edge", featureID: "c"}
	la := node{classID: "load", featureID: "a"}
	lb := node{classID: "load", featureID: "b"}

	tr := &tree{
		order:        []node{src, cond, la, lb},
		parent:       map[node]node{cond: src, la: cond, lb: cond},
		children:     map[node][]node{src: {cond}, cond: {la, lb}},
		childWinding: map[node]string{},
	}
	states := map[node]*nodeState{
		src:  newLoadNode(0, 0),
		cond: newEdgeNode(0.5, 0.3),
		la:   newLoadNode(60, 30),
		lb:   newLoadNode(40, 20),
	}
	runToySweep(tr, states, src, 400, 0, 0)

	// Shared conductor carries the sum of both laterals: 100 kW, 50 kVAR.
	if got := states[cond].cumKW; got != 100 {
		t.Fatalf("cond cumKW = %v, want 100 (60+40)", got)
	}
	if got := states[cond].cumKVAR; got != 50 {
		t.Fatalf("cond cumKVAR = %v, want 50 (30+20)", got)
	}
	// Same 162.5 V drop as the single-load chain, since the summed load matches.
	if got := states[cond].voltageV; !approxEqual(got, 237.5, 1e-9) {
		t.Fatalf("cond voltageV = %v, want 237.5", got)
	}
	// Both laterals sit at the conductor's receiving-end voltage.
	if got := states[la].voltageV; !approxEqual(got, 237.5, 1e-9) {
		t.Fatalf("loadA voltageV = %v, want 237.5", got)
	}
	if got := states[lb].voltageV; !approxEqual(got, 237.5, 1e-9) {
		t.Fatalf("loadB voltageV = %v, want 237.5", got)
	}
}
