package auth

import (
	"crypto/subtle"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// SecurityHeaders sets a conservative baseline of response security
// headers on every request. Implemented as small, reviewable middleware
// rather than a framework -- the whole point of the "minimal audited
// surface" posture. HSTS is only meaningful over TLS, so it's emitted
// only when the request arrived over https (directly or via the proxy's
// X-Forwarded-Proto).
func SecurityHeaders() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "no-referrer")
			h.Set("Cross-Origin-Opener-Policy", "same-origin")
			if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
				h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
			}
			next.ServeHTTP(w, r)
		})
	}
}

// EditSessionLoader attaches the client's edit-mode session id to the request
// context when the X-Edit-Session header carries a well-formed uuid. The web
// client mints one uuid per edit-mode span (Edit toggle on -> off) and sends it
// on every mutating request made while editing; the CRUD history writer stamps
// it onto each feature_history row so the edit-sessions report can group a batch
// of changes back together. Non-failing like SessionLoader: a missing or
// MALFORMED header is simply dropped (no session stamped) -- the value is cast to
// a uuid column downstream, so validating here keeps a bad header from erroring
// an otherwise-good write.
func EditSessionLoader() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if raw := r.Header.Get("X-Edit-Session"); raw != "" {
				if id, err := uuid.Parse(raw); err == nil {
					r = r.WithContext(WithEditSession(r.Context(), id.String()))
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// SessionLoader validates the session cookie (if any) and attaches the
// authenticated user to the request context. It is deliberately
// non-failing: an absent, expired, or invalid session simply yields an
// anonymous request that flows on. This is what lets Slice A add the
// trust-boundary plumbing app-wide without gating (and so without
// breaking) any existing route -- RBAC's RequireAuth/RequirePermission
// (Slice C) is what will actually reject.
func SessionLoader(db *bun.DB, cfg Config) func(http.Handler) http.Handler {
	svc := newService(db, cfg)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(cfg.CookieName)
			if err != nil || cookie.Value == "" {
				next.ServeHTTP(w, r)
				return
			}
			user, loadErr := svc.loadSession(r.Context(), cookie.Value)
			if loadErr != nil {
				// A real DB error -- fail the request rather than silently
				// downgrading a user who does have a valid session.
				writeError(w, loadErr)
				return
			}
			if user != nil {
				r = r.WithContext(withUser(r.Context(), user))
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireAuth rejects any request without an authenticated user (401).
// Slice A applies it only to /auth's own session-scoped endpoints
// (logout, me); Slice C reuses it (plus RequirePermission) to gate the
// rest of the API.
func RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := UserFrom(r.Context()); !ok {
			writeError(w, unauthorized("authentication required"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireCSRF guards state-changing requests with a defense-in-depth
// stack: the session cookie is already SameSite=Strict, and on top of
// that this checks (1) the request Origin/Sec-Fetch-Site is same-origin
// and (2) a double-submit CSRF token -- the X-CSRF-Token header must
// equal the token bound to this session, compared in constant time. Safe
// methods (GET/HEAD/OPTIONS) pass through. Anonymous requests pass
// through here too (there's no session to forge against); it's
// RequireAuth that stops those.
func RequireCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}

		user, ok := UserFrom(r.Context())
		if !ok {
			next.ServeHTTP(w, r)
			return
		}

		// Sec-Fetch-Site is sent by all modern browsers; "same-origin"
		// and "none" (direct navigation) are the only acceptable values
		// for a state-changing API call. If the header is absent (older
		// client, non-browser), fall through to the token check alone.
		if sfs := r.Header.Get("Sec-Fetch-Site"); sfs != "" && sfs != "same-origin" && sfs != "none" {
			writeError(w, forbidden("cross-site request refused"))
			return
		}

		header := r.Header.Get("X-CSRF-Token")
		if header == "" || subtle.ConstantTimeCompare([]byte(header), []byte(user.CSRFToken)) != 1 {
			writeError(w, forbidden("missing or invalid CSRF token"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// setSessionCookies writes the httpOnly session cookie and the readable
// CSRF companion cookie. The session cookie is SameSite=Strict, httpOnly
// (JS can never read it), host-scoped, path "/". The CSRF cookie is
// intentionally NOT httpOnly -- the SPA reads it to echo in the header --
// but knowing it is useless without also holding the session cookie.
func (s *service) setSessionCookies(w http.ResponseWriter, rawToken, csrfToken string) {
	http.SetCookie(w, &http.Cookie{
		Name:     s.cfg.CookieName,
		Value:    rawToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cfg.Secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(s.cfg.AbsoluteMax / time.Second),
	})
	http.SetCookie(w, &http.Cookie{
		Name:     s.cfg.CSRFCookieName,
		Value:    csrfToken,
		Path:     "/",
		HttpOnly: false,
		Secure:   s.cfg.Secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(s.cfg.AbsoluteMax / time.Second),
	})
}

// clearSessionCookies expires both cookies -- used by logout.
func (s *service) clearSessionCookies(w http.ResponseWriter) {
	for _, name := range []string{s.cfg.CookieName, s.cfg.CSRFCookieName} {
		http.SetCookie(w, &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     "/",
			HttpOnly: name == s.cfg.CookieName,
			Secure:   s.cfg.Secure,
			SameSite: http.SameSiteStrictMode,
			MaxAge:   -1,
		})
	}
}
