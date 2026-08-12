package crud

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

const defaultListLimit = 50

// Routes returns a router of the generic, metadata-driven CRUD
// endpoints, meant to be mounted at something like /features:
//
//	GET    /{code}               list
//	POST   /{code}               create
//	GET    /{code}/{id}          get
//	PATCH  /{code}/{id}          update
//	POST   /{code}/{id}/publish   draft -> published, gated on completeness
//	POST   /{code}/{id}/unpublish published -> draft, no gate
//
// No feature-class-specific code exists anywhere here -- {code} is
// resolved against metadata.feature_classes at request time.
func Routes(db *bun.DB) chi.Router {
	r := chi.NewRouter()
	r.Get("/{code}", listHandler(db))
	r.Post("/{code}", createHandler(db))
	r.Get("/{code}/{id}", getHandler(db))
	r.Patch("/{code}/{id}", updateHandler(db))
	r.Post("/{code}/{id}/publish", publishHandler(db))
	r.Post("/{code}/{id}/unpublish", unpublishHandler(db))
	return r
}

func loadOrNotFound(ctx context.Context, db *bun.DB, code string) (*metadata.Loaded, error) {
	loaded, err := metadata.Load(ctx, db, code)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, notFound("feature class %q does not exist", code)
		}
		return nil, err
	}
	return loaded, nil
}

func listHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		loaded, err := loadOrNotFound(r.Context(), db, code)
		if err != nil {
			writeError(w, err)
			return
		}

		limit := defaultListLimit
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, parseErr := strconv.Atoi(v); parseErr == nil && n > 0 && n <= 500 {
				limit = n
			}
		}
		offset := 0
		if v := r.URL.Query().Get("offset"); v != "" {
			if n, parseErr := strconv.Atoi(v); parseErr == nil && n >= 0 {
				offset = n
			}
		}

		results, err := listFeatures(r.Context(), db, loaded, limit, offset)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"features": results})
	}
}

func getHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		id := chi.URLParam(r, "id")

		loaded, err := loadOrNotFound(r.Context(), db, code)
		if err != nil {
			writeError(w, err)
			return
		}

		result, err := getFeature(r.Context(), db, loaded, id)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func createHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		loaded, err := loadOrNotFound(r.Context(), db, code)
		if err != nil {
			writeError(w, err)
			return
		}

		var req createRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}
		if req.Attributes == nil {
			req.Attributes = map[string]any{}
		}

		subtype, err := validateSubtype(loaded, req.Subtype)
		if err != nil {
			writeError(w, err)
			return
		}
		if err := validateGeometry(loaded, req.Geometry); err != nil {
			writeError(w, err)
			return
		}

		if err := validateRequired(loaded, subtype, req.Attributes); err != nil {
			writeError(w, err)
			return
		}

		fields, err := validateAttributes(loaded, req.Attributes, writeCreate, nil)
		if err != nil {
			writeError(w, err)
			return
		}

		values := make(map[string]any, len(req.Attributes))
		for name, raw := range req.Attributes {
			f := fields[name]
			coerced, err := validateValue(loaded, f, raw)
			if err != nil {
				writeError(w, err)
				return
			}
			values[name] = coerced
		}

		result, err := insertFeature(r.Context(), db, loaded, subtype, req.Geometry, values)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, result)
	}
}

func updateHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		id := chi.URLParam(r, "id")

		loaded, err := loadOrNotFound(r.Context(), db, code)
		if err != nil {
			writeError(w, err)
			return
		}

		existing, err := getFeature(r.Context(), db, loaded, id)
		if err != nil {
			writeError(w, err)
			return
		}
		currentStatus, _ := existing["status"].(string)

		var req updateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}
		if req.Attributes == nil {
			req.Attributes = map[string]any{}
		}

		fields, err := validateAttributes(loaded, req.Attributes, writeUpdate, &currentStatus)
		if err != nil {
			writeError(w, err)
			return
		}

		values := make(map[string]any, len(req.Attributes))
		for name, raw := range req.Attributes {
			f := fields[name]
			coerced, err := validateValue(loaded, f, raw)
			if err != nil {
				writeError(w, err)
				return
			}
			values[name] = coerced
		}

		if currentStatus == "published" {
			if err := validateUpdateCompleteness(loaded, existing, values); err != nil {
				writeError(w, err)
				return
			}
		}

		result, err := updateFeature(r.Context(), db, loaded, id, values)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func publishHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		id := chi.URLParam(r, "id")

		loaded, err := loadOrNotFound(r.Context(), db, code)
		if err != nil {
			writeError(w, err)
			return
		}

		result, err := publishFeature(r.Context(), db, loaded, id)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func unpublishHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		id := chi.URLParam(r, "id")

		loaded, err := loadOrNotFound(r.Context(), db, code)
		if err != nil {
			writeError(w, err)
			return
		}

		result, err := unpublishFeature(r.Context(), db, loaded, id)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
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
