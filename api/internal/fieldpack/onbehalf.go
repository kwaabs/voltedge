package fieldpack

// Admin BUILD-ON-BEHALF (S8, docs/shared-field-packs-design.md).
//
// The third shared-pack mechanism (independent of cells): an admin pre-stages a
// PERSONAL pack AS a named crew member, so a tech picks up a ready pack on their
// first connection instead of waiting on a build. Unlike a shared cell base -- which
// is published-only and crew-neutral -- this is a real personal pack owned by the
// target user, so it must be scoped EXACTLY as if that user had requested it
// themselves: the include flags frozen from the TARGET's RBAC (not the admin's), the
// private-draft author set from the TARGET's session, and the territory filter bounded
// by the TARGET's Phase 58 scope. auth.LoadSessionUser gives that full authz snapshot
// from a user id; the pack is then enqueued with requested_by = the target, so it
// shows up in THEIR device list (listPacks is owner-scoped) and nobody else's.
//
// The admin can never grant the target more than the target could pull themselves --
// the scope is the target's, never the admin's.

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/drafts"
	"github.com/uptrace/bun"
)

// onBehalfRequest is the POST /field-packs/on-behalf body: the crew member to stage
// for, plus the same bbox + class set (+ optional territories) a normal pack request
// carries. The include flags are NOT in the body -- they're frozen from the target's
// RBAC, so an admin can't widen them.
type onBehalfRequest struct {
	UserID         string    `json:"user_id"`
	Bbox           []float64 `json:"bbox"`
	FeatureClasses []string  `json:"feature_classes"`
	Territories    []string  `json:"territories"`
}

// adminBuildOnBehalfHandler enqueues a personal pack owned by the target user, scoped
// from the target's own RBAC + territory access. Returns the new pack id (202); the
// target polls/downloads it through their normal device list.
func adminBuildOnBehalfHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req onBehalfRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "request body is not valid JSON")
			return
		}
		if req.UserID == "" {
			writeErr(w, http.StatusBadRequest, "user_id is required")
			return
		}
		if _, err := scopeBBox(req.Bbox); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if len(req.FeatureClasses) == 0 {
			writeErr(w, http.StatusBadRequest, "no feature classes selected for the pack")
			return
		}
		if status, msg := validatePackClasses(r.Context(), db, req.FeatureClasses); status != 0 {
			writeErr(w, status, msg)
			return
		}

		// Freeze the TARGET's authz snapshot (perms + territory scope + subordinates).
		// A no-such-active-user is a 400 (the admin named a bad id), not a 500.
		target, err := auth.LoadSessionUser(r.Context(), db, req.UserID)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "target user: "+err.Error())
			return
		}

		// The private-draft author set is resolved from the TARGET's session (their own
		// id + subordinates), by putting the target on the context drafts.VisibleAuthors
		// reads -- so the staged pack carries exactly the drafts the target could see,
		// never the admin's.
		draftAuthors, _, err := drafts.VisibleAuthors(auth.WithUser(r.Context(), target), db)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}

		territoryIDs, err := onBehalfTerritoryIDs(r.Context(), db, target, req.Territories)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}

		scope := PackScope{
			Bbox:                 req.Bbox,
			FeatureClasses:       req.FeatureClasses,
			TerritoryIDs:         territoryIDs,
			DraftAuthors:         draftAuthors,
			IncludeConnectivity:  target.Has("connectivity:read"),
			IncludeRelationships: target.Has("relationships:read"),
			IncludeWAM:           target.Has("wam:read"),
			IncludeFaults:        target.Has("faults:read"),
			IncludeTags:          target.Has("tags:read"),
		}
		id, err := enqueuePack(r.Context(), db, target.ID, scope)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]string{"pack_id": id, "owner_id": target.ID})
	}
}

// onBehalfTerritoryIDs bounds the staged pack's territory filter by the TARGET's
// Phase 58 scope, so an on-behalf pack can never carry features outside what the
// target could read:
//
//   - an UNSCOPED target gets exactly what a normal request would (the requested
//     territories' descendant set, or nil = no filter when none were requested);
//   - a SCOPED target is bounded to their accessible set: no explicit request => the
//     whole of it; an explicit request => the intersection (requested descendants that
//     are also in the target's scope), which fail-closes to empty (match nothing) when
//     the request is entirely outside the target's reach.
func onBehalfTerritoryIDs(ctx context.Context, db *bun.DB, target *auth.SessionUser, requested []string) ([]string, error) {
	allowed, unscoped := target.TerritoryFilter()
	if unscoped {
		return resolveTerritories(ctx, db, requested)
	}
	if allowed == nil {
		allowed = []string{} // scoped to nothing (fail closed)
	}
	if len(requested) == 0 {
		return allowed, nil
	}
	reqSet, err := resolveTerritories(ctx, db, requested)
	if err != nil {
		return nil, err
	}
	return intersectIDs(reqSet, allowed), nil
}

// intersectIDs returns the ids present in BOTH slices (order follows a), as a non-nil
// slice -- an empty result means "match nothing", the correct fail-closed filter.
func intersectIDs(a, b []string) []string {
	set := make(map[string]struct{}, len(b))
	for _, x := range b {
		set[x] = struct{}{}
	}
	out := make([]string, 0)
	for _, x := range a {
		if _, ok := set[x]; ok {
			out = append(out, x)
		}
	}
	return out
}
