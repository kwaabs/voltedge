package compiler

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// Result summarizes what Compile actually did, for the CLI to report.
type Result struct {
	FeatureClassCode  string
	PhysicalTable     string
	Created           bool
	ColumnsAdded      []string
	MetadataVersionID string
}

// Compile reads the metadata for one feature class by code (via
// api/internal/metadata) and makes the physical schema in gis match it:
// CREATE TABLE the first time, ALTER TABLE ADD COLUMN for any active
// field that doesn't have a column yet. It never drops or alters an
// existing column -- see the package comment in ddl.go.
//
// This is intentionally scoped to one feature class per call. Compiling
// several classes as one atomic metadata_versions publish is future
// work, not this pass -- see docs/phase-1-metadata-core.md.
func Compile(ctx context.Context, db *bun.DB, code string) (*Result, error) {
	loaded, err := metadata.Load(ctx, db, code)
	if err != nil {
		return nil, fmt.Errorf("compiler: %w", err)
	}
	fc := loaded.FeatureClass

	if fc.GeometryType != "none" && fc.SRID == nil {
		return nil, fmt.Errorf("compiler: feature class %q has geometry_type %q but no srid", code, fc.GeometryType)
	}

	tableName := strings.ToLower(fc.Code)
	qualifiedTable := PhysicalSchema + "." + tableName

	exists, err := tableExists(ctx, db, PhysicalSchema, tableName)
	if err != nil {
		return nil, err
	}

	result := &Result{FeatureClassCode: fc.Code, PhysicalTable: qualifiedTable}

	err = db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if !exists {
			stmt, err := buildCreateTable(qualifiedTable, fc, loaded.Fields, loaded.Subtypes, loaded.Domains, loaded.DomainValues)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("create table: %w", err)
			}
			result.Created = true

			if fc.GeometryType != "none" {
				idxStmt := fmt.Sprintf(`CREATE INDEX %s ON %s USING GIST (geom)`,
					quoteIdent(tableName+"_geom_idx"), qualifiedTable)
				if _, err := tx.ExecContext(ctx, idxStmt); err != nil {
					return fmt.Errorf("create spatial index: %w", err)
				}
			}
		} else {
			existingCols, err := existingColumns(ctx, tx, PhysicalSchema, tableName)
			if err != nil {
				return err
			}

			// deleted_at was added to buildCreateTable after some feature
			// classes were already compiled -- back it onto any table that
			// predates it, the same way a new field gets ALTER TABLE ADD
			// COLUMN'd in below. Nullable with no default beyond Postgres's
			// implicit NULL, so this is always safe to run.
			if !existingCols["deleted_at"] {
				stmt := fmt.Sprintf("ALTER TABLE %s ADD COLUMN deleted_at timestamptz", qualifiedTable)
				if _, err := tx.ExecContext(ctx, stmt); err != nil {
					return fmt.Errorf("add column deleted_at: %w", err)
				}
				result.ColumnsAdded = append(result.ColumnsAdded, "deleted_at")
			}

			// Same backfill pattern for name -- added to buildCreateTable
			// after every existing feature class was already compiled.
			if !existingCols["name"] {
				stmt := fmt.Sprintf("ALTER TABLE %s ADD COLUMN name text", qualifiedTable)
				if _, err := tx.ExecContext(ctx, stmt); err != nil {
					return fmt.Errorf("add column name: %w", err)
				}
				result.ColumnsAdded = append(result.ColumnsAdded, "name")
			}

			// created_by / updated_by (Slice E) -- the audit columns the
			// CRUD engine stamps the acting user into. Migration 0026
			// already backfilled every table that existed at deploy time;
			// this mirrors deleted_at/name so a recompile also covers any
			// table that predates the column, keeping the compiler the
			// single source of truth for a feature table's shape. Plain
			// uuid, deliberately not a FK to iam.users -- same no-enforced-
			// join decision as the established_by/published_by text columns.
			for _, col := range []string{"created_by", "updated_by"} {
				if existingCols[col] {
					continue
				}
				stmt := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s uuid", qualifiedTable, col)
				if _, err := tx.ExecContext(ctx, stmt); err != nil {
					return fmt.Errorf("add column %s: %w", col, err)
				}
				result.ColumnsAdded = append(result.ColumnsAdded, col)
			}

			// published_at / published_by (Private-drafts, Phase 61) -- the
			// sticky first-publish markers buildCreateTable emits for a fresh
			// table. Backfilled onto any table compiled before they existed,
			// mirroring the created_by/updated_by pattern above so a recompile
			// keeps the compiler the single source of truth for a feature
			// table's shape. Both nullable with no default: an existing row
			// gets published_at = NULL, which is exactly right -- a row that has
			// never been through the publish path has never been public.
			// published_at is timestamptz, published_by is a bare uuid (no FK,
			// same convention as created_by).
			if !existingCols["published_at"] {
				stmt := fmt.Sprintf("ALTER TABLE %s ADD COLUMN published_at timestamptz", qualifiedTable)
				if _, err := tx.ExecContext(ctx, stmt); err != nil {
					return fmt.Errorf("add column published_at: %w", err)
				}
				result.ColumnsAdded = append(result.ColumnsAdded, "published_at")
			}
			if !existingCols["published_by"] {
				stmt := fmt.Sprintf("ALTER TABLE %s ADD COLUMN published_by uuid", qualifiedTable)
				if _, err := tx.ExecContext(ctx, stmt); err != nil {
					return fmt.Errorf("add column published_by: %w", err)
				}
				result.ColumnsAdded = append(result.ColumnsAdded, "published_by")
			}

			// external_ref (0145) -- the jsonb system column holding this feature's ids in
				// external systems (SCADA/AMI/CIS). All classes get it; nullable, no
				// default, so an existing row starts with no refs. The per-class sync
				// trigger (below) mirrors it into runtime.external_feature_refs.
				if !existingCols["external_ref"] {
					stmt := fmt.Sprintf("ALTER TABLE %s ADD COLUMN external_ref jsonb", qualifiedTable)
					if _, err := tx.ExecContext(ctx, stmt); err != nil {
						return fmt.Errorf("add column external_ref: %w", err)
					}
					result.ColumnsAdded = append(result.ColumnsAdded, "external_ref")
				}

				// Asset lifecycle -- the system axis buildCreateTable now emits on a
				// fresh table, backfilled onto any table compiled before it existed,
				// the same single-source-of-truth pattern as external_ref above.
				// lifecycle_status is added NOT NULL DEFAULT 'IN_SERVICE' with the
				// coded-value CHECK, so every existing row backfills to IN_SERVICE
				// (an asset already in the system is in service) in one ALTER. The
				// two dates are nullable. The DEFAULT is intentionally left on the
				// column so a plain INSERT that omits the status is in-service too.
				if !existingCols["lifecycle_status"] {
					stmt := fmt.Sprintf("ALTER TABLE %s ADD COLUMN lifecycle_status text NOT NULL DEFAULT 'IN_SERVICE' CHECK (lifecycle_status IN ('PLANNED', 'UNDER_CONSTRUCTION', 'IN_SERVICE', 'DERATED', 'RETIRED', 'REMOVED'))", qualifiedTable)
					if _, err := tx.ExecContext(ctx, stmt); err != nil {
						return fmt.Errorf("add column lifecycle_status: %w", err)
					}
					result.ColumnsAdded = append(result.ColumnsAdded, "lifecycle_status")
				}
				if !existingCols["commissioned_at"] {
					stmt := fmt.Sprintf("ALTER TABLE %s ADD COLUMN commissioned_at date", qualifiedTable)
					if _, err := tx.ExecContext(ctx, stmt); err != nil {
						return fmt.Errorf("add column commissioned_at: %w", err)
					}
					result.ColumnsAdded = append(result.ColumnsAdded, "commissioned_at")
				}
				if !existingCols["retired_at"] {
					stmt := fmt.Sprintf("ALTER TABLE %s ADD COLUMN retired_at date", qualifiedTable)
					if _, err := tx.ExecContext(ctx, stmt); err != nil {
						return fmt.Errorf("add column retired_at: %w", err)
					}
					result.ColumnsAdded = append(result.ColumnsAdded, "retired_at")
				}

				// assembly_ref (0192) -- the marker system column a placed assembly's
				// anchor carries (the template `code`), so a map-context symbology rule
				// can give the assembly its own map glyph without a new class. All
				// classes get it; nullable, no default. buildCreateTable emits it on a
				// fresh table; this backfills any table compiled before it existed.
				if !existingCols["assembly_ref"] {
					stmt := fmt.Sprintf("ALTER TABLE %s ADD COLUMN assembly_ref text", qualifiedTable)
					if _, err := tx.ExecContext(ctx, stmt); err != nil {
						return fmt.Errorf("add column assembly_ref: %w", err)
					}
					result.ColumnsAdded = append(result.ColumnsAdded, "assembly_ref")
				}

				// territory_id (Phase 11) -- only geometric classes get it; a
			// geometry-less class has nothing to place in a territory.
			// Mirrors the created_by backfill so a recompile also covers a
			// table compiled before the column existed, alongside migration
			// 0028's own deploy-time backfill, keeping the compiler the
			// single source of truth for a feature table's shape.
			if fc.GeometryType != "none" && !existingCols["territory_id"] {
				stmt := fmt.Sprintf("ALTER TABLE %s ADD COLUMN territory_id uuid", qualifiedTable)
				if _, err := tx.ExecContext(ctx, stmt); err != nil {
					return fmt.Errorf("add column territory_id: %w", err)
				}
				result.ColumnsAdded = append(result.ColumnsAdded, "territory_id")
			}

			// geometry_optional (Phase 26): buildCreateTable emits a nullable
			// geom for a fresh table, but a class already compiled with the
			// historic NOT NULL needs its constraint relaxed on recompile once
			// the flag is set. DROP NOT NULL is a safe, relaxing change and a
			// no-op if the column is already nullable, so this runs whenever
			// the flag is set on a geometric table -- the same
			// single-source-of-truth reasoning as the column backfills above.
			// The reverse (clearing the flag while null rows exist) is left to
			// Postgres's own SET NOT NULL to reject loudly, matching
			// syncCheckConstraints' narrowing philosophy.
			if fc.GeometryType != "none" && fc.GeometryOptional && existingCols["geom"] {
				stmt := fmt.Sprintf("ALTER TABLE %s ALTER COLUMN geom DROP NOT NULL", qualifiedTable)
				if _, err := tx.ExecContext(ctx, stmt); err != nil {
					return fmt.Errorf("drop not null on geom: %w", err)
				}
			}

			// subtype -- a class first compiled with zero subtypes has no
			// subtype column (buildCreateTable only emits it when subtypes
			// exist); gaining its first subtype later must backfill the column
			// before syncCheckConstraints can constrain it, mirroring the
			// system-column backfills above. Added NOT NULL with no default,
			// exactly as buildCreateTable emits it: on an empty table (a class
			// typically gains subtypes before any features exist) this succeeds,
			// and on a table that already has rows Postgres rejects it loudly --
			// the correct outcome, since there is no sensible subtype to
			// retro-assign existing rows. The CHECK is added right after by
			// syncCheckConstraints, so only the bare column is added here.
			if len(loaded.Subtypes) > 0 && !existingCols["subtype"] {
				stmt := fmt.Sprintf("ALTER TABLE %s ADD COLUMN subtype integer NOT NULL", qualifiedTable)
				if _, err := tx.ExecContext(ctx, stmt); err != nil {
					return fmt.Errorf("add column subtype: %w", err)
				}
				result.ColumnsAdded = append(result.ColumnsAdded, "subtype")
			}

			for _, f := range loaded.Fields {
				if existingCols[f.Name] {
					continue
				}
				colDef, err := columnDefinition(f, loaded.Domains, loaded.DomainValues)
				if err != nil {
					return err
				}
				stmt := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s", qualifiedTable, colDef)
				if _, err := tx.ExecContext(ctx, stmt); err != nil {
					return fmt.Errorf("add column %s: %w", f.Name, err)
				}
				result.ColumnsAdded = append(result.ColumnsAdded, f.Name)
			}

			// Rebuilds the subtype/coded-domain CHECK constraints from
			// current metadata every recompile -- not just when a column
			// was added -- so a subtype or domain value added to
			// already-compiled metadata (api/internal/admin's own
			// add-subtype/add-domain-value endpoints) actually takes
			// effect on the physical table once recompiled. See
			// syncCheckConstraints' own doc comment (ddl.go) for why this
			// is safe.
			if err := syncCheckConstraints(ctx, tx, qualifiedTable, tableName, loaded.Fields, loaded.Subtypes, loaded.Domains, loaded.DomainValues); err != nil {
				return err
			}
		}

		// Refresh the Martin tile function for this class so the map serves it
		// as MVT filtered on deleted_at IS NULL -- migration 0038 and
		// infra/martin/config.yaml. gis.sync_tile_function introspects the
		// table's current columns, so calling it on every compile (a fresh
		// CREATE TABLE or a field ADD COLUMN alike) keeps the function's
		// properties in step with the table's shape -- the same
		// single-source-of-truth reasoning as the column backfills above.
		// Only geometric classes are tile sources; a geometry-less class has no
		// geom column and is never published to Martin, so it gets no function.
		if fc.GeometryType != "none" {
			if _, err := tx.ExecContext(ctx, "SELECT gis.sync_tile_function(?, ?)", tableName, *fc.SRID); err != nil {
				return fmt.Errorf("sync tile function: %w", err)
			}

			// Phase 58: the Go MVT tile server and the CRUD read/write scoping
			// both filter on territory_id (`territory_id IN (...)` for a scoped
			// user), so give every geometric table a btree index on it. IF NOT
			// EXISTS makes this idempotent across recompiles and covers tables
			// that predate the column's backfill above, the same
			// single-source-of-truth reasoning as the tile function sync. The
			// GIST geom index handles the bbox && filter; this handles the
			// territory equality.
			idxStmt := fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s ON %s (territory_id)`,
				quoteIdent(tableName+"_territory_id_idx"), qualifiedTable)
			if _, err := tx.ExecContext(ctx, idxStmt); err != nil {
				return fmt.Errorf("create territory_id index: %w", err)
			}
		}

		// Keep runtime.feature_registry (migration 0108) in step with this
		// class. The registry is a single cross-class (feature_class_code,
		// feature_id) index that resolves an asset id without knowing which
		// gis.<code> table holds it -- the asset-id picker and the bare
		// (class, id) references faults/oms/wam carry both read from it. It is
		// maintained by a per-table AFTER INSERT/UPDATE/DELETE trigger, NOT the
		// Go CRUD layer, because features are also written by raw-SQL seeds and
		// field-sync replay that never pass through crud; only a DB trigger
		// catches every write path. Re-attaching on every compile (idempotent
		// via DROP IF EXISTS) is the same single-source-of-truth reasoning as
		// the tile-function sync above -- a freshly created table gets its
		// trigger, an existing one keeps a correct one. All classes register,
		// geometric or not (the picker indexes every asset). territory_id lives
		// only on geometric tables, so the backfill's SELECT reads it only
		// there; the trigger function itself reads it generically via to_jsonb.
		regTrigger := quoteIdent(tableName + "_registry_sync")
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(
			`DROP TRIGGER IF EXISTS %s ON %s`, regTrigger, qualifiedTable)); err != nil {
			return fmt.Errorf("drop registry trigger: %w", err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(
			`CREATE TRIGGER %s AFTER INSERT OR UPDATE OR DELETE ON %s `+
				`FOR EACH ROW EXECUTE FUNCTION runtime.sync_feature_registry(%s)`,
			regTrigger, qualifiedTable, quoteLiteral(fc.Code))); err != nil {
			return fmt.Errorf("create registry trigger: %w", err)
		}
		territoryExpr := "NULL::uuid"
		if fc.GeometryType != "none" {
			territoryExpr = "territory_id"
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(
			`INSERT INTO runtime.feature_registry `+
				`(feature_id, feature_class_code, status, deleted_at, created_at, territory_id) `+
				`SELECT id, %s, status, deleted_at, created_at, %s FROM %s `+
				`ON CONFLICT (feature_id) DO NOTHING`,
			quoteLiteral(fc.Code), territoryExpr, qualifiedTable)); err != nil {
			return fmt.Errorf("backfill feature_registry: %w", err)
		}

		// external_ref reverse index (0145): mirror the registry trigger -- attach
		// runtime.sync_external_feature_refs so each row's external_ref jsonb is
		// indexed in runtime.external_feature_refs for O(1) inbound correlation.
		// No backfill: external_ref is added nullable, so existing rows carry NULL
		// -> nothing to index until set; the trigger fires from the next write on.
		extTrigger := quoteIdent(tableName + "_external_ref_sync")
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(
			`DROP TRIGGER IF EXISTS %s ON %s`, extTrigger, qualifiedTable)); err != nil {
			return fmt.Errorf("drop external-ref trigger: %w", err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(
			`CREATE TRIGGER %s AFTER INSERT OR UPDATE OR DELETE ON %s `+
				`FOR EACH ROW EXECUTE FUNCTION runtime.sync_external_feature_refs(%s)`,
			extTrigger, qualifiedTable, quoteLiteral(fc.Code))); err != nil {
			return fmt.Errorf("create external-ref trigger: %w", err)
		}

			// published_by (text) carries the acting user's uuid when compile
		// was triggered through the admin HTTP route (Slice E); it falls
		// back to the "compiler-cli" system literal on the session-less
		// cmd/compile path, which has no request context to read an actor
		// from.
		publishedBy := "compiler-cli"
		if actor := auth.ActorID(ctx); actor != "" {
			publishedBy = actor
		}
		version := &metadata.Version{
			PublishedAt: time.Now(),
			PublishedBy: publishedBy,
			Status:      "active",
		}
		if _, err := tx.NewInsert().Model(version).Returning("id").Exec(ctx); err != nil {
			return fmt.Errorf("insert metadata_versions: %w", err)
		}
		result.MetadataVersionID = version.ID

		if _, err := tx.NewUpdate().Model(fc).
			Set("physical_table = ?", qualifiedTable).
			Set("status = ?", "active").
			Set("metadata_version_id = ?", version.ID).
			Where("id = ?", fc.ID).
			Exec(ctx); err != nil {
			return fmt.Errorf("update feature_classes: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return result, nil
}
