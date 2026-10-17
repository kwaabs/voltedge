package externallink

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/kwaabs/voltedge/api/internal/crud"
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
	r.Get("/external", featureDataHandler(db, cipher))
	r.Get("/external-refs", featureRefsListHandler(db))
	r.Put("/external-refs", featureRefsSetHandler(db))
	r.Patch("/{id}", updateHandler(db))
	r.Delete("/{id}", deleteHandler(db))
	return r
}

// featureDataHandler serves GET /feature-links/external?feature_class=&feature_id=&
// system= -- live data for a feature's external_ref entry for one system (Slice 2,
// migration 0145). Unlike /{id}/data (a stored api LINK), this resolves the address
// from the feature's own external_ref jsonb + the system's path_template, so no link
// row is needed. Gated on the feature read so external data can't be pulled for a
// feature the caller couldn't open.
func featureDataHandler(db *bun.DB, cipher *secretbox.Cipher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		featureClass := q.Get("feature_class")
		featureID := q.Get("feature_id")
		system := q.Get("system")
		if featureClass == "" || featureID == "" || system == "" {
			writeError(w, invalid("feature_class, feature_id and system query parameters are required"))
			return
		}
		loaded, err := loadFeatureClass(r.Context(), db, featureClass)
		if err != nil {
			writeError(w, err)
			return
		}
		if err := crud.EnforceFeatureReadScope(r.Context(), db, loaded, featureID); err != nil {
			if crud.IsNotFoundError(err) {
				writeError(w, notFound("feature %s (%s) not found", featureID, featureClass))
				return
			}
			writeError(w, err)
			return
		}
		result, err := FetchFeatureExternalData(r.Context(), db, cipher, loaded, featureID, system)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

// featureRefsListHandler serves GET /feature-links/external-refs?feature_class=&
// feature_id= -- a feature's whole external_ref map (migration 0145), one entry
// per external system. Rides features:read (the mount's GET gate) and is scoped to
// a feature the caller can read, so a hidden feature's foreign ids don't leak.
func featureRefsListHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		featureClass := q.Get("feature_class")
		featureID := q.Get("feature_id")
		if featureClass == "" || featureID == "" {
			writeError(w, invalid("feature_class and feature_id query parameters are required"))
			return
		}
		loaded, err := loadFeatureClass(r.Context(), db, featureClass)
		if err != nil {
			writeError(w, err)
			return
		}
		if err := crud.EnforceFeatureReadScope(r.Context(), db, loaded, featureID); err != nil {
			if crud.IsNotFoundError(err) {
				writeError(w, notFound("feature %s (%s) not found", featureID, featureClass))
				return
			}
			writeError(w, err)
			return
		}
		result, err := listFeatureExternalRefs(r.Context(), db, loaded, featureID)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

// featureRefsSetHandler serves PUT /feature-links/external-refs -- replace a
// feature's whole external_ref map. Rides features:write (the mount's non-GET
// gate) and adds the Phase 58 write-scope guard, so a scoped actor can't tag a
// feature outside their territory. The trigger (0145) re-derives the reverse
// index from the new value.
func featureRefsSetHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req setFeatureRefsRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}
		if req.FeatureClass == "" || req.FeatureID == "" {
			writeError(w, invalid("feature_class and feature_id are required"))
			return
		}
		loaded, err := loadFeatureClass(r.Context(), db, req.FeatureClass)
		if err != nil {
			writeError(w, err)
			return
		}
		if err := crud.EnforceFeatureWriteScope(r.Context(), db, loaded, req.FeatureID); err != nil {
			if crud.IsNotFoundError(err) {
				writeError(w, notFound("feature %s (%s) not found", req.FeatureID, req.FeatureClass))
				return
			}
			writeError(w, err)
			return
		}
		result, err := setFeatureExternalRef(r.Context(), db, loaded, req.FeatureID, req.ExternalRef)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
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
	case crud.IsForbiddenError(err):
		status = http.StatusForbidden
	}
	writeJSON(w, status, apiError{Error: err.Error()})
}
