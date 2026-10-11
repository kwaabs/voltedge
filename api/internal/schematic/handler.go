package schematic

import (
	"encoding/json"
	"math"
	"net/http"
	"os"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"
)

// Routes returns the schematic router, mounted at /schematic behind a
// features:read gate (it reads the same features the map already shows):
//
//	GET  /?minx=&miny=&maxx=&maxy=[&max_nodes=]
//	     the per-feature graph for a lon/lat viewport (device_state overlay source)
//	POST /one-line  {bbox|feature_refs}
//	     the ephemeral, laid-out one-line for a selection or the current view
//
// Both are READS (a POST only because the selection is a list in the body) -- the
// mount's method-aware gate resolves to features:read for every call.
func Routes(db *bun.DB) chi.Router {
	r := chi.NewRouter()
	r.Get("/", viewHandler(db))
	r.Post("/one-line", oneLineHandler(db))
	return r
}

// oneLineRequest is the POST /schematic/one-line body: EITHER an explicit feature
// set (a map selection) OR a lon/lat viewport. feature_refs wins when both are
// present.
type oneLineRequest struct {
	FeatureRefs []FeatureRef `json:"feature_refs,omitempty"`
	Bbox        *oneLineBbox `json:"bbox,omitempty"`
}

type oneLineBbox struct {
	MinX float64 `json:"min_x"`
	MinY float64 `json:"min_y"`
	MaxX float64 `json:"max_x"`
	MaxY float64 `json:"max_y"`
}

// oneLineHandler builds the ephemeral one-line for a selection or a viewport and
// returns it as data (nothing is persisted).
func oneLineHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req oneLineRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "request body is not valid JSON")
			return
		}
		maxNodes := envMaxNodes()

		var view *View
		var err error
		switch {
		case len(req.FeatureRefs) > 0:
			view, err = BuildViewForRefs(r.Context(), db, req.FeatureRefs, maxNodes)
		case req.Bbox != nil:
			b := BBox{MinX: req.Bbox.MinX, MinY: req.Bbox.MinY, MaxX: req.Bbox.MaxX, MaxY: req.Bbox.MaxY}
			if !(b.MinX < b.MaxX && b.MinY < b.MaxY) {
				writeError(w, http.StatusBadRequest, "bbox min_x < max_x and min_y < max_y are required")
				return
			}
			view, err = BuildView(r.Context(), db, b, maxNodes)
			// Pull in the endpoints of conductors that cross the viewport, so a long
			// conductor leaving the view still connects on the one-line.
			if err == nil {
				err = CloseConductorEndpoints(r.Context(), db, view, maxNodes)
			}
		default:
			writeError(w, http.StatusBadRequest, "either feature_refs or bbox is required")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to build one-line")
			return
		}
		writeJSON(w, http.StatusOK, BuildOneLine(view))
	}
}

// maxNodesCeiling bounds whatever a request (or the environment) asks for, so a
// client can lower the cap but never raise it past a sane ceiling.
const maxNodesCeiling = 5000

// envMaxNodes reads the server-wide default node cap (SCHEMATIC_MAX_NODES),
// falling back to DefaultMaxNodes when unset or unparseable.
func envMaxNodes() int {
	if v := os.Getenv("SCHEMATIC_MAX_NODES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return DefaultMaxNodes
}

func viewHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		bbox, err := parseBBox(q)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		maxNodes := envMaxNodes()
		if v := q.Get("max_nodes"); v != "" {
			if n, perr := strconv.Atoi(v); perr == nil && n > 0 && n < maxNodes {
				// A client may only TIGHTEN the cap (a stricter scale gate), never
				// loosen it past the server default.
				maxNodes = n
			}
		}
		if maxNodes > maxNodesCeiling {
			maxNodes = maxNodesCeiling
		}

		view, err := BuildView(r.Context(), db, bbox, maxNodes)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to build schematic")
			return
		}
		writeJSON(w, http.StatusOK, view)
	}
}

// parseBBox reads and validates the four required viewport params. It rejects a
// missing, non-numeric, non-finite, or inverted box rather than silently
// querying the whole world.
func parseBBox(q map[string][]string) (BBox, error) {
	get := func(k string) (float64, bool) {
		vs, ok := q[k]
		if !ok || len(vs) == 0 {
			return 0, false
		}
		f, err := strconv.ParseFloat(vs[0], 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return 0, false
		}
		return f, true
	}
	minx, ok1 := get("minx")
	miny, ok2 := get("miny")
	maxx, ok3 := get("maxx")
	maxy, ok4 := get("maxy")
	if !ok1 || !ok2 || !ok3 || !ok4 {
		return BBox{}, errBadBBox
	}
	if minx >= maxx || miny >= maxy {
		return BBox{}, errBadBBox
	}
	return BBox{MinX: minx, MinY: miny, MaxX: maxx, MaxY: maxy}, nil
}

var errBadBBox = badRequest("minx, miny, maxx, maxy are required finite numbers with min < max")

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// badRequest is a tiny sentinel error type so parseBBox can return a typed,
// message-bearing validation error without importing a shared errors package.
type badRequest string

func (e badRequest) Error() string { return string(e) }
