package fieldpack

// The field-app credential surface beyond password login (login.go):
//   POST /auth/field-sso/exchange  redeem a one-time SSO handoff code for a session
//   POST /auth/field-refresh       swap a refresh token for a fresh access token
//   POST /auth/field-logout        revoke a refresh token (device sign-out)
//   GET  /auth/sso-config          tell the device where to start the SSO flow
//
// SSO itself runs on cmd/server (/auth/oidc/...), where the IdP's registered redirect
// lands; it drops a single-use code in the shared DB and bounces the device back via
// the voltedge:// scheme. These routes (on cmd/fieldsync) redeem that code and manage
// the resulting session. All are public: the code / refresh token IS the credential,
// so they sit beside /auth/field-login outside the bearer gate.

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/fieldtrack"
	"github.com/uptrace/bun"
)

// writeFieldSession renders a field session (login / SSO exchange / refresh all share
// it) as the JSON the device parses: the access + refresh tokens, when the access
// token lapses, the identity + permissions, and the org-wide tracking policy (so the
// device learns it at sign-in, same as before).
func writeFieldSession(w http.ResponseWriter, r *http.Request, db *bun.DB, res *auth.FieldLoginResult) {
	trackingEnabled, _ := fieldtrack.TrackingEnabled(r.Context(), db)
	disclosureEnabled, _ := fieldtrack.DisclosureEnabled(r.Context(), db)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"token":             res.Token,
		"refresh_token":     res.RefreshToken,
		"access_expires_at": res.AccessExpiresAt.UTC().Format(time.RFC3339),
		"user_id":           res.UserID,
		"email":             res.Email,
		"display_name":      res.DisplayName,
		// The user's effective permissions, so the device can show/hide modules.
		"permissions":                       res.Permissions,
		"field_tracking_enabled":            trackingEnabled,
		"field_tracking_disclosure_enabled": disclosureEnabled,
	})
}

// writeFieldAuthErr maps an auth.FieldLoginError to its HTTP status + client-safe
// message; anything else is a generic 500.
func writeFieldAuthErr(w http.ResponseWriter, err error) {
	var le *auth.FieldLoginError
	if errors.As(err, &le) {
		writeErr(w, le.Status, le.Message)
		return
	}
	writeErr(w, http.StatusInternalServerError, "auth error")
}

// fieldSSOExchangeHandler redeems the one-time SSO handoff code (minted by cmd/server's
// OIDC callback into the shared DB) for a field session.
func fieldSSOExchangeHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Code       string `json:"code"`
			DeviceName string `json:"device_name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid request body")
			return
		}
		name := "field-app"
		if d := strings.TrimSpace(req.DeviceName); d != "" {
			name = "field-app · " + d
		}
		res, err := auth.FieldSSOExchange(r.Context(), db, req.Code, name)
		if err != nil {
			writeFieldAuthErr(w, err)
			return
		}
		writeFieldSession(w, r, db, res)
	}
}

// fieldRefreshHandler swaps a refresh token for a fresh access token (same session
// shape back, same refresh token -- non-rotating).
func fieldRefreshHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			RefreshToken string `json:"refresh_token"`
			DeviceName   string `json:"device_name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid request body")
			return
		}
		res, err := auth.FieldRefresh(r.Context(), db, req.RefreshToken, req.DeviceName)
		if err != nil {
			writeFieldAuthErr(w, err)
			return
		}
		writeFieldSession(w, r, db, res)
	}
}

// fieldLogoutHandler revokes a refresh token on device sign-out. Best-effort + always
// 204: a bad/missing token is a no-op (the device is signing out regardless, and the
// short-lived access token lapses on its own).
func fieldLogoutHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			RefreshToken string `json:"refresh_token"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		_ = auth.RevokeFieldRefreshToken(r.Context(), db, req.RefreshToken)
		w.WriteHeader(http.StatusNoContent)
	}
}

// ssoConfigHandler tells the device where to START the SSO flow. SSO lives on
// cmd/server (/auth/oidc/... + /auth/providers), which is a DIFFERENT origin than this
// fieldsync service in a split dev setup (separate ports), or the same gateway origin
// in prod. login_base resolves, in order:
//  1. FIELD_SSO_WEB_ORIGIN env (explicit override);
//  2. the origin of the configured OIDC redirect URL (OIDC_AZURE_REDIRECT_URL /
//     OIDC_GOOGLE_REDIRECT_URL) -- the web origin where OIDC already lives, so a shared
//     .env makes this zero-config;
//  3. this request's own origin (correct only when one gateway fronts both services).
// The app then opens login_base/auth/oidc/{provider}/login?field=1 and lists providers
// from login_base/auth/providers.
func ssoConfigHandler() http.HandlerFunc {
	base := resolveSSOWebOrigin() // resolved once at startup; env is static
	return func(w http.ResponseWriter, r *http.Request) {
		loginBase := base
		if loginBase == "" {
			loginBase = requestOrigin(r)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"login_base": loginBase})
	}
}

// resolveSSOWebOrigin finds cmd/server's origin from env: the explicit
// FIELD_SSO_WEB_ORIGIN override, else the scheme://host of a configured OIDC redirect
// URL. Empty when neither is set (caller falls back to the request origin).
func resolveSSOWebOrigin() string {
	if v := strings.TrimRight(os.Getenv("FIELD_SSO_WEB_ORIGIN"), "/"); v != "" {
		return v
	}
	for _, key := range []string{"OIDC_AZURE_REDIRECT_URL", "OIDC_GOOGLE_REDIRECT_URL"} {
		if raw := strings.TrimSpace(os.Getenv(key)); raw != "" {
			if u, err := url.Parse(raw); err == nil && u.Scheme != "" && u.Host != "" {
				return u.Scheme + "://" + u.Host
			}
		}
	}
	return ""
}

// requestOrigin reconstructs the scheme://host the client reached us on, honouring a
// reverse proxy's X-Forwarded-* headers (the API usually sits behind one).
func requestOrigin(r *http.Request) string {
	scheme := "https"
	if xf := r.Header.Get("X-Forwarded-Proto"); xf != "" {
		scheme = strings.TrimSpace(strings.Split(xf, ",")[0])
	} else if r.TLS == nil {
		scheme = "http"
	}
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	return scheme + "://" + host
}
