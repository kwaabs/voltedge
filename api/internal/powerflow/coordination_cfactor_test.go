package powerflow

import (
	"math"
	"testing"
)

// coordination_cfactor_test.go is the provenance for the Switching & Protection
// guide's Feeder 7 grading numbers at BOTH the illustrative c = 1.0 fault and the
// engine-default IEC c_max (1.10 at MV). The guide's §7.1 worked example was
// authored at c = 1.0 (I = 3,913 A); since /analysis/coordination defaults to
// c_max, a live run sees I ≈ 4,304 A and every operate time moves. Rather than
// hand-put recomputed times into a safety guide, this test derives them through the
// REAL curve code: the recloser's slow curve is read by the engine's own
// interpLogLog over the guide's published illustrative points, and the relay is the
// IEC Standard Inverse characteristic the guide quotes. It pins the c = 1.0 numbers
// already in the guide (so the guide is self-consistent with the engine) AND the
// c_max numbers the guide's c-factor callout promises, so §7.1's "the magnitudes
// move" is shown, not asserted.

const (
	// feeder7FaultC1 is the §7.1 illustrative bolted fault at the TX-220 primary at
	// c = 1.0; feeder7FaultCMax is the same fault at the engine-default c_max = 1.10.
	feeder7FaultC1   = 3913.0
	feeder7FaultCMax = feeder7FaultC1 * 1.10 // 4304.3 A
)

// recloser7SlowCurve is the guide's illustrative REC-7 slow curve (§6.3 / §7.1 --
// "not shipped", invented for teaching), an ampere-quantity curve: the engine reads
// it at the absolute fault current.
var recloser7SlowCurve = []curvePoint{
	{current: 800, timeS: 8.0},
	{current: 1000, timeS: 3.5},
	{current: 2000, timeS: 1.0},
	{current: 4000, timeS: 0.45},
	{current: 8000, timeS: 0.30},
}

// iecStandardInverse is the IEC Standard Inverse (IEC_SI) IDMT characteristic the
// relay R-7 runs: t = TD · 0.14 / (M^0.02 − 1), M = I/pickup. The engine grades it
// off seeded curve points generated from this form; the guide quotes the form, and
// using it here keeps the test independent of the database while reproducing the
// guide's figure.
func iecStandardInverse(mult, td float64) float64 {
	return td * 0.14 / (math.Pow(mult, 0.02) - 1)
}

// TestFeeder7RecloserCFactor reads the REC-7 slow curve through the engine's real
// interpLogLog at both fault levels and pins the guide's c = 1.0 figure (0.462 s)
// and the c_max figure (0.431 s).
func TestFeeder7RecloserCFactor(t *testing.T) {
	t1, ex1 := interpLogLog(recloser7SlowCurve, feeder7FaultC1)
	if ex1 {
		t.Errorf("REC-7 at %.0f A flagged extrapolated -- should be inside the curve", feeder7FaultC1)
	}
	if math.Abs(t1-0.462) > 0.003 {
		t.Errorf("REC-7 @ c=1.0 (%.0f A) = %.4f s, want ~0.462 (guide §7.1)", feeder7FaultC1, t1)
	}

	tMax, exMax := interpLogLog(recloser7SlowCurve, feeder7FaultCMax)
	if exMax {
		t.Errorf("REC-7 at %.0f A flagged extrapolated -- should be inside the curve", feeder7FaultCMax)
	}
	if math.Abs(tMax-0.431) > 0.003 {
		t.Errorf("REC-7 @ c_max (%.0f A) = %.4f s, want ~0.431", feeder7FaultCMax, tMax)
	}

	// c_max raises the current, so the device reads FASTER (further down its curve).
	if tMax >= t1 {
		t.Errorf("REC-7 at the higher c_max current (%.4f s) is not faster than at c=1.0 (%.4f s)", tMax, t1)
	}
	t.Logf("REC-7 slow curve: %.0f A -> %.4f s (c=1.0), %.0f A -> %.4f s (c_max)", feeder7FaultC1, t1, feeder7FaultCMax, tMax)
}

// TestFeeder7RelayCFactor pins the CB-7 / R-7 IEC_SI time (pickup 400 A, TD 0.5) at
// both fault levels: the guide's ~1.50 s at c = 1.0 and 1.438 s at c_max.
func TestFeeder7RelayCFactor(t *testing.T) {
	const pickup, td = 400.0, 0.5

	t1 := iecStandardInverse(feeder7FaultC1/pickup, td)
	if math.Abs(t1-1.500) > 0.01 {
		t.Errorf("CB-7/R-7 @ c=1.0 (M=%.3f) = %.4f s, want ~1.50 (guide §7.1)", feeder7FaultC1/pickup, t1)
	}

	tMax := iecStandardInverse(feeder7FaultCMax/pickup, td)
	if math.Abs(tMax-1.438) > 0.01 {
		t.Errorf("CB-7/R-7 @ c_max (M=%.3f) = %.4f s, want ~1.438", feeder7FaultCMax/pickup, tMax)
	}
	if tMax >= t1 {
		t.Errorf("CB-7 at the higher c_max current (%.4f s) is not faster than at c=1.0 (%.4f s)", tMax, t1)
	}
	t.Logf("CB-7/R-7 IEC_SI TD0.5: %.0f A -> %.4f s (c=1.0), %.0f A -> %.4f s (c_max)", feeder7FaultC1, t1, feeder7FaultCMax, tMax)
}

// TestFeeder7MarginsCFactor confirms the grading VERDICT is unchanged at c_max:
// REC-7 (backup) − FSW-12 (primary) and CB-7 (backup) − REC-7 both clear CTI 0.3 s,
// so the path stays coordinated -- the "method and ordering unchanged, magnitudes
// move" claim in the guide's §7.1 c-factor callout, demonstrated.
func TestFeeder7MarginsCFactor(t *testing.T) {
	const cti = 0.3
	// FSW-12 (100 T fuse) is deep in its fast region at these currents (~0.02 s);
	// its exact value barely moves the margin, so the fuse->recloser margin is
	// dominated by the recloser. Use the recloser and relay read through the real
	// curve code above.
	recMax, _ := interpLogLog(recloser7SlowCurve, feeder7FaultCMax)
	relayMax := iecStandardInverse(feeder7FaultCMax/400.0, 0.5)

	const fuseMax = 0.02 // 100 T total-clear, ~0.02 s past its 4,000 A point
	fuseToRec := recMax - fuseMax
	recToRelay := relayMax - recMax

	if fuseToRec < cti {
		t.Errorf("FSW-12 -> REC-7 margin at c_max = %.3f s, below CTI %.2f", fuseToRec, cti)
	}
	if recToRelay < cti {
		t.Errorf("REC-7 -> CB-7 margin at c_max = %.3f s, below CTI %.2f", recToRelay, cti)
	}
	t.Logf("c_max margins: FSW-12->REC-7 %.3f s, REC-7->CB-7 %.3f s (both >= CTI %.2f) -> coordinated", fuseToRec, recToRelay, cti)
}
