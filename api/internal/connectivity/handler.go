package connectivity

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"
)

// Routes returns a router for the connectivity engine, meant to be
// mounted at /connectivity:
//
//	POST   /                establish a connection, gated on network_rules
//	GET    /?feature_class=&feature_id=  list connections for one feature
//	DELETE /{id}             revoke (permanently remove) a connection
//
// Unlike api/internal/crud, there's no {code} in the path -- a
// connection always spans two feature classes, named in the request body
// (establish) or query string (list), not the URL.
func Routes(db *bun.DB) chi.Router {
	r := chi.NewRouter()
	r.Post("/", establishHandler(db))
	r.Get("/", listHandler(db))
	r.Delete("/{id}", revokeHandler(db))
	return r
}

func establishHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req establishRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}
		if req.From.FeatureClass == "" || req.From.FeatureID == "" {
			writeError(w, invalid("from.feature_class and from.feature_id are required"))
			return
		}
		if req.To.FeatureClass == "" || req.To.FeatureID == "" {
			writeError(w, invalid("to.feature_class and to.feature_id are required"))
			return
		}

		result, err := establishConnection(r.Context(), db, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func listHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		featureClass := r.URL.Query().Get("feature_class")
		featureID := r.URL.Query().Get("feature_id")
		if featureClass == "" || featureID == "" {
			writeError(w, invalid("feature_class and feature_id query parameters are required"))
			return
		}

		results, err := listConnections(r.Context(), db, featureClass, featureID)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"connections": results})
	}
}

func revokeHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")

		if err := revokeConnection(r.Context(), db, id); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
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
