package crud

import (
	"context"
	"strings"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/drafts"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/kwaabs/voltedge/api/internal/territory"
	"github.com/uptrace/bun"
)

// This file is the Phase 58 read-enforcement helper: the single place the CRUD
// read paths (list, query, and the GET-by-id post-check) turn the acting user's
// territory scope into a WHERE fragment. It is deliberately a no-op unless every
// gate is on -- see readScope -- so with enforcement off (a fresh install) the
// engine reads exactly as it did before this phase.

// readScope resolves the acting request's spatial read filter for one feature
// class. It returns the id set the read must be constrained to and whether that
// constraint applies at all.
//
// apply is false -- meaning "read everything, add no predicate" -- when any of:
//
//   - enforcement is switched off (territory_enforcement_enabled); this is the
//     rollout gate, so the whole phase is dark until an admin flips it;
//   - the class is geometry-less, so it has no territory_id column to filter on
//     (territories scope geographic features only);
//   - the user holds territories:unscoped (admin / break-glass): scoping is
//     bypassed entirely;
//   - there is no session on the context: an internal, non-HTTP caller (e.g. the
//     engine re-reading a row it just wrote) is trusted and unscoped. Every
//     HTTP read arrives behind the session middleware, so this only spares
//     server-internal reads, never a real user request.
//
// When apply is true, ids is the user's effective territory set. An EMPTY ids is
// the fail-closed case: the user is scoped to nothing, so the caller must match
// no rows (NOT fall back to "no filter").
func readScope(ctx context.Context, db bun.IDB, loaded *metadata.Loaded) (ids []string, apply bool, err error) {
	// Geometry-less classes carry no territory_id column -- nothing to scope.
	if loaded.FeatureClass.GeometryType == "none" {
		return nil, false, nil
	}

	// The rollout gate: off means today's behavior, no predicate anywhere.
	enforced, err := territory.EnforcementEnabled(ctx, db)
	if err != nil {
		return nil, false, err
	}
	if !enforced {
		return nil, false, nil
	}

	// An internal caller (no session) is trusted; only HTTP requests carry one.
	user, ok := auth.UserFrom(ctx)
	if !ok {
		return nil, false, nil
	}

	allowed, unscoped := user.TerritoryFilter()
	if unscoped {
		return nil, false, nil
	}
	// A non-nil-but-empty slice is the genuine "scoped to nothing" case; return
	// a non-nil empty slice so the caller can tell it apart from "not applying".
	if allowed == nil {
		allowed = []string{}
	}
	return allowed, true, nil
}

// enforceReadScope is the GET-by-id counterpart to the list/query predicate:
// given an already-read feature row (formatRow's envelope), it returns a
// notFound when the acting user is scoped and the feature falls outside their
// territory set -- including a feature with no territory_id at all, which is
// invisible to a scoped user under fail-closed. Returns nil (allow) when the
// scope doesn't apply. Hiding an out-of-scope feature as "not found" rather
// than "forbidden" avoids leaking that the id exists.
func enforceReadScope(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, feature map[string]any) error {
	ids, apply, err := readScope(ctx, db, loaded)
	if err != nil {
		return err
	}
	if !apply {
		return nil
	}
	raw, ok := feature["territory_id"]
	tid, isStr := raw.(string)
	if !ok || !isStr || tid == "" {
		// No territory assigned -> invisible to a scoped user.
		return notFound("feature %v not found", feature["id"])
	}
	for _, allowed := range ids {
		if allowed == tid {
			return nil
		}
	}
	return notFound("feature %v not found", feature["id"])
}

// enforceWriteScope is the Phase 58 write guard: a scoped user may only
// create, update, or delete a feature that sits in a territory they hold.
// territoryID is the feature's resolved territory (from territory.Assign on a
// create/geometry-changing update, or the row's existing territory_id on an
// in-place edit/delete). It returns:
//
//   - nil (allow) when the scope doesn't apply -- enforcement off, geometry-less
//     class, unscoped user, or no session (see readScope);
//   - nil (allow) when territoryID is nil: a geometry-less feature isn't
//     territory-scoped, matching the read side where such classes are never
//     filtered;
//   - a forbidden (403) when territoryID is set but outside the user's set.
func enforceWriteScope(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, territoryID *string) error {
	ids, apply, err := readScope(ctx, db, loaded)
	if err != nil {
		return err
	}
	if !apply || territoryID == nil {
		return nil
	}
	for _, allowed := range ids {
		if allowed == *territoryID {
			return nil
		}
	}
	return forbidden("you don't have access to the territory this feature belongs to")
}

// enforceDraftScope is the single-row Private-drafts (Phase 61) gate: given an
// already-read feature row (formatRow's envelope), it returns a notFound when
// the row is a private draft the acting user may not see -- hiding its existence
// exactly as enforceReadScope hides an out-of-territory feature. Returns nil
// (allow) when the filter doesn't apply or the row is public/authored-in-scope.
// Called right after enforceReadScope at every single-row gate (GET, update,
// delete, publish, unpublish) so draft privacy composes with territory privacy.
// The shared filter (gate + author set) lives in package drafts.
func enforceDraftScope(ctx context.Context, db bun.IDB, feature map[string]any) error {
	authors, apply, err := drafts.VisibleAuthors(ctx, db)
	if err != nil {
		return err
	}
	status, _ := feature["status"].(string)
	createdBy, _ := feature["created_by"].(string)
	if drafts.IsVisible(status, feature["published_at"], createdBy, authors, apply) {
		return nil
	}
	return notFound("feature %v not found", feature["id"])
}

// enforceSubResourceReadScope is the read gate for a feature SUB-resource
// (history, GPS captures, capacity): endpoints that hang off a feature id but,
// unlike GET-by-id, historically queried their child rows straight from the id
// without loading and scoping the parent -- so they could be used to read a
// feature the caller is not allowed to see (out-of-territory under Phase 58, or a
// peer's private draft under Phase 61). It loads the feature and applies the same
// enforceReadScope + enforceDraftScope checks the primary GET applies.
//
// It reports a PURGED feature (row physically gone -> getFeature returns
// notFound) via purged=true rather than an error. That lets the append-only
// timeline endpoints keep their documented "history outlives the row" contract
// (a purged feature still returns its history) while every feature that still
// exists -- including soft-deleted rows, which getFeature still returns -- is
// scoped. A sub-resource that needs a live row (capacity) treats purged as a 404.
// Residual: a scoped user can still read a *purged* feature's timeline; low risk,
// as purged ids are not enumerable through the scoped list/query paths.
func enforceSubResourceReadScope(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, id string) (purged bool, err error) {
	feature, err := getFeature(ctx, db, loaded, id)
	if err != nil {
		if isNotFoundError(err) {
			return true, nil
		}
		return false, err
	}
	if err := enforceReadScope(ctx, db, loaded, feature); err != nil {
		return false, err
	}
	if err := enforceDraftScope(ctx, db, feature); err != nil {
		return false, err
	}
	return false, nil
}

// EnforceFeatureReadScope applies the Phase 58 territory + Phase 61 private-draft
// read checks to a feature identified by (loaded class, id), for callers OUTSIDE
// this package -- notably the attachments surface -- that hang off a feature id
// and must not expose a feature the CRUD read side would hide. It returns a
// not-found error (matched by IsNotFoundError) when the feature is missing OR out
// of the caller's scope, so an out-of-scope id is indistinguishable from a
// missing one. A purged feature (row gone) is reported as not-found too: unlike
// the timeline endpoints, an attachment operation always needs a live feature.
func EnforceFeatureReadScope(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, id string) error {
	purged, err := enforceSubResourceReadScope(ctx, db, loaded, id)
	if err != nil {
		return err
	}
	if purged {
		return notFound("feature %s not found", id)
	}
	return nil
}

// scopeWhere turns readScope's (ids, apply) into a WHERE fragment plus its bind
// args, appended to a query's existing predicates. It returns ("", nil) when the
// scope doesn't apply. When it does, an empty id set yields "1 = 0" (match
// nothing, fail closed); otherwise a `territory_id::text IN (?, ?, ...)` list,
// mirroring exportQuery's filter so both read paths compare the same way.
func scopeWhere(ids []string, apply bool) (clause string, args []any) {
	if !apply {
		return "", nil
	}
	if len(ids) == 0 {
		return "1 = 0", nil
	}
	placeholders := make([]string, len(ids))
	args = make([]any, len(ids))
	for i := range ids {
		placeholders[i] = "?"
		args[i] = ids[i]
	}
	return "territory_id::text IN (" + strings.Join(placeholders, ", ") + ")", args
}
