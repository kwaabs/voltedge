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
