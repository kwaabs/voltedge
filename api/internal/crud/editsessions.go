package crud

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/drafts"
	"github.com/uptrace/bun"
)

// editsessions.go rolls gis.feature_history up BY edit-mode session (migration
// 0158): a batch of changes made between clicking Edit and View is stamped with
// one edit_session_id, so this view answers "when did this person's edit session
// start and end, and what did it touch" as a single row per session. It is the
// exact (session-tagged) complement of activity.go's per-change slice. By
// construction it only ever shows sessions where SOMETHING changed -- an empty
// edit session writes no history rows, so it never appears.
//
// Read-only, admin-gated (the same monitoring capability as the activity log),
// and carries the same private-drafts visibility filter so a peer's staged work
// never leaks into the cross-user roll-up.

// EditSession is one edit-mode session: who, when it started/ended (first/last
// stamped change), how many changes it made, and the distinct classes it touched
// (comma-joined, so the client needn't scan an array type).
type EditSession struct {
	SessionID   string    `bun:"session_id" json:"session_id"`
	ActorID     *string   `bun:"actor_id" json:"actor_id"`
	ActorName   *string   `bun:"actor_name" json:"actor_name"`
	ActorEmail  *string   `bun:"actor_email" json:"actor_email"`
	StartedAt   time.Time `bun:"started_at" json:"started_at"`
	EndedAt     time.Time `bun:"ended_at" json:"ended_at"`
	ChangeCount int       `bun:"change_count" json:"change_count"`
	Classes     string    `bun:"classes" json:"classes"`
}

// EditSessionFilter narrows the roll-up. All optional; a bare call returns the
// most recent sessions system-wide.
type EditSessionFilter struct {
	ActorID string // a user id -- the "who" (a specific person to filter to)
	// ScopeActorIDs, when non-nil, restricts the roll-up to sessions by these
	// actors -- the viewer's own id plus their subordinates for a non-admin
	// supervisor. nil means no restriction (an admin / break-glass sees everyone).
	// An empty (non-nil) slice would match nobody, which the handler avoids by
	// refusing access to a user with neither admin rights nor any report.
	ScopeActorIDs []string
	From          *time.Time
	To            *time.Time
	Limit         int
}

// listEditSessions groups the session-stamped history rows into one row per
// session, most-recently-ended first. From/To window on the session's span (a
// session counts if it ended at/after From and started before To), not on
// individual rows, so a session is never split by the window.
func listEditSessions(ctx context.Context, db bun.IDB, f EditSessionFilter) ([]EditSession, error) {
	limit := f.Limit
	if limit <= 0 || limit > 1000 {
		limit = 500
	}

	authors, apply, err := drafts.VisibleAuthors(ctx, db)
	if err != nil {
		return nil, err
	}

	q := db.NewSelect().
		ColumnExpr("h.edit_session_id::text AS session_id, h.actor_id::text AS actor_id, "+
			"u.display_name AS actor_name, u.email AS actor_email, "+
			"MIN(h.at) AS started_at, MAX(h.at) AS ended_at, COUNT(*) AS change_count, "+
			"STRING_AGG(DISTINCT h.feature_class_code, ',') AS classes").
		TableExpr("gis.feature_history AS h").
		Join("LEFT JOIN iam.users AS u ON u.id = h.actor_id").
		Where("h.edit_session_id IS NOT NULL")

	if f.ActorID != "" {
		q = q.Where("h.actor_id::text = ?", f.ActorID)
	}
	// Supervisor scope: a non-admin viewer sees only their own + subordinates'
	// sessions. nil = no restriction (admin / break-glass). The handler guarantees
	// a non-nil slice is non-empty, so this never silently matches nobody.
	if f.ScopeActorIDs != nil {
		q = q.Where("h.actor_id::text IN (?)", bun.In(f.ScopeActorIDs))
	}
	// Private-drafts: keep a staging row only when its actor is visible; keep every
	// non-staging row. Mirrors listActivity.
	if apply {
		q = q.Where("(h.action NOT IN (?) OR h.actor_id::text IN (?))", bun.In(stagingActions), bun.In(authors))
	}

	q = q.GroupExpr("h.edit_session_id, h.actor_id, u.display_name, u.email")
	if f.From != nil {
		q = q.Having("MAX(h.at) >= ?", *f.From)
	}
	if f.To != nil {
		q = q.Having("MIN(h.at) < ?", *f.To)
	}

	sessions := []EditSession{}
	if err := q.OrderExpr("MAX(h.at) DESC").Limit(limit).Scan(ctx, &sessions); err != nil {
		return nil, fmt.Errorf("crud: listing edit sessions: %w", err)
	}
	return sessions, nil
}

// editSessionsHandler serves the edit-session roll-up with tiered visibility:
// a break-glass account or an admin:users holder sees everyone; a non-admin
// supervisor (someone with at least one report) sees their own sessions plus
// their subordinates'; anyone else is refused. The scope is enforced here (and
// re-applied as a row filter in listEditSessions), so a UI that offers the view
// to the wrong person still gets nothing it shouldn't.
func editSessionsHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			writeError(w, forbidden("authentication required"))
			return
		}
		f := EditSessionFilter{ActorID: r.URL.Query().Get("actor_id")}
		// Admin / break-glass see everyone (no scope). A non-admin supervisor is
		// scoped to self + subordinates; a user with no reports and no admin rights
		// is refused outright rather than shown an empty list.
		if !user.BreakGlass && !user.Has(auth.PermUsersManage) {
			if len(user.Subordinates) == 0 {
				writeError(w, forbidden("the edit-session log is for supervisors and admins -- you have no reports to review"))
				return
			}
			f.ScopeActorIDs = append([]string{user.ID}, user.Subordinates...)
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

		sessions, err := listEditSessions(r.Context(), db, f)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"edit_sessions": sessions})
	}
}
