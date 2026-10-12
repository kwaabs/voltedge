package powerflow

import (
	"math/cmplx"
	"testing"
)

// unbalanced_composition_test.go extends the Tier-2 coupled-sweep validation from
// a single line (unbalanced_integration_test.go) to a TWO-SECTION series feeder,
// so the COMPOSITION is exercised: the forward pass must carry each section's drop
// off its PARENT's (already-updated) per-phase voltage, not the source, and the
// cumulative result must match an independent two-step ΔV = Z·I calculation. A
// pile of correct single-line formulas composed wrong still gives wrong answers;
// this pins that they compose right. Reuses Kersting's published Z601 (IEEE 13
// Config 601), so no new reference data is introduced.

func TestCoupledSweepTwoSections(t *testing.T) {
	src := node{classID: "src", featureID: "s"}
	l1 := node{classID: "edge", featureID: "l1"}
	l2 := node{classID: "edge", featureID: "l2"}
	tr := &tree{
		order:    []node{src, l1, l2},
		parent:   map[node]node{l1: src, l2: l1},
		children: map[node][]node{src: {l1}, l1: {l2}},
	}
	states := map[node]*nodeState{src: {}, l1: {}, l2: {}}

	full := phaseMask{true, true, true}
	// Both sections carry the same unbalanced load (it sits below l2, so the series
	// sections l1 and l2 each carry it -- the accumulation a backward pass produces,
	// set here directly).
	cumKW := [phaseCount]float64{120, 100, 80}
	cumKVAR := [phaseCount]float64{60, 50, 40}
	pstates := map[node]*phaseState{
		src: {mask: full},
		l1:  {mask: full, cumKW: cumKW, cumKVAR: cumKVAR},
		l2:  {mask: full, cumKW: cumKW, cumKVAR: cumKVAR},
	}
	carsons := map[node]carsonMatrix{
		l1: {z: z601Published, resolved: true},
		l2: {z: z601Published, resolved: true},
	}

	const vn = 2400
	coupledForwardSweep(tr, states, pstates, carsons, src, vn, 0, 0)

	// Independent reference, replicating the engine's model exactly: each section's
	// current is conj(S / parent_voltage), and V_node = V_parent − Z·I.
	cur := func(vref [phaseCount]complex128) [phaseCount]complex128 {
		var i [phaseCount]complex128
		for q := 0; q < phaseCount; q++ {
			s := complex(cumKW[q]*1000, cumKVAR[q]*1000)
			i[q] = cmplx.Conj(s / vref[q])
		}
		return i
	}
	drop := func(vparent, i [phaseCount]complex128) [phaseCount]complex128 {
		var v [phaseCount]complex128
		for p := 0; p < phaseCount; p++ {
			var d complex128
			for q := 0; q < phaseCount; q++ {
				d += z601Published[p][q] * i[q]
			}
			v[p] = vparent[p] - d
		}
		return v
	}

	vsrc := nominalPhasors(vn)
	wantV1 := drop(vsrc, cur(vsrc))
	wantV2 := drop(wantV1, cur(wantV1))

	for p := 0; p < phaseCount; p++ {
		if d := cmplx.Abs(pstates[l1].voltageC[p] - wantV1[p]); d > 1e-6 {
			t.Errorf("section 1, phase %s: got %v want %v (|Δ| = %g)", phaseLabels[p], pstates[l1].voltageC[p], wantV1[p], d)
		}
		if d := cmplx.Abs(pstates[l2].voltageC[p] - wantV2[p]); d > 1e-6 {
			t.Errorf("section 2, phase %s: got %v want %v (|Δ| = %g)", phaseLabels[p], pstates[l2].voltageC[p], wantV2[p], d)
		}
	}

	// Sanity: the second section must drop FURTHER than the first -- the drop is
	// cumulative, so |V2| < |V1| on every phase.
	for p := 0; p < phaseCount; p++ {
		if cmplx.Abs(pstates[l2].voltageC[p]) >= cmplx.Abs(pstates[l1].voltageC[p]) {
			t.Errorf("phase %s: |V2| = %g not below |V1| = %g -- second section added no drop",
				phaseLabels[p], cmplx.Abs(pstates[l2].voltageC[p]), cmplx.Abs(pstates[l1].voltageC[p]))
		}
	}
}
