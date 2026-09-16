package crud

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/kwaabs/voltedge/api/internal/territory"
	"github.com/uptrace/bun"
)

// territoryError maps an api/internal/territory InvalidError (unknown or
// too-shallow territory, no covering territory under enforcement) to this
// package's own validationError so the HTTP layer returns 400, not 500.
// Anything else passes through unchanged.
func territoryError(err error) error {
	if territory.IsInvalid(err) {
		return invalid("%s", err.Error())
	}
	return err
}

const defaultListLimit = 50

// Routes returns a router of the generic, metadata-driven CRUD
// endpoints, meant to be mounted at something like /features:
//
//	GET    /{code}               list, excludes soft-deleted; ?name=
//	                             adds a case-insensitive partial match
//	                             against the name column; ?status=
//	                             exact-matches draft/published; ?sort=
//	                             orders by any real column on this class
//	                             (falls back to created_at if unrecognized),
//	                             ?order=desc reverses it (default asc)
//	POST   /{code}               create
//	POST   /{code}/query         structured query -- a JSON body of typed
//	                             AND/OR conditions (the map's query builder,
//	                             docs/phase-13-query-builder.md), where the
//	                             flat ?name=/?status= list params can't
//	GET    /{code}/{id}          get, includes soft-deleted
//	GET    /{code}/{id}/history   append-only change timeline, newest first
//	GET    /{code}/{id}/gps-captures  GNSS survey quality stamped on GPS-sourced
//	                             placements (field tool), newest first
//	PATCH  /{code}/{id}          update, rejected if soft-deleted
//	POST   /{code}/{id}/split     cut a line at a point: original keeps the
//	                             near half, the far half becomes a new feature
//	                             (one transaction; line classes only)
//	POST   /{code}/{id}/publish   draft -> published, gated on completeness
//	POST   /{code}/{id}/unpublish published -> draft, no gate
//	DELETE /{code}/{id}          soft delete (sets deleted_at), no gate
//	POST   /{code}/{id}/restore   undoes a soft delete, no gate
//	DELETE /{code}/{id}/purge     hard delete, requires prior soft-delete
//
// No feature-class-specific code exists anywhere here -- {code} is
// resolved against metadata.feature_classes at request time.
func Routes(db *bun.DB) chi.Router {
	r := chi.NewRouter()
	r.Get("/{code}", listHandler(db))
	r.Post("/{code}", createHandler(db))
	r.Post("/{code}/query", queryHandler(db))
	r.Get("/{code}/{id}", getHandler(db))
	r.Get("/{code}/{id}/history", historyHandler(db))
	r.Get("/{code}/{id}/gps-captures", gpsCapturesHandler(db))
	r.Get("/{code}/{id}/capacity", capacityHandler(db))
	r.Patch("/{code}/{id}", updateHandler(db))
	r.Post("/{code}/{id}/split", splitHandler(db))
	r.Post("/{code}/{id}/publish", publishHandler(db))
	r.Post("/{code}/{id}/unpublish", unpublishHandler(db))
	r.Delete("/{code}/{id}", deleteHandler(db))
	r.Post("/{code}/{id}/restore", restoreHandler(db))
	r.Delete("/{code}/{id}/purge", purgeHandler(db))
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
		opts := listOptions{
			Limit:     limit,
			Offset:    offset,
			NameQuery: r.URL.Query().Get("name"),
			Status:    r.URL.Query().Get("status"),
			Sort:      r.URL.Query().Get("sort"),
			Desc:      strings.EqualFold(r.URL.Query().Get("order"), "desc"),
		}

		results, err := listFeatures(r.Context(), db, loaded, opts)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"features": results})
	}
}

func queryHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		loaded, err := loadOrNotFound(r.Context(), db, code)
		if err != nil {
			writeError(w, err)
			return
		}

		var req queryRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}

		results, err := queryFeatures(r.Context(), db, loaded, req)
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
		// Phase 58 spatial read enforcement: a scoped user may not read a
		// feature outside their territory set. getFeature itself stays
		// unscoped (internal callers depend on it), so the check is a
		// post-filter here -- an out-of-scope (or unassigned) feature reads as
		// a 404, the same answer a scoped user gets from list.
		if err := enforceReadScope(r.Context(), db, loaded, result); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

// historyHandler serves a feature's append-only change timeline (Phase 41).
// It never 404s on a purged feature: the feature row may be gone but its
// history outlives it, so an empty list and a purged-feature list are both
// valid answers to the same URL. loadOrNotFound still guards an unknown class.
func historyHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		id := chi.URLParam(r, "id")

		loaded, err := loadOrNotFound(r.Context(), db, code)
		if err != nil {
			writeError(w, err)
			return
		}

		entries, err := listFeatureHistory(r.Context(), db, loaded.FeatureClass.Code, id)
		if err != nil {
			writeError(w, err)
			return
		}
		if entries == nil {
			entries = []historyEntry{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"history": entries})
	}
}

// gpsCapturesHandler serves a feature's GNSS survey-quality captures (Phase
// gnss-quality Slice 3): the fix DOP/accuracy/satellites recorded each time a
// field crew placed or moved it at their device's position. Like the history
// timeline it never 404s on a purged feature -- the capture record outlives the
// row -- and an empty list is the normal answer for any feature never placed by
// GPS (every map-drawn or REST-created feature). loadOrNotFound still guards an
// unknown class.
func gpsCapturesHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		id := chi.URLParam(r, "id")

		loaded, err := loadOrNotFound(r.Context(), db, code)
		if err != nil {
			writeError(w, err)
			return
		}

		entries, err := ListFeatureGpsCaptures(r.Context(), db, loaded.FeatureClass.Code, id)
		if err != nil {
			writeError(w, err)
			return
		}
		if entries == nil {
			entries = []gpsCaptureEntry{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"gps_captures": entries})
	}
}

// capacityHandler serves a feature's coarse spare-way capacity (Phase 44 Slice
// 3) -- way_count minus the count of cable_routing links pulled through it.
// Registered on the generic /features sub-resource path, so it rides the
// mount's features:read gate (no extra RBAC). It 400s for a class without a
// way_count field (see ductCapacity), keeping the generic route honest for a
// non-CABLE_DUCT class.
func capacityHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		id := chi.URLParam(r, "id")

		loaded, err := loadOrNotFound(r.Context(), db, code)
		if err != nil {
			writeError(w, err)
			return
		}

		result, err := ductCapacity(r.Context(), db, loaded, id)
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

		result, err := CreateFeature(r.Context(), db, loaded, CreateFeatureInput{
			Subtype:     req.Subtype,
			Name:        req.Name,
			Geometry:    req.Geometry,
			TerritoryID: req.TerritoryID,
			Attributes:  req.Attributes,
		})
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

		var req updateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}

		result, err := UpdateFeature(r.Context(), db, loaded, id, UpdateFeatureInput{
			Name:              req.Name,
			Geometry:          req.Geometry,
			TerritoryID:       req.TerritoryID,
			Attributes:        req.Attributes,
			ExpectedUpdatedAt: req.ExpectedUpdatedAt,
		})
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

func deleteHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		id := chi.URLParam(r, "id")

		loaded, err := loadOrNotFound(r.Context(), db, code)
		if err != nil {
			writeError(w, err)
			return
		}

		result, err := softDeleteFeature(r.Context(), db, loaded, id)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func restoreHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		id := chi.URLParam(r, "id")

		loaded, err := loadOrNotFound(r.Context(), db, code)
		if err != nil {
			writeError(w, err)
			return
		}

		result, err := restoreFeature(r.Context(), db, loaded, id)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func purgeHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		id := chi.URLParam(r, "id")

		loaded, err := loadOrNotFound(r.Context(), db, code)
		if err != nil {
			writeError(w, err)
			return
		}

		if err := hardDeleteFeature(r.Context(), db, loaded, id); err != nil {
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
	case isConflictError(err):
		status = http.StatusConflict
	case isForbiddenError(err):
		status = http.StatusForbidden
	}
	writeJSON(w, status, apiError{Error: err.Error()})
}
