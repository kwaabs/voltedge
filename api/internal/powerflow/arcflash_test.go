package powerflow

import (
	"math"
	"testing"
)

// arcflash_test.go validates the IEEE 1584-2002 arc-flash KERNELS -- arcing
// current (eq 1/2), incident energy (eq 3-4) and the arc-flash boundary (eq 5) --
// against the standard's canonical worked example. These are the genuinely new
// physics in arcflash.go (the rest of the study stands on the already-validated
// fault and coordination engines). The kernels are pure functions, so this drives
// them directly with the published inputs and asserts the published outputs, which
// catches a transposed coefficient, a wrong Cf, or a units slip in the (610/D)^x
// referral -- none of which the surrounding DB wiring would reveal.
//
// IMPORTANT SCOPE. This validates the empirical MODEL's implementation, not the
// end-to-end hazard study. The study remains Experimental because its governing
// input -- the arc duration (clearing time) -- comes from the protection model and
// real arc behaviour, and a result is never a basis for PPE selection or an
// energised-work permit. See the arc-flash note in the Analysis Validation Report.

// TestArcFlashIEEE1584Example is the standard's canonical low-voltage worked
// example: 480 V, 20 kA bolted, enclosed (box) switchgear, grounded, 32 mm gap,
// 457 mm (18 in) working distance, x = 1.473, arc duration 0.2 s. The published
// results are Ia ~ 11.2 kA, incident energy ~ 7.29 cal/cm2, and an arc-flash
// boundary ~ 1556 mm.
func TestArcFlashIEEE1584Example(t *testing.T) {
	g := arcGeometry{gapMM: 32, wdMM: 457, x: 1.473, box: true, grounded: true}
	const (
		ibfA = 20000.0 // 20 kA bolted three-phase
		vV   = 480.0
		tS   = 0.2 // the normalizing arc duration, so (t/0.2) = 1
	)

	ia := arcingCurrentAmps(ibfA, vV, g)
	// Published arcing current ~ 11.2 kA (eq 1, enclosed, V < 1 kV).
	if ia < 11000 || ia > 11500 {
		t.Errorf("arcing current = %.0f A, want ~11,200 A (IEEE 1584-2002 eq 1)", ia)
	}

	eCal, afbMM := incidentEnergyAndBoundary(ia, vV, tS, g)
	// Published incident energy ~ 7.29 cal/cm2 at the 457 mm working distance.
	if math.Abs(eCal-7.29) > 0.1 {
		t.Errorf("incident energy = %.3f cal/cm2, want ~7.29 (IEEE 1584-2002 eq 3-4)", eCal)
	}
	// Published arc-flash boundary ~ 1556 mm (eq 5, where E falls to 1.2 cal/cm2).
	if math.Abs(afbMM-1556) > 15 {
		t.Errorf("arc-flash boundary = %.0f mm, want ~1556 (IEEE 1584-2002 eq 5)", afbMM)
	}

	// The example lands in NFPA 70E Category 2 (4 < E <= 8 cal/cm2).
	if cat, _ := ppeCategory(eCal); cat != 2 {
		t.Errorf("PPE category = %d, want 2 at %.2f cal/cm2", cat, eCal)
	}

	t.Logf("IEEE 1584-2002 LV example (480 V, 20 kA, box, grounded): Ia = %.0f A, E = %.3f cal/cm2, AFB = %.0f mm",
		ia, eCal, afbMM)
}

// TestArcFlashArcingCurrentMV pins eq 2 (V >= 1 kV), where the arcing current is a
// simple function of the bolted current alone (geometry-independent): for 20 kA
// bolted, lgIa = 0.00402 + 0.983*log10(20) gives ~19.2 kA. At medium voltage the
// arc is nearly as large as the bolted fault, the known behaviour the equation
// encodes.
func TestArcFlashArcingCurrentMV(t *testing.T) {
	g := arcGeometry{gapMM: 153, wdMM: 910, x: 0.973, box: true} // 15 kV switchgear typical
	ia := arcingCurrentAmps(20000, 11000, g)
	if ia < 19000 || ia > 19400 {
		t.Errorf("MV arcing current = %.0f A, want ~19,200 A (IEEE 1584-2002 eq 2)", ia)
	}
	// Geometry-independence: eq 2 ignores the gap, so changing it must not move Ia.
	g2 := g
	g2.gapMM = 40
	if ia2 := arcingCurrentAmps(20000, 11000, g2); math.Abs(ia2-ia) > 1e-6 {
		t.Errorf("eq 2 arcing current moved with the gap (%.3f vs %.3f A) -- it must not above 1 kV", ia2, ia)
	}
	t.Logf("IEEE 1584-2002 MV arcing current (11 kV, 20 kA bolted): Ia = %.0f A", ia)
}

// TestArcFlashPPECategory pins the NFPA 70E category boundaries the hazard label
// rests on, including the ">40 cal/cm2 = dangerous, no live work" (-1) case.
func TestArcFlashPPECategory(t *testing.T) {
	cases := []struct {
		eCal float64
		want int
	}{
		{0.5, 0},    // minimal
		{1.2, 1},    // the 1.2 threshold itself is Category 1
		{4.0, 1},    // boundary: <= 4 is Cat 1
		{4.01, 2},   // just over -> Cat 2
		{8.0, 2},    // <= 8 is Cat 2
		{25.0, 3},   // <= 25 is Cat 3
		{40.0, 4},   // <= 40 is Cat 4
		{40.01, -1}, // dangerous: no live work
	}
	for _, c := range cases {
		if cat, _ := ppeCategory(c.eCal); cat != c.want {
			t.Errorf("ppeCategory(%.2f) = %d, want %d", c.eCal, cat, c.want)
		}
	}
}
