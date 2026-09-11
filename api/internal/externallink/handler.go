package externallink

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/kwaabs/voltedge/api/internal/secretbox"
	"github.com/uptrace/bun"
)

// Routes returns a router for the external-link engine, meant to be
// mounted at /feature-links (top-level -- /features is already taken by
// crud.Routes, and a link always names its feature in the body or query
// string, same as api/internal/relationship):
//
//	POST   /                                create a hyperlink or api link
//	GET    /?feature_class=&feature_id=     list one feature's links
//	GET    /{id}/data                       fetch an api link's live data
//	PATCH  /{id}                            rename/repoint a link
//	DELETE /{id}                            remove a link
//
// cipher is the at-rest credential decryptor the data proxy needs; nil
// when EXTERNAL_SYSTEMS_ENCRYPTION_KEY is unset, in which case fetching a
// link whose system needs auth is refused with a clear error.
func Routes(db *bun.DB, cipher *secretbox.Cipher) chi.Router {
	r := chi.NewRouter()
	r.Post("/", createHandler(db))
	r.Get("/", listHandler(db))
	r.Get("/{id}/data", dataHandler(db, cipher))
	r.Patch("/{id}", updateHandler(db))
	r.Delete("/{id}", deleteHandler(db))
	return r
}

func dataHandler(db *bun.DB, cipher *secretbox.Cipher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		result, err := fetchLinkData(r.Context(), db, cipher, id)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func createHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}
		if req.FeatureClass == "" || req.FeatureID == "" {
			writeError(w, invalid("feature_class and feature_id are required"))
			return
		}

		result, err := createLink(r.Context(), db, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, result)
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

		results, err := listLinks(r.Context(), db, featureClass, featureID)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"links": results})
	}
}

func updateHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		var req updateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}
		if req.Label == nil && req.URL == nil {
			writeError(w, invalid("nothing to update: provide label and/or url"))
			return
		}

		result, err := updateLink(r.Context(), db, id, req)
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

		if err := deleteLink(r.Context(), db, id); err != nil {
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
	case isUpstreamError(err):
		status = http.StatusBadGateway
	}
	writeJSON(w, status, apiError{Error: err.Error()})
}
