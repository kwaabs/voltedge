// Command resetdata is the break-glass factory reset -- the destructive sibling
// of api/cmd/authadmin. It wipes all operational/instance DATA while PRESERVING
// the configuration, then reduces the account set to the single break-glass
// admin who ran it and purges the object store so no orphaned blobs survive.
//
// What it CLEARS (truncated, identities restarted):
//   - every compiled feature table (gis.<code>, enumerated at runtime from
//     metadata.feature_classes.physical_table -- never hardcoded, so
//     admin-created classes are wiped too) plus feature history / GPS captures /
//     circuits,
//   - connectivity, relationships, external links, all diagrams and map
//     annotations (runtime.*),
//   - outages and switching orders (oms.*),
//   - defects, work orders, inspections and their results (wam.*),
//   - export jobs, field packs, the sync inbox (ops.*),
//   - attachment metadata (files.*) and the object-store blobs behind it,
//   - sessions and the auth audit trail (iam.*),
//   - every account except the surviving break-glass admin.
//
// What it PRESERVES (the "clean slate, same system" contract):
//   - the entire metadata schema: feature-class definitions, subtypes/fields,
//     symbology, network/trace/snapping rules, CIM mappings, calc-formula tables
//     (material_specs, protection_curves), assembly/RMU/panel templates,
//   - wam inspection templates, gis territory tree, ops migration_log +
//     sync_policies,
//   - iam roles/permissions/basemaps/system_settings, and the one break-glass
//     admin (with its identity, roles, tokens, territory grants).
//
// It talks straight to the database (as whoever holds DATABASE_URL) and to
// RustFS (RUSTFS_*), independent of the running API. Only a break-glass admin
// may run it: the tool authenticates one by email + password before it touches
// anything, and that same admin is the sole survivor. The password is read from
// RESETDATA_PASSWORD (never a flag), like authadmin.
//
// Usage:
//
//	RESETDATA_PASSWORD=... resetdata --admin-email a@b.com          # DRY RUN: print the plan
//	RESETDATA_PASSWORD=... resetdata --admin-email a@b.com --yes    # execute the wipe
//
// Without --yes it authenticates, prints exactly what it WOULD truncate, and
// exits without changing anything.
//
// In production there is no Go toolchain or Taskfile to `go run` this from: the
// binary is baked into the api image (api/Dockerfile) and run against the live
// deployment with the api service's own DATABASE_URL + RUSTFS_* environment:
//
//	docker compose exec -e RESETDATA_PASSWORD=... api resetdata --admin-email a@b.com --yes
//
// The `task reset-data` / `task reset-data-plan` wrappers are dev-only sugar.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/export"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
)

// staticDataTables are the fixed (non-compiled) data tables truncated on a
// reset, in addition to the dynamic gis.<code> feature tables discovered at
// runtime. Everything here is instance/operational data or an audit trail --
// never a config table. TRUNCATE ... CASCADE is bounded to this set plus the
// compiled tables: no KEEP (config) table has an FK referencing any of them, so
// the cascade cannot reach configuration.
//
// The RESTRICT user-FKs (export_jobs/field_packs/sync_inbox.requested_by/
// submitted_by/reviewed_by) all live on tables in THIS list, so they are emptied
// before the account reduction below and cannot block it.
var staticDataTables = []string{
	// gis: instance history / derived (the compiled gis.<code> tables are added
	// dynamically; gis.territories + territory_levels are KEEP config).
	"gis.feature_history",
	"gis.feature_gps_captures",
	"gis.circuits",
	// runtime: connectivity, relationships, external links, diagrams, annotations.
	"runtime.feature_connectivity",
	"runtime.feature_relationships",
	"runtime.feature_external_links",
	"runtime.diagram_instances",
	"runtime.diagram_placements",
	"runtime.diagram_elements",
	"runtime.diagram_templates",
	"runtime.map_annotations",
	"runtime.asset_tags",
	// oms: outages + planned switching.
	"oms.incidents",
	"oms.trouble_calls",
	"oms.customer_interruptions",
	"oms.switching_orders",
	"oms.switching_steps",
	// wam: operational rows (inspection TEMPLATES + template_items are KEEP).
	"wam.defects",
	"wam.work_orders",
	"wam.inspections",
	"wam.inspection_item_results",
	// ops: jobs / packs / sync (migration_log + sync_policies are KEEP).
	"ops.export_jobs",
	"ops.field_packs",
	"ops.sync_inbox",
	// files: attachment metadata (the blobs are purged from the object store).
	"files.attachments",
	// iam: sessions + the auth audit trail (users are reduced separately;
	// roles/permissions/basemaps/system_settings are KEEP).
	"iam.sessions",
	"iam.auth_events",
}

// gisTableRe guards the physical_table values read from metadata before they are
// interpolated into a TRUNCATE. The compiler stamps them as gis.<snake_code>;
// anything outside that shape is refused rather than truncated.
var gisTableRe = regexp.MustCompile(`^gis\.[a-z0-9_]+$`)

func main() {
	log.SetFlags(0)

	fs := flag.NewFlagSet("resetdata", flag.ExitOnError)
	adminEmail := fs.String("admin-email", "", "break-glass admin authorizing the reset (password from RESETDATA_PASSWORD)")
	yes := fs.Bool("yes", false, "actually perform the wipe; without it, authenticate and print the plan only")
	_ = fs.Parse(os.Args[1:])

	if *adminEmail == "" {
		log.Fatal("resetdata: --admin-email is required")
	}
	pw := os.Getenv("RESETDATA_PASSWORD")
	if pw == "" {
		log.Fatal("resetdata: RESETDATA_PASSWORD is not set (the password is read from the environment, not a flag)")
	}

	ctx := context.Background()
	db := openDB()
	defer db.Close()

	// Authorization gate: only a break-glass admin, with the right password, may
	// proceed -- and that same account is the sole survivor.
	survivor, err := auth.VerifyBreakGlassAdmin(ctx, db, *adminEmail, pw)
	if err != nil {
		log.Fatalf("resetdata: %v", err)
	}

	compiled, err := compiledFeatureTables(ctx, db)
	if err != nil {
		log.Fatalf("resetdata: enumerating feature tables: %v", err)
	}
	targets := append(append([]string{}, staticDataTables...), compiled...)

	if !*yes {
		fmt.Println("DRY RUN -- nothing will be changed. Pass --yes to execute.")
		fmt.Printf("authenticated break-glass survivor: %s (%s)\n", *adminEmail, survivor)
		fmt.Printf("would TRUNCATE %d data tables (%d compiled feature tables):\n", len(targets), len(compiled))
		for _, t := range targets {
			fmt.Printf("  %s\n", t)
		}
		fmt.Println("would DELETE every account except the survivor, then PURGE the object store.")
		return
	}

	// 1. Truncate all data tables and reduce accounts in one transaction: either
	// the database ends up wiped-and-reduced, or nothing changes.
	if err := wipe(ctx, db, targets, survivor); err != nil {
		log.Fatalf("resetdata: wipe failed (transaction rolled back, nothing changed): %v", err)
	}
	fmt.Printf("truncated %d data tables; reduced accounts to %s\n", len(targets), *adminEmail)

	// 2. Purge the object store AFTER the DB commit. Ordering matters: if the DB
	// commit succeeds but the purge fails, the worst case is orphaned blobs (the
	// rows that pointed at them are already gone), which a re-run clears -- never
	// dangling rows pointing at deleted blobs.
	store, err := export.NewObjectStoreFromEnv()
	if err != nil {
		log.Fatalf("resetdata: data wiped, but object store is not configured so blobs remain (%v); set RUSTFS_* and re-run to purge", err)
	}
	n, err := store.Purge(ctx)
	if err != nil {
		log.Fatalf("resetdata: data wiped, but object-store purge failed after removing %d object(s) (blobs may remain, re-run to finish): %v", n, err)
	}
	fmt.Printf("purged %d object(s) from the object store\n", n)
	fmt.Println("factory reset complete -- configuration preserved, data cleared.")
}

func openDB() *bun.DB {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is not set")
	}
	sqldb := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(databaseURL)))
	return bun.NewDB(sqldb, pgdialect.New())
}

// compiledFeatureTables returns the physical tables of every compiled feature
// class (metadata.feature_classes.physical_table), validated and with the KEEP
// config tables filtered out. Enumerating at runtime -- rather than hardcoding a
// list -- is what makes the reset wipe admin-created classes too. Each value is
// checked against gisTableRe before it can reach a TRUNCATE statement.
func compiledFeatureTables(ctx context.Context, db *bun.DB) ([]string, error) {
	var rows []struct {
		PhysicalTable string `bun:"physical_table"`
	}
	err := db.NewSelect().
		ColumnExpr("physical_table").
		TableExpr("metadata.feature_classes").
		Where("physical_table IS NOT NULL").
		Where("physical_table <> ''").
		OrderExpr("physical_table").
		Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}

	out := make([]string, 0, len(rows))
	for _, r := range rows {
		t := strings.ToLower(strings.TrimSpace(r.PhysicalTable))
		if !gisTableRe.MatchString(t) {
			return nil, fmt.Errorf("refusing to truncate suspicious physical_table %q", r.PhysicalTable)
		}
		// The territory tree is config, not data -- never truncate it even if a
		// class were somehow registered against it.
		if t == "gis.territories" || t == "gis.territory_levels" {
			continue
		}
		out = append(out, t)
	}
	return out, nil
}

// wipe truncates every target data table and reduces the account set to the
// surviving break-glass admin, all in one transaction.
func wipe(ctx context.Context, db *bun.DB, targets []string, survivor string) error {
	return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if len(targets) > 0 {
			// RESTART IDENTITY resets any bigserial counters; CASCADE only reaches
			// FK-children that are themselves in the data set (no KEEP table
			// references these), so it truncates dependents in one shot.
			stmt := "TRUNCATE TABLE " + strings.Join(targets, ", ") + " RESTART IDENTITY CASCADE"
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("truncating data tables: %w", err)
			}
		}

		// iam.system_settings.updated_by is the one FK into iam.users on a KEEP
		// table with no ON DELETE rule (NO ACTION) -- it would block deleting a
		// non-survivor who last touched settings. Release it first (all other
		// KEEP-table user FKs are CASCADE or SET NULL).
		if _, err := tx.ExecContext(ctx,
			"UPDATE iam.system_settings SET updated_by = NULL WHERE updated_by IS NOT NULL AND updated_by <> ?", survivor,
		); err != nil {
			return fmt.Errorf("releasing settings ownership: %w", err)
		}

		// Reduce to the single break-glass survivor. CASCADE clears its identities,
		// roles, tokens, and territory grants; SET NULL detaches audit/manager
		// references; the RESTRICT tables were truncated above.
		if _, err := tx.ExecContext(ctx, "DELETE FROM iam.users WHERE id <> ?", survivor); err != nil {
			return fmt.Errorf("reducing accounts: %w", err)
		}
		return nil
	})
}
