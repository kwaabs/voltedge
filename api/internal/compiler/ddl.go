// Package compiler reads published metadata (via api/internal/metadata)
// and compiles it into real physical schema in the gis schema -- CREATE
// TABLE for a class compiled for the first time, ALTER TABLE ADD COLUMN
// for one that already has a table and gained a field. It never drops or
// alters an existing column: disabling a field is a metadata-level
// status flip (fields.status = deprecated), never a physical DROP
// COLUMN, matching docs/metadata-core.md's field lifecycle rules.
package compiler

import (
	"context"
	"fmt"
	"strings"

	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// PhysicalSchema is where every generated feature-class table lives --
// its own schema, separate from metadata (the compiler's own tables) and
// ops (infra bookkeeping), same "separate schemas for all tables"
// principle Phase 0 established. Reserved by migrations/0015.
const PhysicalSchema = "gis"

// postgresType maps a metadata.fields.data_type value to the Postgres
// column type it compiles to.
func postgresType(dataType string) (string, error) {
	switch dataType {
	case "text":
		return "text", nil
	case "numeric":
		return "numeric", nil
	case "integer":
		return "integer", nil
	case "boolean":
		return "boolean", nil
	case "date":
		return "date", nil
	case "timestamp":
		return "timestamptz", nil
	case "uuid":
		return "uuid", nil
	case "jsonb":
		return "jsonb", nil
	default:
		return "", fmt.Errorf("compiler: unknown data_type %q", dataType)
	}
}

// geometryType maps feature_classes.geometry_type to the PostGIS
// geometry subtype used in the generated geom column.
func geometryType(geomType string) (string, error) {
	switch geomType {
	case "point":
		return "Point", nil
	case "line":
		return "LineString", nil
	case "polygon":
		return "Polygon", nil
	default:
		return "", fmt.Errorf("compiler: geometry_type %q has no physical geom column", geomType)
	}
}

// columnDefinition renders one field as a column definition fragment,
// e.g. `voltage_class text CHECK (voltage_class IN ('11KV','33KV'))`.
// A NULL value always satisfies a Postgres CHECK, so a nullable field
// with a domain still permits NULL without a separate IS NULL clause.
func columnDefinition(f metadata.Field, domains map[string]*metadata.Domain, domainValues map[string][]string) (string, error) {
	pgType, err := postgresType(f.DataType)
	if err != nil {
		return "", fmt.Errorf("field %s: %w", f.Name, err)
	}

	def := fmt.Sprintf("%s %s", quoteIdent(f.Name), pgType)
	if !f.Nullable {
		def += " NOT NULL"
	}

	if f.DomainID != nil {
		domain, ok := domains[*f.DomainID]
		if !ok {
			return "", fmt.Errorf("field %s: domain %s not loaded", f.Name, *f.DomainID)
		}
		if domain.DomainType == "coded" {
			values := domainValues[domain.ID]
			if len(values) > 0 {
				def += fmt.Sprintf(" CHECK (%s IN (%s))", quoteIdent(f.Name), quoteLiteralList(values))
			}
			// Range domains have no min/max stored anywhere yet (domains/
			// domain_values only carry coded values) -- nothing to compile
			// into a CHECK for domain_type = 'range' until that's modeled.
		}
	}

	return def, nil
}

// quoteIdent wraps a Postgres identifier in double quotes. Field/table
// names in this system are always lower_snake_case set by an admin
// through the metadata UI, not user input, but quoting costs nothing and
// removes an entire class of "reserved word collides with a column name"
// bugs.
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func quoteLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func quoteLiteralList(values []string) string {
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = quoteLiteral(v)
	}
	return strings.Join(quoted, ", ")
}

// buildCreateTable renders the full CREATE TABLE statement for a feature
// class compiled for the first time: system columns (id, subtype if any,
// status, geom if any, created_at, updated_at) followed by one column
// per active field.
func buildCreateTable(qualifiedTable string, fc *metadata.FeatureClass, fields []metadata.Field, subtypes []metadata.Subtype, domains map[string]*metadata.Domain, domainValues map[string][]string) (string, error) {
	var cols []string

	cols = append(cols, `id uuid PRIMARY KEY DEFAULT gen_random_uuid()`)

	if len(subtypes) > 0 {
		codes := make([]string, len(subtypes))
		for i, s := range subtypes {
			codes[i] = fmt.Sprintf("%d", s.Code)
		}
		cols = append(cols, fmt.Sprintf(`subtype integer NOT NULL CHECK (subtype IN (%s))`, strings.Join(codes, ", ")))
	}

	cols = append(cols, `status text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'published'))`)

	// deleted_at is soft-delete, not a status value: a row can be deleted
	// regardless of draft/published, and NULL vs. set is a cheaper,
	// less error-prone check than adding a third status string everywhere
	// that already switches on 'draft'/'published'.
	cols = append(cols, `deleted_at timestamptz`)

	if fc.GeometryType != "none" {
		geomType, err := geometryType(fc.GeometryType)
		if err != nil {
			return "", err
		}
		cols = append(cols, fmt.Sprintf(`geom geometry(%s, %d) NOT NULL`, geomType, *fc.SRID))
	}

	for _, f := range fields {
		colDef, err := columnDefinition(f, domains, domainValues)
		if err != nil {
			return "", err
		}
		cols = append(cols, colDef)
	}

	cols = append(cols, `created_at timestamptz NOT NULL DEFAULT now()`)
	cols = append(cols, `updated_at timestamptz NOT NULL DEFAULT now()`)

	stmt := fmt.Sprintf("CREATE TABLE %s (\n    %s\n)", qualifiedTable, strings.Join(cols, ",\n    "))
	return stmt, nil
}

// tableExists checks information_schema, not pg_catalog directly, so this
// works identically regardless of search_path.
func tableExists(ctx context.Context, db bun.IDB, schema, table string) (bool, error) {
	exists, err := db.NewSelect().
		ColumnExpr("1").
		TableExpr("information_schema.tables").
		Where("table_schema = ?", schema).
		Where("table_name = ?", table).
		Exists(ctx)
	if err != nil {
		return false, fmt.Errorf("compiler: checking table existence: %w", err)
	}
	return exists, nil
}

// existingColumns returns the set of column names already on a physical
// table, so the compiler only ALTER TABLE ADD COLUMNs what's actually
// missing.
func existingColumns(ctx context.Context, db bun.IDB, schema, table string) (map[string]bool, error) {
	var names []string
	err := db.NewSelect().
		Column("column_name").
		TableExpr("information_schema.columns").
		Where("table_schema = ?", schema).
		Where("table_name = ?", table).
		Scan(ctx, &names)
	if err != nil {
		return nil, fmt.Errorf("compiler: loading existing columns: %w", err)
	}

	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	return set, nil
}
