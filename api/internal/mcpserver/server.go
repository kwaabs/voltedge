// Package mcpserver is VoltEdge's Model Context Protocol server (Phase 18,
// docs/phase-18-mcp.md) -- the data-plane surface an AI agent uses to
// operate on VoltEdge data, never on the metadata that governs the system.
// It sits beside the REST API (its own cmd/mcpserver process) and reaches
// the exact same generic, metadata-validated engine (internal/crud,
// internal/metadata) the REST handlers do; there is deliberately no path
// from here into schema authoring or the metadata compiler.
//
// The tool set is read-only so far (validate_feature writes nothing -- it only
// previews). Slice 1: the four feature tools (list_feature_classes,
// describe_feature_class, search_features, get_feature), gated by features:read.
// Slice 2: the two trace tools (list_trace_configurations, run_trace), gated by
// trace:run. Slice 3: validate_feature, a dry-run of create-time validation,
// gated by features:write (its REST equivalent is the create route). Slice 4:
// suggest_connections / suggest_relationships, ranked candidate proposals gated
// by connectivity:read / relationships:read (they read those domains' rules and
// existing links; they propose, never write). Slice 5: the three composite
// trace tools (run_isolation_trace, run_feeder_trace, run_section_trace), gated
// by trace:run like the /trace mount -- each analyzes the committed topology
// (what would de-energize, the whole feeder, the section between two devices)
// without writing. Slice 6 opens the write surface: create_feature (gated
// features:write, the same as the REST create route), backed by the shared
// crud.CreateFeature. Write safety is client-side per the MCP annotation model
// (docs/phase-18-mcp.md): read tools advertise readOnlyHint:true and the write
// tool readOnlyHint:false, so the human's MCP client surfaces a write for
// approval before calling it -- the server itself stays stateless and does not
// implement a second confirmation round-trip. Slice 7 adds the two establish
// write tools that close the suggest->establish loop: establish_connection
// (gated connectivity:commit) and establish_relationship (gated
// relationships:commit), backed by connectivity.EstablishConnection and
// relationship.EstablishRelationship; both advertise readOnlyHint:false but
// idempotentHint:true, since re-establishing an existing edge is a no-op. Slice 8
// adds update_feature (gated features:write, backed by the shared
// crud.UpdateFeature extracted from updateHandler), completing the feature CRUD
// write surface -- also idempotentHint:true, since re-applying the same partial
// change yields the same state. Each tool is gated by the same RBAC permission
// its equivalent REST route requires.
//
// Transport is a hand-rolled JSON-RPC 2.0 over the MCP streamable-HTTP
// binding -- no third-party MCP SDK, matching this codebase's "minimal,
// audited dependency surface" posture (see the Phase 10 auth write-up).
// Every request authenticates via an Authorization: Bearer <token> header
// carrying an iam.api_tokens credential (auth.ResolveAPIToken), which
// resolves to a real user and their permission set -- so an agent can never
// do more than the user whose token it holds.
package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/kwaabs/voltedge/api/internal/agenttools"
	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/logx"
	"github.com/uptrace/bun"
)

// serverName/serverVersion identify this server to a client in the
// initialize handshake.
const (
	serverName    = "voltedge-mcp"
	serverVersion = "0.1.0"
)

// defaultProtocolVersion is the MCP revision we implement against. When a
// client announces its own protocolVersion on initialize we echo that back
// (this server implements a stable subset that is compatible across recent
// revisions); absent one, we advertise this.
const defaultProtocolVersion = "2025-06-18"

// JSON-RPC 2.0 error codes (the standard set); toolExecutionError is not a
// JSON-RPC code -- a failed-but-well-formed tool call is reported inside the
// result with isError=true so the agent can read what went wrong, per MCP.
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
)

// rpcRequest is an incoming JSON-RPC 2.0 message. A present ID marks a
// request (expects a response); an absent ID marks a notification (no
// response), e.g. notifications/initialized after the handshake.
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

func (r *rpcRequest) isNotification() bool { return len(r.ID) == 0 }

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// --- MCP payload shapes (the subset this server speaks) ---

type initializeParams struct {
	ProtocolVersion string `json:"protocolVersion"`
}

type initializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    serverCapabilities `json:"capabilities"`
	ServerInfo      implementation     `json:"serverInfo"`
	Instructions    string             `json:"instructions,omitempty"`
}

type serverCapabilities struct {
	Tools *toolsCapability `json:"tools,omitempty"`
}

type toolsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

type implementation struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// The tool wire shape (toolDef) and its MCP behavioral hints (toolAnnotations)
// now live on agenttools.Tool / agenttools.Annotations, whose JSON tags match
// the MCP tool object exactly -- so tools/list serializes an agenttools.Tool
// verbatim and both front doors advertise identical hints. See
// docs/phase-18-mcp.md for the write-safety model those hints express (read
// tools readOnlyHint:true, write tools readOnlyHint:false); they are hints, not
// a boundary -- agenttools.Execute's RequiredPerm check is the enforcement.
type toolsListResult struct {
	Tools []agenttools.Tool `json:"tools"`
}

type callToolParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type callToolResult struct {
	Content []contentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// Server holds the database handle it runs tool calls against. The tool
// registry itself lives in internal/agenttools (shared with the in-app
// assistant); the Server just wraps it in JSON-RPC. It carries no per-request
// state, so a single instance serves every connection concurrently.
type Server struct {
	db *bun.DB
}

// NewServer builds a Server over db. The tool set is the shared
// agenttools registry -- no per-server registration.
func NewServer(db *bun.DB) *Server {
	return &Server{db: db}
}

// Handler is the http.Handler serving the MCP streamable-HTTP endpoint.
// Mount it at a single path (cmd/mcpserver mounts it at /mcp). It answers
// POST with JSON-RPC; server-initiated streaming (GET SSE) is not needed by
// this request/response tool server, so GET/DELETE are refused.
func Handler(db *bun.DB) http.Handler {
	return NewServer(db)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		// No server-initiated SSE stream and no session lifecycle to manage
		// in this slice -- only the POST request/response path is supported.
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Authenticate the bearer token before reading any further. Every MCP
	// POST -- including the initialize handshake -- must carry a live
	// iam.api_tokens credential; an anonymous caller cannot even initialize.
	user, err := s.authenticate(r)
	if err != nil {
		logx.From(r.Context()).Error("token resolution error", "component", "mcp", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if user == nil {
		w.Header().Set("WWW-Authenticate", `Bearer realm="voltedge-mcp"`)
		http.Error(w, "missing or invalid bearer token", http.StatusUnauthorized)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeRPCError(w, nil, codeParseError, "could not read request body")
		return
	}

	// MCP 2025-06-18 dropped JSON-RPC batching; a batched array is refused
	// with a clear invalid-request rather than silently mishandled.
	if trimmed := strings.TrimSpace(string(body)); strings.HasPrefix(trimmed, "[") {
		writeRPCError(w, nil, codeInvalidRequest, "JSON-RPC batching is not supported")
		return
	}

	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeRPCError(w, nil, codeParseError, "malformed JSON-RPC message")
		return
	}

	// A notification (no id) gets no JSON-RPC response body -- the streamable
	// HTTP binding says answer 202 Accepted. notifications/initialized is the
	// common one, sent by the client right after the initialize handshake.
	if req.isNotification() {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	resp := s.dispatch(r.Context(), user, &req)
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		logx.From(r.Context()).Error("writing response failed", "component", "mcp", "err", err)
	}
}

// authenticate extracts and resolves the Authorization: Bearer token,
// returning the owning user (nil if no/invalid token) or a real error on a
// DB fault.
func (s *Server) authenticate(r *http.Request) (*auth.SessionUser, error) {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return nil, nil
	}
	raw := strings.TrimSpace(h[len(prefix):])
	if raw == "" {
		return nil, nil
	}
	return auth.ResolveAPIToken(r.Context(), s.db, raw)
}

// dispatch routes one JSON-RPC request to its handler and always returns a
// well-formed response (a request always gets exactly one reply).
func (s *Server) dispatch(ctx context.Context, user *auth.SessionUser, req *rpcRequest) *rpcResponse {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "ping":
		return rpcOK(req.ID, struct{}{})
	case "tools/list":
		return s.handleToolsList(req)
	case "tools/call":
		return s.handleToolsCall(ctx, user, req)
	default:
		return rpcErr(req.ID, codeMethodNotFound, "unknown method: "+req.Method)
	}
}

func (s *Server) handleInitialize(req *rpcRequest) *rpcResponse {
	proto := defaultProtocolVersion
	if len(req.Params) > 0 {
		var p initializeParams
		if err := json.Unmarshal(req.Params, &p); err == nil && p.ProtocolVersion != "" {
			proto = p.ProtocolVersion
		}
	}
	return rpcOK(req.ID, initializeResult{
		ProtocolVersion: proto,
		Capabilities:    serverCapabilities{Tools: &toolsCapability{}},
		ServerInfo:      implementation{Name: serverName, Version: serverVersion},
		Instructions: "VoltEdge data-plane tools (read-only). Use list_feature_classes to " +
			"discover feature classes, describe_feature_class for a class's schema, " +
			"search_features to browse features, and get_feature to fetch one by id. " +
			"For network tracing, use list_trace_configurations to discover trace " +
			"profiles, then run_trace to walk connectivity from a starting feature; " +
			"run_isolation_trace, run_feeder_trace, and run_section_trace answer " +
			"higher-level questions (what a switch de-energizes, a feature's whole " +
			"feeder, the section between two devices). " +
			"Before proposing a new feature, use validate_feature to dry-run its " +
			"create-time validation. To find what a feature could connect or relate " +
			"to, use suggest_connections and suggest_relationships -- they return " +
			"ranked, rule-legal candidates (proposals, not established links). " +
			"To add a feature, use create_feature -- validate_feature first to " +
			"check it, then create_feature to write it. create_feature is a write " +
			"tool (readOnlyHint:false): confirm the change with a human before " +
			"calling it. To change an existing feature, use update_feature (send only " +
			"the fields you want to change) -- also a write tool needing the same " +
			"confirmation. Having found a candidate with suggest_connections or " +
			"suggest_relationships, commit it with establish_connection or " +
			"establish_relationship -- also write tools requiring the same " +
			"human confirmation before " +
			"calling it.",
	})
}

func (s *Server) handleToolsList(req *rpcRequest) *rpcResponse {
	// agenttools.All() is already in the advertised order and carries the MCP
	// wire tags (name/description/inputSchema/annotations), so it serializes
	// straight into the tools/list result.
	return rpcOK(req.ID, toolsListResult{Tools: agenttools.All()})
}

func (s *Server) handleToolsCall(ctx context.Context, user *auth.SessionUser, req *rpcRequest) *rpcResponse {
	var p callToolParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return rpcErr(req.ID, codeInvalidParams, "invalid tools/call params")
	}

	// agenttools.Execute is the shared choke point: it enforces the tool's RBAC
	// permission against this token's user (defense in depth over the bearer
	// gate) and threads the user onto the context so the engine's actor helpers
	// (auth.ActorID) attribute any write -- identical to how the HTTP session
	// middleware carries the user. The error kinds map onto MCP's two failure
	// shapes: an unknown tool is a transport-level invalid-params, while a
	// permission denial or an execution failure is reported in-band (isError) so
	// the agent reads a reason rather than seeing a dropped call.
	result, err := agenttools.Execute(ctx, s.db, user, p.Name, p.Arguments)
	if err != nil {
		var permErr *agenttools.PermissionError
		switch {
		case errors.Is(err, agenttools.ErrUnknownTool):
			return rpcErr(req.ID, codeInvalidParams, "unknown tool: "+p.Name)
		case errors.As(err, &permErr):
			return rpcOK(req.ID, toolError("permission denied: this token's user lacks "+permErr.Perm))
		default:
			return rpcOK(req.ID, toolError(err.Error()))
		}
	}

	// Tools return structured data; serialize it as JSON text content, the
	// standard shape for a data tool's result.
	payload, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return rpcErr(req.ID, codeInternalError, "could not serialize tool result")
	}
	return rpcOK(req.ID, callToolResult{Content: []contentBlock{{Type: "text", Text: string(payload)}}})
}

// toolError builds an isError tool result carrying a human-readable message.
func toolError(msg string) callToolResult {
	return callToolResult{Content: []contentBlock{{Type: "text", Text: msg}}, IsError: true}
}

func rpcOK(id json.RawMessage, result any) *rpcResponse {
	return &rpcResponse{JSONRPC: "2.0", ID: id, Result: result}
}

func rpcErr(id json.RawMessage, code int, msg string) *rpcResponse {
	return &rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}}
}

// writeRPCError emits a top-level JSON-RPC error for failures that occur
// before (or instead of) normal dispatch -- parse errors, batching refusal.
// id is null when the offending message couldn't be parsed far enough to
// recover one, which the JSON-RPC spec explicitly allows.
func writeRPCError(w http.ResponseWriter, id json.RawMessage, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	resp := &rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}}
	if resp.ID == nil {
		resp.ID = json.RawMessage("null")
	}
	_ = json.NewEncoder(w).Encode(resp)
}
