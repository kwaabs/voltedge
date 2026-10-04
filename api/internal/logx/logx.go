// Package logx is VoltEdge's structured-logging seam. Every binary logs through
// a single *slog.Logger built here, so format, level, and destination are
// decided in one place rather than re-derived per package.
//
// Design choices (see the logging discussion):
//   - log/slog, stdlib -- no third-party logging dependency.
//   - Output is a JSON (prod) or text (dev) stream on STDOUT. The app never
//     decides where logs *reside*; it streams, and the platform (Docker/k8s/
//     systemd) routes and persists -- the twelve-factor rule.
//   - Field names follow OpenTelemetry semantic conventions where one exists
//     (http.request.method, url.path, http.response.status_code, user.id, ...).
//     This is OUR schema, emitted as plain JSON today, but named so that a later
//     switch to an OTLP exporter (via the otelslog bridge, set up in Init) needs
//     no change at any call site. slog is the recommended Go logging frontend
//     precisely because it bridges to OpenTelemetry this way.
//
// Call sites read a request/job-scoped logger off the context with From(ctx);
// it is always safe (falls back to the default logger when ctx carries none).
package logx

import (
	"context"
	"log/slog"
	"os"
	"strings"
)

type ctxKey int

const (
	loggerKey ctxKey = iota
	attrsKey
)

// levelVar holds the active minimum level for the process-wide handler. It is a
// slog.LevelVar (not a fixed slog.Level) so the threshold can be changed at
// RUNTIME -- an admin raising detail to chase an incident, then lowering it --
// without rebuilding the handler or restarting the process. Init points the
// shared handler at it; SetLevel mutates it and every subsequent log call, on
// every logger built off that handler, observes the new level. LevelVar is
// atomic, so concurrent SetLevel/From calls are safe.
var levelVar = new(slog.LevelVar)

// Init builds the root logger from the environment and installs it as
// slog.Default, so even a stray slog.Info in code that hasn't adopted logx yet
// lands in the same stream and format. It returns the logger for the caller to
// thread into middleware and workers.
//
//   - LOG_FORMAT = "text" for the human-readable dev handler; anything else
//     (default) selects JSON, the machine-parseable prod format a shipper reads.
//   - LOG_LEVEL  = debug | info | warn | error (default info).
//
// A later OTLP/OpenTelemetry rollout swaps the handler constructed here for the
// otelslog bridge; no call site changes.
func Init() *slog.Logger {
	levelVar.Set(parseLevel(os.Getenv("LOG_LEVEL")))
	opts := &slog.HandlerOptions{Level: levelVar}

	var h slog.Handler
	switch strings.ToLower(strings.TrimSpace(os.Getenv("LOG_FORMAT"))) {
	case "text":
		h = slog.NewTextHandler(os.Stdout, opts)
	default:
		h = slog.NewJSONHandler(os.Stdout, opts)
	}

	l := slog.New(h)
	slog.SetDefault(l)
	return l
}

// SetLevel changes the active minimum level at runtime for every logger built
// off the shared handler (the request logger, the workers, slog.Default). It is
// how the admin log-level control (internal/logsettings) takes effect live, with
// no restart. Safe to call concurrently.
func SetLevel(l slog.Level) { levelVar.Set(l) }

// CurrentLevel returns the active minimum level -- the source of truth the
// settings API reports back, since the env default and any admin change both
// land in the same var.
func CurrentLevel() slog.Level { return levelVar.Level() }

// ParseLevel maps a level name (debug|info|warn|error, case-insensitive) to its
// slog.Level and reports whether it was recognised. Unlike the lenient parse the
// env default uses, this REJECTS an unknown name (ok=false) so an admin PUT can
// 400 rather than silently snapping to info.
func ParseLevel(s string) (slog.Level, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug, true
	case "info":
		return slog.LevelInfo, true
	case "warn", "warning":
		return slog.LevelWarn, true
	case "error":
		return slog.LevelError, true
	}
	return slog.LevelInfo, false
}

// LevelString is ParseLevel's inverse: the canonical lowercase name for a level,
// for persisting it and for reporting it over the settings API.
func LevelString(l slog.Level) string {
	switch {
	case l <= slog.LevelDebug:
		return "debug"
	case l < slog.LevelWarn:
		return "info"
	case l < slog.LevelError:
		return "warn"
	default:
		return "error"
	}
}

// parseLevel is the lenient parse used for the LOG_LEVEL env default: an unknown
// or blank value falls back to info rather than failing a boot.
func parseLevel(s string) slog.Level {
	if l, ok := ParseLevel(s); ok {
		return l
	}
	return slog.LevelInfo
}

// Into returns a child context carrying l as the request/job-scoped logger.
func Into(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey, l)
}

// From returns the logger previously put on ctx by Into, or slog.Default() when
// ctx carries none -- so a handler or engine can always call
// logx.From(ctx).Info(...) without a nil check and inherit whatever
// request-scoped fields (request_id, user.id, ...) the caller attached.
func From(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerKey).(*slog.Logger); ok && l != nil {
		return l
	}
	return slog.Default()
}

// Attrs is a mutable, request-scoped bag of structured fields. It is carried by
// POINTER on the context, so a downstream middleware or handler can enrich it
// (via AddAttr) and an UPSTREAM middleware -- the request logger that opened the
// bag -- sees those additions when it writes the final summary line. A single
// request is served by one goroutine walking the middleware chain in order, so
// the appends are sequential and race-free within a request.
type Attrs struct {
	list []slog.Attr
}

// WithAttrs attaches a fresh, empty attribute bag to ctx and returns both the
// child context and the bag. The request-logger middleware calls this once at
// the start of a request and reads the bag back via Collect at the end.
func WithAttrs(ctx context.Context) (context.Context, *Attrs) {
	b := &Attrs{}
	return context.WithValue(ctx, attrsKey, b), b
}

// AddAttr appends structured fields to the request-scoped bag on ctx, if one is
// present (a no-op otherwise, e.g. off an HTTP request). Use it to enrich the
// request-summary line with values only known mid-request -- most notably the
// authenticated user, added just after the session middleware resolves it.
func AddAttr(ctx context.Context, attrs ...slog.Attr) {
	if b, ok := ctx.Value(attrsKey).(*Attrs); ok && b != nil {
		b.list = append(b.list, attrs...)
	}
}

// Collect returns the attributes accumulated in the bag. The slice is the bag's
// own backing array; the caller must not retain it past the request.
func (a *Attrs) Collect() []slog.Attr {
	if a == nil {
		return nil
	}
	return a.list
}
