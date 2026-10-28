package fieldtrack

// Two HTTP surfaces:
//   - IngestBreadcrumbsHandler — wired into the fieldsync bearer router (behind the
//     field user's features:write, beside POST /sync): the device uploads its
//     movement trail. Writes the caller's own trail only.
//   - Routes — the web supervisor surface, mounted on cmd/server behind
//     field:tracks:read: list workers, their sessions, and one session's trail.
//
// Action waypoints are NOT ingested here — they ride the durable sync inbox and are
// stamped by the sync drain worker (see fieldpack/sync.go, RecordActionWaypoint).

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/uptrace/bun"
)

// IngestBreadcrumbsHandler stores the caller's uploaded movement-trail batch. The
// field user is read from context (the fieldsync bearer middleware put them there);
// the trail is always the caller's own, so there is no cross-user write here.
func IngestBreadcrumbsHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			writeErr(w, http.StatusUnauthorized, "missing or invalid bearer token")
			return
		}
		var req struct {
			DeviceID string            `json:"device_id"`
			Points   []BreadcrumbInput `json:"points"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "request body is not valid JSON: "+err.Error())
			return
		}
		res, err := IngestBreadcrumbs(r.Context(), db, user.ID, req.DeviceID, req.Points)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, res)
	}
}

// Routes is the web supervisor read surface (mounted behind field:tracks:read):
//
//	GET /field-tracks/workers                     field users who have a trail
//	GET /field-tracks/sessions?user_id=&from=&to= a worker's sessions in a window
//	GET /field-tracks/sessions/{id}               one session's trail + waypoints
func Routes(db *bun.DB) chi.Router {
	r := chi.NewRouter()
	r.Get("/workers", listWorkersHandler(db))
	r.Get("/sessions", listSessionsHandler(db))
	r.Get("/sessions/{id}", getTrailHandler(db))
	return r
}

func listWorkersHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workers, err := ListWorkers(r.Context(), db)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"workers": workers})
	}
}

func listSessionsHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		userID := q.Get("user_id")
		if userID == "" {
			writeErr(w, http.StatusBadRequest, "user_id is required")
			return
		}
		from := parseTime(q.Get("from"))
		to := parseTime(q.Get("to"))
		sessions, err := ListSessions(r.Context(), db, userID, from, to)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"sessions": sessions})
	}
}

func getTrailHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		trail, err := GetTrail(r.Context(), db, chi.URLParam(r, "id"))
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, trail)
	}
}

// parseTime accepts an RFC3339 timestamp or a bare YYYY-MM-DD date; anything else
// (including empty) yields the zero time, read as "unbounded" by ListSessions.
func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t
	}
	return time.Time{}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
