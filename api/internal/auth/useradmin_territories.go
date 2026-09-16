package auth

import (
	"context"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/kwaabs/voltedge/api/internal/access"
)

// This file is the Phase 58 slice of the user-administration API: assigning
// and revoking a user's territory grants, and setting the manager edge that
// spatial access cascades up the org tree. It sits beside the role-management
// endpoints in useradmin.go, on the same /auth/admin subrouter behind
// admin:users + CSRF, and reuses the same "return the fresh user row" response
// convention so the management UI updates in place.
//
// Every mutation calls access.InvalidateAll AFTER the write commits, so a
// grant, revoke, or manager change takes effect on the very next request --
// the "instant revocation" the phase requires -- rather than whenever the
// per-user effective-set cache would have expired.

type adminGrantTerritoryRequest struct {
	TerritoryID string `json:"territory_id"`
}

// adminGrantTerritoryHandler is POST /auth/admin/users/{id}/territories.
func (s *service) adminGrantTerritoryHandler(w http.ResponseWriter, r *http.Request) {
	acting, _ := UserFrom(r.Context())
	userID := chi.URLParam(r, "id")

	var req adminGrantTerritoryRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	territoryID := strings.TrimSpace(req.TerritoryID)
	if territoryID == "" {
		writeError(w, invalid("territory_id is required"))
		return
	}

	grantedBy := actorPtr(acting)
	if err := access.Grant(r.Context(), s.db, userID, territoryID, grantedBy); err != nil {
		writeError(w, err)
		return
	}
	access.InvalidateAll()
	s.logEvent(r.Context(), r, eventRoleChanged, userID, "territory "+territoryID+" granted")
	s.writeAdminUser(w, r, userID, http.StatusOK)
}

// adminRevokeTerritoryHandler is
// DELETE /auth/admin/users/{id}/territories/{territoryId}. Idempotent: revoking
// a grant the user doesn't hold is a no-op success.
func (s *service) adminRevokeTerritoryHandler(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "id")
	territoryID := chi.URLParam(r, "territoryId")

	if err := access.Revoke(r.Context(), s.db, userID, territoryID); err != nil {
		writeError(w, err)
		return
	}
	access.InvalidateAll()
	s.logEvent(r.Context(), r, eventRoleChanged, userID, "territory "+territoryID+" revoked")
	s.writeAdminUser(w, r, userID, http.StatusOK)
}

type adminSetManagerRequest struct {
	// ManagerID is the new manager, or null/"" to clear it (make the user a
	// top-of-org node). A pointer so "absent/null" is distinguishable and
	// means clear.
	ManagerID *string `json:"manager_id"`
}

// adminSetManagerHandler is POST /auth/admin/users/{id}/manager. Setting a
// manager that would form a cycle (a user transitively managing themselves) is
// rejected with a 400 by access.SetManager.
func (s *service) adminSetManagerHandler(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "id")

	var req adminSetManagerRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	// Normalize "" -> nil (clear), so the UI can send an empty string to
	// unset without a separate endpoint.
	managerID := req.ManagerID
	if managerID != nil && strings.TrimSpace(*managerID) == "" {
		managerID = nil
	}

	if err := access.SetManager(r.Context(), s.db, userID, managerID); err != nil {
		writeError(w, err)
		return
	}
	access.InvalidateAll()
	detail := "manager cleared"
	if managerID != nil {
		detail = "manager set to " + *managerID
	}
	s.logEvent(r.Context(), r, eventRoleChanged, userID, detail)
	s.writeAdminUser(w, r, userID, http.StatusOK)
}

// loadUserTerritories returns a user's DIRECT territory grants with a display
// label (territory name + level code), ordered by grant time. Used by both the
// batch user list and the single-user response builder. A user with no grants
// yields an empty (non-nil) slice so the JSON is [] not null.
func (s *service) loadUserTerritories(ctx context.Context, userID string) ([]adminTerritoryGrantJSON, error) {
	var rows []struct {
		TerritoryID   string `bun:"territory_id"`
		TerritoryName string `bun:"territory_name"`
		LevelCode     string `bun:"level_code"`
	}
	err := s.db.NewSelect().
		ColumnExpr("ut.territory_id AS territory_id").
		ColumnExpr("t.name AS territory_name").
		ColumnExpr("lv.code AS level_code").
		TableExpr("iam.user_territories AS ut").
		Join("JOIN gis.territories AS t ON t.id = ut.territory_id").
		Join("JOIN gis.territory_levels AS lv ON lv.id = t.level_id").
		Where("ut.user_id = ?", userID).
		OrderExpr("ut.granted_at").
		Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	out := make([]adminTerritoryGrantJSON, len(rows))
	for i, row := range rows {
		out[i] = adminTerritoryGrantJSON{
			TerritoryID:   row.TerritoryID,
			TerritoryName: row.TerritoryName,
			LevelCode:     row.LevelCode,
		}
	}
	return out, nil
}

// actorPtr is the acting user's id as a *string (nil if anonymous, which
// RequirePermission makes unreachable on these routes) -- the granted_by shape
// access.Grant wants.
func actorPtr(u *SessionUser) *string {
	if u == nil || u.ID == "" {
		return nil
	}
	id := u.ID
	return &id
}
