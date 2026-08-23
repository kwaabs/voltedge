package assistant

// The assistant's tool catalog (Phase 19 Slice 2). This is the assistant's half
// of the shared-registry seam: where the MCP server (internal/mcpserver)
// advertises the internal/agenttools set over JSON-RPC tools/list to an external
// agent, the assistant advertises the *same* set over a session-authed GET to
// its own frontend -- the two front doors provably reading one contract. Slice 3
// (the chat orchestrator) hands this catalog to the small local model so it can
// pick a tool; the frontend can also read it to know what the assistant can do.
//
// Unlike MCP's static tools/list, this catalog is scoped to the caller: it
// returns only the tools whose RBAC permission the session user actually holds
// (plus any that need none), so the assistant never advertises an action the
// user couldn't execute -- the same "bounded by exactly their RBAC" posture the
// suggestions endpoint takes.

import (
	"net/http"

	"github.com/kwaabs/voltedge/api/internal/agenttools"
	"github.com/kwaabs/voltedge/api/internal/auth"
)

// toolsResponse is the body of GET /tools: the tools this user may invoke, in
// the shared registry's advertised order, each carrying the same
// name/description/input-schema/annotations the MCP tools/list emits.
type toolsResponse struct {
	Tools []agenttools.Tool `json:"tools"`
	Count int               `json:"count"`
}

// toolsHandler serves the session-scoped tool catalog. It filters the shared
// registry to what the session user is permitted to run; a tool with no required
// permission is always included.
func toolsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			// Belt-and-suspenders: the /assistant mount is behind RequireAuth.
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}

		tools := scopedTools(user)
		writeJSON(w, http.StatusOK, toolsResponse{Tools: tools, Count: len(tools)})
	}
}

// scopedTools filters the shared registry to the tools this session user may
// invoke -- those whose RequiredPerm the user holds, plus any that need none --
// preserving the registry's advertised order. Both the tool catalog (GET /tools)
// and the chat orchestrator (which hands the model only tools the user could
// actually run) build on this, so the assistant never advertises or routes to an
// action outside the user's RBAC.
func scopedTools(user *auth.SessionUser) []agenttools.Tool {
	all := agenttools.All()
	tools := make([]agenttools.Tool, 0, len(all))
	for _, t := range all {
		if t.RequiredPerm == "" || user.Has(t.RequiredPerm) {
			tools = append(tools, t)
		}
	}
	return tools
}
