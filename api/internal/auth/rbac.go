package auth

import (
	"net/http"
	"sort"
)

// This file is Slice C: RBAC enforcement. The permission *catalog* is data
// (iam.permissions, seeded by migration 0025); these constants are the Go
// side of that contract -- the exact keys the route gates in
// api/cmd/server/main.go require. Keeping them here, exported and in one
// place, means a gate and its permission can never drift apart into a
// silent typo, and a caller outside this package (main) references a
// symbol, not a string literal.
//
// A user's effective permission set is loaded with their session
// (session.go) and carried on the request context (SessionUser.Permissions);
// RequirePermission reads it. Authorization never learns how the user
// authenticated -- local, Azure AD, or Google all arrive here identically.
const (
	PermFeaturesRead        = "features:read"
	PermFeaturesWrite       = "features:write"
	PermFeaturesWipe        = "features:wipe"
	PermConnectivityRead    = "connectivity:read"
	PermConnectivityCommit  = "connectivity:commit"
	PermRelationshipsRead   = "relationships:read"
	PermRelationshipsCommit = "relationships:commit"
	PermDiagramsRead        = "diagrams:read"
	PermDiagramsWrite       = "diagrams:write"
	PermDiagramsDelete      = "diagrams:delete"
	PermAnnotationsRead     = "annotations:read"
	PermAnnotationsWrite    = "annotations:write"
	PermTraceRun            = "trace:run"
	PermAnalysisRun         = "analysis:run"
	PermMetadataRead        = "metadata:read"
	PermMetadataWrite       = "metadata:write"
	PermAuthSettings        = "admin:auth-settings"
	PermUsersManage         = "admin:users"
	PermTerritoriesWrite    = "territories:write"
	PermCircuitsWrite       = "circuits:write"
	PermFieldPacksAdmin     = "admin:field-packs"
	PermAttachmentsRead     = "attachments:read"
	PermAttachmentsWrite    = "attachments:write"
	PermSyncReview          = "sync:review"
	PermSyncPost            = "sync:post"
	PermOMSRead             = "oms:read"
	PermOMSWrite            = "oms:write"
	PermWAMRead             = "wam:read"
	PermWAMWrite            = "wam:write"
	PermFaultsRead          = "faults:read"
	PermFaultsWrite         = "faults:write"
	PermTagsRead            = "tags:read"
	PermTagsWrite           = "tags:write"
	PermOperationsRead      = "operations:read"
	PermOperationsWrite     = "operations:write"
	PermSafetyRead          = "safety:read"
	PermSafetyWrite         = "safety:write"
)

// RequirePermission gates a route (sub)tree by RBAC permission. A request
// with no authenticated session is refused 401; a session whose effective
// permission set lacks the required permission is refused 403.
//
// The required permission is chosen by HTTP method: safe methods
// (GET/HEAD/OPTIONS) need readPerm, mutating methods need writePerm -- so a
// single gate covers a mixed read/write mount without the sub-package having
// to know anything about auth. For a subtree that is uniformly one
// capability -- a pure query engine or a pure action endpoint (e.g. trace,
// analysis) -- pass the same permission for both.
func RequirePermission(readPerm, writePerm string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := UserFrom(r.Context())
			if !ok {
				writeError(w, unauthorized("authentication required"))
				return
			}

			required := writePerm
			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions:
				required = readPerm
			}

			if !user.Has(required) {
				writeError(w, forbidden("you do not have the required permission ("+required+")"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// sortedPermissions renders a permission set as a stable, sorted slice for
// the wire -- the SPA (Slice D) reads it to show or hide controls it knows
// the user cannot use, so the UI matches what the server would enforce.
func sortedPermissions(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
