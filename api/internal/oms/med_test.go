package oms

import (
	"math"
	"testing"
)

// med_test.go validates the IEEE 1366 2.5-beta major-event-day kernel (med.go) --
// the statistical content of the MED normalisation -- against a hand-worked
// example. The kernel is pure, so this runs without a database; the daily-SAIDI
// query that feeds it is exercised by the OMS integration path.

// TestMajorEventDayThreshold works the 2.5-beta math by hand: 20 ordinary days of
// SAIDI 1.0 and one storm day of 50.0. Only positive days enter the log-stats, so
// alpha = mean(ln) = ln(50)/21 = 0.18629, beta = sample std dev = 0.85367, and
// T_MED = exp(alpha + 2.5*beta) = exp(2.32047) = 10.18. The storm day (50) is above
// it; an ordinary day (1) is below.
func TestMajorEventDayThreshold(t *testing.T) {
	daily := make([]float64, 0, 21)
	for i := 0; i < 20; i++ {
		daily = append(daily, 1.0)
	}
	daily = append(daily, 50.0)

	threshold, alpha, beta, positiveDays, ok := majorEventDayThreshold(daily)
	if !ok {
		t.Fatalf("threshold not computed, want ok with 21 positive days")
	}
	if positiveDays != 21 {
		t.Errorf("positive days = %d, want 21", positiveDays)
	}
	if math.Abs(alpha-0.186287) > 1e-4 {
		t.Errorf("alpha = %.6f, want ~0.186287", alpha)
	}
	if math.Abs(beta-0.853673) > 1e-4 {
		t.Errorf("beta = %.6f, want ~0.853673", beta)
	}
	if math.Abs(threshold-10.180) > 0.02 {
		t.Errorf("T_MED = %.4f, want ~10.18", threshold)
	}
	// The classification the threshold exists to make.
	if !(50.0 > threshold) {
		t.Errorf("storm day SAIDI 50 not above T_MED %.3f -- should be a major event", threshold)
	}
	if 1.0 > threshold {
		t.Errorf("ordinary day SAIDI 1 above T_MED %.3f -- should be normal", threshold)
	}
	t.Logf("2.5β: alpha=%.5f beta=%.5f T_MED=%.4f over %d positive days", alpha, beta, threshold, positiveDays)
}

// TestMajorEventDayThresholdTooFewDays: the threshold is undefined with fewer than
// two positive days (the sample std dev needs two points), so ok is false and the
// caller excludes nothing.
func TestMajorEventDayThresholdTooFewDays(t *testing.T) {
	if _, _, _, pos, ok := majorEventDayThreshold([]float64{0, 0, 0}); ok || pos != 0 {
		t.Errorf("all-zero days: ok=%v positive=%d, want ok=false positive=0", ok, pos)
	}
	if _, _, _, pos, ok := majorEventDayThreshold([]float64{5.0}); ok || pos != 1 {
		t.Errorf("single positive day: ok=%v positive=%d, want ok=false positive=1", ok, pos)
	}
}

// TestComputeMEDExcludesStorm runs the full normalisation over daily rows: 20 days
// of 100 customer-minutes (1 customer each) and one storm of 5,000 customer-minutes
// (100 customers), on a 100-customer system. Raw SAIDI = 7000/100 = 70; the storm
// is a major event day, so normalised SAIDI drops to (7000-5000)/100 = 20 and
// normalised SAIFI to (120-100)/100 = 0.20.
func TestComputeMEDExcludesStorm(t *testing.T) {
	daily := make([]dailyReliability, 0, 21)
	for i := 0; i < 20; i++ {
		daily = append(daily, dailyReliability{customerMinutes: 100, customersStarted: 1})
	}
	daily = append(daily, dailyReliability{customerMinutes: 5000, customersStarted: 100})

	med := computeMED(daily, 100)
	if !med.Applicable {
		t.Fatalf("MED not applicable, want applicable with 21 outage days")
	}
	if med.MajorEventDays != 1 {
		t.Errorf("major event days = %d, want 1 (the storm)", med.MajorEventDays)
	}
	if math.Abs(med.SAIDINorm-20.0) > 1e-9 {
		t.Errorf("normalised SAIDI = %.6f, want 20.0 (raw 70 minus the excluded storm)", med.SAIDINorm)
	}
	if math.Abs(med.SAIFINorm-0.20) > 1e-9 {
		t.Errorf("normalised SAIFI = %.6f, want 0.20", med.SAIFINorm)
	}
	if med.ThresholdSAIDI < 10.1 || med.ThresholdSAIDI > 10.3 {
		t.Errorf("T_MED = %.4f, want ~10.18", med.ThresholdSAIDI)
	}
}

// TestComputeMEDNoMajorDays: uniform days have zero log-variance, so T_MED equals
// the daily value and the strict ">" excludes nothing -- normalised == raw.
func TestComputeMEDNoMajorDays(t *testing.T) {
	daily := make([]dailyReliability, 0, 10)
	for i := 0; i < 10; i++ {
		daily = append(daily, dailyReliability{customerMinutes: 100, customersStarted: 1})
	}
	med := computeMED(daily, 100)
	if !med.Applicable {
		t.Fatalf("MED not applicable, want applicable")
	}
	if med.MajorEventDays != 0 {
		t.Errorf("major event days = %d, want 0 (uniform days)", med.MajorEventDays)
	}
	if math.Abs(med.SAIDINorm-10.0) > 1e-9 { // 10 days × 100 cust-min / 100 = 10
		t.Errorf("normalised SAIDI = %.6f, want 10.0 (= raw, nothing excluded)", med.SAIDINorm)
	}
}

// TestComputeMEDTooFewDays: with one outage day the threshold is undefined, so
// nothing is excluded and the normalised figures equal the raw ones.
func TestComputeMEDTooFewDays(t *testing.T) {
	med := computeMED([]dailyReliability{{customerMinutes: 100, customersStarted: 1}}, 100)
	if med.Applicable {
		t.Errorf("MED applicable with one outage day, want not applicable")
	}
	if med.MajorEventDays != 0 {
		t.Errorf("major event days = %d, want 0", med.MajorEventDays)
	}
	if math.Abs(med.SAIDINorm-1.0) > 1e-9 { // 100 cust-min / 100 = 1.0 = raw
		t.Errorf("normalised SAIDI = %.6f, want 1.0 (= raw)", med.SAIDINorm)
	}
}
