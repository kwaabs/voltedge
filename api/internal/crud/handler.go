package crud

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/kwaabs/voltedge/api/internal/auth"
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
//	POST   /{code}/aggregate     the same filter tree folded into counts:
//	                             an overall total, a count-by-<field> GROUP BY,
//	                             an optional sum/avg/min/max, and an optional
//	                             territory/radius spatial scope (the guided
//	                             query panel's numeric + spatial analysis)
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
	// Cross-feature activity log (who changed what, in a window). A static segment,
	// so chi matches it ahead of the /{code} class routes below.
	r.Get("/activity", activityHandler(db))
	r.Get("/edit-sessions", editSessionsHandler(db))
	r.Get("/{code}", listHandler(db))
	r.Post("/{code}", createHandler(db))
	r.Post("/{code}/query", queryHandler(db))
	r.Post("/{code}/aggregate", aggregateHandler(db))
	r.Get("/{code}/{id}", getHandler(db))
	r.Get("/{code}/{id}/history", historyHandler(db))
	r.Get("/{code}/{id}/gps-captures", gpsCapturesHandler(db))
	r.Get("/{code}/{id}/capacity", capacityHandler(db))
	r.Patch("/{code}/{id}", updateHandler(db))
	r.Patch("/{code}/{id}/lifecycle", lifecycleHandler(db))
	r.Post("/{code}/{id}/split", splitHandler(db))
	r.Post("/{code}/{id}/publish", publishHandler(db))
	r.Post("/{code}/{id}/unpublish", unpublishHandler(db))
	r.Delete("/{code}/{id}", deleteHandler(db))
	r.Post("/{code}/{id}/restore", restoreHandler(db))
	r.Delete("/{code}/{id}/purge", purgeHandler(db))
	// Private-drafts Phase B: apply/discard the acting author's staged private
	// edit of an already-published feature. apply-pending publishes the change
	// onto the public row (conflict-checked); DELETE .../pending throws it away.
	r.Post("/{code}/{id}/apply-pending", applyPendingHandler(db))
	r.Delete("/{code}/{id}/pending", discardPendingHandler(db))
	// Bulk class wipe (the "nuke" behind the Features browser). Two-step,
	// soft-then-purge, and one tier above features:write: each is gated in-handler
	// on features:wipe (admin / break-glass only, migration 0116) on top of the
	// features:write the /features mount already requires. counts fuels the
	// confirm dialog; wipe soft-deletes every live in-scope row; purge-deleted
	// permanently removes every soft-deleted in-scope row with its cascade.
	r.Get("/{code}/counts", classCountsHandler(db))
	r.Post("/{code}/wipe", wipeClassHandler(db))
	r.Post("/{code}/purge-deleted", purgeDeletedClassHandler(db))
	return r
}

// requireWipePerm is the in-handler gate for the bulk class operations. The
// /features mount already refused anyone without features:write; this adds the
// stricter features:wipe check (admin / break-glass only) so an ordinary editor
// can never trigger a class-scale erasure. Returns a forbidden (403) error to
// feed straight into writeError; a missing session is treated as forbidden too
// (the session middleware normally yields a 401 upstream of here).
func requireWipePerm(r *http.Request) error {
	user, ok := auth.UserFrom(r.Context())
	if !ok || !user.Has(auth.PermFeaturesWipe) {
		return forbidden("bulk class wipe requires the %s permission", auth.PermFeaturesWipe)
	}
	return nil
}

// classCountsHandler returns the in-scope live and soft-deleted row counts so the
// confirm dialog can show how many features each step would touch.
func classCountsHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := requireWipePerm(r); err != nil {
			writeError(w, err)
			return
		}
		code := chi.URLParam(r, "code")
		loaded, err := loadOrNotFound(r.Context(), db, code)
		if err != nil {
			writeError(w, err)
			return
		}
		counts, err := CountClassFeatures(r.Context(), db, loaded)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, counts)
	}
}

// wipeClassHandler soft-deletes every live, in-scope feature in the class (step
// one, reversible via per-feature restore). Returns the number of rows wiped.
func wipeClassHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := requireWipePerm(r); err != nil {
			writeError(w, err)
			return
		}
		code := chi.URLParam(r, "code")
		loaded, err := loadOrNotFound(r.Context(), db, code)
		if err != nil {
			writeError(w, err)
			return
		}
		wiped, err := WipeClass(r.Context(), db, loaded)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"soft_deleted": wiped})
	}
}

// purgeDeletedClassHandler permanently removes every soft-deleted, in-scope
// feature in the class and cleans its polymorphic references (step two,
// irreversible). Returns the number of rows purged.
func purgeDeletedClassHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := requireWipePerm(r); err != nil {
			writeError(w, err)
			return
		}
		code := chi.URLParam(r, "code")
		loaded, err := loadOrNotFound(r.Context(), db, code)
		if err != nil {
			writeError(w, err)
			return
		}
		purged, err := PurgeDeletedClass(r.Context(), db, loaded)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"purged": purged})
	}
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
		// Private-drafts Phase B: overlay this author's staged edits onto the page
		// and drop rows they privately deleted (see overlayFeatures).
		results, err = overlayFeatures(r.Context(), db, loaded, results)
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
		// Private-drafts Phase B: same author overlay as list (see overlayFeatures).
		results, err = overlayFeatures(r.Context(), db, loaded, results)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"features": results})
	}
}

// aggregateHandler serves POST /{code}/aggregate: the numeric/spatial analysis
// counterpart to queryHandler. It's a read (a count, never a write) done via
// POST because the filter/scope body doesn't fit query params -- it rides the
// same features:read/features:write mount gate as /query. No overlayFeatures
// pass: an aggregate returns counts, not the individual rows the private-edit
// overlay rewrites (see aggregateWhere).
func aggregateHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		loaded, err := loadOrNotFound(r.Context(), db, code)
		if err != nil {
			writeError(w, err)
			return
		}

		var req aggregateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}

		resp, err := aggregateFeatures(r.Context(), db, loaded, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, resp)
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
		// Private-drafts (Phase 61): a never-published feature reads as a 404 for
		// anyone but its author and the author's management chain. Same post-filter
		// shape as the territory check above (getFeature stays unscoped for
		// internal callers), and the same 404 so a private draft's id doesn't leak.
		if err := enforceDraftScope(r.Context(), db, result); err != nil {
			writeError(w, err)
			return
		}
		// Private-drafts Phase B: overlay the acting author's private edit onto the
		// public row (their own reads see their staged change), or 404 if they
		// privately deleted it. A no-op unless the overlay is active and this
		// author has a pending edit for this feature. getFeature stays raw for
		// internal callers, so the overlay is applied here at the read boundary.
		hidden, err := overlayFeature(r.Context(), db, loaded, result)
		if err != nil {
			writeError(w, err)
			return
		}
		if hidden {
			writeError(w, notFound("feature %s not found", id))
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

// activityHandler serves the cross-feature activity log (activity.go): who changed
// what, filterable by actor_id / feature_class / action and a from/to time window
// (RFC3339 timestamps), newest first -- the "what did this user do in this window"
// accountability view, as opposed to a single feature's timeline.
func activityHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Escalate above the features:read the /features mount already requires:
		// an accountability view ("what did user X do") is a monitoring capability,
		// not something every viewer should aggregate across people. Gate it on
		// admin:users -- the same permission behind the user-management tree.
		if user, ok := auth.UserFrom(r.Context()); !ok || !user.Has(auth.PermUsersManage) {
			writeError(w, forbidden("the activity log requires the %s permission", auth.PermUsersManage))
			return
		}
		f := ActivityFilter{
			ActorID:      r.URL.Query().Get("actor_id"),
			FeatureClass: r.URL.Query().Get("feature_class"),
			Action:       r.URL.Query().Get("action"),
		}
		if v := r.URL.Query().Get("from"); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				writeError(w, invalid("from must be an RFC3339 timestamp: %v", err))
				return
			}
			f.From = &t
		}
		if v := r.URL.Query().Get("to"); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				writeError(w, invalid("to must be an RFC3339 timestamp: %v", err))
				return
			}
			f.To = &t
		}
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				f.Limit = n
			}
		}

		entries, err := listActivity(r.Context(), db, f)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"activity": entries})
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

		// Phase 58/61: scope the timeline to a feature the caller may actually
		// read. A purged feature (row gone) still returns its history by design,
		// so purged is ignored here -- only a live-but-out-of-scope feature blocks.
		if _, err := enforceSubResourceReadScope(r.Context(), db, loaded, id); err != nil {
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

		// Phase 58/61: these captures carry position + (via the users join) the
		// surveyor's email, so gate them to a feature the caller may read. Purged
		// feature keeps returning its captures, matching the timeline's contract.
		if _, err := enforceSubResourceReadScope(r.Context(), db, loaded, id); err != nil {
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

		// Phase 58/61: scope to a feature the caller may read. Capacity needs a
		// live row, so a purged (or out-of-scope) feature is a 404 either way.
		if purged, err := enforceSubResourceReadScope(r.Context(), db, loaded, id); err != nil {
			writeError(w, err)
			return
		} else if purged {
			writeError(w, notFound("feature %s not found", id))
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
			Subtype:           req.Subtype,
			ExpectedUpdatedAt: req.ExpectedUpdatedAt,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

// lifecycleHandler moves a feature along its asset-lifecycle axis (status and/or
// the commission/retire dates). A dedicated route rather than part of the generic
// PATCH update because a lifecycle change is a deliberate operational act that
// drives the network gate and the aging report -- see crud.SetLifecycle. Gated by
// the /features mount's features:write, same as a normal edit.
func lifecycleHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		id := chi.URLParam(r, "id")

		loaded, err := loadOrNotFound(r.Context(), db, code)
		if err != nil {
			writeError(w, err)
			return
		}

		var req LifecycleInput
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}

		result, err := SetLifecycle(r.Context(), db, loaded, id, req)
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

// applyPendingHandler publishes the acting author's staged private edit onto the
// public feature (Private-drafts Phase B). A missing staged edit is a 404, a base
// row changed since staging is a 409 (see ApplyPendingEdit).
func applyPendingHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		id := chi.URLParam(r, "id")

		loaded, err := loadOrNotFound(r.Context(), db, code)
		if err != nil {
			writeError(w, err)
			return
		}

		result, err := ApplyPendingEdit(r.Context(), db, loaded, id)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

// discardPendingHandler throws away the acting author's staged private edit
// (Private-drafts Phase B), leaving the public feature untouched.
func discardPendingHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		id := chi.URLParam(r, "id")

		loaded, err := loadOrNotFound(r.Context(), db, code)
		if err != nil {
			writeError(w, err)
			return
		}

		if err := DiscardPendingEdit(r.Context(), db, loaded, id); err != nil {
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
