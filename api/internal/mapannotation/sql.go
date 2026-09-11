package mapannotation

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/territory"
	"github.com/uptrace/bun"
)

// annotationSRID is the fixed spatial reference for the map's geographic
// annotations -- lon/lat WGS 84, matching the geometry(Geometry, 4326) column
// in migration 0060 and the map's own projection. Unlike a feature class
// (whose SRID is metadata-configurable), the redline layer is always 4326.
const annotationSRID = 4326

// kinds is the closed set of annotation kinds, mirroring migration 0060's
// CHECK constraint. geomTypeForKind maps each to the single GeoJSON geometry
// type it must carry, enforced Go-side (the column is generic geometry, so the
// DB can't) before any ST_GeomFromGeoJSON write.
var kinds = map[string]bool{"text": true, "box": true, "line": true, "arrow": true}

var geomTypeForKind = map[string]string{
	"text":  "Point",
	"box":   "Polygon",
	"line":  "LineString",
	"arrow": "LineString",
}

// selectColumns is the read projection shared by list/create/update: geometry
// and style come back as text (GeoJSON / raw jsonb) so a driver never has to
// understand PostGIS or jsonb, exactly the wire convention api/internal/crud
// uses. territory_id is cast to text so a nil scans cleanly into *string.
const selectColumns = "id, kind, ST_AsGeoJSON(geom)::text, text, style::text, z_index, territory_id::text, created_at, updated_at"

// scanAnnotations reads rows shaped by selectColumns into responses. Geometry
// and style are re-wrapped as json.RawMessage (they were selected as text);
// nullable columns scan through sql.Null* holders.
func scanAnnotations(rows *sql.Rows) ([]annotationResponse, error) {
	defer rows.Close()
	var out []annotationResponse
	for rows.Next() {
		var (
			r         annotationResponse
			geomJSON  string
			styleJSON string
			text      sql.NullString
			terr      sql.NullString
			updatedAt sql.NullTime
		)
		if err := rows.Scan(&r.ID, &r.Kind, &geomJSON, &text, &styleJSON, &r.ZIndex, &terr, &r.CreatedAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("mapannotation: scan: %w", err)
		}
		r.Geometry = json.RawMessage(geomJSON)
		r.Style = json.RawMessage(styleJSON)
		if text.Valid {
			r.Text = &text.String
		}
		if terr.Valid {
			r.TerritoryID = &terr.String
		}
		if updatedAt.Valid {
			t := updatedAt.Time
			r.UpdatedAt = &t
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// bbox is an optional lon/lat viewport filter for list, [minX, minY, maxX,
// maxY] in SRID 4326.
type bbox struct{ MinX, MinY, MaxX, MaxY float64 }

// listAnnotations returns annotations, newest first, optionally narrowed to a
// territory and/or a lon/lat viewport. A nil territoryID means "no territory
// filter"; a nil bbox means "no spatial filter" -- the map passes the current
// viewport, other callers (export) pass neither.
func listAnnotations(ctx context.Context, db bun.IDB, territoryID *string, box *bbox) ([]annotationResponse, error) {
	var where []string
	var args []any
	if territoryID != nil {
		where = append(where, "territory_id::text = ?")
		args = append(args, *territoryID)
	}
	if box != nil {
		where = append(where, fmt.Sprintf("ST_Intersects(geom, ST_MakeEnvelope(?, ?, ?, ?, %d))", annotationSRID))
		args = append(args, box.MinX, box.MinY, box.MaxX, box.MaxY)
	}

	query := "SELECT " + selectColumns + " FROM runtime.map_annotations"
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	// z_index then recency: the caller draws back-to-front, and z_index is the
	// primary intent for stacking order (created_at only breaks ties).
	query += " ORDER BY z_index ASC, created_at ASC"

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("mapannotation: list: %w", err)
	}
	return scanAnnotations(rows)
}

// getAnnotation reads one row by id, or a notFound error.
func getAnnotation(ctx context.Context, db bun.IDB, id string) (*annotationResponse, error) {
	rows, err := db.QueryContext(ctx, "SELECT "+selectColumns+" FROM runtime.map_annotations WHERE id = ?", id)
	if err != nil {
		return nil, fmt.Errorf("mapannotation: get: %w", err)
	}
	results, err := scanAnnotations(rows)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, notFound("map annotation %s not found", id)
	}
	return &results[0], nil
}

// createAnnotation validates the request, spatially assigns a territory from
// the geometry (Phase 11's territory.Assign, override honoured), and inserts
// one row -- returning the persisted shape (server-minted id, resolved
// territory, stamped audit).
func createAnnotation(ctx context.Context, db bun.IDB, req annotationRequest) (*annotationResponse, error) {
	if !kinds[req.Kind] {
		return nil, invalid("kind %q is not a valid annotation kind (text, box, line, arrow)", req.Kind)
	}
	if len(req.Geometry) == 0 {
		return nil, invalid("geometry is required")
	}
	if err := validateGeometry(req.Kind, req.Geometry); err != nil {
		return nil, err
	}
	// A text annotation with no text is a blank note -- reject it up front, the
	// same "required in spirit" contract migration 0060 documents.
	if req.Kind == "text" && (req.Text == nil || strings.TrimSpace(*req.Text) == "") {
		return nil, invalid("a text annotation requires non-empty text")
	}

	style, err := validateStyle(req.Style)
	if err != nil {
		return nil, err
	}
	if style == nil {
		// The style column is NOT NULL default '{}'; substitute the empty
		// object so a nil never inserts SQL NULL.
		style = json.RawMessage("{}")
	}

	// Spatial territory assignment from the geometry (override honoured),
	// mirroring the CRUD create path -- every annotation carries geometry, so
	// this always runs.
	territoryID, err := territory.Assign(ctx, db, string(req.Geometry), annotationSRID, req.TerritoryID)
	if err != nil {
		return nil, territoryError(err)
	}

	zIndex := 0
	if req.ZIndex != nil {
		zIndex = *req.ZIndex
	}

	actor := actorArg(ctx)
	query := fmt.Sprintf(
		"INSERT INTO runtime.map_annotations (kind, geom, text, style, z_index, territory_id, created_by, updated_by) "+
			"VALUES (?, ST_SetSRID(ST_GeomFromGeoJSON(?), %d), ?, ?::jsonb, ?, ?, ?, ?) RETURNING %s",
		annotationSRID, selectColumns,
	)
	rows, err := db.QueryContext(ctx, query,
		req.Kind, string(req.Geometry), req.Text, string(style), zIndex, territoryID, actor, actor,
	)
	if err != nil {
		return nil, fmt.Errorf("mapannotation: insert: %w", err)
	}
	results, err := scanAnnotations(rows)
	if err != nil {
		return nil, err
	}
	if len(results) != 1 {
		return nil, fmt.Errorf("mapannotation: insert returned %d rows, expected 1", len(results))
	}
	return &results[0], nil
}

// updateAnnotation applies a partial change (geometry, text, style, z_index,
// territory). Kind is immutable, so any new geometry is validated against the
// existing row's kind; a geometry change re-runs territory assignment (the
// override, if given, wins). Fields absent from the request are left as they
// are. Returns a notFound error if no row matches.
func updateAnnotation(ctx context.Context, db bun.IDB, id string, req annotationRequest) (*annotationResponse, error) {
	existing, err := getAnnotation(ctx, db, id)
	if err != nil {
		return nil, err
	}

	var sets []string
	var args []any

	if len(req.Geometry) > 0 {
		if err := validateGeometry(existing.Kind, req.Geometry); err != nil {
			return nil, err
		}
		sets = append(sets, fmt.Sprintf("geom = ST_SetSRID(ST_GeomFromGeoJSON(?), %d)", annotationSRID))
		args = append(args, string(req.Geometry))

		// Geometry moved -> re-resolve the territory from the new shape (the
		// override wins if supplied), mirroring the CRUD update path.
		territoryID, err := territory.Assign(ctx, db, string(req.Geometry), annotationSRID, req.TerritoryID)
		if err != nil {
			return nil, territoryError(err)
		}
		sets = append(sets, "territory_id = ?")
		args = append(args, territoryID)
	}
	// A territory_id override without a geometry move is intentionally ignored:
	// the redline layer's territory is a spatial scoping derived from where the
	// annotation sits, so it only ever changes when the geometry does (matching
	// the CRUD update path, which likewise reassigns territory from geometry).

	if req.Text != nil {
		if existing.Kind == "text" && strings.TrimSpace(*req.Text) == "" {
			return nil, invalid("a text annotation requires non-empty text")
		}
		sets = append(sets, "text = ?")
		args = append(args, *req.Text)
	}

	if len(req.Style) > 0 && string(req.Style) != "null" {
		style, err := validateStyle(req.Style)
		if err != nil {
			return nil, err
		}
		sets = append(sets, "style = ?::jsonb")
		args = append(args, string(style))
	}

	if req.ZIndex != nil {
		sets = append(sets, "z_index = ?")
		args = append(args, *req.ZIndex)
	}

	if len(sets) == 0 {
		// Nothing to change -- return the row as-is rather than issuing an empty
		// UPDATE (which would still bump updated_at/updated_by for no reason).
		return existing, nil
	}

	// Always stamp the editor and the edit time -- the physical table has no
	// trigger to maintain updated_at.
	sets = append(sets, "updated_at = now()", "updated_by = ?")
	args = append(args, actorArg(ctx))
	args = append(args, id)

	query := fmt.Sprintf(
		"UPDATE runtime.map_annotations SET %s WHERE id = ? RETURNING %s",
		strings.Join(sets, ", "), selectColumns,
	)
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("mapannotation: update: %w", err)
	}
	results, err := scanAnnotations(rows)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, notFound("map annotation %s not found", id)
	}
	return &results[0], nil
}

// deleteAnnotation removes one annotation. A missing row is a notFound error so
// the handler can answer 404 rather than a silent 204.
func deleteAnnotation(ctx context.Context, db bun.IDB, id string) error {
	res, err := db.ExecContext(ctx, "DELETE FROM runtime.map_annotations WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("mapannotation: delete: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("mapannotation: delete rows affected: %w", err)
	}
	if n == 0 {
		return notFound("map annotation %s not found", id)
	}
	return nil
}

// validateGeometry checks the GeoJSON's type matches the one the kind implies
// (text->Point, box->Polygon, line/arrow->LineString). The geom column is a
// generic geometry(Geometry, 4326), so this Go-side check is the only thing
// stopping a box from being stored as a stray Point.
func validateGeometry(kind string, raw json.RawMessage) error {
	var g struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &g); err != nil {
		return invalid("geometry must be a GeoJSON object: %v", err)
	}
	want := geomTypeForKind[kind]
	if g.Type != want {
		return invalid("a %s annotation requires %s geometry, got %q", kind, want, g.Type)
	}
	return nil
}

// validateStyle checks the optional style blob is a JSON object (or empty),
// mirroring diagram.validateStyle. Returns nil for an absent/null style so the
// caller can substitute the '{}' default the NOT NULL column needs.
func validateStyle(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, invalid("style must be a JSON object: %v", err)
	}
	return raw, nil
}

// territoryError maps a territory.Assign validation error to this package's 400
// error, mirroring crud.territoryError; anything else passes through as a 500.
func territoryError(err error) error {
	if territory.IsInvalid(err) {
		return invalid("%s", err.Error())
	}
	return err
}

// actorArg returns the acting user's id for the *_by audit columns, or nil for
// a session-less code path (so the text column is written NULL, not ""), the
// same discipline as crud.actorArg.
func actorArg(ctx context.Context) any {
	if id := auth.ActorID(ctx); id != "" {
		return id
	}
	return nil
}
