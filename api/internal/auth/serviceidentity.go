package auth

// Service identity: build a *SessionUser directly from a user id, with no
// cookie and no bearer token in hand. Background writers -- the webhook
// ingestion worker (0124), and any future scheduled job that must act as a
// configured principal -- need the exact same authenticated context an HTTP
// request establishes, so that crud/package writes inherit that user's RBAC
// permissions and Phase-58 territory scope. There is deliberately no ambient
// "system" superuser: a background writer can only ever act as a real,
// scoped user, so it can never write more than that user could through the UI.
//
// This is the same load ResolveAPIToken performs once it has found the token's
// owner (apitoken.go) -- factored out here so a caller holding only a user id
// (an endpoint's service_user_id) can construct the identity without minting or
// presenting a token. Wrap the result with WithUser(ctx, u) before calling into
// crud.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/uptrace/bun"
)

// LoadSessionUser resolves an active user's full authenticated snapshot -- the
// same *SessionUser a cookie session or a bearer token yields, so downstream
// RBAC (SessionUser.Has) and territory filtering (TerritoryFilter) behave
// identically. It returns an error (not a nil user) when the id names no active
// user: unlike a presented credential, a configured service_user_id that no
// longer resolves is a misconfiguration worth surfacing loudly, not a silent
// "anonymous". The returned user carries no SessionID/CSRFToken -- a background
// writer has no cookie-session surface.
func LoadSessionUser(ctx context.Context, db bun.IDB, userID string) (*SessionUser, error) {
	var user User
	err := db.NewSelect().Model(&user).
		Where("id = ?", userID).
		Where("status = ?", "active").
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("auth: no active user with id %q", userID)
	}
	if err != nil {
		return nil, fmt.Errorf("auth: load service identity: %w", err)
	}

	perms, err := loadPermissionsFor(ctx, db, user.ID)
	if err != nil {
		return nil, fmt.Errorf("auth: load service identity permissions: %w", err)
	}
	allowed, unscoped, subordinates, err := loadScopeFields(ctx, db, user.ID, perms)
	if err != nil {
		return nil, fmt.Errorf("auth: load service identity scope: %w", err)
	}

	return &SessionUser{
		ID:                 user.ID,
		Email:              user.Email,
		DisplayName:        user.DisplayName,
		BreakGlass:         user.BreakGlass,
		Permissions:        perms,
		AllowedTerritories: allowed,
		Unscoped:           unscoped,
		Subordinates:       subordinates,
	}, nil
}
