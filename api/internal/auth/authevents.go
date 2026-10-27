package auth

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// authevents.go is the READ side of iam.auth_events -- the append-only security
// audit log that events.go writes. It backs the admin "Sign-in & security log"
// page: who signed in (or failed / was denied), when, from what IP, with which
// browser. Mounted under /auth/admin, so it inherits that subrouter's gate --
// admin:users (and a break-glass account), the same audience as user management.

// authEventJSON is one row of the security log, with the actor resolved to a
// name/email via a LEFT JOIN (both null for an event with no known subject -- a
// login attempt on an unknown email -- or a since-deleted user).
type authEventJSON struct {
	ID        string    `bun:"id" json:"id"`
	UserID    *string   `bun:"user_id" json:"user_id"`
	UserName  *string   `bun:"user_name" json:"user_name"`
	UserEmail *string   `bun:"user_email" json:"user_email"`
	EventType string    `bun:"event_type" json:"event_type"`
	Detail    *string   `bun:"detail" json:"detail"`
	IP        *string   `bun:"ip" json:"ip"`
	UserAgent *string   `bun:"user_agent" json:"user_agent"`
	CreatedAt time.Time `bun:"created_at" json:"created_at"`
}

// authEventFilter narrows the log. All optional; a bare call returns the most
// recent events.
type authEventFilter struct {
	UserID    string
	EventType string
	From      *time.Time
	To        *time.Time
	Limit     int
}

// listAuthEvents returns the security log newest first, bounded by Limit
// (default 200, max 1000).
func (s *service) listAuthEvents(ctx context.Context, f authEventFilter) ([]authEventJSON, error) {
	limit := f.Limit
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows := []authEventJSON{}
	q := s.db.NewSelect().
		TableExpr("iam.auth_events AS e").
		ColumnExpr("e.id::text AS id").
		ColumnExpr("e.user_id::text AS user_id").
		ColumnExpr("u.display_name AS user_name").
		ColumnExpr("u.email AS user_email").
		ColumnExpr("e.event_type, e.detail, e.ip, e.user_agent, e.created_at").
		Join("LEFT JOIN iam.users AS u ON u.id = e.user_id")
	if f.UserID != "" {
		q = q.Where("e.user_id::text = ?", f.UserID)
	}
	if f.EventType != "" {
		q = q.Where("e.event_type = ?", f.EventType)
	}
	if f.From != nil {
		q = q.Where("e.created_at >= ?", *f.From)
	}
	if f.To != nil {
		q = q.Where("e.created_at < ?", *f.To)
	}
	q = q.OrderExpr("e.created_at DESC").Limit(limit)
	if err := q.Scan(ctx, &rows); err != nil {
		return nil, fmt.Errorf("auth: listing security events: %w", err)
	}
	return rows, nil
}

// adminAuthEventsHandler is GET /auth/admin/events -- the security log, filterable
// by user_id / event_type / a from..to window (RFC3339), newest first. Gated with
// the rest of /auth/admin on admin:users (and break-glass).
func (s *service) adminAuthEventsHandler(w http.ResponseWriter, r *http.Request) {
	f := authEventFilter{
		UserID:    r.URL.Query().Get("user_id"),
		EventType: r.URL.Query().Get("event_type"),
	}
	if v := r.URL.Query().Get("from"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeError(w, invalid("from must be an RFC3339 timestamp: %v", err))
			return
		}
		f.From = &t
	}
	if v := r.URL.Query().Get("to"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeError(w, invalid("to must be an RFC3339 timestamp: %v", err))
			return
		}
		f.To = &t
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			f.Limit = n
		}
	}
	events, err := s.listAuthEvents(r.Context(), f)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}
