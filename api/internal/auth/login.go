package auth

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"
)

// Lockout policy for local login: after maxFailedLogins consecutive
// failures an account is refused for lockoutDuration regardless of
// password. Deliberately conservative -- this is a small internal user
// base, not a consumer sign-in.
const (
	maxFailedLogins = 5
	lockoutDuration = 15 * time.Minute
)

// genericLoginFailure is the single message every "the credential didn't
// work" path returns -- unknown email, no local identity, wrong password,
// locked account all look identical to the client, so none of them
// becomes an oracle for the others.
const genericLoginFailure = "invalid email or password"

// authenticateLocal runs the full local-login decision and, on success,
// returns the user. Every rejection is a *unauthorizedError (generic) or
// *forbiddenError (policy), and every path spends comparable CPU so the
// failbranch can't be timed apart from the success branch.
func (s *service) authenticateLocal(ctx context.Context, r *http.Request, email, password string) (*User, error) {
	email = strings.TrimSpace(email)
	if email == "" || password == "" {
		return nil, invalid("email and password are required")
	}

	var user User
	err := s.db.NewSelect().Model(&user).
		Where("email = ?", email).
		Where("status = ?", "active").
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		// Unknown (or disabled) account: still pay the hash cost, then
		// fail generically -- no user-enumeration signal.
		_, _ = verifyPassword(password, dummyArgon2idHash)
		s.logEvent(ctx, r, eventLoginFailure, "", "unknown or disabled email: "+email)
		return nil, unauthorized(genericLoginFailure)
	}
	if err != nil {
		return nil, err
	}

	// Locked out from prior failures?
	if user.LockedUntil != nil && time.Now().Before(*user.LockedUntil) {
		_, _ = verifyPassword(password, dummyArgon2idHash)
		s.logEvent(ctx, r, eventLoginFailure, user.ID, "attempt while locked out")
		return nil, unauthorized(genericLoginFailure)
	}

	// The local identity (Argon2id password). SSO-only users have none.
	var identity Identity
	err = s.db.NewSelect().Model(&identity).
		Where("user_id = ?", user.ID).
		Where("provider = ?", "local").
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && identity.PasswordHash == nil) {
		_, _ = verifyPassword(password, dummyArgon2idHash)
		s.logEvent(ctx, r, eventLoginFailure, user.ID, "no local password on this account")
		return nil, unauthorized(genericLoginFailure)
	}
	if err != nil {
		return nil, err
	}

	// The break-glass gate. A break-glass account bypasses the switch
	// entirely; everyone else needs local_auth_enabled to be on. Checked
	// BEFORE the password so a disabled-local policy never becomes a
	// password oracle -- but we still spend the hash time to keep the
	// timing flat, then return a clear 403 (independent of whether the
	// password was right).
	allowed := user.BreakGlass
	if !allowed {
		on, switchErr := s.localAuthEnabled(ctx)
		if switchErr != nil {
			return nil, switchErr
		}
		allowed = on
	}
	if !allowed {
		_, _ = verifyPassword(password, dummyArgon2idHash)
		s.logEvent(ctx, r, eventLoginDenied, user.ID, "local auth disabled and account is not break-glass")
		return nil, forbidden("local password sign-in is currently disabled; use SSO or contact an administrator")
	}

	ok, verifyErr := verifyPassword(password, *identity.PasswordHash)
	if verifyErr != nil {
		return nil, verifyErr
	}
	if !ok {
		s.recordFailedLogin(ctx, r, &user)
		return nil, unauthorized(genericLoginFailure)
	}

	// Success: clear the failure counter and any lock.
	_, _ = s.db.NewUpdate().Model((*User)(nil)).
		Set("failed_login_count = 0").
		Set("locked_until = NULL").
		Set("updated_at = ?", time.Now()).
		Where("id = ?", user.ID).
		Exec(ctx)

	if user.BreakGlass {
		s.logEvent(ctx, r, eventBreakGlassLogin, user.ID, "")
	} else {
		s.logEvent(ctx, r, eventLoginSuccess, user.ID, "")
	}
	return &user, nil
}

// recordFailedLogin increments the failure counter and, at the threshold,
// locks the account for lockoutDuration (resetting the counter). Best
// effort -- a bookkeeping failure must not change the login outcome.
func (s *service) recordFailedLogin(ctx context.Context, r *http.Request, user *User) {
	next := user.FailedLoginCount + 1
	q := s.db.NewUpdate().Model((*User)(nil)).Where("id = ?", user.ID).Set("updated_at = ?", time.Now())
	if next >= maxFailedLogins {
		q = q.Set("failed_login_count = 0").Set("locked_until = ?", time.Now().Add(lockoutDuration))
		s.logEvent(ctx, r, eventLoginFailure, user.ID, "wrong password -- account locked")
	} else {
		q = q.Set("failed_login_count = ?", next)
		s.logEvent(ctx, r, eventLoginFailure, user.ID, "wrong password")
	}
	_, _ = q.Exec(ctx)
}

// userJSON is the wire shape for the authenticated user -- the only user
// fields a client legitimately needs, and never anything credential-ish.
type userJSON struct {
	ID          string   `json:"id"`
	Email       string   `json:"email"`
	DisplayName string   `json:"display_name"`
	BreakGlass  bool     `json:"break_glass"`
	Permissions []string `json:"permissions"`
	// IsSupervisor is true when the user has at least one report down the manager
	// tree. The SPA reads it to show supervisor-scoped views (e.g. the edit-session
	// audit, which a non-admin supervisor may see for their own reports); the
	// server still enforces the actual scope.
	IsSupervisor bool `json:"is_supervisor"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// loginHandler is POST /auth/login. On success it mints a session, sets
// the httpOnly session cookie + readable CSRF cookie, and returns the
// user.
func (s *service) loginHandler(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}

	user, err := s.authenticateLocal(r.Context(), r, req.Email, req.Password)
	if err != nil {
		writeError(w, err)
		return
	}

	rawToken, _, err := s.createSession(r.Context(), user.ID, r)
	if err != nil {
		writeError(w, err)
		return
	}
	// The CSRF token is on the freshly-created session; re-read it via a
	// load so the cookie carries exactly what the server will compare.
	loaded, err := s.loadSession(r.Context(), rawToken)
	if err != nil || loaded == nil {
		writeError(w, errors.New("auth: could not establish session"))
		return
	}
	s.setSessionCookies(w, rawToken, loaded.CSRFToken)

	writeJSON(w, http.StatusOK, userJSON{
		ID: user.ID, Email: user.Email, DisplayName: user.DisplayName, BreakGlass: user.BreakGlass,
		Permissions:  sortedPermissions(loaded.Permissions),
		IsSupervisor: len(loaded.Subordinates) > 0,
	})
}

// logoutHandler is POST /auth/logout -- revokes the current session
// server-side and clears the cookies. Requires an authenticated session
// (RequireAuth) and a valid CSRF token (RequireCSRF).
func (s *service) logoutHandler(w http.ResponseWriter, r *http.Request) {
	if user, ok := UserFrom(r.Context()); ok {
		_ = s.revokeSession(r.Context(), user.SessionID)
		s.logEvent(r.Context(), r, eventLogout, user.ID, "")
	}
	s.clearSessionCookies(w)
	w.WriteHeader(http.StatusNoContent)
}

// meHandler is GET /auth/me -- the current user, or 401 via RequireAuth.
func (s *service) meHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := UserFrom(r.Context())
	if !ok {
		writeError(w, unauthorized("authentication required"))
		return
	}
	writeJSON(w, http.StatusOK, userJSON{
		ID: user.ID, Email: user.Email, DisplayName: user.DisplayName, BreakGlass: user.BreakGlass,
		Permissions:  sortedPermissions(user.Permissions),
		IsSupervisor: len(user.Subordinates) > 0,
	})
}

type localAuthSwitchRequest struct {
	Enabled bool `json:"enabled"`
}

// setLocalAuthHandler is POST /auth/settings/local-auth -- flip the
// break-glass switch. Authorization (Slice C): the admin:auth-settings
// permission OR a break-glass account. The break-glass bypass is deliberate
// and load-bearing -- it is exactly the runbook lever: during an SSO outage
// a break-glass admin signs in locally (bypassing the switch), turns local
// auth on for everyone, then off again after, and must be able to do so even
// on a freshly bootstrapped account that holds no role yet.
func (s *service) setLocalAuthHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := UserFrom(r.Context())
	if !ok {
		writeError(w, unauthorized("authentication required"))
		return
	}
	if !user.BreakGlass && !user.Has(PermAuthSettings) {
		writeError(w, forbidden("you do not have permission to change this setting"))
		return
	}

	var req localAuthSwitchRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	if err := s.setLocalAuthEnabled(r.Context(), req.Enabled, user.ID); err != nil {
		writeError(w, err)
		return
	}
	detail := "local_auth_enabled set to false"
	if req.Enabled {
		detail = "local_auth_enabled set to true"
	}
	s.logEvent(r.Context(), r, eventLocalAuthToggled, user.ID, detail)
	writeJSON(w, http.StatusOK, map[string]bool{"local_auth_enabled": req.Enabled})
}
