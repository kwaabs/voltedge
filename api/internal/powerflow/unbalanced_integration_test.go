package powerflow

import (
	"math"
	"math/cmplx"
	"testing"

	"github.com/kwaabs/voltedge/api/internal/metadata"
)

// unbalanced_integration_test.go validates the Tier 2 coupled unbalanced engine
// against the IEEE 13-node test feeder's canonical line data (Kersting,
// "Distribution System Modeling and Analysis", Configuration 601 -- the standard
// three-phase overhead main). Two independent checks:
//
//  1. TestCarsonConfig601 feeds Config 601's actual conductor geometry (phase +
//     neutral positions, GMRs, resistances) through the REAL buildCarsonMatrix
//     and compares the computed 3x3 phase-impedance matrix to Kersting's PUBLISHED
//     Z601 (Ω/mile). This is the genuine benchmark: it exercises Carson's
//     equations and the neutral Kron reduction against a number the industry
//     agrees on.
//  2. TestCoupledSweepConfig601Drop feeds the published Z601 straight into the
//     REAL coupledForwardSweep and confirms it applies the full matrix drop
//     ΔV = Z·I (mutual off-diagonal terms included), exactly, for an unbalanced
//     load -- and that dropping to the diagonal alone would give a different
//     answer, so the coupling is provably exercised.
//
// A full published-node-voltage match for the whole IEEE 13 feeder is NOT
// attempted here: that solution includes a voltage regulator, an in-line
// transformer, shunt capacitors and line charging admittance, which this planning
// engine documents it does not model (see unbalanced_coupled.go HONEST LIMITS).
// Those would dominate any discrepancy and obscure what these tests isolate -- the
// line-impedance build and the coupled drop, which are the Tier 2 core.

func ptrF(f float64) *float64 { return &f }

// feetToM / mileM convert Kersting's imperial Config 601 inputs to the metric
// units buildCarsonMatrix expects (positions/GMR in metres, length in metres).
const (
	feetToM = 0.3048
	mileM   = 1609.344
	ohmMile = 1.609344 // Ω/km → Ω/mile, and the per-km→per-mile length scale
)

// z601Published is Kersting's published Configuration 601 phase-impedance matrix
// in Ω/mile (symmetric). The IEEE 13-node feeder's three-phase main uses it.
var z601Published = [3][3]complex128{
	{complex(0.3465, 1.0179), complex(0.1560, 0.5017), complex(0.1580, 0.4236)},
	{complex(0.1560, 0.5017), complex(0.3375, 1.0478), complex(0.1535, 0.3849)},
	{complex(0.1580, 0.4236), complex(0.1535, 0.3849), complex(0.3414, 1.0348)},
}

// TestCarsonConfig601 reproduces the published Z601 from Config 601's geometry
// via the real Carson build. Phase: 556,500 26/7 ACSR (GMR 0.0313 ft, 0.1859
// Ω/mile) -- the ACSR "Dove" conductor the IEEE 13-node Config 601 main actually
// uses. Neutral: 4/0 6/1 ACSR (GMR 0.00814 ft, 0.592 Ω/mile). Config 601 is
// phased B-A-C-N on the crossarm, so by position (ft): B(0,29) A(2.5,29) C(7,29)
// N(4,25) -- i.e. phase A sits at x=2.5, phase B at x=0. Built at 60 Hz / 100 Ω·m
// over one mile, so the result is directly in Ω/mile.
func TestCarsonConfig601(t *testing.T) {
	spec := &metadata.MaterialSpec{
		ResistanceOhmPerKm:        ptrF(0.1859 / ohmMile),
		GmrPhaseMm:                ptrF(0.0313 * feetToM * 1000),
		ResistanceNeutralOhmPerKm: ptrF(0.592 / ohmMile),
		GmrNeutralMm:              ptrF(0.00814 * feetToM * 1000),
		PosAX:                     ptrF(2.5 * feetToM),
		PosAY:                     ptrF(29 * feetToM),
		PosBX:                     ptrF(0 * feetToM),
		PosBY:                     ptrF(29 * feetToM),
		PosCX:                     ptrF(7 * feetToM),
		PosCY:                     ptrF(29 * feetToM),
		PosNX:                     ptrF(4 * feetToM),
		PosNY:                     ptrF(25 * feetToM),
	}

	m := buildCarsonMatrix(spec, mileM, 60, 100)
	if !m.resolved || !m.hasNeutral {
		t.Fatalf("Config 601 build did not resolve with a neutral: %+v", m)
	}

	// This asserts the WHOLE matrix -- self (diagonal) AND mutual (off-diagonal) --
	// against the published Z601 to 4 decimals (0.01 Ω/mile tol). It exercises the
	// entire coupled build: earth return (De, rEarth), the ln coefficient, the self
	// GMR term, the mutual spacing, and the neutral Kron reduction.
	//
	// The self-term is asserted now because the test finally feeds Config 601's
	// ACTUAL phase conductor, 556,500 26/7 ACSR (r = 0.1859 Ω/mile, GMR 0.0313 ft).
	// An earlier revision mistakenly used 336,400 ACSR (r = 0.306, GMR 0.0244): the
	// mutual terms depend only on spacing + the neutral, so they matched regardless
	// and MASKED the wrong phase conductor, while the self terms came out ~0.467+
	// j1.048 instead of the published 0.3465+j1.0179 -- and that mismatch was wrongly
	// read as an unreconcilable Kron discrepancy needing an OpenDSS cross-check. With
	// the right conductor there is nothing to reconcile: the modified-Carson + Kron
	// self-impedance reproduces the published value directly. For Z_aa, the primitive
	// self is r + rEarth = 0.1859 + 0.0953 = 0.2812 (real) and the Kron correction
	// ẑ_an²/ẑ_nn = −0.0654 + j0.3653 (geometry + neutral only), giving
	// 0.2812 − (−0.0654) = 0.3466 + j1.0178 -- the published 0.3465 + j1.0179 to 4 dp.
	const tol = 0.01
	for i := 0; i < phaseCount; i++ {
		for j := 0; j < phaseCount; j++ {
			got, want := m.z[i][j], z601Published[i][j]
			dR := real(got) - real(want)
			dX := imag(got) - imag(want)
			t.Logf("Z[%d][%d] got %.4f%+.4fi  want %.4f%+.4fi  (ΔR %+.4f ΔX %+.4f)",
				i, j, real(got), imag(got), real(want), imag(want), dR, dX)
			kind := "mutual"
			if i == j {
				kind = "self"
			}
			if math.Abs(dR) > tol || math.Abs(dX) > tol {
				t.Errorf("%s Z[%d][%d] = %.4f%+.4fi, want %.4f%+.4fi (Δ exceeds %.3f Ω/mile)",
					kind, i, j, real(got), imag(got), real(want), imag(want), tol)
			}
		}
	}
}

// TestCoupledSweepConfig601Drop drives the real coupledForwardSweep over
// source --[Config 601, 1 mile]--> unbalanced spot load at 2.4 kV (line-to-
// neutral), and checks the line node's per-phase complex voltage against an
// independent ΔV = Z·I computed directly from the published matrix. Because the
// parent (source) voltage is the fixed nominal set, the branch current
// I = conj(S/V_src) is deterministic, so the expected drop is a single exact
// matrix-vector product -- no convergence slack.
func TestCoupledSweepConfig601Drop(t *testing.T) {
	src := node{classID: "src", featureID: "s"}
	line := node{classID: "edge", featureID: "l"}
	tr := &tree{
		order:    []node{src, line},
		parent:   map[node]node{line: src},
		children: map[node][]node{src: {line}},
	}
	states := map[node]*nodeState{src: {}, line: {}}

	full := phaseMask{true, true, true}
	// Unbalanced spot load carried by the line (kW / kVAR per phase).
	cumKW := [phaseCount]float64{120, 100, 80}
	cumKVAR := [phaseCount]float64{60, 50, 40}
	pstates := map[node]*phaseState{
		src:  {mask: full},
		line: {mask: full, cumKW: cumKW, cumKVAR: cumKVAR},
	}
	carsons := map[node]carsonMatrix{line: {z: z601Published, resolved: true}}

	const vn = 2400
	coupledForwardSweep(tr, states, pstates, carsons, src, vn, 0, 0)

	// Independent reference: I_q = conj(S_q / V_src,q); ΔV_p = Σ_q Z[p][q]·I_q.
	vsrc := nominalPhasors(vn)
	var iBr [phaseCount]complex128
	for q := 0; q < phaseCount; q++ {
		s := complex(cumKW[q]*1000, cumKVAR[q]*1000)
		iBr[q] = cmplx.Conj(s / vsrc[q])
	}
	var wantV, diagV [phaseCount]complex128
	for p := 0; p < phaseCount; p++ {
		var drop complex128
		for q := 0; q < phaseCount; q++ {
			drop += z601Published[p][q] * iBr[q]
		}
		wantV[p] = vsrc[p] - drop
		diagV[p] = vsrc[p] - z601Published[p][p]*iBr[p] // diagonal-only (no coupling)
	}

	got := pstates[line].voltageC
	for p := 0; p < phaseCount; p++ {
		if d := cmplx.Abs(got[p] - wantV[p]); d > 1e-6 {
			t.Errorf("phase %s: voltageC = %v, want %v (|Δ| = %g)", phaseLabels[p], got[p], wantV[p], d)
		}
		// Mutual coupling must actually be applied: the coupled answer must differ
		// from the diagonal-only drop by more than a volt on these currents.
		if d := cmplx.Abs(got[p] - diagV[p]); d < 1.0 {
			t.Errorf("phase %s: coupled result %v is within %g V of the diagonal-only drop %v -- mutual terms not applied",
				phaseLabels[p], got[p], d, diagV[p])
		}
	}
}
