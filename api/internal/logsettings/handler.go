// Package logsettings is the runtime log-level control: it lets an administrator
// raise or lower the API server's structured-logging verbosity LIVE -- to chase
// an incident at debug, then drop back to info -- with no restart and no change
// to the service environment. It is the in-app counterpart to the LOG_LEVEL env
// var the twelve-factor deployment sets at boot (internal/logx).
//
//	GET /  read the active level and the selectable options
//	PUT /  set the level (admin); persists it AND applies it immediately
//
// The chosen level is stored in iam.system_settings (key log_level), the same
// global key/value store the auth break-glass and map-view toggles use, so it
// survives a restart: cmd/server/main.go reads it at boot (LoadPersistedLevel)
// and re-applies it over the env default. There is intentionally no "off" -- the
// lowest-noise level is error; an audit-grade log you can silently disable would
// not be much of an audit trail.
//
// The mount gates both GET and PUT on admin:users -- this is a platform-operations
// control (the same audience as docs/logging-and-observability-guide.md), not a
// per-user preference. NOTE it governs the API SERVER process only; the out-of-
// process workers honour their own LOG_LEVEL env (they do not share this var).
package logsettings

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/logx"
)

// settingLogLevel is the iam.system_settings key the active level is stored
// under. No migration seeds it -- the PUT's upsert creates the row on first save
// and a missing row simply means "use the env/default level".
const settingLogLevel = "log_level"

// levels is the selectable set, in increasing severity -- returned to the UI so
// the control and the server validate against one list.
var levels = []string{"debug", "info", "warn", "error"}

// logLevel is the wire shape for GET (and PUT's response): the active level plus
// the options a client may choose from.
type logLevel struct {
	Level   string   `json:"level"`
	Options []string `json:"options"`
}

// Routes returns the /log-settings router.
func Routes(db *bun.DB) chi.Router {
	r := chi.NewRouter()
	r.Get("/", getHandler())
	r.Put("/", putHandler(db))
	return r
}

// getHandler reports the LIVE level (logx.CurrentLevel), which is the source of
// truth: boot synced it from the stored row, and a PUT persists + applies in one
// step, so the running var never lags the stored value.
func getHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, logLevel{
			Level:   logx.LevelString(logx.CurrentLevel()),
			Options: levels,
		})
	}
}

type setLevelRequest struct {
	Level string `json:"level"`
}

// putHandler validates the requested level, persists it, then applies it live.
// Persist-before-apply means a failed write never leaves the running server at a
// level that wouldn't survive a restart.
func putHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req setLevelRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "request body is not valid JSON")
			return
		}
		lvl, ok := logx.ParseLevel(req.Level)
		if !ok {
			writeError(w, http.StatusBadRequest, "level must be one of: debug, info, warn, error")
			return
		}
		canonical := logx.LevelString(lvl)
		if err := store(r.Context(), db, canonical, auth.ActorID(r.Context())); err != nil {
			writeError(w, http.StatusInternalServerError, "could not save the log level")
			return
		}
		logx.SetLevel(lvl)
		writeJSON(w, http.StatusOK, logLevel{Level: canonical, Options: levels})
	}
}

// store upserts the single log_level row, stamping who changed it. ON CONFLICT
// keeps it correct whether or not the row already exists (no migration seeds it).
func store(ctx context.Context, db *bun.DB, value, byUserID string) error {
	var actor *string
	if byUserID != "" {
		actor = &byUserID
	}
	rows := []auth.SystemSetting{{
		Key:       settingLogLevel,
		Value:     value,
		UpdatedAt: time.Now(),
		UpdatedBy: actor,
	}}
	_, err := db.NewInsert().Model(&rows).
		On("CONFLICT (key) DO UPDATE").
		Set("value = EXCLUDED.value").
		Set("updated_at = EXCLUDED.updated_at").
		Set("updated_by = EXCLUDED.updated_by").
		Exec(ctx)
	return err
}

// LoadPersistedLevel reads the stored level so the server can re-apply an admin's
// choice at boot, OVER the env default. It returns ok=false (and no error) when
// no row exists or the stored value is unparseable -- the caller then keeps the
// env/default level. Exported for cmd/server/main.go.
func LoadPersistedLevel(ctx context.Context, db *bun.DB) (slog.Level, bool, error) {
	var row auth.SystemSetting
	err := db.NewSelect().Model(&row).Where("key = ?", settingLogLevel).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	l, ok := logx.ParseLevel(row.Value)
	if !ok {
		return 0, false, nil
	}
	return l, true, nil
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
