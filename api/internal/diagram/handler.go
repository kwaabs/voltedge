package diagram

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"
)

// Routes returns a router for the diagram engine, meant to be mounted
// at /diagrams:
//
//	POST   /                          create a diagram instance for a container feature
//	GET    /?container_feature_class=&container_feature_id=  list diagram instances for one container feature
//	GET    /{id}                      get one diagram instance
//	PATCH  /{id}                      rename a diagram instance
//	DELETE /{id}                      delete a diagram instance (cascades to its placements)
//	POST   /{id}/placements           place (or move) a feature on a diagram
//	GET    /{id}/placements           list every feature placed on a diagram
//	DELETE /{id}/placements/{placementID}  un-place a feature from a diagram
func Routes(db *bun.DB) chi.Router {
	r := chi.NewRouter()
	r.Post("/", createInstanceHandler(db))
	r.Get("/", listInstancesHandler(db))
	r.Route("/{id}", func(r chi.Router) {
		r.Get("/", getInstanceHandler(db))
		r.Patch("/", renameInstanceHandler(db))
		r.Delete("/", deleteInstanceHandler(db))
		r.Post("/placements", placeFeatureHandler(db))
		r.Get("/placements", listPlacementsHandler(db))
		r.Delete("/placements/{placementID}", removePlacementHandler(db))
	})
	return r
}

func createInstanceHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createInstanceRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}
		if req.Container.FeatureClass == "" || req.Container.FeatureID == "" {
			writeError(w, invalid("container.feature_class and container.feature_id are required"))
			return
		}
		if req.DiagramType == "" {
			writeError(w, invalid("diagram_type is required"))
			return
		}

		result, err := createInstance(r.Context(), db, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func listInstancesHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		featureClass := r.URL.Query().Get("container_feature_class")
		featureID := r.URL.Query().Get("container_feature_id")
		if featureClass == "" || featureID == "" {
			writeError(w, invalid("container_feature_class and container_feature_id query parameters are required"))
			return
		}

		results, err := listInstances(r.Context(), db, featureClass, featureID)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"diagrams": results})
	}
}

func getInstanceHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")

		result, err := getInstance(r.Context(), db, id)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func renameInstanceHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")

		var req renameInstanceRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}

		result, err := renameInstance(r.Context(), db, id, req.Name)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func deleteInstanceHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")

		if err := deleteInstance(r.Context(), db, id); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func placeFeatureHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")

		var req placeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}
		if req.FeatureClass == "" || req.FeatureID == "" {
			writeError(w, invalid("feature_class and feature_id are required"))
			return
		}

		result, err := placeFeature(r.Context(), db, id, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func listPlacementsHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")

		results, err := listPlacements(r.Context(), db, id)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"placements": results})
	}
}

func removePlacementHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		placementID := chi.URLParam(r, "placementID")

		if err := removePlacement(r.Context(), db, id, placementID); err != nil {
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
