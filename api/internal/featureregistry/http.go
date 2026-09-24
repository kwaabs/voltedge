package featureregistry

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/drafts"
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
// Both the resolve and typeahead paths apply the SAME visibility filters the
// CRUD read side does (resolveScope): the Phase-58 territory scope (hide
// out-of-territory geometry features, keep geometry-less classes) AND the Phase-61
// private-drafts filter (hide a peer's unpublished draft, keep public + own +
// subordinate authors). So the registry can't be used to enumerate the id or
// existence of anything the reader itself couldn't open. Enforcement-off /
// unscoped (break-glass) / internal callers are unscoped, exactly as elsewhere.
//
// The draft filter relies on registry columns created_by/published_at, populated
// by the shared sync trigger (migration 0118) -- no compiler change was needed
// because every per-class trigger calls that one function.
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

		scope, err := resolveScope(r.Context(), db)
		if err != nil {
			writeError(w, err)
			return
		}

		// An exact id wins: this is the "what is this id?" resolve path.
		if id := q.Get("id"); id != "" {
			entry, err := Lookup(r.Context(), db, id, scope)
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

		entries, err := Search(r.Context(), db, SearchFilter{
			Q:     q.Get("q"),
			Class: q.Get("class"),
			Live:  q.Get("live") == "true",
			Scope: scope,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
	}
}

// resolveScope builds the registry read scope from the caller's session,
// mirroring the CRUD read side so the registry hides exactly what a feature read
// would: the Phase-58 territory set (resolveTerritoryScope) and the Phase-61
// draft-author set (drafts.VisibleAuthors). Either filter is switched off
// independently -- territory scoping is off when territory enforcement is off, and
// the draft filter is off when private-drafts are disabled -- and both are off for
// an unscoped/break-glass user or an internal no-session caller.
func resolveScope(ctx context.Context, db *bun.DB) (Scope, error) {
	territoryIDs, applyTerritory, err := resolveTerritoryScope(ctx, db)
	if err != nil {
		return Scope{}, err
	}
	if applyTerritory && territoryIDs == nil {
		territoryIDs = []string{}
	}

	draftAuthors, applyDrafts, err := drafts.VisibleAuthors(ctx, db)
	if err != nil {
		return Scope{}, err
	}

	return Scope{
		TerritoryIDs:   territoryIDs,
		ApplyTerritory: applyTerritory,
		DraftAuthors:   draftAuthors,
		ApplyDrafts:    applyDrafts,
	}, nil
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
