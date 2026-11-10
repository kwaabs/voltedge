package alarms

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/events"
)

// Routes is the alarm annunciator API, mounted at /alarms. GET reads (oms:read via
// the mount gate), POST mutates (oms:write). SSE "alarms" hints nudge every open
// annunciator to refetch after a change (the hub treats events as refetch hints).
//
//	GET  /alarms             the live annunciator (?status=&severity=&source=&limit=)
//	GET  /alarms/count       live counts for the toolbar badge
//	POST /alarms/reconcile   rescan standing sources (off-normal devices) now
//	POST /alarms/{id}/ack        acknowledge one
//	POST /alarms/{id}/shelve     suppress until a time ({until, reason})
//	POST /alarms/{id}/unshelve   return it to the annunciator
func Routes(db *bun.DB) chi.Router {
	r := chi.NewRouter()
	r.Get("/", listHandler(db))
	r.Get("/count", countHandler(db))
	r.Post("/reconcile", reconcileHandler(db))
	r.Post("/{id}/ack", ackHandler(db))
	r.Post("/{id}/shelve", shelveHandler(db))
	r.Post("/{id}/unshelve", unshelveHandler(db))
	return r
}

func listHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		limit, _ := strconv.Atoi(q.Get("limit"))
		list, err := List(r.Context(), db, Filter{
			Status:   q.Get("status"),
			Severity: q.Get("severity"),
			Source:   q.Get("source"),
			Limit:    limit,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"alarms": list})
	}
}

func countHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := CountActive(r.Context(), db)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, c)
	}
}

func reconcileHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := ReconcileNow(r.Context(), db); err != nil {
			writeError(w, err)
			return
		}
		broadcast(r.Context(), db)
		c, err := CountActive(r.Context(), db)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, c)
	}
}

func ackHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := Acknowledge(r.Context(), db, chi.URLParam(r, "id"), auth.ActorID(r.Context())); err != nil {
			writeError(w, err)
			return
		}
		broadcast(r.Context(), db)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}

func shelveHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Until  time.Time `json:"until"`
			Reason string    `json:"reason"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalidf("invalid request body: %v", err))
			return
		}
		if req.Until.IsZero() {
			writeError(w, invalidf("until (RFC3339) is required"))
			return
		}
		if err := Shelve(r.Context(), db, chi.URLParam(r, "id"), req.Until, req.Reason, auth.ActorID(r.Context())); err != nil {
			writeError(w, err)
			return
		}
		broadcast(r.Context(), db)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}

func unshelveHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := Unshelve(r.Context(), db, chi.URLParam(r, "id")); err != nil {
			writeError(w, err)
			return
		}
		broadcast(r.Context(), db)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}

// broadcast nudges every open annunciator to refetch, carrying the fresh counts so a
// toolbar badge can update without its own fetch. Best-effort (a hint, not truth).
func broadcast(ctx context.Context, db bun.IDB) {
	c, err := CountActive(ctx, db)
	if err != nil {
		return
	}
	events.Broadcast("alarms", c)
}
