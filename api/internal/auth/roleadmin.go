package auth

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"
)

// Role management (Slice D role editor) -- create / rename / clone / delete roles
// and set their permissions, the admin surface behind web /admin/roles. All of it
// hangs off the admin:users-gated adminRoutes() mount (useradmin.go), with
// RequireCSRF on the mutations. is_system roles (the enforcement-critical tiers:
// viewer/analyst/editor/admin/break-glass-admin, and field-technician) are
// read-only here -- the 0025 schema anticipated exactly this ("a future admin UI
// can refuse to delete them"); to tailor one, clone it. The functional presets
// (migration 0189) are is_system = false, so they are fully editable.

var roleKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,48}$`)

// adminListPermissionsHandler is GET /auth/admin/permissions -- the whole RBAC
// catalog, so the role editor can group and offer every permission.
func (s *service) adminListPermissionsHandler(w http.ResponseWriter, r *http.Request) {
	var perms []Permission
	if err := s.db.NewSelect().Model(&perms).Order("key").Scan(r.Context()); err != nil {
		writeError(w, err)
		return
	}
	out := make([]map[string]string, len(perms))
	for i, p := range perms {
		out[i] = map[string]string{"key": p.Key, "description": p.Description}
	}
	writeJSON(w, http.StatusOK, map[string]any{"permissions": out})
}

func (s *service) loadRole(ctx context.Context, id string) (*Role, error) {
	role := new(Role)
	if err := s.db.NewSelect().Model(role).Where("id = ?", id).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, invalid("role %q does not exist", id)
		}
		return nil, err
	}
	return role, nil
}

func (s *service) rolePermissionKeys(ctx context.Context, roleID string) ([]string, error) {
	var keys []string
	if err := s.db.NewSelect().Model((*RolePermission)(nil)).Column("permission_key").
		Where("role_id = ?", roleID).Scan(ctx, &keys); err != nil {
		return nil, err
	}
	sort.Strings(keys)
	if keys == nil {
		keys = []string{}
	}
	return keys, nil
}

func roleResponse(role *Role, perms []string) map[string]any {
	if perms == nil {
		perms = []string{}
	}
	return map[string]any{
		"id": role.ID, "key": role.Key, "name": role.Name,
		"description": role.Description, "is_system": role.IsSystem, "permissions": perms,
	}
}

type roleWriteRequest struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// adminCreateRoleHandler is POST /auth/admin/roles -- a new, editable (is_system
// false) role with no permissions yet.
func (s *service) adminCreateRoleHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req roleWriteRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	key := strings.TrimSpace(req.Key)
	name := strings.TrimSpace(req.Name)
	if !roleKeyPattern.MatchString(key) {
		writeError(w, invalid("role key must be lowercase letters, digits and hyphens (2-49 chars)"))
		return
	}
	if name == "" {
		writeError(w, invalid("a role needs a name"))
		return
	}
	if exists, _ := s.db.NewSelect().Model((*Role)(nil)).Where("key = ?", key).Exists(ctx); exists {
		writeError(w, invalid("a role with key %q already exists", key))
		return
	}
	role := &Role{Key: key, Name: name, Description: strings.TrimSpace(req.Description)}
	if _, err := s.db.NewInsert().Model(role).ExcludeColumn("id", "created_at").Returning("*").Exec(ctx); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, roleResponse(role, nil))
}

// adminUpdateRoleHandler is PATCH /auth/admin/roles/{id} -- rename / re-describe a
// non-system role (the key is immutable).
func (s *service) adminUpdateRoleHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	role, err := s.loadRole(ctx, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, err)
		return
	}
	if role.IsSystem {
		writeError(w, forbidden("a system role can't be edited — clone it to customise"))
		return
	}
	var req roleWriteRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, invalid("a role needs a name"))
		return
	}
	if _, err := s.db.NewUpdate().Model((*Role)(nil)).
		Set("name = ?", name).Set("description = ?", strings.TrimSpace(req.Description)).
		Where("id = ?", role.ID).Exec(ctx); err != nil {
		writeError(w, err)
		return
	}
	perms, _ := s.rolePermissionKeys(ctx, role.ID)
	role.Name, role.Description = name, strings.TrimSpace(req.Description)
	writeJSON(w, http.StatusOK, roleResponse(role, perms))
}

type setRolePermissionsRequest struct {
	Permissions []string `json:"permissions"`
}

// adminSetRolePermissionsHandler is PUT /auth/admin/roles/{id}/permissions -- a
// full replace of a non-system role's grants (validated against the catalog).
func (s *service) adminSetRolePermissionsHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	role, err := s.loadRole(ctx, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, err)
		return
	}
	if role.IsSystem {
		writeError(w, forbidden("a system role's permissions can't be changed — clone it to customise"))
		return
	}
	var req setRolePermissionsRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	seen := make(map[string]struct{}, len(req.Permissions))
	list := make([]string, 0, len(req.Permissions))
	for _, k := range req.Permissions {
		if k = strings.TrimSpace(k); k != "" {
			if _, dup := seen[k]; !dup {
				seen[k] = struct{}{}
				list = append(list, k)
			}
		}
	}
	if len(list) > 0 {
		cnt, cerr := s.db.NewSelect().Model((*Permission)(nil)).Where("key IN (?)", bun.In(list)).Count(ctx)
		if cerr != nil {
			writeError(w, cerr)
			return
		}
		if cnt != len(list) {
			writeError(w, invalid("one or more permissions do not exist"))
			return
		}
	}
	if err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, e := tx.NewDelete().Model((*RolePermission)(nil)).Where("role_id = ?", role.ID).Exec(ctx); e != nil {
			return e
		}
		if len(list) > 0 {
			rows := make([]RolePermission, len(list))
			for i, k := range list {
				rows[i] = RolePermission{RoleID: role.ID, PermissionKey: k}
			}
			if _, e := tx.NewInsert().Model(&rows).Exec(ctx); e != nil {
				return e
			}
		}
		return nil
	}); err != nil {
		writeError(w, err)
		return
	}
	perms, _ := s.rolePermissionKeys(ctx, role.ID)
	writeJSON(w, http.StatusOK, roleResponse(role, perms))
}

// adminCloneRoleHandler is POST /auth/admin/roles/{id}/clone -- copy a role (any,
// including a system one) into a new editable role with the same permissions.
func (s *service) adminCloneRoleHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	src, err := s.loadRole(ctx, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, err)
		return
	}
	var req roleWriteRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	key := strings.TrimSpace(req.Key)
	if !roleKeyPattern.MatchString(key) {
		writeError(w, invalid("role key must be lowercase letters, digits and hyphens (2-49 chars)"))
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = src.Name + " (copy)"
	}
	if exists, _ := s.db.NewSelect().Model((*Role)(nil)).Where("key = ?", key).Exists(ctx); exists {
		writeError(w, invalid("a role with key %q already exists", key))
		return
	}
	srcPerms, err := s.rolePermissionKeys(ctx, src.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	newRole := &Role{Key: key, Name: name, Description: src.Description}
	if err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, e := tx.NewInsert().Model(newRole).ExcludeColumn("id", "created_at").Returning("*").Exec(ctx); e != nil {
			return e
		}
		if len(srcPerms) > 0 {
			rows := make([]RolePermission, len(srcPerms))
			for i, k := range srcPerms {
				rows[i] = RolePermission{RoleID: newRole.ID, PermissionKey: k}
			}
			if _, e := tx.NewInsert().Model(&rows).Exec(ctx); e != nil {
				return e
			}
		}
		return nil
	}); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, roleResponse(newRole, srcPerms))
}

// adminDeleteRoleHandler is DELETE /auth/admin/roles/{id} -- remove a non-system
// role. iam.user_roles + iam.role_permissions cascade, so any assignments drop with it.
func (s *service) adminDeleteRoleHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	role, err := s.loadRole(ctx, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, err)
		return
	}
	if role.IsSystem {
		writeError(w, forbidden("a system role can't be deleted"))
		return
	}
	if _, err := s.db.NewDelete().Model((*Role)(nil)).Where("id = ?", role.ID).Exec(ctx); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}
