package powerflow

import (
	"math"
	"testing"

	"github.com/kwaabs/voltedge/api/internal/metadata"
)

// fault_integration_test.go is the Bet 2 validation slice for the fault-current
// engine (Book of Tests 8.1). Like sweep_integration_test.go it drives the REAL
// in-memory kernels -- accumulateSeriesImpedance, formatFaultResults,
// addGroundFaultFields, generatorFaultContributions -- over small hand-built
// networks with no database, and checks each result against an independent
// hand calculation.
//
// The headline concern these pin is the VOLTAGE BASIS. The engine accumulates
// per-phase ohms, so the standard three-phase symmetrical fault current is
// I = V_LN / |Z| = (V_base / √3) / |Z| when the nominal is line-to-line (the
// default), not the √3-inflated V_base / |Z|. The same √3 governs the SLG and
// the embedded-generation infeed, which share one V²/S convention -- so a
// declared basis must scale all three together. The generator round-trip test
// is the strongest check: a machine's nameplate fault-kA must come back exactly
// at its own terminals once the basis is right.

// edgeLoaded / nodeLoaded are the minimal metadata.Loaded fixtures the fault
// kernels need to tell a conductor (an impedance-bearing edge) from a pass-
// through node, the one field accumulateSeriesImpedance / accumulateRootBase-
// Impedance read off the loaded class.
func edgeLoaded() *metadata.Loaded {
	return &metadata.Loaded{FeatureClass: &metadata.FeatureClass{NetworkRole: "edge"}}
}
func nodeLoaded() *metadata.Loaded {
	return &metadata.Loaded{FeatureClass: &metadata.FeatureClass{NetworkRole: "node"}}
}

// TestPhaseDivisor pins the basis factor: √3 for a line-to-line nominal (and the
// empty/unknown default), 1 for a line-to-neutral one.
func TestPhaseDivisor(t *testing.T) {
	cases := []struct {
		basis string
		want  float64
	}{
		{basisLineToLine, math.Sqrt(3)},
		{basisLineToNeutral, 1},
		{"", math.Sqrt(3)},         // default
		{"nonsense", math.Sqrt(3)}, // unknown falls back to line-to-line
	}
	for _, c := range cases {
		if got := phaseDivisor(c.basis); !approxEqual(got, c.want, 1e-12) {
			t.Fatalf("phaseDivisor(%q) = %v, want %v", c.basis, got, c.want)
		}
	}
}

// TestFaultSeriesImpedanceAccumulation drives the real accumulateSeriesImpedance:
// source(ideal) --[conductor R=0.5 X=0.5]--> [10kV/1kV xfmr, ratio 0.1, Zsec
// R=0.015 X=0.02] --[conductor R=0.03 X=0]--> load. Validates the forward sum and
// the transformer referring the upstream impedance across ratio² onto the LV base.
func TestFaultSeriesImpedanceAccumulation(t *testing.T) {
	src := node{classID: "src", featureID: "s"}
	c1 := node{classID: "edge", featureID: "c1"}
	xf := node{classID: "xfmr", featureID: "t"}
	c2 := node{classID: "edge", featureID: "c2"}

	tr := &tree{
		order:    []node{src, c1, xf, c2},
		parent:   map[node]node{c1: src, xf: c1, c2: xf},
		children: map[node][]node{src: {c1}, c1: {xf}, xf: {c2}},
	}
	states := map[node]*nodeState{
		src: {loaded: nodeLoaded()},
		c1:  {loaded: edgeLoaded(), impedance: conductorImpedance{rOhm: 0.5, xOhm: 0.5, resolved: true}},
		xf:  {loaded: nodeLoaded(), xfmr: transformerModel{ok: true, ratio: 0.1, rOhm: 0.015, xOhm: 0.02, secondaryV: 1000}},
		c2:  {loaded: edgeLoaded(), impedance: conductorImpedance{rOhm: 0.03, xOhm: 0, resolved: true}},
	}
	paths := accumulateSeriesImpedance(tr, states, src, 10000, 0, 0)

	// c1: source (0) + conductor, on the 10 kV base.
	if p := paths[c1]; !approxEqual(p.rOhm, 0.5, 1e-9) || !approxEqual(p.xOhm, 0.5, 1e-9) || p.baseV != 10000 {
		t.Fatalf("c1 path = %+v, want R=0.5 X=0.5 base=10000", p)
	}
	// xf: refer the upstream 0.5+j0.5 across ratio²=0.01 → 0.005+j0.005, then add
	// the transformer's own 0.015+j0.02 → 0.02+j0.025, rebased to 1000 V.
	if p := paths[xf]; !approxEqual(p.rOhm, 0.02, 1e-9) || !approxEqual(p.xOhm, 0.025, 1e-9) || p.baseV != 1000 {
		t.Fatalf("xf path = %+v, want R=0.02 X=0.025 base=1000", p)
	}
	// c2: add its own 0.03+j0 on the 1000 V base.
	if p := paths[c2]; !approxEqual(p.rOhm, 0.05, 1e-9) || !approxEqual(p.xOhm, 0.025, 1e-9) || p.baseV != 1000 {
		t.Fatalf("c2 path = %+v, want R=0.05 X=0.025 base=1000", p)
	}
}

// TestFaultCurrentThreePhaseBasis drives the real formatFaultResults for a node at
// |Z| = 1 Ω on an 11 kV base, and checks the standard per-phase fault current on
// each basis: line-to-line → 11000/√3 A, line-to-neutral → 11000 A.
func TestFaultCurrentThreePhaseBasis(t *testing.T) {
	f := node{classID: "bus", featureID: "f"}
	tr := &tree{order: []node{f}}
	states := map[node]*nodeState{f: {}}
	// R=0.6, X=0.8 → |Z| = 1.0 exactly.
	paths := map[node]pathImpedance{f: {rOhm: 0.6, xOhm: 0.8, baseV: 11000, resolved: true}}
	classCodes := map[string]string{"bus": "BUS"}

	for _, c := range []struct {
		basis string
		want  float64
	}{
		{basisLineToLine, 11000 / math.Sqrt(3)},
		{basisLineToNeutral, 11000},
	} {
		got := formatFaultResults(tr, states, paths, nil, nil, 0, phaseDivisor(c.basis), classCodes)
		if len(got) != 1 || got[0].FaultCurrentAmps == nil {
			t.Fatalf("basis %q: no fault current returned (%+v)", c.basis, got)
		}
		if !approxEqual(*got[0].FaultCurrentAmps, c.want, 1e-6) {
			t.Fatalf("basis %q: fault current = %v, want %v", c.basis, *got[0].FaultCurrentAmps, c.want)
		}
		if !approxEqual(got[0].ImpedanceOhm, 1.0, 1e-12) {
			t.Fatalf("basis %q: |Z| = %v, want 1.0", c.basis, got[0].ImpedanceOhm)
		}
	}
}

// TestFaultSLGBasis drives the real addGroundFaultFields. With Z1 = Z2 = Z0 =
// 0.6+j0.8 and a bolted fault (Zf=0), the denominator is (2·R1+R0) + j(2·X1+X0)
// = 1.8 + j2.4, |Z| = 3.0. The SLG current is 3·V_LN / 3.0, so line-to-line →
// 11000/√3 A, line-to-neutral → 11000 A.
func TestFaultSLGBasis(t *testing.T) {
	p := pathImpedance{rOhm: 0.6, xOhm: 0.8, baseV: 11000, resolved: true}
	zp := zeroSeqPath{r0Ohm: 0.6, x0Ohm: 0.8, baseV: 11000, grounded: true, resolved: true}

	for _, c := range []struct {
		basis string
		want  float64
	}{
		{basisLineToLine, 11000 / math.Sqrt(3)},
		{basisLineToNeutral, 11000},
	} {
		var res faultNodeResult
		addGroundFaultFields(&res, p, zp, 0, phaseDivisor(c.basis))
		if res.GroundFaultCurrentAmps == nil {
			t.Fatalf("basis %q: no SLG current returned", c.basis)
		}
		if !approxEqual(*res.GroundFaultCurrentAmps, c.want, 1e-6) {
			t.Fatalf("basis %q: SLG current = %v, want %v", c.basis, *res.GroundFaultCurrentAmps, c.want)
		}
	}
}

// TestFaultGeneratorInfeedNameplate is the round-trip that proves the basis is
// coherent across BOTH sources: a generator with an explicit 5 kA nameplate
// fault contribution, faulted at its own terminals, must feed back exactly its
// 5000 A nameplate once the √3 in its internal-impedance formula cancels against
// the line-to-neutral driving voltage. A wrong basis would return √3·5000.
func TestFaultGeneratorInfeedNameplate(t *testing.T) {
	src := node{classID: "src", featureID: "s"}
	gen := node{classID: "gen", featureID: "g"}
	tr := &tree{
		order:    []node{src, gen},
		parent:   map[node]node{gen: src},
		children: map[node][]node{src: {gen}},
	}
	states := map[node]*nodeState{
		src: {loaded: nodeLoaded()},
		gen: {loaded: nodeLoaded(), genFault: genFaultSpec{ok: true, mode: genFaultExplicitKA, faultKA: 5}},
	}
	// Both nodes on a 10 kV base (ideal source, no series path to the generator
	// terminals), so the loop impedance is the generator's own internal impedance.
	paths := map[node]pathImpedance{src: {baseV: 10000}, gen: {baseV: 10000}}

	contrib := generatorFaultContributions(tr, states, paths, src, 0, 0, phaseDivisor(basisLineToLine))
	if !approxEqual(contrib[gen], 5000, 1e-6) {
		t.Fatalf("generator infeed at its own terminals = %v A, want 5000 (nameplate 5 kA)", contrib[gen])
	}
}
