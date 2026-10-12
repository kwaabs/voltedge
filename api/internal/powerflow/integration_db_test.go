package powerflow

import (
	"context"
	"database/sql"
	"math"
	"os"
	"testing"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
)

// integration_db_test.go is the first END-TO-END validation that drives a real
// analysis endpoint against a live, seeded database -- resolving the network,
// building the radial tree, accumulating impedance, computing the fault and
// loading TCC curves from metadata.protection_curves -- the whole path every
// kernel test above deliberately stubs. It SKIPS when DATABASE_URL is unset, so
// the ordinary `go test ./...` (no database) is unaffected; it runs in the CI
// bootstrap job, which first applies migrations + seed-all against a throwaway
// Postgres (see .github/workflows/ci.yml).
//
// The reference numbers are the ones observed live against the seeded protection
// network (breaker BRK-1 feeding conductor MV-3): a ~29.8 kA bolted fault at
// nominal, ~32.8 kA with the IEC c_max = 1.10, exactly 1.10x apart -- the end-to-
// end confirmation of the c-factor on real data, plus a regression anchor on the
// seeded fault level and the coordination verdict.

// testDB connects to DATABASE_URL exactly as every binary does (pgdriver +
// pgdialect), or skips the test when it is unset.
func testDB(t *testing.T) *bun.DB {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set -- skipping DB integration test (runs in the CI bootstrap job)")
	}
	sqldb := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(url)))
	db := bun.NewDB(sqldb, pgdialect.New())
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// Seeded protection network (fixed UUIDs): breaker BRK-1 feeding down its feeder
// to conductor MV-3, the fault node.
const (
	seedBRK1 = "9a070000-0000-0000-0000-000000000002" // MV_BREAKER BRK-1
	seedMV3  = "9a070000-0000-0000-0000-000000000013" // MV_CONDUCTOR MV-3
)

// TestEndToEndCoordination drives the real runCoordination against the seeded
// network at the default IEC c and pinned to c = 1.0, and checks the fault level,
// the c-factor, the exact 1.10x scaling, and the (mis)coordination verdict.
func TestEndToEndCoordination(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	req := coordinationRequest{
		Start:           featureRef{FeatureClass: "MV_BREAKER", FeatureID: seedBRK1},
		Fault:           featureRef{FeatureClass: "MV_CONDUCTOR", FeatureID: seedMV3},
		NominalVoltageV: 11000,
		CTISeconds:      0.3,
	}

	def, err := runCoordination(ctx, db, req)
	if err != nil {
		t.Fatalf("coordination (default c): %v", err)
	}
	reqNom := req
	reqNom.VoltageFactorC = 1.0
	nom, err := runCoordination(ctx, db, reqNom)
	if err != nil {
		t.Fatalf("coordination (c=1.0): %v", err)
	}

	if def.FaultCurrentAmps == nil || nom.FaultCurrentAmps == nil {
		t.Fatalf("no fault current returned (default %v, c=1.0 %v)", def.FaultCurrentAmps, nom.FaultCurrentAmps)
	}
	iDef, iNom := *def.FaultCurrentAmps, *nom.FaultCurrentAmps

	// The c-factor applied end-to-end: default c = 1.10 at 11 kV, and the fault
	// level is exactly 1.10x the c=1.0 figure.
	if !approxEqual(def.VoltageFactorC, 1.10, 1e-9) {
		t.Errorf("default c = %v, want 1.10 (MV)", def.VoltageFactorC)
	}
	if !approxEqual(nom.VoltageFactorC, 1.0, 1e-9) {
		t.Errorf("pinned c = %v, want 1.0", nom.VoltageFactorC)
	}
	if ratio := iDef / iNom; !approxEqual(ratio, 1.10, 1e-6) {
		t.Errorf("I_default / I_c1.0 = %.6f, want 1.10 (the voltage factor)", ratio)
	}

	// Regression anchor on the seeded fault level (loose, so a minor seed tweak
	// doesn't break it, but a real change does): ~29.8 kA at c=1.0, ~32.8 kA default.
	if rel := math.Abs(iNom-29824.0) / 29824.0; rel > 0.01 {
		t.Errorf("c=1.0 fault at MV-3 = %.1f A, want ~29824 A (seeded network changed?)", iNom)
	}
	if rel := math.Abs(iDef-32806.0) / 32806.0; rel > 0.01 {
		t.Errorf("default fault at MV-3 = %.1f A, want ~32806 A", iDef)
	}

	// The verdict: a close-in ~30 kA fault trips the breaker's instantaneous
	// element (t = 0) ahead of the fuse, so the path is miscoordinated.
	if def.Coordinated == nil || *def.Coordinated {
		t.Errorf("coordinated = %v, want false (breaker instantaneous beats the fuse at this fault)", def.Coordinated)
	}
	var sawFuse, sawBreaker bool
	for _, d := range def.Devices {
		switch d.FeatureClass {
		case "MV_FUSE":
			sawFuse = true
		case "MV_BREAKER":
			sawBreaker = true
			if d.OperateTimeS != nil && *d.OperateTimeS != 0 {
				t.Errorf("breaker operate time = %v s, want 0 (instantaneous at ~30 kA)", *d.OperateTimeS)
			}
		}
	}
	if !sawFuse || !sawBreaker {
		t.Errorf("expected both a fuse and a breaker on the path (fuse=%v breaker=%v)", sawFuse, sawBreaker)
	}

	t.Logf("end-to-end coordination BRK-1→MV-3: I\"k default=%.1f A (c=%.2f), c=1.0=%.1f A; coordinated=%v",
		iDef, def.VoltageFactorC, iNom, def.Coordinated != nil && *def.Coordinated)
}

// TestEndToEndFaultCurrent drives the real runFaultCurrent (the fault-at-every-
// node walk) from BRK-1 and checks the fault level reported at MV-3, confirming
// the c-factor is applied on the fault endpoint too.
func TestEndToEndFaultCurrent(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	resp, err := runFaultCurrent(ctx, db, faultCurrentRequest{
		Start:           featureRef{FeatureClass: "MV_BREAKER", FeatureID: seedBRK1},
		NominalVoltageV: 11000,
	})
	if err != nil {
		t.Fatalf("fault current: %v", err)
	}

	var mv3 *faultNodeResult
	for i := range resp.Nodes {
		if resp.Nodes[i].FeatureID == seedMV3 {
			mv3 = &resp.Nodes[i]
		}
	}
	if mv3 == nil || mv3.FaultCurrentAmps == nil {
		t.Fatalf("no fault current for MV-3 in the fault walk")
	}
	if !approxEqual(mv3.VoltageFactorC, 1.10, 1e-9) {
		t.Errorf("MV-3 c = %v, want 1.10", mv3.VoltageFactorC)
	}
	if rel := math.Abs(*mv3.FaultCurrentAmps-32806.0) / 32806.0; rel > 0.01 {
		t.Errorf("MV-3 fault = %.1f A, want ~32806 A (c_max)", *mv3.FaultCurrentAmps)
	}
}

// TestEndToEndLoadFlow drives the real runLoadFlow (balanced backward/forward
// sweep) from BRK-1, exercising the whole DB-coupled assembly -- buildTree,
// resolveNodeStates, the accumulation passes -- that the in-memory kernel tests
// (sweep_accuracy_test.go) deliberately stub. No external reference number is
// asserted (none is live-verified for this feeder); instead it pins the invariants
// a correct solve must satisfy, so a broken assembly (a bad query, a NaN, a node
// that won't resolve) fails CI rather than returning quiet garbage.
func TestEndToEndLoadFlow(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	resp, err := runLoadFlow(ctx, db, loadFlowRequest{
		Start:           featureRef{FeatureClass: "MV_BREAKER", FeatureID: seedBRK1},
		NominalVoltageV: 11000,
	})
	if err != nil {
		t.Fatalf("load flow: %v", err)
	}
	if len(resp.Nodes) < 2 {
		t.Fatalf("expected the walk to reach multiple nodes, got %d", len(resp.Nodes))
	}
	if resp.NominalVoltageV <= 0 {
		t.Errorf("nominal base = %v, want > 0", resp.NominalVoltageV)
	}

	var sawStart, sawMV3 bool
	for _, n := range resp.Nodes {
		if math.IsNaN(n.VoltageV) || math.IsInf(n.VoltageV, 0) || n.VoltageV <= 0 {
			t.Errorf("%s %s: voltage = %v, want a finite positive value", n.FeatureClass, n.FeatureID, n.VoltageV)
		}
		// A sane feeder solve: every node's drop is finite and physically bounded.
		// (Left deliberately loose -- no assumption about the seeded loading -- so it
		// catches NaN/Inf/garbage from a broken assembly without a spurious failure.)
		if math.IsNaN(n.VoltageDropPct) || math.IsInf(n.VoltageDropPct, 0) || n.VoltageDropPct < -5 || n.VoltageDropPct > 100 {
			t.Errorf("%s %s: drop%% = %v, outside the physical [-5,100] band", n.FeatureClass, n.FeatureID, n.VoltageDropPct)
		}
		switch n.FeatureID {
		case seedBRK1:
			sawStart = true
		case seedMV3:
			sawMV3 = true
		}
	}
	if !sawStart || !sawMV3 {
		t.Errorf("expected both the start and MV-3 in the result (start=%v MV-3=%v)", sawStart, sawMV3)
	}
	t.Logf("end-to-end load flow BRK-1@11kV: %d nodes, base %.0f V", len(resp.Nodes), resp.NominalVoltageV)
}

// TestEndToEndNodalLoadFlow drives the real runNodalLoadFlow against the seeded
// network with BOTH solvers (Gauss-Seidel and Newton). Beyond the per-solver
// invariants (converged, bounded mismatch, finite positive voltages), the load-
// bearing check is that the two solvers -- which assemble the IDENTICAL Y-bus from
// the live database and solve it by different numerical methods -- converge to the
// same voltages. That agreement is an independent cross-check on the whole DB
// assembly (buildBuses -> solve), needing no external reference, and complements the
// in-memory kernel tests that pin each solver against the exact/manufactured AC.
func TestEndToEndNodalLoadFlow(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	run := func(solver string) *nodalLoadFlowResponse {
		resp, err := runNodalLoadFlow(ctx, db, nodalLoadFlowRequest{
			Start:           featureRef{FeatureClass: "MV_BREAKER", FeatureID: seedBRK1},
			NominalVoltageV: 11000,
			Solver:          solver,
		})
		if err != nil {
			t.Fatalf("nodal load flow (solver %q): %v", solver, err)
		}
		return resp
	}

	gs := run(solverGaussSeidel)
	nr := run(solverNewton)

	for _, tc := range []struct {
		name string
		resp *nodalLoadFlowResponse
	}{{solverGaussSeidel, gs}, {solverNewton, nr}} {
		r := tc.resp
		if !r.Converged {
			t.Errorf("%s did not converge (mismatch %g pu, %d iters)", tc.name, r.MaxMismatch, r.Iterations)
		}
		if r.MaxMismatch > 1e-3 {
			t.Errorf("%s max mismatch = %g pu, want < 1e-3", tc.name, r.MaxMismatch)
		}
		if r.Solver != tc.name {
			t.Errorf("response solver = %q, want %q", r.Solver, tc.name)
		}
		if r.BusCount < 1 || r.SlackCount < 1 {
			t.Errorf("%s: buses=%d slacks=%d, want >= 1 each", tc.name, r.BusCount, r.SlackCount)
		}
		if len(r.Nodes) == 0 {
			t.Errorf("%s: no nodes returned", tc.name)
		}
		for _, n := range r.Nodes {
			if math.IsNaN(n.VoltageV) || math.IsInf(n.VoltageV, 0) || n.VoltageV <= 0 {
				t.Errorf("%s: %s %s voltage = %v, want finite positive", tc.name, n.FeatureClass, n.FeatureID, n.VoltageV)
			}
		}
	}

	// Cross-check: the two solvers solve one and the same assembled model, so their
	// per-node voltages must match to solver tolerance. ~0.5 V on an 11 kV base is
	// ~5e-5 pu -- far above convergence noise, far below any real divergence.
	nrV := make(map[string]float64, len(nr.Nodes))
	for _, n := range nr.Nodes {
		nrV[n.FeatureID] = n.VoltageV
	}
	var compared int
	var maxDelta float64
	for _, n := range gs.Nodes {
		if v, ok := nrV[n.FeatureID]; ok {
			compared++
			if d := math.Abs(n.VoltageV - v); d > maxDelta {
				maxDelta = d
			}
		}
	}
	if compared == 0 {
		t.Fatalf("gauss-seidel and newton share no feature nodes to compare")
	}
	if maxDelta > 0.5 {
		t.Errorf("gauss-seidel vs newton disagree by up to %.4f V over %d nodes -- same model, different solver, must match", maxDelta, compared)
	}
	t.Logf("end-to-end nodal BRK-1@11kV: GS %d buses conv=%v (%d it), NR conv=%v (%d it); GS↔NR max ΔV %.6f V over %d nodes",
		gs.BusCount, gs.Converged, gs.Iterations, nr.Converged, nr.Iterations, maxDelta, compared)
}
