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
