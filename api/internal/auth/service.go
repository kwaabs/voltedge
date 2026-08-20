package auth

import (
	"net/http"
	"strings"

	"github.com/uptrace/bun"
)

// service binds the database and config the auth handlers and middleware
// share. Routes and the middleware constructors each build one; it holds
// no per-request state, so a single instance is safe across goroutines.
//
// oidc is the registry of configured SSO providers (Slice B), keyed by
// provider ("azuread" / "google"). It is populated only by Routes (which
// does the one-time provider discovery at startup); the middleware
// constructors leave it nil, since session loading never needs it.
type service struct {
	db   bun.IDB
	cfg  Config
	oidc map[string]*oidcProvider
}

func newService(db bun.IDB, cfg Config) *service {
	return &service{db: db, cfg: cfg}
}

// clientIP and clientUA extract best-effort audit fields from a request.
// clientIP prefers the first X-Forwarded-For hop (the API runs behind a
// reverse proxy, per docs) and falls back to RemoteAddr. Neither is
// trusted for security decisions -- only recorded for the audit log.
func clientIP(r *http.Request) *string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		first := strings.TrimSpace(strings.SplitN(xff, ",", 2)[0])
		if first != "" {
			return &first
		}
	}
	if r.RemoteAddr != "" {
		v := r.RemoteAddr
		return &v
	}
	return nil
}

func clientUA(r *http.Request) *string {
	if ua := r.Header.Get("User-Agent"); ua != "" {
		return &ua
	}
	return nil
}
