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

// addBranch stamps one series admittance y between buses a and b into a Y-bus
// (the textbook off-diagonal −y, diagonal +y contribution of a shunt-less series
// branch). Accumulating is what lets a bus pair be joined by more than one path --
// i.e. form a loop.
func addBranch(y [][]complex128, a, b int, adm complex128) {
	y[a][a] += adm
	y[b][b] += adm
	y[a][b] -= adm
	y[b][a] -= adm
}

// TestNodalMeshedManufacturedSolution validates solveGaussSeidel on a genuinely
// MESHED network -- a 3-bus triangle (branches 0-1, 1-2, 0-2) where buses 1 and 2
// are joined by TWO paths, so a loop current flows and the radial sweep could not
// represent it. The reference is the method of MANUFACTURED SOLUTIONS: choose the
// bus voltages first, then derive the exact injections S_i = V_i·conj((Y·V)_i) that
// must produce them. Feeding those injections back, a correct solver must recover
// the chosen voltages. The target is built WITHOUT running the solver, so it is a
// true independent reference -- and because it is derived from the full Y·V product
// it pins the loop (off-diagonal, two-path) coupling, not just radial drop.
func TestNodalMeshedManufacturedSolution(t *testing.T) {
	// Three series branches forming a triangle (a loop): 0-1, 1-2 and the closing
	// 0-2. The 0-2 + 1-2 pair gives bus 2 a second path to the slack, meshing it.
	y := [][]complex128{
		{0, 0, 0},
		{0, 0, 0},
		{0, 0, 0},
	}
	addBranch(y, 0, 1, 1/complex(0.02, 0.06))
	addBranch(y, 1, 2, 1/complex(0.03, 0.08))
	addBranch(y, 0, 2, 1/complex(0.04, 0.10)) // the closing branch that forms the loop

	// Chosen target voltages (the manufactured solution): slack at 1∠0, the two PQ
	// buses sagged in magnitude and angle, as a loaded mesh would be.
	target := []complex128{
		complex(1, 0),
		cmplx.Rect(0.970, -0.020),
		cmplx.Rect(0.955, -0.035),
	}

	// Derive the injection each PQ bus must draw to hold its target voltage:
	// S_i = V_i · conj( Σ_j Y_ij V_j ). This is the ONLY place the reference and the
	// solver meet -- and it is the exact nodal balance, including the loop terms.
	inj := func(i int) complex128 {
		var yv complex128
		for j := range target {
			yv += y[i][j] * target[j]
		}
		return target[i] * cmplx.Conj(yv)
	}

	model := &nodalModel{
		buses: []busInfo{
			{vBase: 1, kind: busSlack, vSetPU: 1},
			{vBase: 1, kind: busPQ, sInjPU: inj(1)},
			{vBase: 1, kind: busPQ, sInjPU: inj(2)},
		},
		yBus:   y,
		slacks: map[int]bool{0: true},
	}

	sol := solveGaussSeidel(model)
	if !sol.converged {
		t.Fatalf("Gauss-Seidel did not converge on the mesh in %d iters (maxMismatch %g)", sol.iterations, sol.maxMismatch)
	}
	if len(sol.vPU) != 3 {
		t.Fatalf("expected 3 bus voltages, got %d", len(sol.vPU))
	}

	// The solver must recover the manufactured voltages -- magnitude AND angle --
	// at every bus, confirming it solved the loop, not merely a radial reduction.
	for i := range target {
		if d := cmplx.Abs(sol.vPU[i] - target[i]); d > 1e-7 {
			t.Errorf("bus %d: solved V = %v, manufactured V = %v (|Δ| = %g > 1e-7)", i, sol.vPU[i], target[i], d)
		}
	}

	// Guard that the network is actually meshed: bus 1↔2 and 0↔2 must both be live
	// branches (non-zero off-diagonals), so bus 2 genuinely has two paths to source.
	if y[1][2] == 0 || y[0][2] == 0 {
		t.Fatalf("test network is not meshed (missing a loop branch): Y12=%v Y02=%v", y[1][2], y[0][2])
	}

	t.Logf("meshed 3-bus (triangle): recovered V1=%v V2=%v in %d iters; S1=%v S2=%v",
		sol.vPU[1], sol.vPU[2], sol.iterations, inj(1), inj(2))
}
