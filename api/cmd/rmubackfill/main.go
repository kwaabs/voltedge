// Command rmubackfill promotes the cable side of RMUs that were placed before
// the fan-out grew one: `go run ./cmd/rmubackfill [--dry-run]` walks every
// existing Ring Main Unit and, for each contained switched way that still has a
// bare, unwired cable terminal, creates the MV_CONDUCTOR + wires it exactly as a
// freshly placed RMU now does (api/internal/rmu/fanout.go, migration 0123 +
// seed/rmu_templates.sql), then rebuilds that RMU's single-line so the promoted
// cables render on the mimic.
//
// It is IDEMPOTENT: a way that already has a cable is left untouched, so a re-run
// only fills gaps. --dry-run reports what would change (RMUs, ways, cables to
// create) without writing anything -- run it first to see the scope.
//
// Prerequisites (the cable target's own, not this command's): MV_CONDUCTOR must
// be geometry_optional AND recompiled (seed/mv_conductor_diagram_promote.sql +
// `task compile CODE=MV_CONDUCTOR`) so gis.mv_conductor.geom drops NOT NULL,
// and seed/rmu_templates.sql (with migration 0123 applied) must be re-seeded so
// the templates carry a cable definition -- otherwise there is nothing to
// promote and the run is a no-op.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"log"
	"os"

	"github.com/kwaabs/voltedge/api/internal/rmu"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
)

func main() {
	dryRun := flag.Bool("dry-run", false, "report what would change without writing anything")
	rmuClass := flag.String("class", "RMU", "the RMU feature class code to scan")
	flag.Parse()

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is not set")
	}

	sqldb := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(databaseURL)))
	db := bun.NewDB(sqldb, pgdialect.New())
	defer db.Close()

	report, err := rmu.BackfillCableConductors(context.Background(), db, rmu.BackfillOptions{
		RMUFeatureClass: *rmuClass,
		DryRun:          *dryRun,
	})
	if err != nil {
		log.Fatalf("rmu cable backfill: %v", err)
	}

	out, _ := json.MarshalIndent(report, "", "  ")
	if report.DryRun {
		log.Printf("rmu cable backfill (DRY RUN -- nothing written):\n%s", out)
	} else {
		log.Printf("rmu cable backfill complete:\n%s", out)
	}
	if len(report.Errors) > 0 {
		log.Printf("%d RMU(s) had errors (see report above); other RMUs were processed normally", len(report.Errors))
	}
}
