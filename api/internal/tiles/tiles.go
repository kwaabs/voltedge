// Package tiles is the Phase 58 Route A vector-tile server: a session-authed
// Go endpoint that generates MVT tiles with ST_AsMVT, filtered to the acting
// user's territory scope. It replaces Martin as the origin the frontend's
// /tiles proxy points at (see web/vite.config.ts) so that a scoped user is
// never even sent the geometry of a feature outside their territories -- the
// "must not even see them" requirement the design locked on.
//
// The generated SQL is byte-for-byte the same MVT query Martin's per-class tile
// function runs (migrations/0038_add_tile_functions.sql), with one addition: a
// `territory_id IN (...)` predicate when the request is scoped. So with
// enforcement off (the rollout gate) the tiles served here are identical to
// what Martin served -- same layer name, same 4096 extent, same 64px buffer,
// same clip, same property set (every column except geom/deleted_at). The
// source id / layer name stays the lowercased class code the frontend already
// keys every per-class layer on, so nothing on the web side changes.
package tiles

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/drafts"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/kwaabs/voltedge/api/internal/territory"
	"github.com/uptrace/bun"
)

// mvtContentType is the MIME type Martin serves MVT bytes as, kept identical so
// the frontend's tile source (OpenLayers MVT format) parses them the same way.
const mvtContentType = "application/x-protobuf"

// Routes builds the /tiles router: the catalog (the live list of tile-servable
// feature classes, which the map fetches to know what per-class layers to
// create) and the per-class tile endpoint. Both are GETs gated by features:read
// at the mount, the same permission every map viewer already holds.
func Routes(db *bun.DB) http.Handler {
	r := chi.NewRouter()
	r.Get("/catalog", catalogHandler(db))
	r.Get("/{code}/{z}/{x}/{y}", tileHandler(db))
	return r
}

// catalogEntry mirrors the shape the frontend's martin.ts CatalogEntry expects:
// a content type and an optional human description.
type catalogEntry struct {
	ContentType string `json:"content_type"`
	Description string `json:"description,omitempty"`
}

// catalog is the JSON body GET /tiles/catalog returns -- the same
// {"tiles": {sourceId: entry}} shape Martin's /catalog produced, so
// martin.ts fetchCatalog/featureClassSourceIds consume it unchanged.
type catalog struct {
	Tiles map[string]catalogEntry `json:"tiles"`
}

// catalogHandler lists every compiled, geometric feature class as a tile
// source keyed by its lowercased code -- exactly the ids Martin auto-published
// and the frontend already keys per-class layers on (classCode =
// sourceId.toUpperCase()). Geometry-less classes have no geom column and were
// never tile sources; draft/uncompiled classes have no physical table.
func catalogHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		classes, err := metadata.ListFeatureClasses(r.Context(), db)
		if err != nil {
			http.Error(w, "failed to list feature classes", http.StatusInternalServerError)
			return
		}
		out := catalog{Tiles: make(map[string]catalogEntry)}
		for i := range classes {
			fc := classes[i]
			if fc.GeometryType == "none" || fc.PhysicalTable == nil {
				continue
			}
			out.Tiles[strings.ToLower(fc.Code)] = catalogEntry{
				ContentType: mvtContentType,
				Description: fc.Name,
			}
		}
		writeJSON(w, out)
	}
}

// tileHandler serves one MVT tile for one feature class, scoped to the acting
// user's territories. A missing/uncompiled/geometry-less class is a 404 (there
// is no such tile source), a bad z/x/y is a 400, and an empty tile (no features
// in this tile's envelope, or none in scope) is a 204 -- what an MVT client
// expects for "nothing here", the same as Martin returned.
func tileHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		sourceID := strings.ToLower(chi.URLParam(r, "code"))

		z, okz := parseTileCoord(chi.URLParam(r, "z"))
		x, okx := parseTileCoord(chi.URLParam(r, "x"))
		y, oky := parseTileCoord(chi.URLParam(r, "y"))
		if !okz || !okx || !oky {
			http.Error(w, "tile coordinates must be non-negative integers", http.StatusBadRequest)
			return
		}

		fc, err := loadTileClass(ctx, db, sourceID)
		if err != nil {
			http.Error(w, "no such tile source", http.StatusNotFound)
			return
		}

		ids, apply, err := tileScope(ctx, db)
		if err != nil {
			http.Error(w, "failed to resolve access scope", http.StatusInternalServerError)
			return
		}

		mvt, err := renderTile(ctx, db, fc, z, x, y, ids, apply)
		if err != nil {
			http.Error(w, "failed to render tile", http.StatusInternalServerError)
			return
		}
		if len(mvt) == 0 {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", mvtContentType)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(mvt)
	}
}

// tileClass is the slice of a feature class the tile query needs: the physical
// table to read, the class's SRID (its geometry's coordinate space), and the
// lowercased code that names both the tile source and the MVT layer.
type tileClass struct {
	sourceID      string
	code          string // canonical class code (feature_pending_edits keys on it)
	physicalTable string
	srid          int
}

// loadTileClass resolves a lowercased source id to a compiled geometric class.
// It matches on lower(code) because that's the id Martin published and the
// frontend requests, independent of how the code is cased in metadata.
func loadTileClass(ctx context.Context, db bun.IDB, sourceID string) (tileClass, error) {
	fc := new(metadata.FeatureClass)
	err := db.NewSelect().Model(fc).
		Where("lower(code) = ?", sourceID).
		Where("status = ?", "active").
		Where("geometry_type != ?", "none").
		Where("physical_table IS NOT NULL").
		Where("srid IS NOT NULL").
		Scan(ctx)
	if err != nil {
		return tileClass{}, fmt.Errorf("tiles: source %q not found: %w", sourceID, err)
	}
	return tileClass{
		sourceID:      strings.ToLower(fc.Code),
		code:          fc.Code,
		physicalTable: *fc.PhysicalTable,
		srid:          *fc.SRID,
	}, nil
}

// tileScope resolves the acting request's territory filter for a tile, with the
// exact same gates as the CRUD/trace read paths: no predicate (apply=false)
// when enforcement is off, there is no session, or the user is unscoped
// (territories:unscoped). When apply is true, ids is the user's effective set;
// an EMPTY set is the fail-closed case -- the tile must contain nothing.
// Geometry-less classes never reach here (they aren't tile sources), so there
// is no class-shape gate to apply.
func tileScope(ctx context.Context, db bun.IDB) (ids []string, apply bool, err error) {
	enforced, err := territory.EnforcementEnabled(ctx, db)
	if err != nil {
		return nil, false, err
	}
	if !enforced {
		return nil, false, nil
	}
	user, ok := auth.UserFrom(ctx)
	if !ok {
		return nil, false, nil
	}
	allowed, unscoped := user.TerritoryFilter()
	if unscoped {
		return nil, false, nil
	}
	if allowed == nil {
		allowed = []string{}
	}
	return allowed, true, nil
}

// renderTile runs the MVT query for one class/tile and returns the protobuf
// bytes (nil when the tile is empty). The query is the same one
// gis.sync_tile_function generates (migrations/0038), with the territory
// predicate appended when the request is scoped. The layer name is the source
// id, matching Martin so the client parses the same layer.
func renderTile(ctx context.Context, db bun.IDB, fc tileClass, z, x, y int, ids []string, apply bool) ([]byte, error) {
	cols, err := propertyColumns(ctx, db, fc.physicalTable)
	if err != nil {
		return nil, err
	}
	// No property columns is degenerate (a table with only geom/deleted_at can't
	// happen -- every gis table carries id/status/... ), but guard anyway so the
	// SELECT list is never empty.
	colList := "NULL"
	if len(cols) > 0 {
		quoted := make([]string, len(cols))
		for i, c := range cols {
			quoted[i] = "t." + quoteIdent(c)
		}
		colList = strings.Join(quoted, ", ")
	}

	// Private-drafts Phase B overlay: the acting author sees THEIR OWN private,
	// unpublished edits of already-published features on their tiles -- a moved
	// feature renders at its new position, a privately-deleted one disappears --
	// while every other viewer sees the untouched public geometry. Attribute-only
	// private edits are NOT overlaid on tiles (they show in the attribute panel,
	// which is overlaid server-side); the map cares about position and presence.
	// A no-op unless the overlay is active for this actor (see drafts.OverlayAuthor).
	// Tiles are rendered per-session behind the API (never a shared cache), so an
	// author's overlaid tile can't leak to anyone else.
	overlayAuthor, overlayActive, err := drafts.OverlayAuthor(ctx, db)
	if err != nil {
		return nil, err
	}
	geomExpr := "t.geom"
	joinSQL := ""
	deleteMask := ""
	var joinArgs []any
	if overlayActive {
		// The effective geometry: the author's privately moved geom if they staged
		// one, else the public geom. Used in BOTH the MVT-geom projection and the
		// bbox filter so a moved feature is tested against its new position.
		geomExpr = fmt.Sprintf("COALESCE(ST_SetSRID(ST_GeomFromGeoJSON(pe.geom_json), %d), t.geom)", fc.srid)
		joinSQL = "\n            LEFT JOIN runtime.feature_pending_edits pe" +
			"\n              ON pe.feature_class_code = ? AND pe.feature_id = t.id AND pe.author_id = ?"
		joinArgs = []any{fc.code, overlayAuthor}
		deleteMask = "\n              AND pe.is_delete IS NOT TRUE"
	}

	// Params, in the order the placeholders appear: layer name, then z/x/y for
	// the MVT-geom envelope, then the overlay join binds (if any), then z/x/y
	// again for the bbox && filter, then the scope ids (if any), then the Phase A
	// draft-visibility binds. Table name and SRID are trusted (metadata, not
	// request input) so they're interpolated; every request-derived value is a bind.
	args := []any{fc.sourceID, z, x, y}
	args = append(args, joinArgs...)
	args = append(args, z, x, y)

	scope := ""
	switch {
	case !apply:
		// no predicate -- serves exactly what Martin served
	case len(ids) == 0:
		scope = "\n            AND 1 = 0" // fail closed: scoped to nothing
	default:
		placeholders := make([]string, len(ids))
		for i := range ids {
			placeholders[i] = "?"
			args = append(args, ids[i])
		}
		scope = "\n            AND t.territory_id::text IN (" + strings.Join(placeholders, ", ") + ")"
	}

	// Private-drafts (Phase 61): a never-published feature must not leak onto the
	// map to anyone but its author and the author's management chain. The clause
	// is AND-ed after the territory scope, and its author-id binds are appended
	// after the scope ids so placeholder order stays aligned. A no-op unless the
	// rollout gate is on and the viewer is scoped (see drafts.VisibleAuthors).
	// Tiles are rendered per-session behind the API (never a shared Martin
	// cache), so the predicate can't leak a private draft via a cached tile.
	draftAuthors, draftApply, err := drafts.VisibleAuthors(ctx, db)
	if err != nil {
		return nil, err
	}
	draftScope := ""
	if clause, draftArgs := drafts.WhereClause(draftAuthors, draftApply, "t."); clause != "" {
		draftScope = "\n              AND " + clause
		args = append(args, draftArgs...)
	}

	query := fmt.Sprintf(`
        SELECT ST_AsMVT(tile, ?, 4096, 'geom')
        FROM (
            SELECT
                ST_AsMVTGeom(
                    ST_Transform(%s, 3857),
                    ST_TileEnvelope(?, ?, ?),
                    4096, 64, true
                ) AS geom,
                %s
            FROM %s t%s
            WHERE t.deleted_at IS NULL
              AND %s && ST_Transform(ST_TileEnvelope(?, ?, ?), %d)%s%s%s
        ) AS tile
        WHERE tile.geom IS NOT NULL`, geomExpr, colList, fc.physicalTable, joinSQL, geomExpr, fc.srid, scope, draftScope, deleteMask)

	var mvt []byte
	if err := db.QueryRowContext(ctx, query, args...).Scan(&mvt); err != nil {
		return nil, fmt.Errorf("tiles: render %s/%d/%d/%d: %w", fc.sourceID, z, x, y, err)
	}
	return mvt, nil
}

// propertyColumns returns the table's columns that become MVT feature
// properties -- every column except geom (which becomes the tile geometry) and
// deleted_at (the row is filtered on it, never a property), ordered by ordinal
// position for a stable property set. This is the Go twin of the string_agg the
// gis.sync_tile_function helper runs, so the properties a tile carries here are
// identical to what Martin published.
func propertyColumns(ctx context.Context, db bun.IDB, physicalTable string) ([]string, error) {
	schema, table := splitQualified(physicalTable)
	var cols []string
	err := db.NewSelect().
		ColumnExpr("column_name").
		TableExpr("information_schema.columns").
		Where("table_schema = ?", schema).
		Where("table_name = ?", table).
		Where("column_name NOT IN (?)", bun.In([]string{"geom", "deleted_at"})).
		OrderExpr("ordinal_position").
		Scan(ctx, &cols)
	if err != nil {
		return nil, fmt.Errorf("tiles: introspect %s: %w", physicalTable, err)
	}
	return cols, nil
}

// splitQualified splits a "schema.table" identifier into its parts, defaulting
// the schema to "gis" (every tile source lives there) when unqualified.
func splitQualified(qualified string) (schema, table string) {
	if i := strings.IndexByte(qualified, '.'); i >= 0 {
		return qualified[:i], qualified[i+1:]
	}
	return "gis", qualified
}

// parseTileCoord parses a z/x/y path segment as a non-negative integer. An
// extension (e.g. a ".pbf" some clients append) is tolerated by trimming at the
// first dot, though the frontend's tileUrlTemplate never sends one.
func parseTileCoord(s string) (int, bool) {
	if i := strings.IndexByte(s, '.'); i >= 0 {
		s = s[:i]
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

// writeJSON serializes v as the JSON body of a 200 response. Tiny local helper
// so this package doesn't pull a shared response util; the catalog is the only
// JSON this package returns.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		http.Error(w, "failed to encode response", http.StatusInternalServerError)
	}
}

// quoteIdent wraps a Postgres identifier in double quotes -- the column names
// come from information_schema (trusted DB catalog, not request input), but
// quoting is free and removes any reserved-word hazard, matching the crud
// package's own helper.
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
