package powerflow

import (
	"fmt"
	"testing"
)

// scale_bench_test.go measures the ALGORITHMIC scaling of the analysis compute
// kernels, in memory -- the complement to scripts/scale/*.sql, which measure the
// DATABASE round-trip scaling (tiles, the per-node trace/resolve queries). The two
// cost models are different and both matter: a feeder can be cheap to resolve from
// the DB yet expensive to solve, or vice versa. Everything here is DB-less (built
// synthetic networks), so it runs in plain `go test` / `go test -bench`.
//
// The headline contrast these expose:
//   - the balanced sweep is O(N): two linear tree passes, so it scales to whole-
//     network node counts.
//   - the nodal solver carries a DENSE N×N Y-bus and, per Gauss-Seidel update, sums
//     over every bus -- O(N²) per iteration and O(N²) memory. That is fine at
//     substation scale (hundreds of buses) but not whole-network (thousands+);
//     TestNodalSolverScaleProfile logs the iteration curve so the Gauss-Seidel vs
//     Newton trade-off is a measured fact, not a prediction.

// buildRadialSweepChain builds a synthetic radial feeder of n segments as the real
// sweep structs: source -> (edge -> load) × n. The per-node load and conductor
// impedance are kept SMALL (load scaled by 1/n so the whole feeder draws ~100 kW
// regardless of length, 0.01 Ω segments) so the feeder stays physical -- positive,
// gently sagging voltages -- at any n. (A fixed per-node load would put megawatts
// through the first conductor at large n and drive the linearised drop negative,
// which is a modelling error, not a solver one.) order is root-first (the sweep's
// required topological order).
func buildRadialSweepChain(n int) (*tree, map[node]*nodeState, node) {
	src := node{classID: "src", featureID: "s"}
	order := []node{src}
	parent := map[node]node{}
	children := map[node][]node{}
	states := map[node]*nodeState{src: newLoadNode(0, 0)}

	loadKW := 100.0 / float64(n)
	loadKVAR := 40.0 / float64(n)
	prev := src
	for i := 0; i < n; i++ {
		e := node{classID: "edge", featureID: fmt.Sprintf("e%d", i)}
		l := node{classID: "load", featureID: fmt.Sprintf("l%d", i)}
		order = append(order, e, l)
		parent[e] = prev
		children[prev] = append(children[prev], e)
		parent[l] = e
		children[e] = append(children[e], l)
		states[e] = newEdgeNode(0.01, 0.01)
		states[l] = newLoadNode(loadKW, loadKVAR)
		prev = l
	}
	return &tree{order: order, parent: parent, children: children, childWinding: map[node]string{}}, states, src
}

// buildRadialNodalChain builds an n-bus radial feeder as a per-unit nodalModel:
// bus 0 slack, each subsequent bus a small PQ load joined to the previous by a
// FIXED 0.002+j0.006 pu series impedance (well-scaled admittances). Note a single
// radial chain is only PHYSICAL up to a modest length -- its TOTAL series impedance
// grows with n, so a very long chain is past voltage collapse (no solution exists);
// that is a property of a 1-D chain, not of the solver (real large networks branch
// into trees with short paths). So the convergence PROFILE below uses a physical n
// range; the benchmarks push n higher purely as a per-solve COST probe, where
// convergence is irrelevant to the O(N²) time / Jacobian-memory being measured.
func buildRadialNodalChain(n int) *nodalModel {
	buses := make([]busInfo, n)
	buses[0] = busInfo{vBase: 1, kind: busSlack, vSetPU: 1}
	for i := 1; i < n; i++ {
		buses[i] = busInfo{vBase: 1, kind: busPQ, sInjPU: complex(-0.002, -0.0006)}
	}
	y := make([][]complex128, n)
	for i := range y {
		y[i] = make([]complex128, n)
	}
	seg := 1 / complex(0.002, 0.006)
	for i := 1; i < n; i++ {
		addBranch(y, i-1, i, seg)
	}
	return &nodalModel{buses: buses, yBus: y, slacks: map[int]bool{0: true}}
}

// BenchmarkSweepRadialChain measures the balanced sweep at growing feeder sizes.
// Expect ~linear growth in ns/op with N (two O(N) tree passes).
func BenchmarkSweepRadialChain(b *testing.B) {
	for _, n := range []int{1000, 10000, 50000} {
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				tr, states, start := buildRadialSweepChain(n)
				b.StartTimer()
				runToySweep(tr, states, start, 11000, 0.1, 0.1)
			}
		})
	}
}

// BenchmarkNodalRadialChain measures each nodal solver at growing sizes. The dense
// Y-bus makes this O(N²) per iteration, so sizes are kept modest on purpose -- the
// quadratic growth is itself the result.
func BenchmarkNodalRadialChain(b *testing.B) {
	for _, n := range []int{100, 400, 1000} {
		m := buildRadialNodalChain(n)
		b.Run(fmt.Sprintf("gauss-seidel/N=%d", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				solveGaussSeidel(m)
			}
		})
		b.Run(fmt.Sprintf("newton/N=%d", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				solveNewton(m)
			}
		})
	}
}

// TestSweepScaleMonotonic is the O(N) correctness-at-scale guard: a long radial
// feeder must still produce a finite, monotonically sagging voltage profile (each
// node at or below its parent, no NaN/Inf), confirming the passes don't degrade or
// overflow at size.
func TestSweepScaleMonotonic(t *testing.T) {
	const n = 20000
	tr, states, start := buildRadialSweepChain(n)
	runToySweep(tr, states, start, 11000, 0.1, 0.1)

	var checked int
	for _, nd := range tr.order {
		st := states[nd]
		if isNaNOrInf(st.voltageV) || st.voltageV <= 0 {
			t.Fatalf("node %s voltage = %v at scale, want finite positive", nd.featureID, st.voltageV)
		}
		if p, ok := tr.parent[nd]; ok {
			// Passive radial, no generation: a node cannot rise above its parent
			// (small epsilon for float noise).
			if states[nd].voltageV > states[p].voltageV+1e-6 {
				t.Errorf("node %s (%.3f V) rose above its parent %s (%.3f V) -- no generation in the feeder",
					nd.featureID, states[nd].voltageV, p.featureID, states[p].voltageV)
			}
			checked++
		}
	}
	t.Logf("sweep over %d-segment feeder: %d parent/child drops all monotonic, end voltage %.1f V",
		n, checked, states[tr.order[len(tr.order)-1]].voltageV)
}

// TestNodalSolverScaleProfile logs the convergence profile of both nodal solvers
// over a PHYSICAL feeder-length range and guards the robust solver (Newton). The
// logged iteration counts make the scaling trade-off explicit and measured:
// Gauss-Seidel's iteration count climbs with feeder length (it relaxes one bus at a
// time), while Newton stays at a handful -- so Newton is the right solver for a
// long or meshed feeder, evidenced not asserted. Newton must converge across this
// physical range (a radial feeder is its easy case); if it ever does not, that is a
// real robustness finding, so it is asserted. Gauss-Seidel is logged only: on a long
// feeder it can legitimately hit its 5000-iteration cap before reaching the tight
// 1e-9 tolerance, which is the measured limit, not a test failure.
func TestNodalSolverScaleProfile(t *testing.T) {
	for _, n := range []int{5, 10, 15, 20, 25, 30} {
		m := buildRadialNodalChain(n)
		gs := solveGaussSeidel(m)
		nr := solveNewton(m)
		t.Logf("N=%3d | GS conv=%-5v iters=%-5d maxMM=%.1e | NR conv=%-5v iters=%-3d maxMM=%.1e",
			n, gs.converged, gs.iterations, gs.maxMismatch, nr.converged, nr.iterations, nr.maxMismatch)
		if !nr.converged {
			t.Errorf("Newton did not converge at N=%d on a physical radial feeder (iters=%d, maxMM=%.2e) -- robustness finding", n, nr.iterations, nr.maxMismatch)
		}
	}
	t.Log("note: the O(N²) per-solve TIME and Newton's O(N²) Jacobian MEMORY are in BenchmarkNodalRadialChain (run with -bench)")
}

// isNaNOrInf avoids importing math just for the guard above.
func isNaNOrInf(f float64) bool {
	return f != f || f > 1e308 || f < -1e308
}
