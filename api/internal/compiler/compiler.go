package compiler

import (
	"context"
	"fmt"
	"strings"
	"time"

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
		}

		version := &metadata.Version{
			PublishedAt: time.Now(),
			PublishedBy: "compiler-cli",
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
