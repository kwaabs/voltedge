package auth

import "context"

// contextKey is unexported so nothing outside this package can collide
// with or forge the authenticated-user slot on a request context.
type contextKey int

const userContextKey contextKey = iota

// SessionUser is the authenticated identity carried on a request's
// context once the session middleware has validated the cookie. It is a
// small, read-only snapshot -- enough for handlers and (Slice C) the RBAC
// middleware to decide, without re-querying. It deliberately carries no
// password or token material.
type SessionUser struct {
	ID          string
	Email       string
	DisplayName string
	BreakGlass  bool

	// Permissions is the user's effective permission set -- the union of
	// every permission granted by every role the user holds, loaded with
	// the session (Slice C). RequirePermission reads it to gate a route;
	// handlers can read it via Has to shape a response. Nil/empty means the
	// user holds no roles (e.g. a freshly bootstrapped account before any
	// role is assigned), which authorizes nothing.
	Permissions map[string]struct{}

	// AllowedTerritories is the set of territory ids this request is scoped
	// to (Phase 58): the union of every territory granted to the user or to
	// anyone who reports up to them, expanded to whole subtrees
	// (access.EffectiveIDs). Enforcement sites build a
	// `territory_id = ANY(...)` predicate from it. Nil/empty means "scoped to
	// nothing" (fail closed) -- NOT "no filter" -- UNLESS Unscoped is set.
	// Left nil when Unscoped, since no predicate is applied then.
	AllowedTerritories []string

	// Unscoped is true when the user holds access.PermUnscoped: territory
	// scoping is bypassed entirely (every feature is visible/editable), so
	// enforcement sites skip the predicate. Admin/break-glass hold it.
	Unscoped bool

	// Subordinates is the set of user ids who report up to this user
	// (transitively, down the manager tree), NOT including this user's own id
	// (Private-drafts, Phase 61). The draft-visibility predicate lets a viewer
	// see a private (never-published) feature when its created_by is the viewer
	// or one of these ids. Left nil for an Unscoped user (they bypass the draft
	// predicate entirely, seeing every draft) and for a session-less internal
	// caller (trusted, unscoped).
	Subordinates []string

	// SessionID and CSRFToken back logout (revoke this session) and the
	// double-submit CSRF check respectively.
	SessionID string
	CSRFToken string
}

// Has reports whether the user's effective permission set includes perm.
func (u *SessionUser) Has(perm string) bool {
	_, ok := u.Permissions[perm]
	return ok
}

// TerritoryFilter returns the territory ids this request is scoped to and
// whether the user bypasses scoping (Phase 58). An enforcement site applies
// `territory_id = ANY(ids)` when unscoped is false, and no predicate at all
// when it is true. A false unscoped with an empty ids means "scoped to
// nothing" (fail closed). The caller is still responsible for checking whether
// enforcement is switched on at all (territory.EnforcementEnabled); when it is
// off, no predicate should be applied regardless of what this returns.
func (u *SessionUser) TerritoryFilter() (ids []string, unscoped bool) {
	return u.AllowedTerritories, u.Unscoped
}

// withUser returns a child context carrying u as the authenticated user.
func withUser(ctx context.Context, u *SessionUser) context.Context {
	return context.WithValue(ctx, userContextKey, u)
}

// WithUser is the exported form of withUser: it attaches an already-resolved
// SessionUser to ctx so a non-HTTP-middleware caller can establish the same
// authenticated context the session middleware does. The MCP server
// (internal/mcpserver) uses it after resolving a bearer token, so the engine
// downstream reads the acting user via UserFrom/ActorID exactly as it would
// behind a cookie session.
func WithUser(ctx context.Context, u *SessionUser) context.Context {
	return withUser(ctx, u)
}

// UserFrom returns the authenticated user on ctx, if the session
// middleware put one there. The boolean is false for anonymous requests.
// This is the one supported way for other packages (handlers, the CRUD
// engine stamping an actor in Slice E) to read who is acting.
func UserFrom(ctx context.Context) (*SessionUser, bool) {
	u, ok := ctx.Value(userContextKey).(*SessionUser)
	return u, ok
}

// ActorID returns the authenticated user's id for stamping the audit
// columns (Slice E), or "" when ctx carries no session -- a system/CLI
// code path (e.g. the compile CLI, a background job) rather than an HTTP
// request behind the session middleware. Callers store the id verbatim in
// the *_by columns (a uuid, held as text in the pre-auth placeholder
// columns and natively in the feature tables' created_by/updated_by), and
// treat "" as "no actor -> write NULL / keep the system literal".
func ActorID(ctx context.Context) string {
	if u, ok := UserFrom(ctx); ok {
		return u.ID
	}
	return ""
}
