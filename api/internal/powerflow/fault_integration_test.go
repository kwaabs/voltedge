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

// TestAsymmetryKappa pins the IEC 60909 peak-asymmetry factor κ = 1.02 + 0.98·
// e^(−3·R/X) against hand values, and its guards/limits.
func TestAsymmetryKappa(t *testing.T) {
	cases := []struct {
		name       string
		rOhm, xOhm float64
		want       float64
	}{
		// X/R = 1  → κ = 1.02 + 0.98·e^(−3)   = 1.02 + 0.98·0.04978707 = 1.06879133
		{"X/R = 1", 1, 1, 1.06879133},
		// X/R = 10 → κ = 1.02 + 0.98·e^(−0.3) = 1.02 + 0.98·0.74081822 = 1.74600186
		{"X/R = 10", 1, 10, 1.74600186},
		// Very inductive (R→0): κ → 2.0 (maximum DC offset).
		{"near purely reactive", 1e-6, 1, 2.0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := asymmetryKappa(c.rOhm, c.xOhm); !approxEqual(got, c.want, 1e-4) {
				t.Fatalf("asymmetryKappa(%v,%v) = %v, want %v", c.rOhm, c.xOhm, got, c.want)
			}
		})
	}
	// Guards: X/R undefined (no reactance, or no resistance) → 0 (caller omits the peak).
	for _, g := range [][2]float64{{0, 1}, {1, 0}, {0, 0}} {
		if got := asymmetryKappa(g[0], g[1]); got != 0 {
			t.Fatalf("asymmetryKappa(%v,%v) = %v, want 0", g[0], g[1], got)
		}
	}
}

// TestPeakFaultCurrentAmps pins i_p = κ·√2·I_sym.
func TestPeakFaultCurrentAmps(t *testing.T) {
	// κ = 1.746, I_sym = 1000 A → i_p = 1.746·√2·1000 = 2469.2165 A.
	if got := peakFaultCurrentAmps(1000, 1.746); !approxEqual(got, 1.746*math.Sqrt2*1000, 1e-9) {
		t.Fatalf("peakFaultCurrentAmps(1000, 1.746) = %v, want %v", got, 1.746*math.Sqrt2*1000)
	}
}

// TestFaultPeakAsymmetryInResult drives the real formatFaultResults and checks
// the peak-asymmetry fields are populated and internally consistent: X/R from the
// path, κ from the IEC formula, and i_p = κ·√2·I_sym on the total current.
func TestFaultPeakAsymmetryInResult(t *testing.T) {
	f := node{classID: "bus", featureID: "f"}
	tr := &tree{order: []node{f}}
	states := map[node]*nodeState{f: {}}
	// R=0.6, X=0.8 → |Z|=1.0, X/R = 1.3333; line-to-neutral basis → I_sym = 11000 A.
	paths := map[node]pathImpedance{f: {rOhm: 0.6, xOhm: 0.8, baseV: 11000, resolved: true}}
	classCodes := map[string]string{"bus": "BUS"}

	got := formatFaultResults(tr, states, paths, nil, nil, 0, phaseDivisor(basisLineToNeutral), classCodes)
	if len(got) != 1 {
		t.Fatalf("expected 1 node, got %d", len(got))
	}
	r := got[0]
	if r.XrRatio == nil || r.PeakAsymmetryKappa == nil || r.PeakFaultCurrentAmps == nil || r.FaultCurrentAmps == nil {
		t.Fatalf("peak-asymmetry fields not populated: %+v", r)
	}
	if !approxEqual(*r.XrRatio, 0.8/0.6, 1e-9) {
		t.Fatalf("XrRatio = %v, want %v", *r.XrRatio, 0.8/0.6)
	}
	// κ = 1.02 + 0.98·e^(−3·0.6/0.8) = 1.02 + 0.98·e^(−2.25) = 1.12329124.
	if !approxEqual(*r.PeakAsymmetryKappa, 1.12329124, 1e-6) {
		t.Fatalf("κ = %v, want 1.12329124", *r.PeakAsymmetryKappa)
	}
	// i_p must be κ·√2·I_sym on the reported total current.
	wantIp := *r.PeakAsymmetryKappa * math.Sqrt2 * *r.FaultCurrentAmps
	if !approxEqual(*r.PeakFaultCurrentAmps, wantIp, 1e-6) {
		t.Fatalf("i_p = %v, want %v (κ·√2·I_sym)", *r.PeakFaultCurrentAmps, wantIp)
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

func ptrS(s string) *string { return &s }

// xfmrBoundaryState builds a start nodeState for a boundary transformer
// (boundary_role = 'transformer') carrying a resolved secondary %Z -- the
// DIST_XFMR-as-LV-source case sourceFromStart is for.
func xfmrBoundaryState(rOhm, xOhm, secondaryV float64, attrs map[string]any) *nodeState {
	return &nodeState{
		loaded: &metadata.Loaded{FeatureClass: &metadata.FeatureClass{
			BoundaryRole: ptrS(boundaryRoleTransformer), NetworkRole: "junction",
		}},
		xfmr:  transformerModel{ok: true, rOhm: rOhm, xOhm: xOhm, secondaryV: secondaryV},
		attrs: attrs,
	}
}

// TestSourceFromStart pins the source-seed resolver that makes a boundary
// transformer the source of its own secondary network.
func TestSourceFromStart(t *testing.T) {
	// Transformer boundary with a real %Z -> transformer-as-source: its secondary
	// Z, on the secondary base, fromTransformer = true.
	r, x, base, fromX := sourceFromStart(xfmrBoundaryState(0.002, 0.014, 400, nil), 11000)
	if !fromX || !approxEqual(r, 0.002, 1e-12) || !approxEqual(x, 0.014, 1e-12) || base != 400 {
		t.Fatalf("transformer boundary: (%v,%v,%v,%v), want (0.002,0.014,400,true)", r, x, base, fromX)
	}

	// nominal_voltage_v OMITTED (0) at a transformer boundary -> still derives the
	// secondary voltage as the base (Slice 2: no voltage input needed to analyse the
	// LV network below a transformer).
	r, x, base, fromX = sourceFromStart(xfmrBoundaryState(0.002, 0.014, 400, nil), 0)
	if !fromX || base != 400 {
		t.Fatalf("omitted nominal at transformer: base=%v fromTransformer=%v, want 400/true", base, fromX)
	}

	// Upstream Thevenin source_r/x is added in series on the secondary base.
	r, x, base, fromX = sourceFromStart(
		xfmrBoundaryState(0.002, 0.014, 400, map[string]any{"source_r_ohm": 0.001, "source_x_ohm": 0.003}), 11000)
	if !fromX || !approxEqual(r, 0.003, 1e-12) || !approxEqual(x, 0.017, 1e-12) || base != 400 {
		t.Fatalf("transformer + upstream: (%v,%v,%v,%v), want (0.003,0.017,400,true)", r, x, base, fromX)
	}

	// A boundary transformer with NO %Z (rOhm = xOhm = 0) falls back to its own
	// source_r/x on the supplied nominal -- the feeder-head POWER_XFMR / SUB-SOURCE
	// case, so existing MV studies are unchanged.
	r, x, base, fromX = sourceFromStart(
		xfmrBoundaryState(0, 0, 11000, map[string]any{"source_r_ohm": 0.048, "source_x_ohm": 0.48}), 11000)
	if fromX || !approxEqual(r, 0.048, 1e-12) || !approxEqual(x, 0.48, 1e-12) || base != 11000 {
		t.Fatalf("transformer no-%%Z: (%v,%v,%v,%v), want (0.048,0.48,11000,false)", r, x, base, fromX)
	}

	// A non-transformer start always uses source_r/x on the supplied nominal.
	nonX := &nodeState{
		loaded: &metadata.Loaded{FeatureClass: &metadata.FeatureClass{NetworkRole: "junction"}},
		attrs:  map[string]any{"source_r_ohm": 0.1, "source_x_ohm": 0.2},
	}
	r, x, base, fromX = sourceFromStart(nonX, 400)
	if fromX || !approxEqual(r, 0.1, 1e-12) || !approxEqual(x, 0.2, 1e-12) || base != 400 {
		t.Fatalf("non-transformer: (%v,%v,%v,%v), want (0.1,0.2,400,false)", r, x, base, fromX)
	}

	// Nil start -> zero source on the supplied nominal (guard).
	if r, x, base, fromX = sourceFromStart(nil, 400); fromX || r != 0 || x != 0 || base != 400 {
		t.Fatalf("nil start: (%v,%v,%v,%v), want (0,0,400,false)", r, x, base, fromX)
	}
}

// TestFaultFromTransformerSource is the end-to-end payoff: a fault study STARTED
// at a boundary distribution transformer (500 kVA, 4.5 % ≈ 0.0144 Ω on the 400 V
// base) sees the transformer's own stiffness as its source, giving the classic
// ~16 kA LV fault level -- with no upstream MV network modelled at all.
func TestFaultFromTransformerSource(t *testing.T) {
	tx := node{classID: "dxf", featureID: "t"}  // the DIST_XFMR boundary (start)
	bus := node{classID: "lvb", featureID: "b"} // an LV busbar just below it
	tr := &tree{
		order:    []node{tx, bus},
		parent:   map[node]node{bus: tx},
		children: map[node][]node{tx: {bus}},
	}
	// 500 kVA, 4.5 % at 400 V -> Zbase = 400²/500000 = 0.32 Ω, Z = 0.0144 Ω
	// (pure reactance here for a clean |Z|).
	states := map[node]*nodeState{
		tx:  xfmrBoundaryState(0, 0.0144, 400, nil),
		bus: {loaded: nodeLoaded()},
	}
	sr, sx, base, fromX := sourceFromStart(states[tx], 400)
	if !fromX || base != 400 {
		t.Fatalf("expected transformer source on the 400 V base, got base=%v fromTransformer=%v", base, fromX)
	}
	paths := accumulateSeriesImpedance(tr, states, tx, base, sr, sx)
	nodes := formatFaultResults(tr, states, paths, nil, nil, 0, phaseDivisor(basisLineToLine),
		map[string]string{"dxf": "DXF", "lvb": "LVB"})

	var busAmps float64
	for _, n := range nodes {
		if n.FeatureClass == "LVB" && n.FaultCurrentAmps != nil {
			busAmps = *n.FaultCurrentAmps
		}
	}
	want := (400 / math.Sqrt(3)) / 0.0144 // ≈ 16038 A
	if !approxEqual(busAmps, want, 1e-3) {
		t.Fatalf("LV fault at the busbar = %v A, want %v A (~16 kA from the transformer %%Z)", busAmps, want)
	}
}
