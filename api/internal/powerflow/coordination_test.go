package powerflow

import "testing"

// coordination_test.go -- validation of the protection-coordination VERDICT
// logic (gradePairs): given each device's operate time at the fault, it is the
// safety-critical decision "do these devices trip in the right order, with enough
// margin?". The network resolution and TCC-curve loading above it are DB-coupled
// (an integration-test concern for the DB-backed CI job); gradePairs itself is a
// pure kernel, so it is pinned here against hand calculations.
//
// The rule: order devices by path position (Order 0 = nearest the fault), take
// each adjacent (downstream, upstream) pair, margin = t_upstream − t_downstream,
// coordinated when margin ≥ CTI; overall is the AND of every graded pair, and nil
// when fewer than two devices actually operate (nothing to grade -- reporting
// false would wrongly imply a failure).

func opDev(class, id string, order int, t float64) coordinationDevice {
	return coordinationDevice{FeatureClass: class, FeatureID: id, Order: order, OperateTimeS: &t}
}

func nonOpDev(class, id string, order int) coordinationDevice {
	return coordinationDevice{FeatureClass: class, FeatureID: id, Order: order} // OperateTimeS nil
}

// TestGradePairsCoordinated is the Feeder 7 example from the Switching &
// Protection guide: fuse (nearest the fault, 0.05 s) → recloser (0.45 s) →
// breaker (1.49 s), CTI 0.3 s. Margins 0.40 s and 1.04 s, both clear -- coordinated.
func TestGradePairsCoordinated(t *testing.T) {
	devs := []coordinationDevice{
		opDev("MV_FUSE", "f", 0, 0.05),
		opDev("MV_RECLOSER", "r", 1, 0.45),
		opDev("MV_BREAKER", "b", 2, 1.49),
	}
	pairs, overall := gradePairs(devs, 0.3)
	if len(pairs) != 2 {
		t.Fatalf("pairs = %d, want 2", len(pairs))
	}
	if !approxEqual(*pairs[0].MarginS, 0.40, 1e-9) { // 0.45 − 0.05
		t.Fatalf("pair0 margin = %v, want 0.40", *pairs[0].MarginS)
	}
	if !approxEqual(*pairs[1].MarginS, 1.04, 1e-9) { // 1.49 − 0.45
		t.Fatalf("pair1 margin = %v, want 1.04", *pairs[1].MarginS)
	}
	for i, p := range pairs {
		if p.Coordinated == nil || !*p.Coordinated {
			t.Fatalf("pair%d should be coordinated", i)
		}
	}
	if overall == nil || !*overall {
		t.Fatalf("overall = %v, want coordinated", overall)
	}
}

// TestGradePairsMiscoordination: a slow fuse collapses its margin to the recloser
// below CTI. That one bad pair makes overall false, even though the upstream pair
// still clears.
func TestGradePairsMiscoordination(t *testing.T) {
	devs := []coordinationDevice{
		opDev("MV_FUSE", "f", 0, 0.30), // too slow
		opDev("MV_RECLOSER", "r", 1, 0.45),
		opDev("MV_BREAKER", "b", 2, 1.49),
	}
	pairs, overall := gradePairs(devs, 0.3)
	if !approxEqual(*pairs[0].MarginS, 0.15, 1e-9) || (pairs[0].Coordinated != nil && *pairs[0].Coordinated) {
		t.Fatalf("pair0 margin %v should miscoordinate (< 0.3)", *pairs[0].MarginS)
	}
	if pairs[1].Coordinated == nil || !*pairs[1].Coordinated {
		t.Fatalf("pair1 (margin 1.04) should still coordinate")
	}
	if overall == nil || *overall {
		t.Fatalf("overall = %v, want FALSE (one pair miscoordinates)", overall)
	}
}

// TestGradePairsExactMargin: margin exactly equal to CTI counts as coordinated
// (the check is ≥, not >).
func TestGradePairsExactMargin(t *testing.T) {
	devs := []coordinationDevice{
		opDev("MV_FUSE", "f", 0, 0.10),
		opDev("MV_BREAKER", "b", 1, 0.40), // margin exactly 0.30
	}
	_, overall := gradePairs(devs, 0.3)
	if overall == nil || !*overall {
		t.Fatalf("margin == CTI must coordinate (≥), got %v", overall)
	}
}

// TestGradePairsOrdering: devices supplied out of path order are graded by Order
// (0 = nearest the fault), so the first pair is the nearest-fault adjacency.
func TestGradePairsOrdering(t *testing.T) {
	devs := []coordinationDevice{
		opDev("MV_BREAKER", "b", 2, 1.49),
		opDev("MV_FUSE", "f", 0, 0.05),
		opDev("MV_RECLOSER", "r", 1, 0.45),
	}
	pairs, overall := gradePairs(devs, 0.3)
	if len(pairs) != 2 || pairs[0].Downstream.FeatureID != "f" || pairs[0].Upstream.FeatureID != "r" {
		t.Fatalf("pair0 = %s→%s, want f→r (nearest-fault adjacency)", pairs[0].Downstream.FeatureID, pairs[0].Upstream.FeatureID)
	}
	if overall == nil || !*overall {
		t.Fatalf("overall = %v, want coordinated", overall)
	}
}

// TestGradePairsNonOperatingExcluded: a device with no operate time (unresolved
// settings, or through-current at/below pickup) is skipped, so the two devices
// that DO operate grade directly past it.
func TestGradePairsNonOperatingExcluded(t *testing.T) {
	devs := []coordinationDevice{
		opDev("MV_FUSE", "f", 0, 0.05),
		nonOpDev("MV_SWITCH", "s", 1), // doesn't operate
		opDev("MV_BREAKER", "b", 2, 1.49),
	}
	pairs, overall := gradePairs(devs, 0.3)
	if len(pairs) != 1 || pairs[0].Downstream.FeatureID != "f" || pairs[0].Upstream.FeatureID != "b" {
		t.Fatalf("want a single f→b pair, got %d pairs", len(pairs))
	}
	if overall == nil || !*overall {
		t.Fatalf("overall = %v, want coordinated", overall)
	}
}

// TestGradePairsTooFewToGrade: fewer than two operating devices means nothing to
// grade -- overall is nil (NOT false, which would wrongly imply a failure).
func TestGradePairsTooFewToGrade(t *testing.T) {
	if _, overall := gradePairs([]coordinationDevice{opDev("MV_FUSE", "f", 0, 0.05)}, 0.3); overall != nil {
		t.Fatalf("one device: overall = %v, want nil (nothing to grade)", *overall)
	}
	if _, overall := gradePairs(nil, 0.3); overall != nil {
		t.Fatalf("no devices: overall = %v, want nil", *overall)
	}
}
