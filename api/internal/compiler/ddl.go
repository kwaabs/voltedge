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
	"database/sql"
	"fmt"
	"strconv"
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
		switch domain.DomainType {
		case "coded":
			values := domainValues[domain.ID]
			if len(values) > 0 {
				def += fmt.Sprintf(" CHECK (%s IN (%s))", quoteIdent(f.Name), quoteLiteralList(values))
			}
		case "range":
			// RangeMin/RangeMax are both nullable (migrations/0020) -- a
			// range domain with either unset compiles with no CHECK at
			// all, same as a coded domain with zero values.
			if domain.RangeMin != nil && domain.RangeMax != nil {
				def += fmt.Sprintf(" CHECK (%s BETWEEN %s AND %s)",
					quoteIdent(f.Name), formatNumber(*domain.RangeMin), formatNumber(*domain.RangeMax))
			}
		}
	}

	return def, nil
}

// formatNumber renders a domain's range bound as a plain decimal literal
// -- strconv's 'f' format never falls back to scientific notation the way
// fmt's %g can for very large/small values, which would otherwise emit
// invalid-looking (though still parseable) SQL like "1e+06".
func formatNumber(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
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
// class compiled for the first time: system columns (id, name, subtype
// if any, status, geom if any, created_at, updated_at, created_by,
// updated_by, territory_id if geometric) followed by one column per
// active field.
func buildCreateTable(qualifiedTable string, fc *metadata.FeatureClass, fields []metadata.Field, subtypes []metadata.Subtype, domains map[string]*metadata.Domain, domainValues map[string][]string) (string, error) {
	var cols []string

	cols = append(cols, `id uuid PRIMARY KEY DEFAULT gen_random_uuid()`)

	// A human-assigned label, distinct from any metadata field: every
	// feature class gets one identically, an admin never opts a class
	// in or out of it the way they would a real attribute. Optional
	// (nullable, no default) -- a feature is fully valid with none set,
	// identified by its class and id alone, same as before this column
	// existed.
	cols = append(cols, `name text`)

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
		// geometry_optional (Phase 26) lets a feature exist without map
		// geometry (map-only / diagram-only / both, per feature), so the
		// column is nullable; default false keeps the historic NOT NULL.
		// geometry_type still decides the column's geometry type -- this only
		// governs the null constraint.
		nullability := " NOT NULL"
		if fc.GeometryOptional {
			nullability = ""
		}
		cols = append(cols, fmt.Sprintf(`geom geometry(%s, %d)%s`, geomType, *fc.SRID, nullability))
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

	// Audit actor (Slice E): the CRUD engine stamps the acting user's id
	// here on insert/update. Nullable (a session-less write leaves it
	// NULL), and deliberately not a FK to iam.users -- the same no-
	// enforced-join decision as the established_by/published_by columns,
	// and it keeps the gis schema free of a hard dependency on iam.
	cols = append(cols, `created_by uuid`)
	cols = append(cols, `updated_by uuid`)

	// The territory this feature belongs to (Phase 11) -- only for classes
	// that actually carry a geometry to place; a geometry-less class has
	// nothing to contain in a territory, so it never gets the column.
	// Nullable, and deliberately NOT a FK to gis.territories: the same
	// no-enforced-join convention as created_by/updated_by. Spatial
	// assignment and the "at least a district" rule are enforced in the
	// CRUD write path, not by the database (docs/phase-11-territories.md).
	if fc.GeometryType != "none" {
		cols = append(cols, `territory_id uuid`)
	}

	stmt := fmt.Sprintf("CREATE TABLE %s (\n    %s\n)", qualifiedTable, strings.Join(cols, ",\n    "))
	return stmt, nil
}

// syncCheckConstraints rebuilds the subtype CHECK constraint and every
// domain-linked field's CHECK constraint (coded or range) on an
// already-compiled table to match current metadata -- called every time
// Compile runs on an existing table, not just when a column is missing.
// This is what makes it safe to add a new subtype to an already-compiled
// class, or a new value to a domain a compiled field already uses:
// recompiling widens the constraint to match. There's no admin endpoint
// that edits an existing range domain's bounds today, so the range branch
// here has nothing to widen yet in practice -- it exists so that if one
// is ever added, it works the same way the coded case already does,
// without a second compiler change.
//
// Safe because it only ever DROP+ADDs the constraint itself, never
// touches a row -- widening (the only kind of change
// api/internal/admin's own "add a subtype"/"add a domain value"
// endpoints can ever produce) trivially satisfies existing data.
// Postgres validates ADD CONSTRAINT against existing rows by default;
// if metadata ever narrowed instead (outside this API's own
// additive-only surface -- there's no "remove a subtype" endpoint),
// the ADD CONSTRAINT would fail outright naming the offending rows,
// a safe loud failure rather than silently applying a narrower rule.
func syncCheckConstraints(ctx context.Context, tx bun.Tx, qualifiedTable, tableName string, fields []metadata.Field, subtypes []metadata.Subtype, domains map[string]*metadata.Domain, domainValues map[string][]string) error {
	if len(subtypes) > 0 {
		codes := make([]string, len(subtypes))
		for i, s := range subtypes {
			codes[i] = fmt.Sprintf("%d", s.Code)
		}
		if err := replaceCheckConstraint(ctx, tx, qualifiedTable, tableName, "subtype",
			fmt.Sprintf("subtype IN (%s)", strings.Join(codes, ", "))); err != nil {
			return err
		}
	}

	for _, f := range fields {
		if f.DomainID == nil {
			continue
		}
		domain, ok := domains[*f.DomainID]
		if !ok {
			continue
		}
		switch domain.DomainType {
		case "coded":
			values := domainValues[domain.ID]
			if len(values) == 0 {
				continue
			}
			if err := replaceCheckConstraint(ctx, tx, qualifiedTable, tableName, f.Name,
				fmt.Sprintf("%s IN (%s)", quoteIdent(f.Name), quoteLiteralList(values))); err != nil {
				return err
			}
		case "range":
			if domain.RangeMin == nil || domain.RangeMax == nil {
				continue
			}
			if err := replaceCheckConstraint(ctx, tx, qualifiedTable, tableName, f.Name,
				fmt.Sprintf("%s BETWEEN %s AND %s", quoteIdent(f.Name), formatNumber(*domain.RangeMin), formatNumber(*domain.RangeMax))); err != nil {
				return err
			}
		}
	}
	return nil
}

// replaceCheckConstraint drops whatever CHECK constraint currently
// covers columnName (if any -- a fresh CREATE TABLE already has one,
// named by Postgres's own default convention, but this doesn't assume
// that naming and looks it up instead) and adds a new one under a
// stable, self-chosen name instead of whatever Postgres would pick.
func replaceCheckConstraint(ctx context.Context, tx bun.Tx, qualifiedTable, tableName, columnName, checkExpr string) error {
	constraintName, err := findCheckConstraint(ctx, tx, PhysicalSchema, tableName, columnName)
	if err != nil {
		return err
	}
	if constraintName != "" {
		stmt := fmt.Sprintf("ALTER TABLE %s DROP CONSTRAINT %s", qualifiedTable, quoteIdent(constraintName))
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("drop constraint on %s: %w", columnName, err)
		}
	}
	newName := tableName + "_" + columnName + "_check"
	stmt := fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT %s CHECK (%s)", qualifiedTable, quoteIdent(newName), checkExpr)
	if _, err := tx.ExecContext(ctx, stmt); err != nil {
		return fmt.Errorf("add constraint on %s: %w", columnName, err)
	}
	return nil
}

// findCheckConstraint returns the name of the CHECK constraint (if any)
// currently covering one column, via information_schema -- not a
// guess at Postgres's own default naming convention, which could in
// principle differ (e.g. if a name got truncated at the 63-byte
// identifier limit, the same reason migrations/0019 names one of its
// own constraints explicitly rather than relying on the derived name).
func findCheckConstraint(ctx context.Context, db bun.IDB, schema, table, column string) (string, error) {
	var name string
	err := db.QueryRowContext(ctx, `
		SELECT tc.constraint_name
		FROM information_schema.table_constraints tc
		JOIN information_schema.constraint_column_usage ccu
		  ON ccu.constraint_name = tc.constraint_name AND ccu.constraint_schema = tc.constraint_schema
		WHERE tc.constraint_type = 'CHECK'
		  AND tc.table_schema = ? AND tc.table_name = ? AND ccu.column_name = ?
		LIMIT 1`, schema, table, column).Scan(&name)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", fmt.Errorf("compiler: finding check constraint on %s.%s: %w", table, column, err)
	}
	return name, nil
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
