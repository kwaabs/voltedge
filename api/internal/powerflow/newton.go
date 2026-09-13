package powerflow

// newton.go is Phase 52 Tier 2, Slice S5: the Newton-Raphson AC power-flow solver
// -- the second solver over the SAME assembled model (nodalModel: buses,
// branches, the per-unit Y-bus, the slack/PV/PQ classification) that
// solveGaussSeidel already consumes. It returns the same nodalSolution, so
// formatNodalResults / busInjectionPower read whichever solver ran without caring
// which; runNodalLoadFlow picks between them on the request's "solver" field.
//
// Why a second solver: Gauss-Seidel converges only linearly -- fine for a
// well-conditioned radial-ish LV network but slow, and it can crawl or stall on a
// stiff, heavily meshed grid. Newton-Raphson converges QUADRATICALLY (the residual
// squares each step near the solution), the standard method for a meshed AC power
// flow, at the cost of forming and solving a linear system each iteration.
//
// Formulation: polar, the textbook form. The unknowns are the voltage ANGLE at
// every non-slack bus and the voltage MAGNITUDE at every PQ bus (a PV bus holds
// its magnitude, a slack holds both). The mismatch equations are the real-power
// injection residual ΔP at every non-slack bus and the reactive residual ΔQ at
// every PQ bus. Each iteration solves J·Δx = mismatch for the angle/magnitude
// corrections, where the Jacobian J = [[H, N], [M, L]] holds the standard partial
// derivatives of (P, Q) w.r.t. (θ, |V|), then updates and repeats until the
// largest power mismatch falls below newtonTolerancePU.
//
// The one new numerical primitive a Newton step needs -- solving a dense linear
// system -- is hand-rolled here (Gaussian elimination with partial pivoting,
// solveDenseLinear). Deliberately NO external linear-algebra dependency: a
// distribution study is bounded to at most a few hundred buses, where a dense
// O(m³) solve is a few milliseconds, and adding gonum for one routine is not worth
// the dependency. (If studies ever grow to thousands of buses a sparse solve would
// be the upgrade, isolated to this one function.)
//
// PV reactive limits: when a PV bus's solved reactive injection would exceed its
// limit, the bus latches to PQ with its reactive fixed at the binding limit and
// its magnitude released (standard PV->PQ switching), matching what
// solveGaussSeidel does inline. The latch is one-way per solve (a switched bus
// stays switched) -- simple and oscillation-free; the seeds that exercise PV today
// carry no reactive limit, so this path is conservative headroom, not the common
// case.

import "math"

// solveNewton runs Newton-Raphson on the per-unit Y-bus model and returns the
// solved per-unit bus voltages plus convergence telemetry, in the same
// nodalSolution shape solveGaussSeidel returns. A bus with no admittance (an
// isolated, zero-base island) is excluded from the unknowns and left at its flat
// start, exactly as Gauss-Seidel skips it.
func solveNewton(model *nodalModel) nodalSolution {
	n := len(model.buses)
	y := model.yBus

	// State: voltage magnitude and angle per bus. Flat start -- 1∠0 everywhere,
	// except a slack or PV bus starts at its held magnitude setpoint.
	vm := make([]float64, n)
	va := make([]float64, n)
	for i := range model.buses {
		vm[i] = 1
		bus := model.buses[i]
		if model.slacks[i] || bus.kind == busPV {
			if bus.vSetPU > 0 {
				vm[i] = bus.vSetPU
			}
		}
	}

	// A bus participates in the solve only if it has admittance (matches
	// solveGaussSeidel's `if yii == 0 continue`). Slacks never carry unknowns.
	connected := make([]bool, n)
	for i := 0; i < n; i++ {
		connected[i] = y[i][i] != 0
	}

	// released latches a PV bus to PQ once its reactive limit binds; qFixed holds
	// the reactive injection it is then pinned at. One-way per solve.
	released := make([]bool, n)
	qFixed := make([]float64, n)

	var maxMis float64
	converged := false
	iter := 0
	for iter = 0; iter < newtonMaxIter; iter++ {
		p, q := nodalInjections(model, vm, va)

		// PV -> PQ switching: latch any PV bus whose reactive injection has run past
		// its limit, pinning Q at the binding limit and releasing its magnitude.
		for i := range model.buses {
			bus := model.buses[i]
			if bus.kind != busPV || model.slacks[i] || !bus.qLimited || released[i] {
				continue
			}
			if q[i] > bus.qMaxPU {
				released[i] = true
				qFixed[i] = bus.qMaxPU
			} else if q[i] < bus.qMinPU {
				released[i] = true
				qFixed[i] = bus.qMinPU
			}
		}

		// Unknown sets: angle at every connected non-slack bus; magnitude at every
		// connected PQ bus (an original PQ bus, or a PV bus released to PQ).
		var angleBuses, vmBuses []int
		for i := 0; i < n; i++ {
			if model.slacks[i] || !connected[i] {
				continue
			}
			angleBuses = append(angleBuses, i)
			isPQ := model.buses[i].kind != busPV || released[i]
			if isPQ {
				vmBuses = append(vmBuses, i)
			}
		}

		la := len(angleBuses)
		lv := len(vmBuses)
		m := la + lv
		if m == 0 {
			// Nothing free to solve (only slacks / islands). Balanced by definition.
			converged = true
			maxMis = 0
			break
		}

		// Mismatch vector: [ΔP over angleBuses ; ΔQ over vmBuses].
		mis := make([]float64, m)
		for r, i := range angleBuses {
			pSpec := real(model.buses[i].sInjPU)
			if model.buses[i].kind == busPV {
				pSpec = model.buses[i].pInjPU
			}
			mis[r] = pSpec - p[i]
		}
		for r, i := range vmBuses {
			var qSpec float64
			if released[i] {
				qSpec = qFixed[i]
			} else {
				qSpec = imag(model.buses[i].sInjPU)
			}
			mis[la+r] = qSpec - q[i]
		}

		maxMis = 0
		for _, v := range mis {
			if a := math.Abs(v); a > maxMis {
				maxMis = a
			}
		}
		if maxMis < newtonTolerancePU {
			converged = true
			break
		}

		// Jacobian J = [[H, N], [M, L]] over the unknown sets.
		jac := make([][]float64, m)
		for r := range jac {
			jac[r] = make([]float64, m)
		}
		for ri, i := range angleBuses {
			for ci, j := range angleBuses {
				jac[ri][ci] = jacH(y, vm, va, p, q, i, j)
			}
			for ci, j := range vmBuses {
				jac[ri][la+ci] = jacN(y, vm, va, p, q, i, j)
			}
		}
		for ri, i := range vmBuses {
			for ci, j := range angleBuses {
				jac[la+ri][ci] = jacM(y, vm, va, p, q, i, j)
			}
			for ci, j := range vmBuses {
				jac[la+ri][la+ci] = jacL(y, vm, va, p, q, i, j)
			}
		}

		dx, ok := solveDenseLinear(jac, mis)
		if !ok {
			// Singular Jacobian -- return what we have, flagged not converged.
			break
		}

		for r, i := range angleBuses {
			va[i] += dx[r]
		}
		for r, i := range vmBuses {
			vm[i] += dx[la+r]
		}
	}

	v := make([]complex128, n)
	for i := 0; i < n; i++ {
		s, c := math.Sincos(va[i])
		v[i] = complex(vm[i]*c, vm[i]*s)
	}
	return nodalSolution{vPU: v, converged: converged, iterations: iter, maxMismatch: maxMis}
}

// nodalInjections computes the real (P) and reactive (Q) power injected at every
// bus for the current voltage state, in per-unit:
//
//	P_i = |V_i| Σ_j |V_j| (G_ij cos θ_ij + B_ij sin θ_ij)
//	Q_i = |V_i| Σ_j |V_j| (G_ij sin θ_ij − B_ij cos θ_ij),  θ_ij = θ_i − θ_j
func nodalInjections(model *nodalModel, vm, va []float64) (p, q []float64) {
	n := len(model.buses)
	p = make([]float64, n)
	q = make([]float64, n)
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			yij := model.yBus[i][j]
			if yij == 0 {
				continue
			}
			g, b := real(yij), imag(yij)
			s, c := math.Sincos(va[i] - va[j])
			p[i] += vm[i] * vm[j] * (g*c + b*s)
			q[i] += vm[i] * vm[j] * (g*s - b*c)
		}
	}
	return p, q
}

// jacH = ∂P_i/∂θ_j. jacN = ∂P_i/∂|V_j|. jacM = ∂Q_i/∂θ_j. jacL = ∂Q_i/∂|V_j|.
// The diagonal (i == j) forms use the already-computed injections p[i]/q[i] and
// the self-admittance; the off-diagonal forms use Y_ij and the angle difference.
// (Standard polar-form Newton-Raphson partials, e.g. Grainger & Stevenson.)
func jacH(y [][]complex128, vm, va, p, q []float64, i, j int) float64 {
	if i == j {
		return -q[i] - imag(y[i][i])*vm[i]*vm[i]
	}
	g, b := real(y[i][j]), imag(y[i][j])
	s, c := math.Sincos(va[i] - va[j])
	return vm[i] * vm[j] * (g*s - b*c)
}

func jacN(y [][]complex128, vm, va, p, q []float64, i, j int) float64 {
	if i == j {
		return p[i]/vm[i] + real(y[i][i])*vm[i]
	}
	g, b := real(y[i][j]), imag(y[i][j])
	s, c := math.Sincos(va[i] - va[j])
	return vm[i] * (g*c + b*s)
}

func jacM(y [][]complex128, vm, va, p, q []float64, i, j int) float64 {
	if i == j {
		return p[i] - real(y[i][i])*vm[i]*vm[i]
	}
	g, b := real(y[i][j]), imag(y[i][j])
	s, c := math.Sincos(va[i] - va[j])
	return -vm[i] * vm[j] * (g*c + b*s)
}

func jacL(y [][]complex128, vm, va, p, q []float64, i, j int) float64 {
	if i == j {
		return q[i]/vm[i] - imag(y[i][i])*vm[i]
	}
	g, b := real(y[i][j]), imag(y[i][j])
	s, c := math.Sincos(va[i] - va[j])
	return vm[i] * (g*s - b*c)
}

// solveDenseLinear solves A·x = b for x by Gaussian elimination with partial
// pivoting, returning (x, true) on success or (nil, false) if A is singular (a
// near-zero pivot). A is modified in place; b is copied. Dense O(m³) -- fine for
// the few-hundred-bus ceiling of a distribution study (see the file header). A is
// square m×m and b has length m.
func solveDenseLinear(a [][]float64, b []float64) ([]float64, bool) {
	m := len(b)
	// Work on copies so the caller's Jacobian/mismatch aren't consumed.
	rhs := make([]float64, m)
	copy(rhs, b)

	for col := 0; col < m; col++ {
		// Partial pivot: pick the row (at or below col) with the largest |A[row][col]|.
		pivot := col
		best := math.Abs(a[col][col])
		for r := col + 1; r < m; r++ {
			if v := math.Abs(a[r][col]); v > best {
				best = v
				pivot = r
			}
		}
		if best < 1e-14 {
			return nil, false // singular (or numerically so)
		}
		if pivot != col {
			a[col], a[pivot] = a[pivot], a[col]
			rhs[col], rhs[pivot] = rhs[pivot], rhs[col]
		}
		// Eliminate below the pivot.
		pv := a[col][col]
		for r := col + 1; r < m; r++ {
			factor := a[r][col] / pv
			if factor == 0 {
				continue
			}
			a[r][col] = 0
			for c := col + 1; c < m; c++ {
				a[r][c] -= factor * a[col][c]
			}
			rhs[r] -= factor * rhs[col]
		}
	}

	// Back-substitution.
	x := make([]float64, m)
	for row := m - 1; row >= 0; row-- {
		sum := rhs[row]
		for c := row + 1; c < m; c++ {
			sum -= a[row][c] * x[c]
		}
		x[row] = sum / a[row][row]
	}
	return x, true
}
