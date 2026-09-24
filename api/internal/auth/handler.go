package auth

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/access"
	"github.com/kwaabs/voltedge/api/internal/httpx"
	"github.com/kwaabs/voltedge/api/internal/ratelimit"
)

// Routes returns the auth router, meant to be mounted at /auth:
//
//	GET  /providers                       which SSO buttons + local-form policy the login screen shows
//	POST /login                           email + password -> session cookie + CSRF cookie
//	GET  /oidc/{provider}/login           start the SSO auth-code + PKCE flow (redirect to IdP)
//	GET  /oidc/{provider}/callback        IdP redirect back: verify -> match-or-reject -> same session
//	POST /logout                          revoke the current session (auth + CSRF required)
//	GET  /me                              the current user (auth required)
//	POST /settings/local-auth             flip the break-glass switch (admin:auth-settings, or a break-glass account)
//	/admin/...                            user-management API, all behind admin:users (see useradmin.go)
//
// SessionLoader (applied globally in cmd/server) is what populates the
// request context these read; it is NOT applied here so the same loaded
// context is shared with the rest of the API. login and the SSO endpoints
// are intentionally unauthenticated (there is no session yet); logout and
// settings are state-changing and so carry both RequireAuth and
// RequireCSRF; me is a safe read behind RequireAuth. The SSO callback is a
// top-level GET redirect from the IdP, so it is CSRF-protected by its own
// state/nonce, not RequireCSRF.
func Routes(db *bun.DB, cfg Config) chi.Router {
	s := newService(db, cfg)
	// One-time SSO provider discovery at startup. A provider that is not
	// configured (or unreachable) is simply absent from the registry, so
	// SSO degrades to whatever providers are available -- or local-only.
	s.oidc = buildOIDCRegistry(context.Background(), cfg)

	r := chi.NewRouter()

	// Per-IP throttle on the credential endpoint: the account lockout stops
	// guessing at one account, this caps a spray across many from one source and
	// blunts a lockout-flood. Sized for a real person retyping a password (10
	// bursts, then 10/min), not a scripted attempt rate.
	loginLimiter := ratelimit.New(10, 10)

	r.Get("/providers", s.providersHandler)
	// A login body is an email + password -- a few hundred bytes. Cap it hard so
	// the unauthenticated endpoint can't be used to make the server buffer a large
	// body (the rate limit bounds attempt count; this bounds each attempt's size).
	r.With(loginLimiter.Middleware, httpx.MaxBytes(64<<10)).Post("/login", s.loginHandler)
	r.Get("/oidc/{provider}/login", s.oidcLoginHandler)
	r.Get("/oidc/{provider}/callback", s.oidcCallbackHandler)
	r.With(RequireAuth, RequireCSRF).Post("/logout", s.logoutHandler)
	r.With(RequireAuth).Get("/me", s.meHandler)
	r.With(RequireAuth, RequireCSRF).Post("/settings/local-auth", s.setLocalAuthHandler)

	// The Slice D user-management API. Its own subrouter gates the whole
	// tree behind admin:users (and CSRF on mutations); mounting it here
	// keeps every session-aware endpoint under the one /auth prefix.
	r.Mount("/admin", s.adminRoutes())

	return r
}

// decodeJSON reads a JSON body into dst, returning a *validationError (so
// writeError maps it to 400) on malformed input.
func decodeJSON(r *http.Request, dst any) error {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		return invalid("request body is not valid JSON: %v", err)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// writeError maps an error's Go type to an HTTP status -- the same
// classify-by-type pattern as api/internal/powerflow, extended with the
// 401/403 this package introduces. An unclassified error is a 500 with a
// fixed message: the real error is never leaked to the client (it belongs
// in server logs), only its shape.
func writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	message := "internal server error"

	switch {
	case isValidationError(err), access.IsInvalid(err):
		status = http.StatusBadRequest
		message = err.Error()
	case isUnauthorizedError(err):
		status = http.StatusUnauthorized
		message = err.Error()
	case isForbiddenError(err):
		status = http.StatusForbidden
		message = err.Error()
	}

	writeJSON(w, status, map[string]string{"error": message})
}
