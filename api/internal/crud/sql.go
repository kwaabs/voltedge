package crud

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/drafts"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// actorArg returns the acting user's id for a created_by/updated_by bind
// param (Slice E), or nil when the context carries no session so the
// column is written NULL rather than an empty string (which would fail the
// uuid cast). The id is a uuid string; Postgres casts it to the uuid
// column implicitly.
func actorArg(ctx context.Context) any {
	if id := auth.ActorID(ctx); id != "" {
		return id
	}
	return nil
}

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

// geomSelectExpr returns the SELECT expression that renders a geometric class's
// geom column to GeoJSON text (aliased geom_json). With targetSRID nil the
// geometry is emitted verbatim in its stored SRID; with a target it is
// reprojected first with PostGIS ST_Transform -- the single hinge of the export
// reprojection engine, mirroring the import path's server-side ST_Transform
// (importer/geopackage.go wkbToGeoJSON). targetSRID is an int spliced with %d
// (not a bind param), same as the other SRIDs in this package; the export
// enqueue path validates it against spatial_ref_sys before it ever reaches here,
// so it is never attacker-controlled text.
func geomSelectExpr(targetSRID *int) string {
	if targetSRID != nil {
		return fmt.Sprintf("ST_AsGeoJSON(ST_Transform(geom, %d))::text AS geom_json", *targetSRID)
	}
	return "ST_AsGeoJSON(geom)::text AS geom_json"
}

// selectColumns returns the column expressions for reading back a
// feature row: system columns, geometry as GeoJSON text (aliased so it's
// easy to pick out while scanning), then every active field in the same
// order the compiler created them. Geometry is emitted in its stored SRID;
// the export read path uses selectColumnsGeom to reproject.
func selectColumns(loaded *metadata.Loaded) []string {
	return selectColumnsGeom(loaded, nil)
}

// selectColumnsGeom is selectColumns with an optional export reprojection: when
// targetSRID is non-nil the geometry column is wrapped in ST_Transform to that
// SRID. Every other column is identical, so scanRows/formatRow are unaffected.
func selectColumnsGeom(loaded *metadata.Loaded, targetSRID *int) []string {
	cols := []string{"id", "name"}
	if len(loaded.Subtypes) > 0 {
		cols = append(cols, "subtype")
	}
	cols = append(cols, "status", "deleted_at")
	if loaded.FeatureClass.GeometryType != "none" {
		cols = append(cols, geomSelectExpr(targetSRID))
		// territory_id only exists on geometric tables (Phase 11) -- read it
		// back so the API surfaces which territory a feature was assigned to.
		cols = append(cols, "territory_id")
	}
	for _, f := range loaded.Fields {
		cols = append(cols, quoteIdent(f.Name))
	}
	cols = append(cols, "created_at", "updated_at")
	// Audit actors (Slice E): stamped on every write. created_by is also read by
	// the Private-drafts single-row gate (enforceDraftScope) to decide whether
	// the viewer authored a private draft, so it must be selected, not just
	// stored.
	cols = append(cols, "created_by", "updated_by")
	// Private-drafts (Phase 61): the sticky first-publish markers. published_at
	// IS NULL means the feature has never been public -- the privacy predicate
	// keys off it, and publishFeature reads it back here to decide whether this
	// is a first publish (stamp) or a re-publish (leave alone).
	cols = append(cols, "published_at", "published_by")
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
		// Audit actors (Slice E). created_by also drives the Private-drafts
		// single-row visibility gate (enforceDraftScope).
		"created_by": row["created_by"],
		"updated_by": row["updated_by"],
		// Private-drafts (Phase 61): sticky first-publish markers. Nil until the
		// feature is first published, then set forever (an unpublish leaves them).
		"published_at": row["published_at"],
		"published_by": row["published_by"],
	}
	if v, ok := row["subtype"]; ok {
		out["subtype"] = v
	}
	// Present only for geometric classes (selectColumns adds the column
	// alongside geom_json); absent entirely for a geometry-less class.
	if v, ok := row["territory_id"]; ok {
		out["territory_id"] = v
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
func insertFeature(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, id *string, subtype *metadata.Subtype, geometry []byte, name *string, territoryID *string, values map[string]any) (map[string]any, error) {
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

	// A caller-supplied primary key (the field-tool sync path, Phase 21) is
	// written as the row's id instead of letting the column default mint one.
	// Postgres casts the string to the uuid column implicitly. Absent (the
	// normal REST create), the id column is omitted and the DB generates it.
	if id != nil {
		cols = append(cols, "id")
		placeholders = append(placeholders, "?")
		args = append(args, *id)
	}

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

	// The resolved territory (Phase 11), only ever non-nil for a geometric
	// class -- the column doesn't exist otherwise. nil leaves it unset
	// (NULL), the correct state while enforcement is off and no territory
	// covers the feature.
	if territoryID != nil {
		cols = append(cols, "territory_id")
		placeholders = append(placeholders, "?")
		args = append(args, *territoryID)
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

	// Stamp the acting user (Slice E). A new row's updated_by mirrors
	// created_by -- the creator is also its most recent editor -- so the
	// column is meaningful from the first write, not just after an update.
	actor := actorArg(ctx)
	cols = append(cols, "created_by", "updated_by")
	placeholders = append(placeholders, "?", "?")
	args = append(args, actor, actor)

	// With a caller-supplied id, make the insert idempotent: a replayed sync
	// op whose row already exists must be a no-op, not a duplicate-key error.
	// ON CONFLICT (id) DO NOTHING suppresses the insert, and RETURNING then
	// yields no row -- handled below by reading the existing feature back.
	onConflict := ""
	if id != nil {
		onConflict = " ON CONFLICT (id) DO NOTHING"
	}

	query := fmt.Sprintf(
		"INSERT INTO %s (%s) VALUES (%s)%s RETURNING %s",
		table,
		strings.Join(cols, ", "),
		strings.Join(placeholders, ", "),
		onConflict,
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
	if len(results) == 0 {
		// DO NOTHING fired: a row with this id already exists (an idempotent
		// sync replay). Return it so the caller sees success, not a phantom
		// "0 rows" error. Only reachable on the caller-supplied-id path.
		if id != nil {
			return getFeature(ctx, db, loaded, *id)
		}
		return nil, fmt.Errorf("crud: insert returned 0 rows, expected 1")
	}
	if len(results) != 1 {
		return nil, fmt.Errorf("crud: insert returned %d rows, expected 1", len(results))
	}
	out, err := formatRow(loaded, results[0])
	if err != nil {
		return nil, err
	}
	// Audit trail (Phase 41): one immutable row per write. The idempotent
	// ON CONFLICT DO NOTHING replay above returns early without reaching here,
	// so a re-applied sync create records no phantom second "create".
	if err := recordFeatureHistory(ctx, db, loaded, historyCreate, out); err != nil {
		return nil, err
	}
	return out, nil
}

// updateFeature builds and executes a parameterized, partial UPDATE --
// only the columns present in values (plus geom, if geometry is
// non-empty or being cleared) are touched. Always sets updated_at, since
// the physical table has no trigger to do it automatically yet.
//
// clearGeometry (Phase 26 Slice 3) sets geom = NULL to demote a feature to
// diagram-only; it's mutually exclusive with a non-empty geometry (the caller,
// UpdateFeature, never sets both) and only reachable for a geometry_optional
// class, which the compiler emitted with a nullable geom column.
func updateFeature(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, id string, geometry []byte, clearGeometry bool, name *string, territoryID *string, assignTerritory bool, subtypeCode *int, values map[string]any, expectedUpdatedAt *string) (map[string]any, error) {
	table, err := physicalTable(loaded.FeatureClass)
	if err != nil {
		return nil, err
	}

	var sets []string
	var args []any

	// Subtype change (Phase 60): the caller resolved and validated a new subtype
	// code (UpdateFeature; nil when the request left subtype alone or it was
	// unchanged), so all this does is write the column. Counts as a real change
	// for the nothing-to-change guard below.
	if subtypeCode != nil {
		sets = append(sets, "subtype = ?")
		args = append(args, *subtypeCode)
	}

	if clearGeometry {
		sets = append(sets, "geom = NULL")
	} else if len(geometry) > 0 {
		sets = append(sets, fmt.Sprintf("geom = ST_SetSRID(ST_GeomFromGeoJSON(?), %d)", *loaded.FeatureClass.SRID))
		args = append(args, string(geometry))
	}

	// Re-assign the territory (Phase 11) only when the caller resolved a new
	// one -- either an explicit override or a geometry change. assignTerritory
	// distinguishes "recompute to this value (possibly NULL)" from "don't
	// touch the existing assignment," which a bare nil pointer can't. Counted
	// as a real change so a territory-only PATCH doesn't trip the
	// nothing-to-change guard below.
	if assignTerritory {
		if territoryID != nil {
			sets = append(sets, "territory_id = ?")
			args = append(args, *territoryID)
		} else {
			sets = append(sets, "territory_id = NULL")
		}
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

	// Stamp the editor (Slice E). Appended after the "nothing to change"
	// guard above so a no-op update still 400s rather than counting the
	// actor stamp as a change; created_by is left untouched.
	sets = append(sets, "updated_by = ?")
	args = append(args, actorArg(ctx))

	// Optimistic concurrency (Phase 40): when the caller sent the updated_at it
	// last read, the write is conditional on the row still carrying that exact
	// value -- so an edit made against a stale copy affects 0 rows instead of
	// silently overwriting whoever wrote in between. The token is compared as
	// text cast to timestamptz, so the client just echoes the value it got back
	// (any precision Postgres stored round-trips through the cast). Absent token
	// leaves the write unconditional. The WHERE args go after every SET arg,
	// matching bun's appearance-order placeholder formatting.
	where := "id = ?"
	args = append(args, id)
	guarded := expectedUpdatedAt != nil && *expectedUpdatedAt != ""
	if guarded {
		where += " AND updated_at = ?::timestamptz"
		args = append(args, *expectedUpdatedAt)
	}
	query := fmt.Sprintf(
		"UPDATE %s SET %s WHERE %s RETURNING %s",
		table,
		strings.Join(sets, ", "),
		where,
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
		// Under a concurrency guard, UpdateFeature already confirmed the row
		// exists and isn't deleted moments before this write, so a 0-row result
		// means the token no longer matches -- someone else changed the row.
		// Report that as a conflict (409) rather than a misleading "not found".
		if guarded {
			return nil, conflict("this feature was changed by someone else since you opened it -- reload and reapply your changes")
		}
		return nil, notFound("feature %s not found", id)
	}
	out, err := formatRow(loaded, results[0])
	if err != nil {
		return nil, err
	}
	if err := recordFeatureHistory(ctx, db, loaded, historyUpdate, out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetFeature is the exported form of getFeature -- other packages (e.g.
// api/internal/connectivity, resolving a feature's subtype and
// deleted_at state before establishing a connection) need the same
// physical-table lookup and generic scanning this package already has,
// without duplicating it.
func GetFeature(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, id string) (map[string]any, error) {
	return getFeature(ctx, db, loaded, id)
}

// GetFeatureScoped is GetFeature with the full HTTP read gate applied: it reads
// the feature, then runs the Phase-58 territory check, the Phase-61 private-draft
// check, and the Phase-61-B pending-edit overlay -- exactly the sequence
// getHandler applies -- and returns a notFound (which IsNotFoundError reports)
// when any of them hides the row. It exists so a caller OUTSIDE this package
// (the asset-tag resolver, which turns a scanned code into a feature) gets the
// same answer a scoped user would get from GET /features/{code}/{id}, without
// re-implementing the gates or this package exporting them. getFeature itself
// stays unscoped for internal callers that must read a row they just wrote.
func GetFeatureScoped(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, id string) (map[string]any, error) {
	result, err := getFeature(ctx, db, loaded, id)
	if err != nil {
		return nil, err
	}
	if err := enforceReadScope(ctx, db, loaded, result); err != nil {
		return nil, err
	}
	if err := enforceDraftScope(ctx, db, result); err != nil {
		return nil, err
	}
	hidden, err := overlayFeature(ctx, db, loaded, result)
	if err != nil {
		return nil, err
	}
	if hidden {
		return nil, notFound("feature %s not found", id)
	}
	return result, nil
}

// IsNotFoundError lets another package (api/internal/connectivity) tell
// a GetFeature "no such row" error apart from any other failure, without
// this package exporting its whole error-type hierarchy.
func IsNotFoundError(err error) bool {
	return isNotFoundError(err)
}

// FeatureVersions reads just a feature's created_at / updated_at timestamps -- the
// version stamps the field-sync worker compares against an op's base_version for
// optimistic-concurrency conflict detection (Phase 23 Slice 4). exists is false when
// no row has that id (the create it depends on hasn't applied, or it was hard-deleted);
// the caller treats a missing row as "nothing to conflict with". Kept separate from
// getFeature so the check is one cheap two-column read, not a full feature hydrate.
func FeatureVersions(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, id string) (created, updated time.Time, exists bool, err error) {
	table, err := physicalTable(loaded.FeatureClass)
	if err != nil {
		return time.Time{}, time.Time{}, false, err
	}
	query := fmt.Sprintf("SELECT created_at, updated_at FROM %s WHERE id = ?", table)
	row := db.QueryRowContext(ctx, query, id)
	if err := row.Scan(&created, &updated); err != nil {
		if err == sql.ErrNoRows {
			return time.Time{}, time.Time{}, false, nil
		}
		return time.Time{}, time.Time{}, false, fmt.Errorf("crud: feature versions: %w", err)
	}
	return created, updated, true, nil
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

	// Phase 58 spatial read enforcement: constrain to the acting user's
	// territory scope. A no-op unless enforcement is on and the user is scoped
	// (see readScope); fail-closed for a scoped-to-nothing user.
	scopeIDs, scopeApply, err := readScope(ctx, db, loaded)
	if err != nil {
		return nil, err
	}
	if clause, scopeArgs := scopeWhere(scopeIDs, scopeApply); clause != "" {
		where = append(where, clause)
		args = append(args, scopeArgs...)
	}

	// Private-drafts (Phase 61): hide never-published features from anyone but
	// their author and the author's management chain. AND-ed with the territory
	// predicate above; a no-op unless the rollout gate is on and the user is
	// scoped (see draftScope).
	draftAuthors, draftApply, err := drafts.VisibleAuthors(ctx, db)
	if err != nil {
		return nil, err
	}
	if clause, draftArgs := drafts.WhereClause(draftAuthors, draftApply, ""); clause != "" {
		where = append(where, clause)
		args = append(args, draftArgs...)
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
