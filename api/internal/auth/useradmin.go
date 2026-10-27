package auth

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"
)

// This file is the Slice D user-administration API -- the in-app surface
// behind the `admin:users` permission that the frontend user-management
// page drives. It is the online counterpart to api/cmd/authadmin (the
// out-of-band CLI): same operations -- create an account, assign/revoke
// roles, disable, manage the local credential, unlink an SSO identity --
// but gated by RBAC and CSRF and mounted inside the running API. SSO
// *linking* is deliberately absent: per the locked design an SSO identity
// is bound automatically on the user's first successful SSO login (by
// email match), never pre-set by an admin, so only unlink is an admin
// action.
//
// Routes (mounted at /auth/admin, every one behind admin:users):
//
//	GET    /users                          list accounts (roles, linked providers, status)
//	GET    /roles                          the assignable role catalog
//	GET    /events                         read-only security audit log (sign-ins, failures, SSO, who/when/IP/browser)
//	POST   /users                          create an account (email, name, break_glass, role?, password?)
//	PATCH  /users/{id}                      edit profile (display_name); email is immutable
//	POST   /users/{id}/roles               assign a role            {"role":"editor"}
//	DELETE /users/{id}/roles/{role}        revoke a role
//	POST   /users/{id}/status              enable/disable           {"status":"disabled"}
//	POST   /users/{id}/password            set/replace local password {"password":"..."}
//	DELETE /users/{id}/identities/{prov}   unlink an SSO identity (azuread|google)

// adminRoutes is the /auth/admin subrouter. The whole tree needs the
// admin:users permission (RequirePermission also 401s an anonymous
// request); the mutating endpoints additionally carry RequireCSRF.
func (s *service) adminRoutes() chi.Router {
	r := chi.NewRouter()
	r.Use(RequirePermission(PermUsersManage, PermUsersManage))

	r.Get("/users", s.adminListUsersHandler)
	r.Get("/roles", s.adminListRolesHandler)
	// Read-only security audit log (iam.auth_events): sign-ins, failures, SSO
	// links, logouts, role changes -- who/when/IP/browser. See authevents.go.
	r.Get("/events", s.adminAuthEventsHandler)

	r.Group(func(r chi.Router) {
		r.Use(RequireCSRF)
		r.Post("/users", s.adminCreateUserHandler)
		r.Patch("/users/{id}", s.adminUpdateUserHandler)
		r.Post("/users/{id}/roles", s.adminAssignRoleHandler)
		r.Delete("/users/{id}/roles/{role}", s.adminRemoveRoleHandler)
		r.Post("/users/{id}/status", s.adminSetStatusHandler)
		r.Post("/users/{id}/password", s.adminSetPasswordHandler)
		r.Delete("/users/{id}/identities/{provider}", s.adminUnlinkIdentityHandler)
		// Phase 58 spatial access: territory grants + the org-tree manager
		// edge (see useradmin_territories.go).
		r.Post("/users/{id}/territories", s.adminGrantTerritoryHandler)
		r.Delete("/users/{id}/territories/{territoryId}", s.adminRevokeTerritoryHandler)
		r.Post("/users/{id}/manager", s.adminSetManagerHandler)
	})

	return r
}

// adminUserJSON is the wire shape for one account in the admin list --
// everything the management UI needs to render a row, and nothing
// credential-ish (never a hash, never a token).
type adminUserJSON struct {
	ID          string   `json:"id"`
	Email       string   `json:"email"`
	DisplayName string   `json:"display_name"`
	Status      string   `json:"status"`
	BreakGlass  bool     `json:"break_glass"`
	Roles       []string `json:"roles"`
	Providers   []string `json:"providers"`
	// ManagerID is this user's manager (Phase 58), or null at the top of the
	// org tree. Territories are the user's DIRECT territory grants (not the
	// expanded/inherited effective set) -- what the management UI edits.
	ManagerID   *string                   `json:"manager_id"`
	Territories []adminTerritoryGrantJSON `json:"territories"`
	CreatedAt   time.Time                 `json:"created_at"`
}

// adminTerritoryGrantJSON is one direct territory grant in a user's admin
// row -- the id the grant names plus a human label (territory name + level
// code) so the UI needn't re-resolve the tree.
type adminTerritoryGrantJSON struct {
	TerritoryID   string `json:"territory_id"`
	TerritoryName string `json:"territory_name"`
	LevelCode     string `json:"level_code"`
}

// adminListUsersHandler is GET /auth/admin/users. It returns every account
// with its role keys and linked identity providers, plus the current
// local-auth switch state so the page can render the toggle in one round
// trip.
func (s *service) adminListUsersHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var users []User
	if err := s.db.NewSelect().Model(&users).Order("created_at").Scan(ctx); err != nil {
		writeError(w, err)
		return
	}

	// Roles per user, one join rather than a query per row.
	var roleRows []struct {
		UserID  string `bun:"user_id"`
		RoleKey string `bun:"role_key"`
	}
	err := s.db.NewSelect().
		ColumnExpr("ur.user_id AS user_id").
		ColumnExpr("r.key AS role_key").
		TableExpr("iam.user_roles AS ur").
		Join("JOIN iam.roles AS r ON r.id = ur.role_id").
		OrderExpr("r.key").
		Scan(ctx, &roleRows)
	if err != nil {
		writeError(w, err)
		return
	}
	rolesByUser := make(map[string][]string, len(users))
	for _, row := range roleRows {
		rolesByUser[row.UserID] = append(rolesByUser[row.UserID], row.RoleKey)
	}

	// Linked identity providers per user (local / azuread / google).
	var idRows []struct {
		UserID   string `bun:"user_id"`
		Provider string `bun:"provider"`
	}
	err = s.db.NewSelect().
		ColumnExpr("user_id").
		ColumnExpr("provider").
		TableExpr("iam.user_identities").
		OrderExpr("provider").
		Scan(ctx, &idRows)
	if err != nil {
		writeError(w, err)
		return
	}
	providersByUser := make(map[string][]string, len(users))
	for _, row := range idRows {
		providersByUser[row.UserID] = append(providersByUser[row.UserID], row.Provider)
	}

	// Phase 58: direct territory grants per user, one labelled join rather
	// than a query per row (mirrors loadUserTerritories, grouped).
	var grantRows []struct {
		UserID        string `bun:"user_id"`
		TerritoryID   string `bun:"territory_id"`
		TerritoryName string `bun:"territory_name"`
		LevelCode     string `bun:"level_code"`
	}
	err = s.db.NewSelect().
		ColumnExpr("ut.user_id AS user_id").
		ColumnExpr("ut.territory_id AS territory_id").
		ColumnExpr("t.name AS territory_name").
		ColumnExpr("lv.code AS level_code").
		TableExpr("iam.user_territories AS ut").
		Join("JOIN gis.territories AS t ON t.id = ut.territory_id").
		Join("JOIN gis.territory_levels AS lv ON lv.id = t.level_id").
		OrderExpr("ut.granted_at").
		Scan(ctx, &grantRows)
	if err != nil {
		writeError(w, err)
		return
	}
	territoriesByUser := make(map[string][]adminTerritoryGrantJSON, len(users))
	for _, row := range grantRows {
		territoriesByUser[row.UserID] = append(territoriesByUser[row.UserID], adminTerritoryGrantJSON{
			TerritoryID:   row.TerritoryID,
			TerritoryName: row.TerritoryName,
			LevelCode:     row.LevelCode,
		})
	}

	out := make([]adminUserJSON, len(users))
	for i, u := range users {
		territories := territoriesByUser[u.ID]
		if territories == nil {
			territories = []adminTerritoryGrantJSON{}
		}
		// A user with no roles / no linked identity has a nil slice here, which
		// marshals to JSON null; the admin UI treats roles/providers as arrays
		// (user.roles.includes(...)), so emit [] rather than null.
		roles := rolesByUser[u.ID]
		if roles == nil {
			roles = []string{}
		}
		providers := providersByUser[u.ID]
		if providers == nil {
			providers = []string{}
		}
		out[i] = adminUserJSON{
			ID:          u.ID,
			Email:       u.Email,
			DisplayName: u.DisplayName,
			Status:      u.Status,
			BreakGlass:  u.BreakGlass,
			Roles:       roles,
			Providers:   providers,
			ManagerID:   u.ManagerID,
			Territories: territories,
			CreatedAt:   u.CreatedAt,
		}
	}

	localEnabled, _ := s.localAuthEnabled(ctx)
	writeJSON(w, http.StatusOK, map[string]any{
		"users":              out,
		"local_auth_enabled": localEnabled,
	})
}

// adminListRolesHandler is GET /auth/admin/roles -- the role catalog the
// create/assign controls pick from.
func (s *service) adminListRolesHandler(w http.ResponseWriter, r *http.Request) {
	var roles []Role
	if err := s.db.NewSelect().Model(&roles).Order("key").Scan(r.Context()); err != nil {
		writeError(w, err)
		return
	}
	out := make([]map[string]string, len(roles))
	for i, role := range roles {
		out[i] = map[string]string{"key": role.Key, "name": role.Name, "description": role.Description}
	}
	writeJSON(w, http.StatusOK, map[string]any{"roles": out})
}

type adminCreateUserRequest struct {
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	BreakGlass  bool   `json:"break_glass"`
	Role        string `json:"role"`
	Password    string `json:"password"`
}

// adminCreateUserHandler is POST /auth/admin/users. It pre-provisions an
// account (the locked design: an admin creates every account first; SSO
// never self-creates). A password is optional -- an SSO-only account is
// created with no local identity and links its provider on first login --
// except for a break-glass account, which must have a local credential
// since being break-glass is meaningless without one.
func (s *service) adminCreateUserHandler(w http.ResponseWriter, r *http.Request) {
	acting, _ := UserFrom(r.Context())

	var req adminCreateUserRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	if req.BreakGlass && req.Password == "" {
		writeError(w, invalid("a break-glass account requires a local password"))
		return
	}

	userID, err := s.createManagedUser(r.Context(), req.Email, req.DisplayName, req.BreakGlass, req.Password)
	if err != nil {
		writeError(w, err)
		return
	}

	if role := strings.TrimSpace(req.Role); role != "" {
		if err := s.assignRoleByID(r.Context(), userID, role, actorID(acting)); err != nil {
			// The account exists; surface the role failure rather than
			// pretend the whole create failed.
			writeError(w, err)
			return
		}
		s.logEvent(r.Context(), r, eventRoleChanged, userID, "role "+role+" granted on create")
	}

	s.writeAdminUser(w, r, userID, http.StatusCreated)
}

// createManagedUser inserts a user and, only when a password is given, a
// local identity -- in one transaction so a half-made account can never be
// left behind. Unlike CreateUser (provision.go), the password is optional
// here, which is what lets an admin pre-provision an SSO-only account.
func (s *service) createManagedUser(ctx context.Context, email, name string, breakGlass bool, password string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	name = strings.TrimSpace(name)
	if email == "" {
		return "", invalid("email is required")
	}
	if !strings.Contains(email, "@") {
		return "", invalid("%q is not a valid email address", email)
	}
	// The display name is optional for an SSO account enrolled with just an email:
	// show the email as a placeholder until the provider's token fills the real
	// name on first login (authenticateOIDC -> syncDisplayName). An admin who types
	// a name keeps it (syncDisplayName never overwrites a non-placeholder).
	if name == "" {
		name = email
	}
	if password != "" {
		if err := validatePasswordStrength(password); err != nil {
			return "", invalid("%s", err.Error())
		}
	}

	exists, err := s.db.NewSelect().Model((*User)(nil)).Where("email = ?", email).Exists(ctx)
	if err != nil {
		return "", err
	}
	if exists {
		return "", invalid("an account with email %q already exists", email)
	}

	var hash string
	if password != "" {
		h, hErr := hashPassword(password)
		if hErr != nil {
			return "", hErr
		}
		hash = h
	}

	var userID string
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		u := &User{Email: email, DisplayName: name, BreakGlass: breakGlass, Status: "active"}
		if _, err := tx.NewInsert().Model(u).Returning("id").Exec(ctx); err != nil {
			return err
		}
		userID = u.ID
		if password != "" {
			now := time.Now()
			id := &Identity{UserID: u.ID, Provider: "local", PasswordHash: &hash, PasswordChangedAt: &now}
			if _, err := tx.NewInsert().Model(id).Exec(ctx); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return userID, nil
}

type adminUpdateUserRequest struct {
	DisplayName string `json:"display_name"`
}

// adminUpdateUserHandler is PATCH /auth/admin/users/{id} -- edit an account's
// mutable profile. Only the display name is editable here; email is the
// login identity and the SSO identity-link key, so it is deliberately
// immutable through this API (changing it would orphan a linked provider).
func (s *service) adminUpdateUserHandler(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "id")

	var req adminUpdateUserRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	name := strings.TrimSpace(req.DisplayName)
	if name == "" {
		writeError(w, invalid("display name is required"))
		return
	}

	if _, err := s.userByID(r.Context(), userID); err != nil {
		writeError(w, err)
		return
	}
	if _, err := s.db.NewUpdate().Model((*User)(nil)).
		Set("display_name = ?", name).
		Set("updated_at = ?", time.Now()).
		Where("id = ?", userID).
		Exec(r.Context()); err != nil {
		writeError(w, err)
		return
	}
	s.logEvent(r.Context(), r, eventRoleChanged, userID, "display name updated")
	s.writeAdminUser(w, r, userID, http.StatusOK)
}

type adminAssignRoleRequest struct {
	Role string `json:"role"`
}

// adminAssignRoleHandler is POST /auth/admin/users/{id}/roles.
func (s *service) adminAssignRoleHandler(w http.ResponseWriter, r *http.Request) {
	acting, _ := UserFrom(r.Context())
	userID := chi.URLParam(r, "id")

	var req adminAssignRoleRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	role := strings.TrimSpace(req.Role)
	if role == "" {
		writeError(w, invalid("role is required"))
		return
	}
	if err := s.assignRoleByID(r.Context(), userID, role, actorID(acting)); err != nil {
		writeError(w, err)
		return
	}
	s.logEvent(r.Context(), r, eventRoleChanged, userID, "role "+role+" granted")
	s.writeAdminUser(w, r, userID, http.StatusOK)
}

// adminRemoveRoleHandler is DELETE /auth/admin/users/{id}/roles/{role}.
func (s *service) adminRemoveRoleHandler(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "id")
	roleKey := chi.URLParam(r, "role")

	role, err := s.roleByKey(r.Context(), roleKey)
	if err != nil {
		writeError(w, err)
		return
	}
	if _, err := s.db.NewDelete().Model((*UserRole)(nil)).
		Where("user_id = ?", userID).
		Where("role_id = ?", role.ID).
		Exec(r.Context()); err != nil {
		writeError(w, err)
		return
	}
	s.logEvent(r.Context(), r, eventRoleChanged, userID, "role "+roleKey+" revoked")
	s.writeAdminUser(w, r, userID, http.StatusOK)
}

type adminStatusRequest struct {
	Status string `json:"status"`
}

// adminSetStatusHandler is POST /auth/admin/users/{id}/status. Disabling an
// account also revokes its live sessions, so the change takes effect on the
// very next request rather than whenever the session happens to expire. An
// admin cannot disable their own account -- that is the classic way to lock
// the last admin out of the system.
func (s *service) adminSetStatusHandler(w http.ResponseWriter, r *http.Request) {
	acting, _ := UserFrom(r.Context())
	userID := chi.URLParam(r, "id")

	var req adminStatusRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	status := strings.TrimSpace(req.Status)
	if status != "active" && status != "disabled" {
		writeError(w, invalid(`status must be "active" or "disabled"`))
		return
	}
	if acting != nil && acting.ID == userID && status == "disabled" {
		writeError(w, forbidden("you cannot disable your own account"))
		return
	}

	if _, err := s.userByID(r.Context(), userID); err != nil {
		writeError(w, err)
		return
	}
	if _, err := s.db.NewUpdate().Model((*User)(nil)).
		Set("status = ?", status).
		Set("updated_at = ?", time.Now()).
		Where("id = ?", userID).
		Exec(r.Context()); err != nil {
		writeError(w, err)
		return
	}
	if status == "disabled" {
		// Revoke every live session for this user so the disable is
		// immediate, not eventual.
		_, _ = s.db.NewUpdate().Model((*Session)(nil)).
			Set("revoked_at = ?", time.Now()).
			Where("user_id = ?", userID).
			Where("revoked_at IS NULL").
			Exec(r.Context())
	}
	s.logEvent(r.Context(), r, eventRoleChanged, userID, "status set to "+status)
	s.writeAdminUser(w, r, userID, http.StatusOK)
}

type adminPasswordRequest struct {
	Password string `json:"password"`
}

// adminSetPasswordHandler is POST /auth/admin/users/{id}/password -- set or
// replace an account's local credential (also how an admin adds one to an
// SSO-only account, e.g. to make it break-glass-capable).
func (s *service) adminSetPasswordHandler(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "id")

	var req adminPasswordRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	if err := validatePasswordStrength(req.Password); err != nil {
		writeError(w, invalid("%s", err.Error()))
		return
	}

	user, err := s.userByID(r.Context(), userID)
	if err != nil {
		writeError(w, err)
		return
	}
	if err := SetLocalPassword(r.Context(), s.bunDB(), user.Email, req.Password); err != nil {
		writeError(w, err)
		return
	}
	s.writeAdminUser(w, r, userID, http.StatusOK)
}

// adminUnlinkIdentityHandler is DELETE
// /auth/admin/users/{id}/identities/{provider} -- remove one linked
// identity. Restricted to the SSO providers; the local credential is
// managed through the password endpoint, not unlinked here.
func (s *service) adminUnlinkIdentityHandler(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "id")
	provider := chi.URLParam(r, "provider")

	if provider != "azuread" && provider != "google" {
		writeError(w, invalid("only SSO identities (azuread, google) can be unlinked"))
		return
	}
	if _, err := s.db.NewDelete().Model((*Identity)(nil)).
		Where("user_id = ?", userID).
		Where("provider = ?", provider).
		Exec(r.Context()); err != nil {
		writeError(w, err)
		return
	}
	s.logEvent(r.Context(), r, eventSSOLinked, userID, provider+" identity unlinked by admin")
	s.writeAdminUser(w, r, userID, http.StatusOK)
}

// --- small shared helpers -------------------------------------------------

// assignRoleByID grants roleKey to a user id (idempotent). grantedBy is the
// acting admin's id, or "" for none.
func (s *service) assignRoleByID(ctx context.Context, userID, roleKey, grantedBy string) error {
	if _, err := s.userByID(ctx, userID); err != nil {
		return err
	}
	role, err := s.roleByKey(ctx, roleKey)
	if err != nil {
		return err
	}
	ur := &UserRole{UserID: userID, RoleID: role.ID}
	if grantedBy != "" {
		ur.GrantedBy = &grantedBy
	}
	_, err = s.db.NewInsert().Model(ur).
		On("CONFLICT (user_id, role_id) DO NOTHING").
		Exec(ctx)
	return err
}

func (s *service) userByID(ctx context.Context, userID string) (*User, error) {
	var user User
	err := s.db.NewSelect().Model(&user).Where("id = ?", userID).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, invalid("no account with id %q", userID)
	}
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func (s *service) roleByKey(ctx context.Context, roleKey string) (*Role, error) {
	var role Role
	err := s.db.NewSelect().Model(&role).Where("key = ?", roleKey).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, invalid("no role with key %q", roleKey)
	}
	if err != nil {
		return nil, err
	}
	return &role, nil
}

// writeAdminUser re-reads one account and writes it as the response body --
// every mutating handler returns the fresh row so the SPA can update in
// place without a second round trip.
func (s *service) writeAdminUser(w http.ResponseWriter, r *http.Request, userID string, status int) {
	ctx := r.Context()
	user, err := s.userByID(ctx, userID)
	if err != nil {
		writeError(w, err)
		return
	}

	var roles []string
	_ = s.db.NewSelect().
		ColumnExpr("r.key").
		TableExpr("iam.user_roles AS ur").
		Join("JOIN iam.roles AS r ON r.id = ur.role_id").
		Where("ur.user_id = ?", userID).
		OrderExpr("r.key").
		Scan(ctx, &roles)

	var providers []string
	_ = s.db.NewSelect().
		ColumnExpr("provider").
		TableExpr("iam.user_identities").
		Where("user_id = ?", userID).
		OrderExpr("provider").
		Scan(ctx, &providers)

	// A user with no roles / no identity leaves these nil, which marshals to
	// JSON null; the admin UI treats them as arrays, so emit [] instead.
	if roles == nil {
		roles = []string{}
	}
	if providers == nil {
		providers = []string{}
	}

	// Phase 58: direct territory grants (labelled) so the UI can render the
	// scope chips without re-resolving the tree.
	territories, err := s.loadUserTerritories(ctx, userID)
	if err != nil {
		writeError(w, err)
		return
	}

	writeJSON(w, status, adminUserJSON{
		ID:          user.ID,
		Email:       user.Email,
		DisplayName: user.DisplayName,
		Status:      user.Status,
		BreakGlass:  user.BreakGlass,
		Roles:       roles,
		Providers:   providers,
		ManagerID:   user.ManagerID,
		Territories: territories,
		CreatedAt:   user.CreatedAt,
	})
}

// bunDB returns the concrete *bun.DB for the few call sites that need a
// *bun.DB (the provisioning helpers in provision.go take one). At runtime
// s.db is always the *bun.DB handed to Routes; the assertion is a safe
// narrowing, and nil if it were ever a Tx (it never is here).
func (s *service) bunDB() *bun.DB {
	if db, ok := s.db.(*bun.DB); ok {
		return db
	}
	return nil
}

// actorID is the acting user's id, or "" if the request is somehow
// anonymous (RequirePermission makes that unreachable on these routes).
func actorID(u *SessionUser) string {
	if u == nil {
		return ""
	}
	return u.ID
}
