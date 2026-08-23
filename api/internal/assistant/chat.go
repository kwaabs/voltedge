package assistant

// The chat orchestrator (Phase 19 Slice 3, docs/phase-19-assistant.md): the
// Tier-1 half of the assistant, where a small local model turns a freeform
// request into tool calls the deterministic engines carry out. It is the same
// second-front-door posture as the rest of this package -- in-process, behind
// the cookie session, every call bounded by the logged-in user's RBAC -- with a
// language model added only as a cheap router: parse intent, pick a tool, fill
// its args. All domain reasoning stays in the agenttools handlers the model
// routes to.
//
// One POST /chat runs a short tool-routing loop over the model (llm.Provider):
//
//   - The model is offered only the tools this user is permitted to run
//     (scopedTools), so it can never propose an action outside their RBAC.
//   - A read tool call executes inline via agenttools.Execute and its result is
//     fed back so the model can answer or chain another read (bounded by
//     maxToolRounds, so a confused model can't loop forever).
//   - A write tool call is NOT executed. It comes back as a proposed action the
//     frontend surfaces for confirmation -- the same "human click is the commit"
//     contract as the Slice 1 suggestion chips; the confirmed write then runs
//     through POST /execute (execute.go), which re-enforces the write permission.
//
// The model is never a security boundary here: Execute's RequiredPerm check is,
// exactly as for the MCP front door. The model only decides which of the tools
// the user already holds to call.

import (
	"encoding/json"
	"net/http"

	"github.com/kwaabs/voltedge/api/internal/agenttools"
	"github.com/kwaabs/voltedge/api/internal/assistant/llm"
	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/uptrace/bun"
)

// maxToolRounds bounds how many times one /chat call will let the model call
// read tools and see their results before it must produce a spoken answer. It's
// a safety rail against a model that keeps calling tools without converging, not
// a target -- most requests resolve in one or two rounds. A write short-circuits
// the loop immediately (writes never auto-run), so this only ever caps reads.
const maxToolRounds = 4

// toolResultBudget caps the bytes of one tool result fed back to the model. A
// broad search can return far more rows than a small local model's context
// window holds; we trim (with an explicit marker, never silently) so a large
// read can't wedge the loop.
const toolResultBudget = 6 << 10 // 6 KiB

// chatRequest is the body of POST /chat: the running conversation. The frontend
// owns the transcript and resends it each turn (the backend keeps no chat state
// between calls). Only user and assistant text turns are carried here; the
// tool-call/tool-result messages of a routing loop are internal to one call.
type chatRequest struct {
	Messages []chatMessage `json:"messages"`
}

// chatMessage is one visible turn: role "user" or "assistant", plain text.
type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// proposedWrite is a write tool call the model wants to make, returned for the
// human to confirm rather than executed. Tool + Args are what POST /execute will
// run verbatim on confirm; Title/Detail are for the confirmation card. It mirrors
// the Slice 1 suggestion action's shape (tool + prebuilt args + label) so the
// frontend can reuse the same confirm-then-commit affordance.
type proposedWrite struct {
	Tool   string          `json:"tool"`
	Title  string          `json:"title"`
	Detail string          `json:"detail,omitempty"`
	Args   json.RawMessage `json:"args"`
}

// toolRunInfo records a read tool the orchestrator executed inline this turn, so
// the frontend can show what the assistant looked at (transparency), without
// dumping the raw results into the chat.
type toolRunInfo struct {
	Tool string `json:"tool"`
	OK   bool   `json:"ok"`
}

// chatResponse is the body of POST /chat: the assistant's spoken reply, any
// writes awaiting the user's confirmation, and the reads it ran to get here.
type chatResponse struct {
	Reply    string          `json:"reply"`
	Actions  []proposedWrite `json:"actions,omitempty"`
	ToolRuns []toolRunInfo   `json:"tool_runs,omitempty"`
}

// systemPrompt frames the model as VoltEdge's assistant and pins the behaviour
// the orchestrator depends on: prefer tools over guessing, never invent ids,
// keep answers short. It deliberately does not describe each tool -- the tool
// specs (name + description + JSON schema) carry that -- so it stays stable as
// the registry grows.
const systemPrompt = `You are the VoltEdge assistant, embedded in a utility GIS application.
Help the signed-in user inspect and edit their electrical network by calling the provided tools.

Rules:
- Prefer calling a tool over guessing. Never invent feature ids, class codes, or terminal names; look them up with a search or describe tool first.
- Use read tools freely to answer questions. Their results are returned to you before you reply.
- To create, update, connect, or relate features, call the matching write tool. Write tools are not executed automatically -- the user confirms them -- so state clearly what you are proposing.
- Keep replies short and specific. If a request is ambiguous or you lack a permitted tool for it, say so plainly.`

func chatHandler(db *bun.DB, provider llm.Provider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		if provider == nil {
			// Chat is optional: without a configured model the deterministic
			// suggestions still work, but freeform chat cannot. Say so clearly
			// (503) so the frontend can hide or disable the input.
			writeError(w, http.StatusServiceUnavailable, "assistant chat is not configured")
			return
		}

		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "request body is not valid JSON")
			return
		}
		if len(req.Messages) == 0 {
			writeError(w, http.StatusBadRequest, "messages must not be empty")
			return
		}

		catalog := scopedTools(user)
		specs := toToolSpecs(catalog)

		// Seed the model conversation: the system frame, then the visible
		// transcript. Anything the model returns (tool calls, tool results) is
		// appended below as the routing loop runs.
		msgs := make([]llm.Message, 0, len(req.Messages)+1)
		msgs = append(msgs, llm.Message{Role: llm.RoleSystem, Content: systemPrompt})
		for _, m := range req.Messages {
			role := llm.RoleUser
			if m.Role == "assistant" {
				role = llm.RoleAssistant
			}
			msgs = append(msgs, llm.Message{Role: role, Content: m.Content})
		}

		var proposals []proposedWrite
		var runs []toolRunInfo
		reply := ""

		for round := 0; round < maxToolRounds; round++ {
			completion, err := provider.Complete(r.Context(), llm.Request{Messages: msgs, Tools: specs})
			if err != nil {
				// The model backend failed (down, timed out, bad response). A
				// 502 marks it as an upstream fault, not the client's.
				writeError(w, http.StatusBadGateway, "assistant model error: "+err.Error())
				return
			}

			if len(completion.ToolCalls) == 0 {
				reply = completion.Text
				break
			}

			// Record the assistant turn that asked for tools, so the following
			// tool-result messages have a call to answer.
			msgs = append(msgs, llm.Message{
				Role:      llm.RoleAssistant,
				Content:   completion.Text,
				ToolCalls: completion.ToolCalls,
			})

			// Any write in this batch short-circuits: propose every write for
			// confirmation and stop, without executing anything. (Reads in the
			// same batch are dropped -- the model can re-issue them next turn if
			// it still needs them after the writes are confirmed.)
			if writes := writeCalls(completion.ToolCalls); len(writes) > 0 {
				for _, tc := range writes {
					proposals = append(proposals, proposeWrite(tc))
				}
				reply = completion.Text
				break
			}

			// All reads: execute each inline under the user's RBAC and feed the
			// result back for the next turn.
			for _, tc := range completion.ToolCalls {
				result, execErr := agenttools.Execute(r.Context(), db, user, tc.Name, argsOrEmpty(tc.Args))
				runs = append(runs, toolRunInfo{Tool: tc.Name, OK: execErr == nil})
				msgs = append(msgs, llm.Message{
					Role:       llm.RoleTool,
					ToolCallID: tc.ID,
					Content:    toolResultContent(result, execErr),
				})
			}
		}

		// If the loop hit its round cap still mid-tool-use, ask once more with no
		// tools offered so the model has to convert what it has read into an
		// answer instead of the user getting nothing.
		if reply == "" && len(proposals) == 0 {
			if completion, err := provider.Complete(r.Context(), llm.Request{Messages: msgs}); err == nil {
				reply = completion.Text
			}
		}
		if reply == "" {
			if len(proposals) > 0 {
				reply = "I've prepared an action for you to review and confirm."
			} else {
				reply = "I wasn't able to complete that."
			}
		}

		writeJSON(w, http.StatusOK, chatResponse{Reply: reply, Actions: proposals, ToolRuns: runs})
	}
}

// writeCalls returns the subset of tool calls that mutate state (write tools),
// looked up against the shared registry. An unknown tool name is ignored here --
// it is neither a known read nor write; the read path will surface it as an
// unknown-tool error to the model if the model insists on it.
func writeCalls(calls []llm.ToolCall) []llm.ToolCall {
	var out []llm.ToolCall
	for _, tc := range calls {
		if t, ok := agenttools.Get(tc.Name); ok && t.Writes() {
			out = append(out, tc)
		}
	}
	return out
}

// proposeWrite turns a write tool call into a confirmation proposal: the tool's
// display title (its annotation Title, else the bare name) for the card heading,
// a compact one-line of the arguments as detail, and the exact tool+args the
// confirm step will run unchanged.
func proposeWrite(tc llm.ToolCall) proposedWrite {
	title := tc.Name
	if t, ok := agenttools.Get(tc.Name); ok && t.Annotations != nil && t.Annotations.Title != "" {
		title = t.Annotations.Title
	}
	return proposedWrite{
		Tool:   tc.Name,
		Title:  title,
		Detail: truncateForModel(string(argsOrEmpty(tc.Args)), 200),
		Args:   argsOrEmpty(tc.Args),
	}
}

// toToolSpecs maps the session-scoped registry tools to the model-facing spec
// (name, description, argument schema) the provider advertises.
func toToolSpecs(tools []agenttools.Tool) []llm.ToolSpec {
	specs := make([]llm.ToolSpec, 0, len(tools))
	for _, t := range tools {
		specs = append(specs, llm.ToolSpec{
			Name:        t.Name,
			Description: t.Description,
			Schema:      t.InputSchema,
		})
	}
	return specs
}

// toolResultContent renders a read tool's outcome as the text fed back to the
// model. An error (unknown ref, permission, validation) is passed through as a
// plain "error: ..." line so the model can adjust or apologise rather than being
// left blind; a success is the JSON result, trimmed to the model's byte budget.
func toolResultContent(result any, execErr error) string {
	if execErr != nil {
		return "error: " + execErr.Error()
	}
	buf, err := json.Marshal(result)
	if err != nil {
		return "error: could not encode tool result"
	}
	return truncateForModel(string(buf), toolResultBudget)
}

// argsOrEmpty normalises absent tool arguments to an empty JSON object so a
// handler's json.Unmarshal never chokes on a nil/empty body.
func argsOrEmpty(args json.RawMessage) json.RawMessage {
	if len(args) == 0 {
		return json.RawMessage("{}")
	}
	return args
}

// truncateForModel trims a string to a byte budget, appending an explicit marker
// so a cut is always visible (in a fed-back tool result or a proposal's detail
// line), never silent.
func truncateForModel(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	return s[:maxBytes] + " ...[truncated]"
}
