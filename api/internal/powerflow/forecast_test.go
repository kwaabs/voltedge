package powerflow

import (
	"testing"
	"time"
)

// forecast_test.go pins the pure kernels the load-growth forecast (forecast.go)
// rests on -- the compound-growth multiplier and the first-overload-year extraction.
// Like sweep_test.go these run with no database; the assembled year-loop over a seeded
// network is validated in a later (DB-backed) slice. Reuses approxEqual from
// sweep_test.go (same package).

// TestGrowthFactor checks the compound multiplier (1 + pct/100)^year: year 0 is always
// unity, positive growth compounds, and a negative rate models decline.
func TestGrowthFactor(t *testing.T) {
	cases := []struct {
		name string
		pct  float64
		year int
		want float64
	}{
		{"year 0 is unity regardless of rate", 5, 0, 1.0},
		{"zero growth stays flat", 0, 10, 1.0},
		{"1.5%/yr over 1 year", 1.5, 1, 1.015},
		{"2%/yr compounds over 10 years", 2, 10, 1.2189944199947573},
		{"negative rate declines", -3, 2, 0.9409},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := growthFactor(c.pct, c.year)
			if !approxEqual(got, c.want, 1e-9) {
				t.Fatalf("growthFactor(%v, %d) = %v, want %v", c.pct, c.year, got, c.want)
			}
		})
	}
}

// TestGrowthFactorCompounds asserts the multiplicative identity a forecast relies on:
// scaling by year t then one more year equals scaling by year t+1.
func TestGrowthFactorCompounds(t *testing.T) {
	const pct = 2.5
	for year := 0; year < 20; year++ {
		step := growthFactor(pct, year) * (1 + pct/100)
		next := growthFactor(pct, year+1)
		if !approxEqual(step, next, 1e-9) {
			t.Fatalf("compounding broke at year %d: %v vs %v", year, step, next)
		}
	}
}

// TestEffectiveFactor checks the Slice-2 growth-rule precedence: when both a feeder
// and a class rate match, their factors MULTIPLY; a single match uses that rate; no
// match falls back to the blanket base rate. Year 0 is always unity.
func TestEffectiveFactor(t *testing.T) {
	const base = 1.5
	year := 10
	baseF := growthFactor(base, year)

	cases := []struct {
		name string
		rule growthRule
		want float64
	}{
		{"no override -> base", growthRule{}, baseF},
		{"feeder only", growthRule{hasFeeder: true, feederPct: 3}, growthFactor(3, year)},
		{"class only", growthRule{hasClass: true, classPct: 8}, growthFactor(8, year)},
		{"both multiply", growthRule{hasFeeder: true, feederPct: 3, hasClass: true, classPct: 8},
			growthFactor(3, year) * growthFactor(8, year)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := effectiveFactor(c.rule, base, year); !approxEqual(got, c.want, 1e-9) {
				t.Fatalf("effectiveFactor(%+v) = %v, want %v", c.rule, got, c.want)
			}
		})
	}

	// Year 0 is unity whatever the rule.
	for _, rule := range []growthRule{{}, {hasFeeder: true, feederPct: 9}, {hasFeeder: true, feederPct: 3, hasClass: true, classPct: 8}} {
		if got := effectiveFactor(rule, base, 0); !approxEqual(got, 1.0, 1e-12) {
			t.Fatalf("effectiveFactor at year 0 = %v, want 1 (rule %+v)", got, rule)
		}
	}
}

// TestFirstViolationYear checks the first-overload-year extraction: the earliest year
// strictly above the threshold, nil when nothing crosses, and that exactly-at-threshold
// does NOT count (the 100% line is a crossing only when exceeded, matching
// formatResults' `> 100`).
func TestFirstViolationYear(t *testing.T) {
	traj := []forecastPoint{
		{Year: 0, LoadingPct: 70},
		{Year: 1, LoadingPct: 85},
		{Year: 2, LoadingPct: 100}, // exactly at the limit -- not a violation
		{Year: 3, LoadingPct: 104}, // first real crossing
		{Year: 4, LoadingPct: 120},
	}
	got := firstViolationYear(traj, forecastViolation)
	if got == nil {
		t.Fatalf("expected a violation year, got nil")
	}
	if *got != 3 {
		t.Fatalf("firstViolationYear = %d, want 3", *got)
	}

	// Never crosses -> nil.
	safe := []forecastPoint{{Year: 0, LoadingPct: 40}, {Year: 1, LoadingPct: 60}, {Year: 2, LoadingPct: 99.9}}
	if got := firstViolationYear(safe, forecastViolation); got != nil {
		t.Fatalf("expected nil for a never-overloaded trajectory, got %d", *got)
	}

	// Empty trajectory -> nil.
	if got := firstViolationYear(nil, forecastViolation); got != nil {
		t.Fatalf("expected nil for an empty trajectory, got %d", *got)
	}
}

// TestYearFromDateAttr checks the date-to-year-offset kernel (Slice 3): a time.Time, a
// common date string, and a bare year all resolve to (dateYear - nowYear); absent or
// unparseable values return ok=false so the caller falls through to the next tier.
func TestYearFromDateAttr(t *testing.T) {
	const now = 2026
	cases := []struct {
		name   string
		v      any
		want   int
		wantOk bool
	}{
		{"time.Time", time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC), 4, true},
		{"ISO date string", "2029-06-01", 3, true},
		{"RFC3339 string", "2028-01-01T00:00:00Z", 2, true},
		{"bare year string", "2031", 5, true},
		{"past date -> negative (caller floors)", "2020-01-01", -6, true},
		{"nil -> not ok", nil, 0, false},
		{"empty -> not ok", "", 0, false},
		{"garbage -> not ok", "someday", 0, false},
		{"non-date type -> not ok", 42, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := yearFromDateAttr(c.v, now)
			if ok != c.wantOk || (ok && got != c.want) {
				t.Fatalf("yearFromDateAttr(%v) = (%d, %v), want (%d, %v)", c.v, got, ok, c.want, c.wantOk)
			}
		})
	}
}

// TestResolvePlannedYear checks the in-service-year precedence (Slice 3): the date
// field wins, then a per-asset override, then the scenario default; a past date (and a
// negative override/default) floors to 0.
func TestResolvePlannedYear(t *testing.T) {
	const now = 2026
	three := 3

	// Date field present -> used, ahead of override + default.
	req := forecastRequest{PlannedYearOverrides: map[string]int{"F1": 5}, PlannedDefaultYear: &three}
	if got := resolvePlannedYear(req, map[string]any{"in_service_date": "2030-01-01"}, "F1", "in_service_date", now); got != 4 {
		t.Fatalf("date field should win: got %d, want 4", got)
	}
	// No date -> per-asset override wins over default.
	if got := resolvePlannedYear(req, map[string]any{}, "F1", "in_service_date", now); got != 5 {
		t.Fatalf("override should win: got %d, want 5", got)
	}
	// No date, no override -> scenario default.
	if got := resolvePlannedYear(req, map[string]any{}, "F2", "in_service_date", now); got != 3 {
		t.Fatalf("default should apply: got %d, want 3", got)
	}
	// No default supplied -> defaultPlannedYear (1).
	bare := forecastRequest{}
	if got := resolvePlannedYear(bare, map[string]any{}, "X", "in_service_date", now); got != defaultPlannedYear {
		t.Fatalf("missing default -> %d, want %d", got, defaultPlannedYear)
	}
	// A past date floors to 0 (on now).
	if got := resolvePlannedYear(bare, map[string]any{"in_service_date": "2000-01-01"}, "X", "in_service_date", now); got != 0 {
		t.Fatalf("past date should floor to 0: got %d", got)
	}
}

// TestWorstVoltage checks the worst (lowest) per-unit voltage across a trajectory,
// and +Inf for an empty one (so the caller's margin test treats it as "no sag").
func TestWorstVoltage(t *testing.T) {
	traj := []voltagePoint{{Year: 0, VoltagePu: 0.99}, {Year: 1, VoltagePu: 0.94}, {Year: 2, VoltagePu: 0.96}}
	if got := worstVoltage(traj); !approxEqual(got, 0.94, 1e-9) {
		t.Fatalf("worstVoltage = %v, want 0.94", got)
	}
	if got := worstVoltage(nil); got <= 1e6 {
		t.Fatalf("worstVoltage(nil) = %v, want +Inf", got)
	}
}

// TestFirstUnderVoltageYear checks the first year voltage drops BELOW the floor: a
// value exactly at the floor is not a violation (strict <), none crossing -> nil.
func TestFirstUnderVoltageYear(t *testing.T) {
	traj := []voltagePoint{
		{Year: 0, VoltagePu: 0.98},
		{Year: 1, VoltagePu: 0.95},  // exactly at the floor -- not a violation
		{Year: 2, VoltagePu: 0.949}, // first real sag below
		{Year: 3, VoltagePu: 0.91},
	}
	got := firstUnderVoltageYear(traj, 0.95)
	if got == nil || *got != 2 {
		t.Fatalf("firstUnderVoltageYear = %v, want 2", got)
	}
	safe := []voltagePoint{{Year: 0, VoltagePu: 0.99}, {Year: 1, VoltagePu: 0.96}}
	if got := firstUnderVoltageYear(safe, 0.95); got != nil {
		t.Fatalf("expected nil for a never-sagging trajectory, got %d", *got)
	}
}

// TestPeakLoading checks the peak is the max across the trajectory (and 0 for empty).
func TestPeakLoading(t *testing.T) {
	traj := []forecastPoint{{Year: 0, LoadingPct: 72}, {Year: 1, LoadingPct: 118}, {Year: 2, LoadingPct: 95}}
	if got := peakLoading(traj); !approxEqual(got, 118, 1e-9) {
		t.Fatalf("peakLoading = %v, want 118", got)
	}
	if got := peakLoading(nil); got != 0 {
		t.Fatalf("peakLoading(nil) = %v, want 0", got)
	}
}
