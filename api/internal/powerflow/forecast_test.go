package powerflow

import "testing"

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
