package featureregistry

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/territory"
)

// Routes returns the feature-registry read endpoint:
//
//	GET /feature-registry?id=<uuid>                 resolve one id -> {feature_id, feature_class_code, class_name, ...} (404 if unknown)
//	GET /feature-registry?q=<prefix>&class=&live=   typeahead over the registry (id prefix, optional class, live-only)
//
// Read-only, gated features:read on the mount in api/cmd/server/main.go (the
// same capability the feature CRUD read side uses -- the registry is just an
// index of those same features). No write verbs: the registry is maintained by
// the compiler-attached DB trigger, never over HTTP.
//
// Both the resolve and typeahead paths are territory-scoped (Phase 58): the
// session's accessible territories are resolved (resolveTerritoryScope) and
// threaded into the store, which hides out-of-territory geometry features while
// keeping geometry-less classes visible -- the same rule crud.enforceReadScope
// applies -- so the registry can't be used to enumerate ids the CRUD read side
// would refuse. Enforcement-off / unscoped (break-glass) / internal callers are
// unscoped, exactly as elsewhere.
//
// It does NOT yet apply the Phase-61 private-drafts filter: the registry row
// carries status but not created_by/published_at, so an author-chain predicate
// would need those columns added to feature_registry (a migration + a change to
// the compiler-attached maintenance trigger). Until then the registry can still
// surface the id/existence of a peer's unpublished draft WITHIN the caller's own
// territories (an id-only leak -- no attributes or geometry, which the
// CRUD/tiles/export/pack read paths all gate). Tracked as a follow-up.
func Routes(db *bun.DB) chi.Router {
	r := chi.NewRouter()
	// Relative to the /feature-registry mount (see cmd/server/main.go) -- the
	// oms/wam convention. The mount adds the prefix; routing "/feature-registry"
	// here would double it to /feature-registry/feature-registry.
	r.Get("/", listHandler(db))
	return r
}

func listHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()

		scope, applyScope, err := resolveTerritoryScope(r.Context(), db)
		if err != nil {
			writeError(w, err)
			return
		}

		// An exact id wins: this is the "what is this id?" resolve path.
		if id := q.Get("id"); id != "" {
			entry, err := Lookup(r.Context(), db, id, scope, applyScope)
			if err != nil {
				writeError(w, err)
				return
			}
			if entry == nil {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "no feature with that id"})
				return
			}
			writeJSON(w, http.StatusOK, entry)
			return
		}

		filter := SearchFilter{
			Q:     q.Get("q"),
			Class: q.Get("class"),
			Live:  q.Get("live") == "true",
		}
		// A non-nil TerritoryIDs (even empty) is the store's apply signal; only
		// set it when scoping applies, so an unscoped caller stays unfiltered.
		if applyScope {
			if scope == nil {
				scope = []string{}
			}
			filter.TerritoryIDs = scope
		}
		entries, err := Search(r.Context(), db, filter)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
	}
}

// resolveTerritoryScope mirrors crud.readScope for the registry endpoint: it
// returns the caller's accessible territory id set and whether to constrain to
// it. apply is false -- read everything -- when enforcement is off, there is no
// session (an internal caller), or the user holds territories:unscoped
// (admin/break-glass). An empty non-nil id set with apply=true is "scoped to
// nothing" (only geometry-less classes remain visible; see scopeTerritory).
func resolveTerritoryScope(ctx context.Context, db *bun.DB) (ids []string, apply bool, err error) {
	enforced, err := territory.EnforcementEnabled(ctx, db)
	if err != nil {
		return nil, false, err
	}
	if !enforced {
		return nil, false, nil
	}
	user, ok := auth.UserFrom(ctx)
	if !ok {
		return nil, false, nil
	}
	allowed, unscoped := user.TerritoryFilter()
	if unscoped {
		return nil, false, nil
	}
	if allowed == nil {
		allowed = []string{}
	}
	return allowed, true, nil
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, err error) {
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
}
