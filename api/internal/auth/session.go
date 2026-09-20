package auth

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/uptrace/bun"
)

// createSession mints a fresh opaque session for userID and returns the
// raw cookie token (shown to the client exactly once, never stored) and
// the persisted row. The session gets both a sliding idle window and a
// hard absolute cap from Config.
func (s *service) createSession(ctx context.Context, userID string, r *http.Request) (rawToken string, sess *Session, err error) {
	rawToken, err = newRandomToken(sessionTokenBytes)
	if err != nil {
		return "", nil, err
	}
	csrf, err := newRandomToken(csrfTokenBytes)
	if err != nil {
		return "", nil, err
	}

	now := time.Now()
	sess = &Session{
		UserID:            userID,
		TokenSHA256:       hashToken(rawToken),
		CSRFToken:         csrf,
		CreatedAt:         now,
		LastUsedAt:        now,
		IdleExpiresAt:     now.Add(s.cfg.IdleTimeout),
		AbsoluteExpiresAt: now.Add(s.cfg.AbsoluteMax),
		IP:                clientIP(r),
		UserAgent:         clientUA(r),
	}
	if _, err := s.db.NewInsert().Model(sess).Returning("id").Exec(ctx); err != nil {
		return "", nil, err
	}
	return rawToken, sess, nil
}

// loadSession validates a presented raw cookie token and, if it is live,
// returns the authenticated user snapshot. "Live" means: the session
// exists, is not revoked, is within both its idle and absolute windows,
// and its user is still active. A valid use slides the idle window
// forward (never past the absolute cap). Any invalidity returns
// (nil, nil) -- an anonymous request, not an error -- so the middleware
// can proceed unauthenticated rather than 500.
func (s *service) loadSession(ctx context.Context, rawToken string) (*SessionUser, error) {
	if rawToken == "" {
		return nil, nil
	}

	var sess Session
	err := s.db.NewSelect().Model(&sess).
		Where("token_sha256 = ?", hashToken(rawToken)).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	now := time.Now()
	if sess.RevokedAt != nil || now.After(sess.IdleExpiresAt) || now.After(sess.AbsoluteExpiresAt) {
		return nil, nil
	}

	var user User
	err = s.db.NewSelect().Model(&user).
		Where("id = ?", sess.UserID).
		Where("status = ?", "active").
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		// user was disabled or deleted out from under a live session
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	// The effective permission set (Slice C). A read failure here is a real
	// server fault for an otherwise-valid session, so it propagates and the
	// request fails closed rather than silently authorizing nothing.
	perms, permErr := s.loadPermissions(ctx, sess.UserID)
	if permErr != nil {
		return nil, permErr
	}

	// The spatial-access scope (Phase 58): whether this user bypasses
	// territory scoping, and if not, the effective territory-id set the
	// request is confined to. A read failure fails the request closed, same
	// as the permission load above.
	allowed, unscoped, subordinates, scopeErr := loadScopeFields(ctx, s.db, sess.UserID, perms)
	if scopeErr != nil {
		return nil, scopeErr
	}

	// Slide the idle window forward, capped at the absolute expiry. Best
	// effort -- a failed refresh must not fail the request.
	newIdle := now.Add(s.cfg.IdleTimeout)
	if newIdle.After(sess.AbsoluteExpiresAt) {
		newIdle = sess.AbsoluteExpiresAt
	}
	_, _ = s.db.NewUpdate().Model((*Session)(nil)).
		Set("last_used_at = ?", now).
		Set("idle_expires_at = ?", newIdle).
		Where("id = ?", sess.ID).
		Exec(ctx)

	return &SessionUser{
		ID:                 user.ID,
		Email:              user.Email,
		DisplayName:        user.DisplayName,
		BreakGlass:         user.BreakGlass,
		Permissions:        perms,
		AllowedTerritories: allowed,
		Unscoped:           unscoped,
		Subordinates:       subordinates,
		SessionID:          sess.ID,
		CSRFToken:          sess.CSRFToken,
	}, nil
}

// loadPermissions returns a user's effective permission set: the union of
// every permission granted by every role the user holds. An empty set (the
// user holds no roles) is a valid result, not an error.
//
// The query lives in the standalone loadPermissionsFor so the API-token
// path (apitoken.go) resolves the same session -> user -> roles ->
// permissions chain a cookie session does -- one source of truth for "what
// can this user do," regardless of how they authenticated.
func (s *service) loadPermissions(ctx context.Context, userID string) (map[string]struct{}, error) {
	return loadPermissionsFor(ctx, s.db, userID)
}

// loadPermissionsFor is the db-only core of loadPermissions -- usable by any
// caller holding a bun.IDB (the session middleware via the service method,
// and the API-token resolver directly).
func loadPermissionsFor(ctx context.Context, db bun.IDB, userID string) (map[string]struct{}, error) {
	var keys []string
	err := db.NewSelect().
		ColumnExpr("DISTINCT rp.permission_key").
		TableExpr("iam.user_roles AS ur").
		Join("JOIN iam.role_permissions AS rp ON rp.role_id = ur.role_id").
		Where("ur.user_id = ?", userID).
		Scan(ctx, &keys)
	if err != nil {
		return nil, err
	}
	set := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		set[k] = struct{}{}
	}
	return set, nil
}

// revokeSession marks one session revoked (idempotent). Used by logout.
func (s *service) revokeSession(ctx context.Context, sessionID string) error {
	_, err := s.db.NewUpdate().Model((*Session)(nil)).
		Set("revoked_at = ?", time.Now()).
		Where("id = ?", sessionID).
		Where("revoked_at IS NULL").
		Exec(ctx)
	return err
}
