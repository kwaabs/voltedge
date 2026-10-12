package powerflow

import (
	"math"
	"testing"
)

// sweep_accuracy_test.go -- Phase 1 (operational hardening) slice 1: the FIRST
// validation of the balanced load-flow sweep against an EXTERNAL reference rather
// than a hand calculation of the engine's own formula.
//
// The reference is the exact closed-form solution of the two-bus, constant-power
// AC load flow -- a standard power-systems result, derived independently of the
// code under test (see exactTwoBusVr). The engine's backward/forward sweep is a
// balanced, single-phase, LINEARISED approximation (it divides the drop by the
// nominal voltage, not the depressed local voltage), so the right yardstick is a
// BALANCED exact solution, not IEEE 13/34/123, which are UNBALANCED per-phase
// cases for the separate Phase-55 unbalanced engine. Validating "against what" is
// the whole point: this pins how close planning-grade actually is, and where it
// starts to drift.
//
// What it establishes, as asserted facts in CI:
//   - across realistic radial drops (~0.5% to ~8%), the sweep tracks the exact AC
//     receiving-end voltage to well under 1%;
//   - the linearisation is consistently OPTIMISTIC (it under-predicts the drop),
//     because it uses the nominal voltage in the drop denominator -- a known,
//     now-documented bias a planner should be aware of.

// exactTwoBusVr returns the exact receiving-end voltage magnitude of a two-bus,
// single-line, constant-power load flow: a slack source of magnitude vs feeding a
// load (p watts, q vars) through a series impedance r + jx (ohms), no shunt. It
// solves the standard biquadratic |Vr|^4 + (2(pR+qX) - |Vs|^2)|Vr|^2 +
// (p^2+q^2)(R^2+X^2) = 0 and takes the upper (stable) root. ok is false when the
// load exceeds the line's transfer capacity (negative discriminant = voltage
// collapse), i.e. there is no real operating point to compare against.
func exactTwoBusVr(vs, r, x, p, q float64) (vr float64, ok bool) {
	b := 2*(p*r+q*x) - vs*vs
	c := (p*p + q*q) * (r*r + x*x)
	disc := b*b - 4*c
	if disc < 0 {
		return 0, false
	}
	u := (-b + math.Sqrt(disc)) / 2 // the stable (near-nominal) root
	if u <= 0 {
		return 0, false
	}
	return math.Sqrt(u), true
}

// TestSweepMatchesExactAC validates the assembled backward/forward sweep against
// the exact two-bus AC solution over a range of realistic feeder loadings, and
// pins the optimistic bias of the linearisation. Reuses the in-memory network
// helpers from sweep_integration_test.go (newLoadNode / newEdgeNode / runToySweep).
func TestSweepMatchesExactAC(t *testing.T) {
	cases := []struct {
		name               string
		vs, r, x           float64 // source nominal (V) and line impedance (ohms)
		pKW, qKVAR         float64 // load
		relTol             float64 // allowed |sweep - exact| / exact
		approxDropFraction float64 // documentation only: the rough drop this case exercises
	}{
		{"MV feeder, light load (~0.5% drop)", 11000, 1.0, 0.5, 500, 150, 0.001, 0.005},
		{"LV main, moderate load (~4% drop)", 400, 0.1, 0.05, 50, 20, 0.005, 0.04},
		{"LV main, heavy load (~8% drop)", 400, 0.1, 0.05, 100, 40, 0.01, 0.08},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pW, qVAR := c.pKW*1000, c.qKVAR*1000

			exact, ok := exactTwoBusVr(c.vs, c.r, c.x, pW, qVAR)
			if !ok {
				t.Fatalf("reference case has no AC operating point (voltage collapse) -- the load exceeds the line's transfer limit, pick a lighter one")
			}

			src := node{classID: "src", featureID: "s"}
			cond := node{classID: "edge", featureID: "c"}
			load := node{classID: "load", featureID: "l"}
			tr := &tree{
				order:        []node{src, cond, load},
				parent:       map[node]node{cond: src, load: cond},
				children:     map[node][]node{src: {cond}, cond: {load}},
				childWinding: map[node]string{},
			}
			states := map[node]*nodeState{
				src:  newLoadNode(0, 0),
				cond: newEdgeNode(c.r, c.x),
				load: newLoadNode(c.pKW, c.qKVAR),
			}
			runToySweep(tr, states, src, c.vs, 0, 0)

			got := states[load].voltageV
			relErr := math.Abs(got-exact) / exact
			t.Logf("sweep=%.4f V  exactAC=%.4f V  relErr=%.4f%%  (drop≈%.1f%%)",
				got, exact, relErr*100, c.approxDropFraction*100)

			if relErr > c.relTol {
				t.Fatalf("sweep %.4f V vs exact AC %.4f V: rel err %.4f%% exceeds tolerance %.4f%%",
					got, exact, relErr*100, c.relTol*100)
			}

			// The linearisation uses the NOMINAL voltage in the drop denominator, so
			// it under-estimates the current and therefore the drop: the sweep voltage
			// should sit at or above the exact AC voltage. Pin this direction -- a
			// planning tool that errs optimistic is a property to know, not discover.
			if got < exact-1e-9 {
				t.Errorf("expected the linearised sweep to be optimistic (>= exact AC); got %.4f V < %.4f V", got, exact)
			}
		})
	}
}
