package powerflow

import (
	"math"
	"testing"
)

// fault_iec60909_test.go -- Phase 1 slice 2/3: validate the fault-current engine
// against a worked IEC 60909 example, including the voltage factor c.
//
// Slice 2 found that the engine computed the bolted fault at nominal (c = 1.0),
// omitting IEC 60909's voltage factor c (1.05 LV / 1.10 MV, maximum), so its I"k
// was ~5%(LV)/~10%(MV) BELOW I"k_max -- non-conservative for switchgear duty.
// Slice 3 added c (fault.go effectiveFaultC / iecVoltageFactorC): the fault
// endpoint now reports the IEC MAXIMUM by default, overridable per request.
//
// Worked example: a 630 kVA 11 kV / 400 V distribution transformer, uk = 4%,
// X/R = 6, bolted three-phase fault at its LV terminals, no upstream network.
// Transformer short-circuit impedance referred to the 400 V side: Z_T = uk·Un²/S.
// Validated against the standard: I"k = c·Un/(√3·Z_T), κ = 1.02 + 0.98·e^(−3R/X),
// i_p = κ·√2·I"k, and the per-base default c (1.05 LV, 1.10 MV) plus an override.

// iecTransformerRX returns a transformer's short-circuit R, X and |Z| (ohms,
// referred to the voltage unV) from its impedance voltage uk (fraction), rating
// sVA (VA) and X/R ratio -- the standard IEC transformer-impedance derivation
// Z_T = uk · Un² / S, split by X/R.
func iecTransformerRX(uk, sVA, unV, xr float64) (rOhm, xOhm, zOhm float64) {
	zOhm = uk * unV * unV / sVA
	rOhm = zOhm / math.Sqrt(1+xr*xr)
	xOhm = xr * rOhm
	return
}

// TestFaultIEC60909TransformerFed drives the real resolver + accumulator +
// formatter over the worked example and checks the result against the IEC 60909
// maximum, including the LV voltage factor c = 1.05 the engine now applies.
func TestFaultIEC60909TransformerFed(t *testing.T) {
	const (
		uk  = 0.04     // 4% impedance voltage
		sVA = 630000.0 // 630 kVA
		un  = 400.0    // LV terminal nominal (line-to-line)
		xr  = 6.0      // X/R
		cLV = 1.05     // IEC 60909 voltage factor c -- LV maximum (Un ≤ 1 kV)
	)
	r, x, z := iecTransformerRX(uk, sVA, un, xr)

	tx := node{classID: "dxf", featureID: "t"}  // DIST_XFMR boundary (the start/source)
	bus := node{classID: "lvb", featureID: "b"} // LV busbar at the fault
	tr := &tree{
		order:    []node{tx, bus},
		parent:   map[node]node{bus: tx},
		children: map[node][]node{tx: {bus}},
	}
	states := map[node]*nodeState{
		tx:  xfmrBoundaryState(r, x, un, nil),
		bus: {loaded: nodeLoaded()},
	}

	sr, sx, base, fromX := sourceFromStart(states[tx], un)
	if !fromX || base != un {
		t.Fatalf("expected transformer-as-source on the %.0f V base, got base=%v fromTransformer=%v", un, base, fromX)
	}
	paths := accumulateSeriesImpedance(tr, states, tx, base, sr, sx)
	nodes := formatFaultResults(tr, states, paths, nil, nil, 0, phaseDivisor(basisLineToLine), 0,
		map[string]string{"dxf": "DXF", "lvb": "LVB"})

	var res *faultNodeResult
	for i := range nodes {
		if nodes[i].FeatureClass == "LVB" {
			res = &nodes[i]
		}
	}
	if res == nil || res.FaultCurrentAmps == nil {
		t.Fatalf("no LV fault current returned: %+v", nodes)
	}

	// |Z| at the fault = the transformer Z_T (nothing above it).
	if !approxEqual(res.ImpedanceOhm, z, 1e-9) {
		t.Fatalf("|Z| = %v Ω, want Z_T = uk·Un²/S = %v Ω", res.ImpedanceOhm, z)
	}

	// Default c for a 400 V (LV) node is 1.05.
	if !approxEqual(res.VoltageFactorC, cLV, 1e-12) {
		t.Fatalf("voltage factor c = %v, want %v (LV default)", res.VoltageFactorC, cLV)
	}

	// I"k = c·Un/(√3·Z_T) -- the IEC 60909 maximum initial symmetrical current.
	engineIsym := *res.FaultCurrentAmps
	wantIEC := cLV * (un / math.Sqrt(3)) / z
	if rel := math.Abs(engineIsym-wantIEC) / wantIEC; rel > 1e-6 {
		t.Fatalf("I\"k engine = %.1f A, want IEC c·Un/(√3·Z_T) = %.1f A", engineIsym, wantIEC)
	}

	// κ per IEC 60909 from the fault-point X/R.
	wantKappa := 1.02 + 0.98*math.Exp(-3*r/x)
	if res.PeakAsymmetryKappa == nil || !approxEqual(*res.PeakAsymmetryKappa, wantKappa, 1e-6) {
		t.Fatalf("κ = %v, want 1.02+0.98·e^(−3R/X) = %v", res.PeakAsymmetryKappa, wantKappa)
	}
	// i_p = κ·√2·I"k.
	if res.PeakFaultCurrentAmps == nil || !approxEqual(*res.PeakFaultCurrentAmps, wantKappa*math.Sqrt2*engineIsym, 1e-6) {
		t.Fatalf("i_p = %v, want κ·√2·I\"k = %v", res.PeakFaultCurrentAmps, wantKappa*math.Sqrt2*engineIsym)
	}

	t.Logf("IEC 60909 worked example -- 630 kVA, uk=4%%, X/R=6, 400 V LV, bolted 3φ:")
	t.Logf("  I\"k = %.0f A   i_p = %.0f A   (c = %.2f, IEC maximum)", engineIsym, *res.PeakFaultCurrentAmps, res.VoltageFactorC)
}

// TestFaultAppliesIEC60909VoltageFactorC pins that the engine now applies the IEC
// 60909 voltage factor c -- 1.10 above 1 kV, 1.05 at/below -- by default, and that
// an explicit override (c = 1.0) reproduces the old bolted-at-nominal figure.
func TestFaultAppliesIEC60909VoltageFactorC(t *testing.T) {
	// |Z| = 1 Ω on an 11 kV MV base.
	f := node{classID: "bus", featureID: "f"}
	tr := &tree{order: []node{f}}
	states := map[node]*nodeState{f: {}}
	paths := map[node]pathImpedance{f: {rOhm: 0.6, xOhm: 0.8, baseV: 11000, resolved: true}}
	classCodes := map[string]string{"bus": "BUS"}

	// Default: c = 1.10 at 11 kV -> I"k_max = 1.10·11000/√3.
	def := formatFaultResults(tr, states, paths, nil, nil, 0, phaseDivisor(basisLineToLine), 0, classCodes)
	if len(def) != 1 || def[0].FaultCurrentAmps == nil {
		t.Fatalf("no fault current returned: %+v", def)
	}
	if !approxEqual(def[0].VoltageFactorC, 1.10, 1e-12) {
		t.Fatalf("default c at 11 kV = %v, want 1.10", def[0].VoltageFactorC)
	}
	if want := 1.10 * 11000 / math.Sqrt(3); !approxEqual(*def[0].FaultCurrentAmps, want, 1e-6) {
		t.Fatalf("I\"k = %.1f A, want IEC max (c=1.10) %.1f A", *def[0].FaultCurrentAmps, want)
	}

	// Override c = 1.0 -> the bolted fault at nominal (the slice-2 behaviour),
	// available for a client that wants the un-factored figure.
	nom := formatFaultResults(tr, states, paths, nil, nil, 0, phaseDivisor(basisLineToLine), 1.0, classCodes)
	if !approxEqual(nom[0].VoltageFactorC, 1.0, 1e-12) {
		t.Fatalf("override c = %v, want 1.0", nom[0].VoltageFactorC)
	}
	if want := 11000 / math.Sqrt(3); !approxEqual(*nom[0].FaultCurrentAmps, want, 1e-6) {
		t.Fatalf("I\"k with c=1.0 override = %.1f A, want %.1f A", *nom[0].FaultCurrentAmps, want)
	}
}
