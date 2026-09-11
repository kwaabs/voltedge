package mapannotation

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"
)

// Routes returns a router for the map-annotation (redlining) engine, meant to
// be mounted at /map-annotations. It is the geographic sibling of the diagram
// element API: free-form markup (text/box/line/arrow) drawn on the map, each
// individually styled, pointing at no feature.
//
//	GET    /?territory_id=&bbox=minx,miny,maxx,maxy  list, optionally scoped to a territory and/or viewport
//	POST   /                                          create an annotation
//	GET    /{id}                                      get one annotation
//	PATCH  /{id}                                      update geometry/text/style/z_index
//	DELETE /{id}                                      delete an annotation
func Routes(db *bun.DB) chi.Router {
	r := chi.NewRouter()
	r.Get("/", listHandler(db))
	r.Post("/", createHandler(db))
	r.Route("/{id}", func(r chi.Router) {
		r.Get("/", getHandler(db))
		r.Patch("/", updateHandler(db))
		r.Delete("/", deleteHandler(db))
	})
	return r
}

func listHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var territoryID *string
		if t := r.URL.Query().Get("territory_id"); t != "" {
			territoryID = &t
		}

		box, err := parseBBox(r.URL.Query().Get("bbox"))
		if err != nil {
			writeError(w, err)
			return
		}

		results, err := listAnnotations(r.Context(), db, territoryID, box)
		if err != nil {
			writeError(w, err)
			return
		}
		if results == nil {
			results = []annotationResponse{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"annotations": results})
	}
}

func createHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req annotationRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}

		result, err := createAnnotation(r.Context(), db, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func getHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")

		result, err := getAnnotation(r.Context(), db, id)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func updateHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")

		var req annotationRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}

		result, err := updateAnnotation(r.Context(), db, id, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func deleteHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")

		if err := deleteAnnotation(r.Context(), db, id); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// parseBBox turns a "minx,miny,maxx,maxy" query param into a *bbox, or nil when
// the param is absent. All four parts must be present, numeric, and form a
// non-degenerate envelope.
func parseBBox(raw string) (*bbox, error) {
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	if len(parts) != 4 {
		return nil, invalid("bbox must be four comma-separated numbers minx,miny,maxx,maxy")
	}
	vals := make([]float64, 4)
	for i, p := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return nil, invalid("bbox value %q is not a number", p)
		}
		vals[i] = v
	}
	if vals[0] >= vals[2] || vals[1] >= vals[3] {
		return nil, invalid("bbox must have minx<maxx and miny<maxy")
	}
	return &bbox{MinX: vals[0], MinY: vals[1], MaxX: vals[2], MaxY: vals[3]}, nil
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case isValidationError(err):
		status = http.StatusBadRequest
	case isNotFoundError(err):
		status = http.StatusNotFound
	}
	writeJSON(w, status, apiError{Error: err.Error()})
}
