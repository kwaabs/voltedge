package auth

import (
	"os"
	"strconv"
	"time"
)

// Config is the auth layer's runtime configuration. Everything here is a
// deployment concern (cookie names, session lifetimes, whether we're
// behind TLS) rather than a per-request one. The break-glass switch is
// deliberately NOT here -- it lives in iam.system_settings so a
// break-glass admin can flip it at runtime with no redeploy; env only
// seeds its initial value on an empty database (migration 0024).
type Config struct {
	// CookieName is the session cookie. CSRFCookieName is the readable
	// companion cookie carrying the double-submit CSRF token.
	CookieName     string
	CSRFCookieName string

	// IdleTimeout is the sliding inactivity window; AbsoluteMax is the
	// hard ceiling a refresh can never push past.
	IdleTimeout time.Duration
	AbsoluteMax time.Duration

	// Secure marks cookies Secure (HTTPS-only). Off for local http dev,
	// on everywhere real. Defaults to on unless AUTH_INSECURE_COOKIES=1.
	Secure bool
}

// NewConfigFromEnv builds a Config from the environment, with safe
// defaults so a bare `docker compose up` still yields a working,
// hardened setup. Recognized:
//
//	SESSION_COOKIE_NAME     (default "ve_session")
//	CSRF_COOKIE_NAME        (default "ve_csrf")
//	SESSION_IDLE_TIMEOUT    Go duration (default "12h")
//	SESSION_ABSOLUTE_MAX    Go duration (default "720h" = 30d)
//	AUTH_INSECURE_COOKIES   "1"/"true" to drop the Secure flag (local dev)
func NewConfigFromEnv() Config {
	return Config{
		CookieName:     envOr("SESSION_COOKIE_NAME", "ve_session"),
		CSRFCookieName: envOr("CSRF_COOKIE_NAME", "ve_csrf"),
		IdleTimeout:    envDuration("SESSION_IDLE_TIMEOUT", 12*time.Hour),
		AbsoluteMax:    envDuration("SESSION_ABSOLUTE_MAX", 30*24*time.Hour),
		Secure:         !envBool("AUTH_INSECURE_COOKIES", false),
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

func envBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}
