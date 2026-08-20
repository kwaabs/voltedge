package auth

import (
	"context"
	"net/http"
)

// logEvent appends one row to iam.auth_events -- the append-only
// security audit log. Best-effort: an audit-write failure must never
// change the outcome of the action being audited, so callers ignore the
// returned error's effect on control flow (they may log it). userID may
// be empty for events with no known subject (e.g. a login attempt for an
// unknown email); detail may be empty.
func (s *service) logEvent(ctx context.Context, r *http.Request, eventType, userID, detail string) {
	ev := &AuthEvent{
		EventType: eventType,
		IP:        clientIP(r),
		UserAgent: clientUA(r),
	}
	if userID != "" {
		ev.UserID = &userID
	}
	if detail != "" {
		ev.Detail = &detail
	}
	_, _ = s.db.NewInsert().Model(ev).Exec(ctx)
}
