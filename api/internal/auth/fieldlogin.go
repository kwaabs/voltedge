package auth

// Field login (field-app credential exchange). The browser signs in for a
// cookie session (login.go); the MCP/field bearer clients present a long-lived
// iam.api_tokens token minted out-of-band by api/cmd/authadmin (apitoken.go).
// This file bridges the two for the field app: a worker types an email +
// password on the device, and the server verifies them under the EXACT same
// policy as the browser login, then mints a bearer token and hands it back once.
//
// The point is that nothing downstream changes. The device stores the returned
// token in its keystore and presents it on every /packs and /sync call just as
// if an admin had pasted an `authadmin mint-token` value -- the Phase 18 trust
// boundary (ResolveAPIToken -> the owner's RBAC) is untouched. Username/password
// is only the FRONT of the door; a token is still what walks through it.

import (
	"context"
	"net/http"
	"sort"
	"time"

	"github.com/uptrace/bun"
)

// FieldLoginError is a login failure carrying the HTTP status the transport
// should return and a client-safe message. Any other error from FieldLogin
// (e.g. a database fault) is a plain error the handler renders as 500. The
// authenticate path already returns a single generic "invalid email or
// password" for every credential-rejection branch (login.go), so this type
// never leaks which of unknown-user / wrong-password / locked applied.
type FieldLoginError struct {
	Status  int
	Message string
}

func (e *FieldLoginError) Error() string { return e.Message }

// FieldLoginResult is what a successful field login yields: the raw bearer token
// (returned exactly once -- the device keystores it, it is never persisted in
// the clear) plus the identity the app shows as "signed in as".
type FieldLoginResult struct {
	// Token is the short-lived ACCESS bearer the device presents on every call
	// (an iam.api_tokens row with fieldAccessTTL). AccessExpiresAt is when it
	// lapses, so the device knows when to refresh.
	Token           string
	AccessExpiresAt time.Time
	// RefreshToken is the long-lived credential the device keystores and swaps for a
	// fresh access token (POST /auth/field-refresh) without re-authenticating.
	RefreshToken string
	UserID       string
	Email        string
	DisplayName  string
	// Permissions is the user's effective permission set (sorted), so the field
	// app can show/hide each module by capability -- the same list the browser
	// login returns. The device gates its UI off this; the server still enforces
	// every operation independently.
	Permissions []string
}

// FieldLogin authenticates an email + password under the same policy as the
// browser login -- account lockout, the break-glass bypass, the org-wide
// local-auth switch, and the same audit events (authenticateLocal in login.go)
// -- then mints a long-lived iam.api_tokens bearer for the field app and returns
// it once. The credential is indistinguishable from one minted by
// `authadmin mint-token`, so everything behind it is the unchanged bearer model.
//
// tokenName is the human label the token is stored under (so an admin listing
// tokens can tell field-app logins apart, ideally per device). ttl is retained for
// signature compatibility but no longer used: the field session model fixes the
// access-token TTL (fieldAccessTTL) and pairs it with a refresh token, replacing the
// old single non-expiring bearer.
func FieldLogin(ctx context.Context, db *bun.DB, r *http.Request, email, password, tokenName string, ttl time.Duration) (*FieldLoginResult, error) {
	// The verify path lives on the same lightweight service the browser login
	// uses; it needs only db + the password/lockout/switch logic (no cookies),
	// so a fresh service with env config is equivalent to the server's.
	svc := newService(db, NewConfigFromEnv())
	user, err := svc.authenticateLocal(ctx, r, email, password)
	if err != nil {
		switch {
		case isUnauthorizedError(err):
			return nil, &FieldLoginError{Status: http.StatusUnauthorized, Message: err.Error()}
		case isForbiddenError(err):
			return nil, &FieldLoginError{Status: http.StatusForbidden, Message: err.Error()}
		case isValidationError(err):
			return nil, &FieldLoginError{Status: http.StatusBadRequest, Message: err.Error()}
		default:
			return nil, err
		}
	}

	// Credential verified; issue the field session (access + refresh token). The
	// field:login capability gate lives in issueFieldSession, shared with the SSO
	// path so both sign-in routes enforce it identically.
	return issueFieldSession(ctx, db, user.ID, user.Email, user.DisplayName, tokenName)
}

// permissionList is the user's effective permission set as a sorted slice, for the
// field-login response (the map order is otherwise non-deterministic).
func permissionList(su *SessionUser) []string {
	out := make([]string, 0, len(su.Permissions))
	for p := range su.Permissions {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
