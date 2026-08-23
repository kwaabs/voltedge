package assistant

// The chat commit path (Phase 19 Slice 3, docs/phase-19-assistant.md). Where
// chat.go proposes a write and refuses to run it, POST /execute is where the
// user's confirmation click actually commits: it runs one named tool with the
// exact arguments the proposal carried, through the same agenttools.Execute
// choke point every front door uses -- so the write is re-checked against the
// session user's RBAC at commit time, attributed to them, and no more powerful
// than the REST route the tool wraps. Keeping the confirm step a single generic
// endpoint (rather than mapping each write tool to its own REST route on the
// frontend) means any write the chat can propose -- create_feature,
// update_feature, establish_connection, establish_relationship -- commits the
// same way.
//
// POST /status is the tiny capability probe the frontend reads to decide whether
// to show the chat input at all: chat needs a configured model, the rest of the
// assistant does not.

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/kwaabs/voltedge/api/internal/agenttools"
	"github.com/kwaabs/voltedge/api/internal/assistant/llm"
	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/uptrace/bun"
)

// executeRequest is the body of POST /execute: the tool to run and its arguments,
// echoed verbatim from a chat proposal (or, in principle, any tool the user is
// permitted to call). Args is raw JSON passed straight through to the handler.
type executeRequest struct {
	Tool string          `json:"tool"`
	Args json.RawMessage `json:"args"`
}

// executeResponse returns the committed tool's structured result, so the
// frontend can confirm what happened (the new feature id, the created link, etc).
type executeResponse struct {
	Tool   string `json:"tool"`
	Result any    `json:"result"`
}

// executeHandler commits one confirmed tool call. It runs through
// agenttools.Execute, which enforces the tool's RequiredPerm against the session
// user and threads them onto the context so the write is attributed correctly.
// The typed errors Execute returns map to distinct statuses: an unknown tool is
// a 404, a missing permission a 403, and any engine-level failure (bad ref,
// validation, rule rejection) a 400 with the engine's own message.
func executeHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}

		var req executeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "request body is not valid JSON")
			return
		}
		if req.Tool == "" {
			writeError(w, http.StatusBadRequest, "tool is required")
			return
		}

		result, err := agenttools.Execute(r.Context(), db, user, req.Tool, argsOrEmpty(req.Args))
		if err != nil {
			var permErr *agenttools.PermissionError
			switch {
			case errors.Is(err, agenttools.ErrUnknownTool):
				writeError(w, http.StatusNotFound, "unknown tool: "+req.Tool)
			case errors.As(err, &permErr):
				writeError(w, http.StatusForbidden, "permission denied: you lack "+permErr.Perm)
			default:
				writeError(w, http.StatusBadRequest, err.Error())
			}
			return
		}

		writeJSON(w, http.StatusOK, executeResponse{Tool: req.Tool, Result: result})
	}
}

// statusResponse tells the frontend whether the chat surface is usable and, when
// it is, which model answers -- handy in a status line and for debugging which
// backend a deployment wired up.
type statusResponse struct {
	ChatEnabled bool   `json:"chat_enabled"`
	Model       string `json:"model,omitempty"`
}

// statusHandler reports assistant chat availability: enabled exactly when a model
// provider is configured. The suggestions and tool-catalog endpoints work
// regardless, so a deployment without a model still has a working assistant --
// only freeform chat is absent.
func statusHandler(provider llm.Provider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp := statusResponse{ChatEnabled: provider != nil}
		if provider != nil {
			resp.Model = provider.Name()
		}
		writeJSON(w, http.StatusOK, resp)
	}
}
