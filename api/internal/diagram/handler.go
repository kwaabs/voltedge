package diagram

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/auth"
)

// Routes returns a router for the diagram engine, meant to be mounted
// at /diagrams:
//
//	POST   /                          create a diagram instance for a container feature
//	GET    /?container_feature_class=&container_feature_id=  list diagram instances for one container feature
//	GET    /{id}                      get one diagram instance
//	PATCH  /{id}                      rename a diagram instance
//	DELETE /{id}                      delete a diagram instance (cascades to its placements); requires diagrams:delete
//	POST   /{id}/placements           place (or move) a feature on a diagram
//	GET    /{id}/placements           list every feature placed on a diagram
//	DELETE /{id}/placements/{placementID}  un-place a feature from a diagram
//	POST   /{id}/placements/{placementID}/sectionalize  cut a drawn busbar in two, dividing its connectivity (Phase 29 Slice 3)
//	POST   /{id}/elements             add a non-feature element (box/text/line/...) to a diagram
//	GET    /{id}/elements             list every element on a diagram
//	PATCH  /{id}/elements/{elementID} move/resize/restyle/reshape an element
//	DELETE /{id}/elements/{elementID} delete an element
//	POST   /{id}/save-as-template     capture this diagram as a reusable template (Phase 24 Slice 4)
//	POST   /{id}/apply-template       instantiate a template's slots + furniture onto this diagram
//	POST   /{id}/regenerate           rebuild an auto-generated assembly single-line from its source template
//	GET    /templates                 list the reusable template library
//	DELETE /templates/{templateID}    delete one template from the library
func Routes(db *bun.DB) chi.Router {
	r := chi.NewRouter()
	r.Post("/", createInstanceHandler(db))
	r.Get("/", listInstancesHandler(db))
	// The template library, keyed by nothing but its own id -- static
	// "/templates" sits beside the "/{id}" param route (chi resolves the
	// static segment first, so a diagram id never shadows it).
	r.Get("/templates", listTemplatesHandler(db))
	r.Delete("/templates/{templateID}", deleteTemplateHandler(db))
	r.Route("/{id}", func(r chi.Router) {
		r.Get("/", getInstanceHandler(db))
		r.Patch("/", renameInstanceHandler(db))
		// Deleting a whole diagram (cascades to its placements) is escalated
		// beyond the diagrams:write the /diagrams mount already requires for
		// mutating methods: it additionally needs diagrams:delete, held only by
		// supervisor/admin/break-glass-admin (migration 0078), never editor.
		r.With(auth.RequirePermission(auth.PermDiagramsDelete, auth.PermDiagramsDelete)).
			Delete("/", deleteInstanceHandler(db))
		r.Post("/placements", placeFeatureHandler(db))
		r.Get("/placements", listPlacementsHandler(db))
		r.Patch("/placements/{placementID}", updatePlacementHandler(db))
		r.Delete("/placements/{placementID}", removePlacementHandler(db))
		r.Post("/placements/{placementID}/sectionalize", sectionalizeHandler(db))
		r.Post("/elements", createElementHandler(db))
		r.Get("/elements", listElementsHandler(db))
		r.Patch("/elements/{elementID}", updateElementHandler(db))
		r.Delete("/elements/{elementID}", removeElementHandler(db))
		r.Post("/save-as-template", saveAsTemplateHandler(db))
		r.Post("/apply-template", applyTemplateHandler(db))
		r.Post("/regenerate", regenerateHandler(db))
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

// regenerateHandler rebuilds an auto-generated assembly single-line from the
// template it was built from, replacing the old instance atomically. It rides
// the /diagrams mount's diagrams:write gate (a POST) -- a rebuild is a write, not
// a delete, even though it internally replaces the instance. Returns the build
// summary (new instance id + placed/unbound/skipped counts) so the canvas can
// navigate to the fresh diagram and surface a partial rebuild.
func regenerateHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")

		result, err := RegenerateAssemblyDiagram(r.Context(), db, id)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
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

// updatePlacementHandler applies a narrow, geometry-free change to one
// placement -- today only label visibility (migration 0054). Geometry moves
// still go through POST /placements (the upsert), so this never touches x/y/
// path.
func updatePlacementHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		placementID := chi.URLParam(r, "placementID")

		var req updatePlacementRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}

		result, err := updatePlacement(r.Context(), db, id, placementID, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
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

func createElementHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")

		var req elementRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}
		if req.Kind == "" {
			writeError(w, invalid("kind is required"))
			return
		}

		result, err := createElement(r.Context(), db, id, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func listElementsHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")

		results, err := listElements(r.Context(), db, id)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"elements": results})
	}
}

func updateElementHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		elementID := chi.URLParam(r, "elementID")

		var req elementRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}

		result, err := updateElement(r.Context(), db, id, elementID, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func removeElementHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		elementID := chi.URLParam(r, "elementID")

		if err := removeElement(r.Context(), db, id, elementID); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func saveAsTemplateHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")

		var req createTemplateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}

		result, err := saveAsTemplate(r.Context(), db, id, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func listTemplatesHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		results, err := listTemplates(r.Context(), db)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"templates": results})
	}
}

func applyTemplateHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")

		var req applyTemplateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}
		if req.TemplateID == "" {
			writeError(w, invalid("template_id is required"))
			return
		}

		results, err := applyTemplate(r.Context(), db, id, req.TemplateID)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"elements": results})
	}
}

func deleteTemplateHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		templateID := chi.URLParam(r, "templateID")

		if err := deleteTemplate(r.Context(), db, templateID); err != nil {
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
