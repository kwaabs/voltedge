package httpx

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/kwaabs/voltedge/api/internal/logx"
)

// RequestIDHeader is the response header carrying each request's correlation id,
// also emitted as the `request_id` field on that request's log line. A client or
// proxy can echo it to tie a user-visible error back to the server-side trail.
const RequestIDHeader = "X-Request-Id"

// Logger is the structured request-logging middleware -- the replacement for
// chi's plain-text middleware.Logger. For each request it:
//
//  1. mints a correlation id, returns it as X-Request-Id, and derives a
//     request-scoped *slog.Logger (root.With("request_id", id)) that it puts on
//     the context (logx.Into) so every handler/engine log line inherits the id;
//  2. opens a logx attribute bag (logx.WithAttrs) that downstream middleware can
//     enrich -- the session stack adds `user.id` once it resolves the caller;
//  3. logs ONE summary line when the request finishes, with OpenTelemetry-named
//     fields (http.request.method, url.path, http.response.status_code, ...),
//     at a level keyed to the status (5xx error, 4xx warn, else info).
//
// Logging at the END is deliberate: it's the only point at which the user the
// session middleware attached downstream is visible to this outermost
// middleware (via the shared, pointer-carried attribute bag).
//
// It wraps the ResponseWriter with chi's middleware.NewWrapResponseWriter, which
// preserves http.Flusher/Hijacker/ReaderFrom -- so streaming endpoints (the
// assistant SSE chat) keep working, unlike a naive hand-rolled wrapper.
//
// Only url.path is logged, never the raw query string, which can carry tokens or
// other sensitive values; request bodies and auth headers are never logged.
func Logger(root *slog.Logger) func(http.Handler) http.Handler {
	if root == nil {
		root = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			reqID := newRequestID()
			w.Header().Set(RequestIDHeader, reqID)

			reqLogger := root.With(slog.String("request_id", reqID))
			ctx, bag := logx.WithAttrs(r.Context())
			ctx = logx.Into(ctx, reqLogger)
			r = r.WithContext(ctx)

			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			defer func() {
				status := ww.Status()
				if status == 0 {
					status = http.StatusOK // handler returned without WriteHeader
				}
				attrs := []slog.Attr{
					slog.String("http.request.method", r.Method),
					slog.String("url.path", r.URL.Path),
					slog.Int("http.response.status_code", status),
					slog.Int("http.response.body.size", ww.BytesWritten()),
					slog.Int64("duration_ms", time.Since(start).Milliseconds()),
					slog.String("client.address", clientIP(r)),
				}
				attrs = append(attrs, bag.Collect()...)
				reqLogger.LogAttrs(r.Context(), levelFor(status), "http request", attrs...)
			}()

			next.ServeHTTP(ww, r)
		})
	}
}

// levelFor keys the summary line's severity to the response status: a server
// error is an Error, a client error a Warn, everything else Info -- so a log
// backend can alert on error-level lines without parsing the status field.
func levelFor(status int) slog.Level {
	switch {
	case status >= 500:
		return slog.LevelError
	case status >= 400:
		return slog.LevelWarn
	default:
		return slog.LevelInfo
	}
}

// clientIP is the peer address with the port stripped (OTel's client.address).
// It uses the transport peer (r.RemoteAddr), not a spoofable X-Forwarded-For
// header; mount a trusted-proxy middleware (chi middleware.RealIP) ahead of this
// if the service runs behind a proxy whose forwarded header you trust.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// newRequestID returns a short random hex id. crypto/rand failing is
// effectively impossible; on the off chance it does, fall back to a timestamp so
// a request is never left without a correlation id.
func newRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "t" + hex.EncodeToString([]byte(time.Now().Format("150405.000000")))
	}
	return hex.EncodeToString(b[:])
}
