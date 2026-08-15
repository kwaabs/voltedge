package crud

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// quoteIdent wraps a Postgres identifier in double quotes -- same
// reasoning as compiler's copy: field names are always admin-set
// lower_snake_case, not user input, but quoting is free and removes a
// whole class of reserved-word bugs.
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// physicalTable returns the compiled table's qualified name, e.g.
// gis.pole. Feature classes that haven't been compiled yet
// (physical_table is null) can't be read or written.
func physicalTable(fc *metadata.FeatureClass) (string, error) {
	if fc.PhysicalTable == nil {
		return "", notFound("feature class %q has not been compiled yet -- no physical table exists", fc.Code)
	}
	return *fc.PhysicalTable, nil
}

// selectColumns returns the column expressions for reading back a
// feature row: system columns, geometry as GeoJSON text (aliased so it's
// easy to pick out while scanning), then every active field in the same
// order the compiler created them.
func selectColumns(loaded *metadata.Loaded) []string {
	cols := []string{"id", "name"}
	if len(loaded.Subtypes) > 0 {
		cols = append(cols, "subtype")
	}
	cols = append(cols, "status", "deleted_at")
	if loaded.FeatureClass.GeometryType != "none" {
		cols = append(cols, "ST_AsGeoJSON(geom)::text AS geom_json")
	}
	for _, f := range loaded.Fields {
		cols = append(cols, quoteIdent(f.Name))
	}
	cols = append(cols, "created_at", "updated_at")
	return cols
}

// scanRows converts *sql.Rows into a slice of column-name -> value maps.
// The physical table's shape is only known at runtime (it's whatever the
// compiler generated for this feature class), so this scans generically
// rather than into a fixed struct.
func scanRows(rows *sql.Rows) ([]map[string]any, error) {
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("crud: reading columns: %w", err)
	}

	var results []map[string]any
	for rows.Next() {
		values := make([]any, len(columns))
		ptrs := make([]any, len(columns))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, fmt.Errorf("crud: scanning row: %w", err)
		}

		row := make(map[string]any, len(columns))
		for i, col := range columns {
			// pgdriver returns some column types (uuid confirmed; others
			// possibly) as []byte rather than string when scanned into a
			// generic `any`. encoding/json has no way to know that's text
			// -- it silently base64-encodes any bare []byte, which is
			// exactly wrong for a uuid the client needs to read back
			// verbatim. Normalize here, once, rather than at every call
			// site that touches a scanned value.
			if b, ok := values[i].([]byte); ok {
				row[col] = string(b)
			} else {
				row[col] = values[i]
			}
		}
		results = append(results, row)
	}
	return results, rows.Err()
}

// formatRow reshapes one scanned row into the API response envelope:
// system fields at the top level, geometry parsed back into a JSON
// object (not left as text), everything else nested under "attributes".
func formatRow(loaded *metadata.Loaded, row map[string]any) (map[string]any, error) {
	out := map[string]any{
		"id":         row["id"],
		"name":       row["name"],
		"status":     row["status"],
		"deleted_at": row["deleted_at"],
		"created_at": row["created_at"],
		"updated_at": row["updated_at"],
	}
	if v, ok := row["subtype"]; ok {
		out["subtype"] = v
	}

	if raw, ok := row["geom_json"]; ok && raw != nil {
		text, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("crud: geom_json column was not text")
		}
		var geom any
		if err := json.Unmarshal([]byte(text), &geom); err != nil {
			return nil, fmt.Errorf("crud: parsing geometry: %w", err)
		}
		out["geometry"] = geom
	}

	attrs := make(map[string]any, len(loaded.Fields))
	for _, f := range loaded.Fields {
		attrs[f.Name] = row[f.Name]
	}
	out["attributes"] = attrs

	return out, nil
}

// insertFeature builds and executes a parameterized INSERT for one
// feature, returning the row read back via RETURNING (same shape as a
// GET). Column/placeholder lists are built up in lockstep so a bug here
// shows up as a Postgres arity error immediately rather than silently
// writing the wrong value into the wrong column.
func insertFeature(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, subtype *metadata.Subtype, geometry []byte, name *string, values map[string]any) (map[string]any, error) {
	table, err := physicalTable(loaded.FeatureClass)
	if err != nil {
		return nil, err
	}

	// bun.DB.QueryContext formats its own placeholders client-side (via
	// db.gen.FormatQuery) before ever handing the query to the driver --
	// it uses "?" (in appearance order), not raw Postgres "$N" syntax.
	// Using "$N" here looks right but bun won't recognize it, args never
	// reach the driver, and pgdriver (which only speaks the simple query
	// protocol -- see its formatQuery) sends the literal "$1" text,
	// which Postgres then rejects with "there is no parameter $1".
	var cols, placeholders []string
	var args []any

	if subtype != nil {
		cols = append(cols, "subtype")
		placeholders = append(placeholders, "?")
		args = append(args, subtype.Code)
	}

	if name != nil {
		cols = append(cols, "name")
		placeholders = append(placeholders, "?")
		args = append(args, *name)
	}

	if len(geometry) > 0 {
		cols = append(cols, "geom")
		placeholders = append(placeholders, fmt.Sprintf("ST_SetSRID(ST_GeomFromGeoJSON(?), %d)", *loaded.FeatureClass.SRID))
		args = append(args, string(geometry))
	}

	for _, f := range loaded.Fields {
		v, present := values[f.Name]
		if !present {
			continue
		}
		cols = append(cols, quoteIdent(f.Name))
		placeholders = append(placeholders, "?")
		args = append(args, v)
	}

	query := fmt.Sprintf(
		"INSERT INTO %s (%s) VALUES (%s) RETURNING %s",
		table,
		strings.Join(cols, ", "),
		strings.Join(placeholders, ", "),
		strings.Join(selectColumns(loaded), ", "),
	)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("crud: insert: %w", err)
	}
	results, err := scanRows(rows)
	if err != nil {
		return nil, err
	}
	if len(results) != 1 {
		return nil, fmt.Errorf("crud: insert returned %d rows, expected 1", len(results))
	}
	return formatRow(loaded, results[0])
}

// updateFeature builds and executes a parameterized, partial UPDATE --
// only the columns present in values (plus geom, if geometry is
// non-empty) are touched. Always sets updated_at, since the physical
// table has no trigger to do it automatically yet.
func updateFeature(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, id string, geometry []byte, name *string, values map[string]any) (map[string]any, error) {
	table, err := physicalTable(loaded.FeatureClass)
	if err != nil {
		return nil, err
	}

	var sets []string
	var args []any

	if len(geometry) > 0 {
		sets = append(sets, fmt.Sprintf("geom = ST_SetSRID(ST_GeomFromGeoJSON(?), %d)", *loaded.FeatureClass.SRID))
		args = append(args, string(geometry))
	}

	if name != nil {
		sets = append(sets, "name = ?")
		args = append(args, *name)
	}

	for _, f := range loaded.Fields {
		v, present := values[f.Name]
		if !present {
			continue
		}
		args = append(args, v)
		sets = append(sets, fmt.Sprintf("%s = ?", quoteIdent(f.Name)))
	}
	if len(sets) == 0 {
		return nil, invalid("update body has no recognized fields to change")
	}
	sets = append(sets, "updated_at = now()")

	args = append(args, id)
	query := fmt.Sprintf(
		"UPDATE %s SET %s WHERE id = ? RETURNING %s",
		table,
		strings.Join(sets, ", "),
		strings.Join(selectColumns(loaded), ", "),
	)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("crud: update: %w", err)
	}
	results, err := scanRows(rows)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, notFound("feature %s not found", id)
	}
	return formatRow(loaded, results[0])
}

// GetFeature is the exported form of getFeature -- other packages (e.g.
// api/internal/connectivity, resolving a feature's subtype and
// deleted_at state before establishing a connection) need the same
// physical-table lookup and generic scanning this package already has,
// without duplicating it.
func GetFeature(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, id string) (map[string]any, error) {
	return getFeature(ctx, db, loaded, id)
}

// IsNotFoundError lets another package (api/internal/connectivity) tell
// a GetFeature "no such row" error apart from any other failure, without
// this package exporting its whole error-type hierarchy.
func IsNotFoundError(err error) bool {
	return isNotFoundError(err)
}

// getFeature reads one feature by id.
func getFeature(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, id string) (map[string]any, error) {
	table, err := physicalTable(loaded.FeatureClass)
	if err != nil {
		return nil, err
	}

	query := fmt.Sprintf("SELECT %s FROM %s WHERE id = ?", strings.Join(selectColumns(loaded), ", "), table)
	rows, err := db.QueryContext(ctx, query, id)
	if err != nil {
		return nil, fmt.Errorf("crud: get: %w", err)
	}
	results, err := scanRows(rows)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, notFound("feature %s not found", id)
	}
	return formatRow(loaded, results[0])
}

// listFeatures reads a bounded page of features, oldest first. Excludes
// soft-deleted rows -- list is the normal browsing view, and a deleted
// row showing back up in it would look like the delete didn't work.
// getFeature has no such filter: a direct GET by id still returns a
// deleted row (deleted_at set, visible in the response) since a client
// following up on a delete or deciding whether to restore needs to see
// it, not get a 404 for a row that still exists.
//
// listOptions is listFeatures' own request shape -- a struct rather
// than a growing positional-argument list now that list takes on real
// filtering/sorting alongside pagination and the name search that was
// already here.
type listOptions struct {
	Limit     int
	Offset    int
	NameQuery string // case-insensitive partial match against name, unchanged from before this existed
	Status    string // exact match against the status system column ("draft"/"published"); "" means no filter
	Sort      string // a column to order by; falls back to created_at if empty or not a real column on this class
	Desc      bool   // sort direction; false = ascending (the existing default before this existed)
}

// validSortColumns is every column list may safely sort by: the system
// columns selectColumns always includes, plus this feature class's own
// active fields -- geom is deliberately excluded, ordering by a raw
// geometry isn't something a sort control is ever really asking for.
// A requested sort column not in this set is silently ignored (falls
// back to created_at) rather than rejected with a 400, matching this
// endpoint's existing lenient handling of an out-of-range limit/offset.
func validSortColumns(loaded *metadata.Loaded) map[string]bool {
	cols := map[string]bool{"id": true, "name": true, "status": true, "deleted_at": true, "created_at": true, "updated_at": true}
	if len(loaded.Subtypes) > 0 {
		cols["subtype"] = true
	}
	for _, f := range loaded.Fields {
		cols[f.Name] = true
	}
	return cols
}

// listFeatures applies opts.NameQuery/Status as an AND-ed WHERE clause
// (both via bind params, never string-interpolated) and opts.Sort/Desc
// as the ORDER BY, with `id` always appended as a stable tiebreaker so
// two rows with an identical sort value don't reorder between pages.
// NameQuery's "%"/"_" aren't escaped since ILIKE treating them as
// wildcards inside a human-typed search string is harmless (at worst a
// slightly broader match).
func listFeatures(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, opts listOptions) ([]map[string]any, error) {
	table, err := physicalTable(loaded.FeatureClass)
	if err != nil {
		return nil, err
	}

	where := []string{"deleted_at IS NULL"}
	args := make([]any, 0, 4)
	if opts.NameQuery != "" {
		where = append(where, "name ILIKE ?")
		args = append(args, "%"+opts.NameQuery+"%")
	}
	if opts.Status != "" {
		where = append(where, "status = ?")
		args = append(args, opts.Status)
	}

	sortCol := "created_at"
	if opts.Sort != "" && validSortColumns(loaded)[opts.Sort] {
		sortCol = opts.Sort
	}
	direction := "ASC"
	if opts.Desc {
		direction = "DESC"
	}
	orderBy := quoteIdent(sortCol) + " " + direction
	if sortCol != "id" {
		orderBy += ", id"
	}

	query := fmt.Sprintf(
		"SELECT %s FROM %s WHERE %s ORDER BY %s LIMIT ? OFFSET ?",
		strings.Join(selectColumns(loaded), ", "), table, strings.Join(where, " AND "), orderBy,
	)
	args = append(args, opts.Limit, opts.Offset)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("crud: list: %w", err)
	}
	results, err := scanRows(rows)
	if err != nil {
		return nil, err
	}

	out := make([]map[string]any, 0, len(results))
	for _, r := range results {
		formatted, err := formatRow(loaded, r)
		if err != nil {
			return nil, err
		}
		out = append(out, formatted)
	}
	return out, nil
}
