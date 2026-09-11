package externalsystem

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/kwaabs/voltedge/api/internal/secretbox"
	"github.com/uptrace/bun"
)

// Routes returns a router for the external-systems catalog, meant to be
// mounted at /external-systems behind the metadata:read/metadata:write
// gate (same admin capability as domains and material specs). The
// frontend surfaces it under /admin/external-systems, mirroring how
// field-packs' admin API sits top-level while its page lives under
// /admin.
//
//	GET    /            list every external system (never the secrets)
//	POST   /            create a system
//	PATCH  /{code}      update a system (credential is write-only)
//	DELETE /{code}      remove a system
//
// cipher is the at-rest credential encryptor; nil when
// EXTERNAL_SYSTEMS_ENCRYPTION_KEY is unset, in which case storing a
// credential is refused with a clear error.
func Routes(db *bun.DB, cipher *secretbox.Cipher) chi.Router {
	s := &store{db: db, cipher: cipher}
	r := chi.NewRouter()
	r.Get("/", listHandler(s))
	r.Post("/", createHandler(s))
	r.Patch("/{code}", updateHandler(s))
	r.Delete("/{code}", deleteHandler(s))
	return r
}

func listHandler(s *store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		systems, err := listSystems(r.Context(), s)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"external_systems": systems})
	}
}

func createHandler(s *store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		result, err := createSystem(r.Context(), s, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, result)
	}
}

func updateHandler(s *store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		var req updateRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		result, err := updateSystem(r.Context(), s, code, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func deleteHandler(s *store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		if err := deleteSystem(r.Context(), s, code); err != nil {
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

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, invalid("request body is not valid JSON: %v", err))
		return false
	}
	return true
}
