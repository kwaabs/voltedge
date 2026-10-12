package powerflow

import (
	"math"
	"math/cmplx"
	"testing"
)

// nodal_accuracy_test.go validates the Tier-2 NODAL solver -- the Gauss-Seidel
// Y-bus iteration (solveGaussSeidel), a different engine from the backward/forward
// sweep validated in sweep_accuracy_test.go -- against the SAME external reference:
// the exact closed-form two-bus AC solution (the biquadratic, exactTwoBusVr). A
// slack bus feeding one PQ bus through a series impedance is the smallest network
// with an analytical answer, so the iterative solver has an independent target it
// must converge to, not merely a self-consistent fixed point.
//
// Everything here is per-unit (Vs = 1 pu): the solver's Y-bus and injections are
// per-unit, and the biquadratic is scale-invariant, so exactTwoBusVr(1, r, x, p, q)
// is the pu receiving-end magnitude. The two solve the identical power-flow
// equations (I = conj(S/V), V from the nodal balance), so they must agree to the
// Gauss-Seidel tolerance, not merely "closely".

// buildTwoBusModel assembles a per-unit 2-bus nodalModel: bus 0 is the slack held
// at 1∠0, bus 1 is a PQ bus drawing load (p+jq) pu, joined by one series impedance
// z = r+jx pu. The Y-bus of a single series branch (no shunt) is the textbook
// [[y,-y],[-y,y]] with y = 1/z; the load is a NEGATIVE injection (generation
// positive, load negative), matching busInfo's sign convention.
func buildTwoBusModel(r, x, p, q float64) *nodalModel {
	y := 1 / complex(r, x)
	return &nodalModel{
		// bus 0: slack held at 1∠0; bus 1: PQ bus drawing (p+jq) pu (negative injection).
		buses: []busInfo{
			{vBase: 1, kind: busSlack, vSetPU: 1},
			{vBase: 1, kind: busPQ, sInjPU: complex(-p, -q)},
		},
		yBus: [][]complex128{
			{y, -y},
			{-y, y},
		},
		slacks: map[int]bool{0: true},
	}
}

// TestNodalMatchesExactAC drives solveGaussSeidel on a 2-bus per-unit system and
// asserts the solved PQ-bus voltage magnitude matches the exact closed-form AC
// solution. It sweeps a light and a heavy load so the check covers a small drop
// and a deep one (where the linearised sweep would visibly diverge but the full
// nonlinear nodal solve should not).
func TestNodalMatchesExactAC(t *testing.T) {
	cases := []struct {
		name       string
		r, x, p, q float64
		tol        float64 // pu magnitude tolerance vs the exact AC root
	}{
		{"light load", 0.02, 0.04, 0.20, 0.10, 1e-7},
		{"moderate load", 0.03, 0.06, 0.50, 0.25, 1e-7},
		{"heavy load", 0.05, 0.10, 1.00, 0.50, 1e-6},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			want, ok := exactTwoBusVr(1, c.r, c.x, c.p, c.q)
			if !ok {
				t.Fatalf("exact AC solution does not exist for %+v (voltage collapse)", c)
			}

			sol := solveGaussSeidel(buildTwoBusModel(c.r, c.x, c.p, c.q))
			if !sol.converged {
				t.Fatalf("Gauss-Seidel did not converge in %d iters (maxMismatch %g)", sol.iterations, sol.maxMismatch)
			}
			if len(sol.vPU) != 2 {
				t.Fatalf("expected 2 bus voltages, got %d", len(sol.vPU))
			}

			// Slack stays pinned at 1∠0.
			if d := cmplx.Abs(sol.vPU[0] - complex(1, 0)); d > 1e-12 {
				t.Errorf("slack voltage moved: %v (|Δ| = %g)", sol.vPU[0], d)
			}

			got := cmplx.Abs(sol.vPU[1])
			if d := math.Abs(got - want); d > c.tol {
				t.Errorf("PQ-bus |V| = %.9f pu, exact AC = %.9f pu (|Δ| = %g > %g)", got, want, d, c.tol)
			}
			// The PQ bus must sag below the slack -- a positive load cannot raise it.
			if got >= 1 {
				t.Errorf("PQ-bus |V| = %.9f pu did not drop below the 1.0 slack under load", got)
			}
			t.Logf("%s: nodal |V| = %.9f pu, exact AC = %.9f pu, |Δ| = %g (%d iters)",
				c.name, got, want, math.Abs(got-want), sol.iterations)
		})
	}
}
