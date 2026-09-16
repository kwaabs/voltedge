package auth

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/access"
)

// loadScopeFields resolves the Phase 58 spatial-access fields for a
// SessionUser given its already-loaded permission set: whether the user
// bypasses territory scoping (holds access.PermUnscoped), and -- when it does
// not -- the effective territory-id set the request is scoped to
// (access.CachedEffectiveIDs, the cached form of the recursive resolver).
//
// Kept in package auth (not access) so access never imports auth -- auth
// depends on access, not the other way around. Both the cookie-session path
// (loadSession) and the API-token path (ResolveAPIToken) call it, so a bearer
// client is scoped exactly as a browser session is; a token can never see more
// than the user it belongs to.
//
// An unscoped user skips the resolver entirely (nil ids, no query), which also
// keeps admin/break-glass reads and tiles fast. A resolver failure for an
// otherwise-valid session is a real server fault, so it propagates and the
// request fails closed rather than silently widening or narrowing scope.
func loadScopeFields(ctx context.Context, db bun.IDB, userID string, perms map[string]struct{}) (allowed []string, unscoped bool, err error) {
	if _, ok := perms[access.PermUnscoped]; ok {
		return nil, true, nil
	}
	allowed, err = access.CachedEffectiveIDs(ctx, db, userID)
	if err != nil {
		return nil, false, err
	}
	return allowed, false, nil
}
