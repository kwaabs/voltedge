// Package auth is VoltEdge's trust boundary: identities, opaque
// server-side sessions, the local-auth break-glass switch, and the
// security audit log (see docs/phase-10-auth.md). Slice A -- this
// package's first cut -- covers local (password) login/logout, the
// session + CSRF + security-header middleware, and the break-glass
// semantics. SSO (Azure AD / Google, Slice B) and RBAC enforcement
// (Slice C) build on this same foundation without changing its shape:
// every login path, however it authenticates, terminates in the one
// uniform session this package issues.
//
// Enforcement lives entirely here in the Go layer -- the API is the only
// trusted database gatekeeper (it connects as the postgres superuser over
// one shared DATABASE_URL), so Postgres RLS is not a lever this design
// uses.
package auth

import (
	"time"

	"github.com/uptrace/bun"
)

// User mirrors iam.users -- see migrations/0024. Password lives on
// Identity, not here, so a person can hold a local password and a linked
// SSO identity at once. (The physical schema is iam, not auth, because
// the supabase/postgres image reserves the auth schema for GoTrue.)
type User struct {
	bun.BaseModel `bun:"table:iam.users"`

	ID               string     `bun:"id,pk,nullzero"`
	Email            string     `bun:"email"`
	DisplayName      string     `bun:"display_name"`
	Status           string     `bun:"status"`
	BreakGlass       bool       `bun:"break_glass"`
	FailedLoginCount int        `bun:"failed_login_count"`
	LockedUntil      *time.Time `bun:"locked_until"`
	// ManagerID is this user's manager in the org tree (Phase 58,
	// migration 0094) -- the edge spatial access cascades up. NULL at the
	// top of the org.
	ManagerID *string   `bun:"manager_id"`
	CreatedAt time.Time `bun:"created_at,nullzero"`
	UpdatedAt time.Time `bun:"updated_at,nullzero"`
}

// Identity mirrors iam.user_identities -- one way a user can prove
// identity. Provider is one of "local", "azuread", "google". PasswordHash
// (Argon2id, PHC-encoded) is set only on local rows; ProviderSubject only
// on SSO rows.
type Identity struct {
	bun.BaseModel `bun:"table:iam.user_identities"`

	ID                string     `bun:"id,pk,nullzero"`
	UserID            string     `bun:"user_id"`
	Provider          string     `bun:"provider"`
	ProviderSubject   *string    `bun:"provider_subject"`
	PasswordHash      *string    `bun:"password_hash"`
	PasswordChangedAt *time.Time `bun:"password_changed_at"`
	CreatedAt         time.Time  `bun:"created_at,nullzero"`
}

// Session mirrors iam.sessions -- an opaque, revocable, server-side
// session. TokenSHA256 is the SHA-256 of the random cookie token (the
// token itself is never persisted).
type Session struct {
	bun.BaseModel `bun:"table:iam.sessions"`

	ID                string     `bun:"id,pk,nullzero"`
	UserID            string     `bun:"user_id"`
	TokenSHA256       []byte     `bun:"token_sha256"`
	CSRFToken         string     `bun:"csrf_token"`
	CreatedAt         time.Time  `bun:"created_at,nullzero"`
	LastUsedAt        time.Time  `bun:"last_used_at,nullzero"`
	IdleExpiresAt     time.Time  `bun:"idle_expires_at"`
	AbsoluteExpiresAt time.Time  `bun:"absolute_expires_at"`
	RevokedAt         *time.Time `bun:"revoked_at"`
	IP                *string    `bun:"ip"`
	UserAgent         *string    `bun:"user_agent"`
}

// Permission mirrors iam.permissions -- one entry in the RBAC catalog
// (Slice C). Key is the stable "resource:action" string the route gates
// compare against; it is the natural primary key.
type Permission struct {
	bun.BaseModel `bun:"table:iam.permissions"`

	Key         string    `bun:"key,pk"`
	Description string    `bun:"description"`
	CreatedAt   time.Time `bun:"created_at,nullzero"`
}

// Role mirrors iam.roles -- a named bundle of permissions.
type Role struct {
	bun.BaseModel `bun:"table:iam.roles"`

	ID          string    `bun:"id,pk,nullzero"`
	Key         string    `bun:"key"`
	Name        string    `bun:"name"`
	Description string    `bun:"description"`
	IsSystem    bool      `bun:"is_system"`
	CreatedAt   time.Time `bun:"created_at,nullzero"`
}

// RolePermission mirrors iam.role_permissions -- the role<->permission
// grant. Composite PK (role_id, permission_key), so a re-grant is a no-op.
type RolePermission struct {
	bun.BaseModel `bun:"table:iam.role_permissions"`

	RoleID        string `bun:"role_id,pk"`
	PermissionKey string `bun:"permission_key,pk"`
}

// UserRole mirrors iam.user_roles -- the user<->role assignment. A user's
// effective permission set is the union of permissions across these roles.
type UserRole struct {
	bun.BaseModel `bun:"table:iam.user_roles"`

	UserID    string    `bun:"user_id,pk"`
	RoleID    string    `bun:"role_id,pk"`
	GrantedAt time.Time `bun:"granted_at,nullzero"`
	GrantedBy *string   `bun:"granted_by"`
}

// SystemSetting mirrors iam.system_settings -- a single global toggle.
type SystemSetting struct {
	bun.BaseModel `bun:"table:iam.system_settings"`

	Key       string    `bun:"key,pk"`
	Value     string    `bun:"value"`
	UpdatedAt time.Time `bun:"updated_at,nullzero"`
	UpdatedBy *string   `bun:"updated_by"`
}

// AuthEvent mirrors iam.auth_events -- one row of the append-only
// security audit log.
type AuthEvent struct {
	bun.BaseModel `bun:"table:iam.auth_events"`

	ID        string    `bun:"id,pk,nullzero"`
	UserID    *string   `bun:"user_id"`
	EventType string    `bun:"event_type"`
	Detail    *string   `bun:"detail"`
	IP        *string   `bun:"ip"`
	UserAgent *string   `bun:"user_agent"`
	CreatedAt time.Time `bun:"created_at,nullzero"`
}

// The key iam.system_settings holds the break-glass switch under.
const settingLocalAuthEnabled = "local_auth_enabled"

// Event-type constants for iam.auth_events -- one place so the audit log
// stays queryable rather than accreting free-form strings.
const (
	eventLoginSuccess     = "login_success"
	eventLoginFailure     = "login_failure"
	eventLoginDenied      = "login_denied" // correct password, but local auth off and not break-glass
	eventBreakGlassLogin  = "break_glass_login"
	eventLogout           = "logout"
	eventLocalAuthToggled = "local_auth_toggled"
	eventSSOLinked        = "sso_linked"   // first SSO login linked a provider identity to a pre-provisioned user
	eventRoleChanged      = "role_changed" // a role was granted to or revoked from a user (Slice C)
)
