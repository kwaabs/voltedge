package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/uptrace/bun"
)

// This file is the out-of-band provisioning surface used by
// api/cmd/authadmin -- the "last resort beneath the floor" from the
// design. It exists so a break-glass admin can be created or reset
// directly against the database, independent of the running API and
// independent of the local-auth switch, and so the very first admin can
// be bootstrapped with no shipped credentials. Password hashing stays
// unexported (password.go); only these deliberate, reviewed entry points
// are public.

// minLocalPasswordLen is the floor for any local password set through the
// provisioning path. Break-glass credentials are held to a higher bar
// than the switch-gated accounts precisely because they bypass the
// switch.
const minLocalPasswordLen = 12

func validatePasswordStrength(pw string) error {
	if len(pw) < minLocalPasswordLen {
		return fmt.Errorf("password must be at least %d characters", minLocalPasswordLen)
	}
	return nil
}

// CreateUser provisions a new active account with a local (password)
// identity, returning the new user id. breakGlass marks it exempt from
// the local-auth switch. Fails if the email already exists.
func CreateUser(ctx context.Context, db *bun.DB, email, displayName, password string, breakGlass bool) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	displayName = strings.TrimSpace(displayName)
	if email == "" || displayName == "" {
		return "", errors.New("email and display name are required")
	}
	if err := validatePasswordStrength(password); err != nil {
		return "", err
	}
	hash, err := hashPassword(password)
	if err != nil {
		return "", err
	}

	var userID string
	err = db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		u := &User{Email: email, DisplayName: displayName, BreakGlass: breakGlass, Status: "active"}
		if _, err := tx.NewInsert().Model(u).Returning("id").Exec(ctx); err != nil {
			return err
		}
		userID = u.ID
		now := time.Now()
		id := &Identity{UserID: u.ID, Provider: "local", PasswordHash: &hash, PasswordChangedAt: &now}
		if _, err := tx.NewInsert().Model(id).Exec(ctx); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return userID, nil
}

// SetLocalPassword sets (or replaces) the local password identity for an
// existing user -- the reset path for a lost break-glass credential.
func SetLocalPassword(ctx context.Context, db *bun.DB, email, password string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	if err := validatePasswordStrength(password); err != nil {
		return err
	}
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}

	var user User
	err = db.NewSelect().Model(&user).Where("email = ?", email).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("no user with email %q", email)
	}
	if err != nil {
		return err
	}

	now := time.Now()
	var existing Identity
	err = db.NewSelect().Model(&existing).
		Where("user_id = ?", user.ID).
		Where("provider = ?", "local").
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		id := &Identity{UserID: user.ID, Provider: "local", PasswordHash: &hash, PasswordChangedAt: &now}
		_, err = db.NewInsert().Model(id).Exec(ctx)
		return err
	}
	if err != nil {
		return err
	}
	_, err = db.NewUpdate().Model((*Identity)(nil)).
		Set("password_hash = ?", hash).
		Set("password_changed_at = ?", now).
		Where("id = ?", existing.ID).
		Exec(ctx)
	return err
}

// AssignRole grants roleKey to the account with the given email
// (idempotent -- a repeat grant is a no-op). This is the out-of-band role
// path used by api/cmd/authadmin: it is how the bootstrap admin gets the
// admin role (no role => no permissions => a locked-out account), and the
// fallback when the Slice D admin UI is not available. grantedBy is the
// acting user id, or "" for a bootstrap/out-of-band grant with no actor.
func AssignRole(ctx context.Context, db *bun.DB, email, roleKey, grantedBy string) error {
	user, role, err := lookupUserAndRole(ctx, db, email, roleKey)
	if err != nil {
		return err
	}
	ur := &UserRole{UserID: user.ID, RoleID: role.ID}
	if grantedBy != "" {
		ur.GrantedBy = &grantedBy
	}
	_, err = db.NewInsert().Model(ur).
		On("CONFLICT (user_id, role_id) DO NOTHING").
		Exec(ctx)
	return err
}

// RemoveRole revokes roleKey from the account (idempotent).
func RemoveRole(ctx context.Context, db *bun.DB, email, roleKey string) error {
	user, role, err := lookupUserAndRole(ctx, db, email, roleKey)
	if err != nil {
		return err
	}
	_, err = db.NewDelete().Model((*UserRole)(nil)).
		Where("user_id = ?", user.ID).
		Where("role_id = ?", role.ID).
		Exec(ctx)
	return err
}

// lookupUserAndRole resolves an email + role key to their rows, with clear
// "no such user/role" errors -- the shared front half of AssignRole/RemoveRole.
func lookupUserAndRole(ctx context.Context, db *bun.DB, email, roleKey string) (*User, *Role, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	var user User
	err := db.NewSelect().Model(&user).Where("email = ?", email).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, fmt.Errorf("no user with email %q", email)
	}
	if err != nil {
		return nil, nil, err
	}
	var role Role
	err = db.NewSelect().Model(&role).Where("key = ?", roleKey).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, fmt.Errorf("no role with key %q", roleKey)
	}
	if err != nil {
		return nil, nil, err
	}
	return &user, &role, nil
}

// RoleSummary is the read shape for CLI role listing.
type RoleSummary struct {
	Key         string
	Name        string
	Description string
}

// ListRoles returns every seeded/defined role, by key, for the CLI.
func ListRoles(ctx context.Context, db *bun.DB) ([]RoleSummary, error) {
	var roles []Role
	if err := db.NewSelect().Model(&roles).Order("key").Scan(ctx); err != nil {
		return nil, err
	}
	out := make([]RoleSummary, len(roles))
	for i, r := range roles {
		out[i] = RoleSummary{Key: r.Key, Name: r.Name, Description: r.Description}
	}
	return out, nil
}

// UserSummary is the read shape for CLI listing -- never any credential
// material.
type UserSummary struct {
	ID          string
	Email       string
	DisplayName string
	Status      string
	BreakGlass  bool
	Roles       []string
}

// ListUsers returns every account, oldest first, for the CLI `list`
// command, each with the keys of the roles it holds.
func ListUsers(ctx context.Context, db *bun.DB) ([]UserSummary, error) {
	var users []User
	if err := db.NewSelect().Model(&users).Order("created_at").Scan(ctx); err != nil {
		return nil, err
	}

	// Roles per user in one join, rather than a query per user.
	var rows []struct {
		UserID  string `bun:"user_id"`
		RoleKey string `bun:"role_key"`
	}
	err := db.NewSelect().
		ColumnExpr("ur.user_id AS user_id").
		ColumnExpr("r.key AS role_key").
		TableExpr("iam.user_roles AS ur").
		Join("JOIN iam.roles AS r ON r.id = ur.role_id").
		OrderExpr("r.key").
		Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	rolesByUser := make(map[string][]string, len(users))
	for _, row := range rows {
		rolesByUser[row.UserID] = append(rolesByUser[row.UserID], row.RoleKey)
	}

	out := make([]UserSummary, len(users))
	for i, u := range users {
		out[i] = UserSummary{
			ID: u.ID, Email: u.Email, DisplayName: u.DisplayName,
			Status: u.Status, BreakGlass: u.BreakGlass, Roles: rolesByUser[u.ID],
		}
	}
	return out, nil
}
