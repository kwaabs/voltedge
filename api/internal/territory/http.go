package territory

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"
)

// Routes returns the territory endpoints:
//
//	GET    /            the whole tree as a flat, boundary-free list
//	GET    /levels      the level catalog (for the level picker)
//	GET    /boundaries  the drawn boundaries as a GeoJSON FeatureCollection
//	POST   /            create a territory
//	PATCH  /{id}        rename / re-parent / edit-or-clear boundary
//	DELETE /{id}        delete a leaf territory
//
// Reads and writes share one mount but split by capability: the GETs are
// gated by features:read (every map viewer resolves the territory label and
// may see the boundary overlay), while the mutating verbs require
// territories:write -- an administrative capability (migration 0031).
// api/cmd/server/main.go wires that method-aware gate
// (RequirePermission(features:read, territories:write)), so this package
// stays unaware of auth; it only has to register the right verbs.
//
// This reverses the phase's earlier "fixed reference data, no authoring UI"
// stance: territories are now curated in-app by administrators
// (docs/phase-11-territories.md, Slice F).
func Routes(db *bun.DB) chi.Router {
	r := chi.NewRouter()
	r.Get("/", listTerritoriesHandler(db))
	r.Get("/levels", listLevelsHandler(db))
	r.Get("/boundaries", boundariesHandler(db))
	r.Post("/", createTerritoryHandler(db))
	r.Patch("/{id}", updateTerritoryHandler(db))
	r.Delete("/{id}", deleteTerritoryHandler(db))
	return r
}

// Summary is the boundary-free read model returned by the list endpoint,
// with the level resolved (code/name/rank) so a client can label and
// order without a second catalog fetch.
type Summary struct {
	ID        string  `bun:"id" json:"id"`
	ParentID  *string `bun:"parent_id" json:"parent_id"`
	Name      string  `bun:"name" json:"name"`
	LevelCode string  `bun:"level_code" json:"level_code"`
	LevelName string  `bun:"level_name" json:"level_name"`
	LevelRank int     `bun:"level_rank" json:"level_rank"`
}

// ListSummaries returns every territory as a flat, boundary-free row with
// its level resolved, ordered top-of-tree first (level rank) then by name.
func ListSummaries(ctx context.Context, db bun.IDB) ([]Summary, error) {
	var summaries []Summary
	err := db.NewSelect().
		TableExpr("gis.territories AS t").
		ColumnExpr("t.id, t.parent_id, t.name").
		ColumnExpr("l.code AS level_code, l.name AS level_name, l.rank AS level_rank").
		Join("JOIN gis.territory_levels AS l ON l.id = t.level_id").
		OrderExpr("l.rank, t.name").
		Scan(ctx, &summaries)
	if err != nil {
		return nil, fmt.Errorf("territory: listing territory summaries: %w", err)
	}
	return summaries, nil
}

func listTerritoriesHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		summaries, err := ListSummaries(r.Context(), db)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"territories": summaries})
	}
}

func listLevelsHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		levels, err := ListLevels(r.Context(), db)
		if err != nil {
			writeError(w, err)
			return
		}
		out := make([]map[string]any, 0, len(levels))
		for _, l := range levels {
			out = append(out, map[string]any{
				"id":                   l.ID,
				"code":                 l.Code,
				"name":                 l.Name,
				"rank":                 l.Rank,
				"is_min_feature_level": l.IsMinFeatureLevel,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"levels": out})
	}
}

// boundaryRow is one drawn territory for the overlay: identity, level, and
// its boundary rendered as GeoJSON text (read back through ST_AsGeoJSON,
// the only way the geometry column round-trips out).
type boundaryRow struct {
	ID        string  `bun:"id"`
	ParentID  *string `bun:"parent_id"`
	Name      string  `bun:"name"`
	LevelCode string  `bun:"level_code"`
	LevelName string  `bun:"level_name"`
	LevelRank int     `bun:"level_rank"`
	GeomJSON  string  `bun:"geom_json"`
}

// boundariesHandler serves every territory that has a boundary as a GeoJSON
// FeatureCollection -- the source for the client-side territory overlay
// layer (a click on a polygon opens a read-only territory panel). Only
// drawn territories appear; a territory with no boundary yet is simply
// absent from the overlay. Each feature's properties carry the identity and
// level so the client can style by rank and label on click without a second
// fetch.
func boundariesHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var rows []boundaryRow
		err := db.NewSelect().
			TableExpr("gis.territories AS t").
			ColumnExpr("t.id, t.parent_id, t.name").
			ColumnExpr("l.code AS level_code, l.name AS level_name, l.rank AS level_rank").
			ColumnExpr("ST_AsGeoJSON(t.geom) AS geom_json").
			Join("JOIN gis.territory_levels AS l ON l.id = t.level_id").
			Where("t.geom IS NOT NULL").
			OrderExpr("l.rank, t.name").
			Scan(r.Context(), &rows)
		if err != nil {
			writeError(w, fmt.Errorf("territory: listing boundaries: %w", err))
			return
		}
		features := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			features = append(features, map[string]any{
				"type":     "Feature",
				"id":       row.ID,
				"geometry": json.RawMessage(row.GeomJSON),
				"properties": map[string]any{
					"id":         row.ID,
					"parent_id":  row.ParentID,
					"name":       row.Name,
					"level_code": row.LevelCode,
					"level_name": row.LevelName,
					"level_rank": row.LevelRank,
				},
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"type":     "FeatureCollection",
			"features": features,
		})
	}
}

// createTerritoryRequest is the POST body. Geometry is an optional GeoJSON
// geometry object (Polygon or MultiPolygon) -- a territory may be created
// before its boundary is drawn.
type createTerritoryRequest struct {
	ParentID *string         `json:"parent_id"`
	LevelID  string          `json:"level_id"`
	Name     string          `json:"name"`
	Geometry json.RawMessage `json:"geometry"`
}

func createTerritoryHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createTerritoryRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalidf("invalid request body: %v", err))
			return
		}
		params := CreateParams{
			ParentID: req.ParentID,
			LevelID:  req.LevelID,
			Name:     req.Name,
		}
		if len(req.Geometry) > 0 {
			geom := string(req.Geometry)
			params.GeomGeoJSON = &geom
		}
		t, err := CreateTerritory(r.Context(), db, params)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, territoryResponse(t))
	}
}

// updateTerritoryRequest is the PATCH body. Every field is optional; only
// what's present is changed. ReParent must be set true for ParentID to take
// effect (so "move to root" -- parent_id: null -- is distinguishable from
// "leave the parent alone"). Geometry replaces the boundary; ClearGeom
// removes it.
type updateTerritoryRequest struct {
	Name      *string         `json:"name"`
	Geometry  json.RawMessage `json:"geometry"`
	ClearGeom bool            `json:"clear_geom"`
	ReParent  bool            `json:"reparent"`
	ParentID  *string         `json:"parent_id"`
}

func updateTerritoryHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		var req updateTerritoryRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalidf("invalid request body: %v", err))
			return
		}
		params := UpdateParams{
			Name:      req.Name,
			ClearGeom: req.ClearGeom,
			ReParent:  req.ReParent,
			ParentID:  req.ParentID,
		}
		if len(req.Geometry) > 0 {
			geom := string(req.Geometry)
			params.GeomGeoJSON = &geom
		}
		t, err := UpdateTerritory(r.Context(), db, id, params)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, territoryResponse(t))
	}
}

func deleteTerritoryHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if err := DeleteTerritory(r.Context(), db, id); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// territoryResponse renders a single territory for a write response, with
// the boundary as a nested GeoJSON object (null when undrawn) rather than
// the escaped string the scanonly field holds.
func territoryResponse(t *Territory) map[string]any {
	var geom any
	if t.GeomGeoJSON != nil {
		geom = json.RawMessage(*t.GeomGeoJSON)
	}
	return map[string]any{
		"id":         t.ID,
		"parent_id":  t.ParentID,
		"level_id":   t.LevelID,
		"name":       t.Name,
		"geometry":   geom,
		"created_at": t.CreatedAt,
		"updated_at": t.UpdatedAt,
	}
}

// writeJSON writes a JSON body with the given status.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

// writeError maps a client-fixable InvalidError to 400 and everything else
// to 500 -- the same split api/internal/crud draws, so the territory
// endpoints report bad input distinctly from server faults.
func writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if IsInvalid(err) {
		status = http.StatusBadRequest
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
