package powerflow

import (
	"math"
	"testing"
)

// sweep_test.go is the first numerical-validation slice for the balanced
// load-flow engine (Bet 2 / Book of Tests 8.1): hand-calc checks of the pure
// math kernels the sweep's accuracy rests on. These run with no database -- they
// pin the FORMULAS. End-to-end validation of the assembled sweep against a toy
// network and the IEEE 13/34-bus feeders is the next slice (needs a seeded DB).

func approxEqual(a, b, eps float64) bool {
	return math.Abs(a-b) <= eps
}

// TestApproxDropV validates the linearized voltage-drop kernel
// ΔV(volts) = (P·R + Q·X) / V against hand calculations -- the single formula
// the whole load flow's node voltages come from. kW/kVAR are scaled to
// watts/VAR (×1000) before the divide.
func TestApproxDropV(t *testing.T) {
	cases := []struct {
		name                          string
		cumKW, cumKVAR, r, x, v, want float64
	}{
		// (100kW·0.5Ω + 50kVAR·0.3Ω)·1000 / 11000 V = 65000 / 11000.
		{"mixed P and Q", 100, 50, 0.5, 0.3, 11000, 65000.0 / 11000.0},
		// Pure resistive: the X term drops out at Q=0 regardless of X.
		{"pure resistive, X ignored at Q=0", 10, 0, 1, 99, 1000, 10},
		// Pure reactive (pf=0 load): only the Q·X term. 20kVAR·0.25Ω·1000/400.
		{"pure reactive", 0, 20, 99, 0.25, 400, 20 * 1000 * 0.25 / 400},
		// Guard: a non-positive reference voltage yields no drop (never /0).
		{"zero reference voltage", 100, 50, 1, 1, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := approxDropV(c.cumKW, c.cumKVAR, c.r, c.x, c.v)
			if !approxEqual(got, c.want, 1e-9) {
				t.Fatalf("approxDropV(%v,%v,%v,%v,%v) = %v, want %v",
					c.cumKW, c.cumKVAR, c.r, c.x, c.v, got, c.want)
			}
		})
	}
}

// TestApproxCurrentAmps validates I = |S|/V = sqrt(P²+Q²)/V (the engine's
// single-phase-magnitude branch current, S in VA).
func TestApproxCurrentAmps(t *testing.T) {
	// 3-4-5 right triangle: S = hypot(3000, 4000) = 5000 VA; / 1000 V = 5 A.
	if got := approxCurrentAmps(3, 4, 1000); !approxEqual(got, 5, 1e-9) {
		t.Fatalf("approxCurrentAmps(3,4,1000) = %v, want 5", got)
	}
	// Pure real power: 100 kW / 1000 V = 100 A.
	if got := approxCurrentAmps(100, 0, 1000); !approxEqual(got, 100, 1e-9) {
		t.Fatalf("approxCurrentAmps(100,0,1000) = %v, want 100", got)
	}
	// Guard: zero voltage yields zero current (never /0).
	if got := approxCurrentAmps(100, 50, 0); got != 0 {
		t.Fatalf("approxCurrentAmps(100,50,0) = %v, want 0", got)
	}
}

// TestSplitZ validates the X/R split R = |Z|/sqrt(1+xr²), X = R·xr: the ratio
// X/R must equal xr, and the magnitude must be preserved (R² + X² == |Z|²).
func TestSplitZ(t *testing.T) {
	for _, c := range []struct{ z, xr float64 }{
		{10, 0}, {10, 1}, {5, 2}, {0.8, 0.5},
	} {
		r, x := splitZ(c.z, c.xr)
		if r != 0 && !approxEqual(x/r, c.xr, 1e-9) {
			t.Fatalf("splitZ(%v,%v): X/R = %v, want %v", c.z, c.xr, x/r, c.xr)
		}
		if !approxEqual(r*r+x*x, c.z*c.z, 1e-9) {
			t.Fatalf("splitZ(%v,%v): R²+X² = %v, want |Z|² = %v", c.z, c.xr, r*r+x*x, c.z*c.z)
		}
	}
	// xr = 0 is all resistance, no reactance.
	if r, x := splitZ(10, 0); !approxEqual(r, 10, 1e-9) || x != 0 {
		t.Fatalf("splitZ(10,0) = (%v,%v), want (10,0)", r, x)
	}
}
