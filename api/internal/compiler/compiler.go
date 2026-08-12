package compiler

import (
	"context"
	"fmt"
	"strings"
	"time"

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

// Compile reads the metadata for one feature class by code and makes the
// physical schema in gis match it: CREATE TABLE the first time, ALTER
// TABLE ADD COLUMN for any active field that doesn't have a column yet.
// It never drops or alters an existing column -- see the package comment
// in models.go.
//
// This is intentionally scoped to one feature class per call. Compiling
// several classes as one atomic metadata_versions publish is future
// work, not this pass -- see docs/phase-1-metadata-core.md.
func Compile(ctx context.Context, db *bun.DB, code string) (*Result, error) {
	fc := new(FeatureClass)
	if err := db.NewSelect().Model(fc).Where("code = ?", code).Scan(ctx); err != nil {
		return nil, fmt.Errorf("compiler: feature class %q not found: %w", code, err)
	}

	if fc.GeometryType != "none" && fc.SRID == nil {
		return nil, fmt.Errorf("compiler: feature class %q has geometry_type %q but no srid", code, fc.GeometryType)
	}

	var fields []Field
	if err := db.NewSelect().Model(&fields).
		Where("feature_class_id = ?", fc.ID).
		Where("status = ?", "active").
		Order("name").
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("compiler: loading fields for %q: %w", code, err)
	}

	var subtypes []Subtype
	if err := db.NewSelect().Model(&subtypes).
		Where("feature_class_id = ?", fc.ID).
		Order("code").
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("compiler: loading subtypes for %q: %w", code, err)
	}

	domains, domainValues, err := loadDomains(ctx, db, fields)
	if err != nil {
		return nil, fmt.Errorf("compiler: %q: %w", code, err)
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
			stmt, err := buildCreateTable(qualifiedTable, fc, fields, subtypes, domains, domainValues)
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
			for _, f := range fields {
				if existingCols[f.Name] {
					continue
				}
				colDef, err := columnDefinition(f, domains, domainValues)
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

		version := &MetadataVersion{
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

// loadDomains fetches, once each, every domain (and its coded values)
// referenced by the given fields.
func loadDomains(ctx context.Context, db *bun.DB, fields []Field) (map[string]*Domain, map[string][]string, error) {
	domains := map[string]*Domain{}
	domainValues := map[string][]string{}

	for _, f := range fields {
		if f.DomainID == nil {
			continue
		}
		if _, ok := domains[*f.DomainID]; ok {
			continue
		}

		d := new(Domain)
		if err := db.NewSelect().Model(d).Where("id = ?", *f.DomainID).Scan(ctx); err != nil {
			return nil, nil, fmt.Errorf("field %s references missing domain %s: %w", f.Name, *f.DomainID, err)
		}
		domains[d.ID] = d

		if d.DomainType != "coded" {
			continue
		}

		var values []DomainValue
		if err := db.NewSelect().Model(&values).
			Where("domain_id = ?", d.ID).
			Order("sort_order").
			Scan(ctx); err != nil {
			return nil, nil, fmt.Errorf("loading values for domain %s: %w", d.Code, err)
		}
		codes := make([]string, len(values))
		for i, v := range values {
			codes[i] = v.Code
		}
		domainValues[d.ID] = codes
	}

	return domains, domainValues, nil
}
