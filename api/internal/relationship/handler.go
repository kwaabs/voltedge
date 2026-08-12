package relationship

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"
)

// Routes returns a router for the relationship engine, meant to be
// mounted at /relationships:
//
//	POST   /                establish a relationship, gated on relationship_classes + cardinality
//	GET    /?feature_class=&feature_id=  list relationships for one feature
//	DELETE /{id}             revoke (permanently remove) a relationship
//
// Same shape as api/internal/connectivity's Routes -- no {code} in the
// path, since a relationship always spans two feature classes named in
// the request body or query string.
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
		if req.Origin.FeatureClass == "" || req.Origin.FeatureID == "" {
			writeError(w, invalid("origin.feature_class and origin.feature_id are required"))
			return
		}
		if req.Destination.FeatureClass == "" || req.Destination.FeatureID == "" {
			writeError(w, invalid("destination.feature_class and destination.feature_id are required"))
			return
		}

		result, err := establishRelationship(r.Context(), db, req)
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

		results, err := listRelationships(r.Context(), db, featureClass, featureID)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"relationships": results})
	}
}

func revokeHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")

		if err := revokeRelationship(r.Context(), db, id); err != nil {
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
