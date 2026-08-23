// Package agenttools is the single, transport-agnostic definition of VoltEdge's
// AI data-plane tool set -- the contract (name, JSON schema, required RBAC
// permission, and handler) that every "front door" onto the tools shares.
//
// There are two such front doors, and before Phase 19 Slice 2 each defined the
// tools itself, which risked drift:
//   - internal/mcpserver -- the external MCP server (Phase 18): a separate
//     process, a bearer token, JSON-RPC over HTTP for an outside agent.
//   - internal/assistant -- the in-app assistant (Phase 19): in-process, behind
//     the ordinary cookie session, for the VoltEdge web frontend.
//
// Both now build their tool list and run tool calls from this one package, so a
// tool added, renamed, re-permissioned, or re-schemaed here changes both
// surfaces at once. Neither front door reimplements any query or write logic:
// every handler is the same thin adapter over an already-exported engine
// function (internal/crud, internal/metadata, internal/trace,
// internal/connectivity, internal/relationship) the REST handlers use, and each
// declares exactly the RBAC permission its REST equivalent requires.
//
// This package owns the tool *contract and execution* (definitions + the Execute
// choke point that enforces the permission and threads the acting user), but not
// the *transport*: JSON-RPC framing and the MCP behavioral-hint wire shape stay
// in mcpserver, and the HTTP/session surface stays in assistant. The MCP write-
// safety model (annotations: read tools advertise readOnlyHint:true, write tools
// readOnlyHint:false) is expressed here on the shared Annotations so both front
// doors present identical hints; it remains a client-side convention, while the
// RequiredPerm gate Execute enforces is the actual boundary.
package agenttools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/uptrace/bun"
)

// Handler runs one tool under an already-authenticated user's context (Execute
// puts the user on ctx before calling it, so engine actor helpers like
// auth.ActorID see who is acting). It returns the tool's structured result,
// which the caller serializes for its own transport, or an error the caller
// reports in whatever way its protocol expects.
type Handler func(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error)

// Annotations are the MCP behavioral hints a client uses to decide how to
// present a tool -- crucially, whether a call needs human confirmation before it
// runs. The chosen write-safety model (docs/phase-18-mcp.md) is client-side:
// read tools advertise readOnlyHint:true so a client runs them freely, and a
// write tool advertises readOnlyHint:false (plus the destructive/idempotent
// hints) so a client surfaces it for approval first. Each field is a pointer so
// an unset hint is omitted rather than sent as a misleading explicit false.
// These are hints, not a security boundary -- Execute's RequiredPerm check is the
// actual enforcement. The JSON tags match the MCP wire shape so mcpserver can
// serialize a Tool directly into a tools/list response.
type Annotations struct {
	Title           string `json:"title,omitempty"`
	ReadOnlyHint    *bool  `json:"readOnlyHint,omitempty"`
	DestructiveHint *bool  `json:"destructiveHint,omitempty"`
	IdempotentHint  *bool  `json:"idempotentHint,omitempty"`
	OpenWorldHint   *bool  `json:"openWorldHint,omitempty"`
}

// Tool is one registered tool: its wire definition (Name/Description/
// InputSchema/Annotations, JSON-tagged to match the MCP tool object so it can be
// listed verbatim), the RBAC permission a caller must hold to invoke it, and the
// handler that runs it. RequiredPerm and Handler carry json:"-" so serializing a
// Tool for a client emits only the public contract, never the internal wiring.
type Tool struct {
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	InputSchema  json.RawMessage `json:"inputSchema"`
	Annotations  *Annotations    `json:"annotations,omitempty"`
	RequiredPerm string          `json:"-"`
	Handler      Handler         `json:"-"`
}

// ErrUnknownTool wraps a call for a tool name not in the registry. Callers can
// errors.Is against it to map an unknown tool to their own "bad request" shape
// (mcpserver returns a JSON-RPC invalid-params) distinct from a permission or
// execution failure.
var ErrUnknownTool = errors.New("unknown tool")

// PermissionError is returned by Execute when the acting user lacks the tool's
// RequiredPerm. Callers can errors.As to it to report the specific missing
// permission (mcpserver surfaces it as an in-band tool error, not a transport
// error, so the caller sees a readable reason).
type PermissionError struct{ Perm string }

func (e *PermissionError) Error() string { return "permission denied: missing " + e.Perm }

// registry is the ordered tool set, built once: the read-only tools first
// (stamped readOnlyHint:true where they declare no annotations of their own),
// then the write tools, which carry their own annotations. byName indexes it for
// Execute. The order is the advertised tools/list order.
var (
	registry = buildRegistry()
	byName   = indexByName(registry)
)

// buildRegistry assembles the full tool set and bakes in the read-only
// annotation stamping that used to live in mcpserver.markReadOnly: every read
// tool without its own annotations advertises readOnlyHint:true. Write tools are
// appended after, keeping the annotations they declare. Doing this here means
// both front doors see identical hints.
func buildRegistry() []Tool {
	read := readTools()
	readOnly := true
	for i := range read {
		if read[i].Annotations == nil {
			read[i].Annotations = &Annotations{ReadOnlyHint: &readOnly}
		}
	}
	return append(read, writeTools()...)
}

func indexByName(tools []Tool) map[string]Tool {
	m := make(map[string]Tool, len(tools))
	for _, t := range tools {
		m[t.Name] = t
	}
	return m
}

// All returns the tool set in advertised order. Callers list it (mcpserver's
// tools/list, the assistant's tool catalog) but must not mutate the elements;
// the slice backs the shared registry.
func All() []Tool { return registry }

// Get returns the tool with the given name, if registered.
func Get(name string) (Tool, bool) {
	t, ok := byName[name]
	return t, ok
}

// Writes reports whether invoking this tool mutates state -- i.e. it is a write
// tool, not a read. It reads the same readOnlyHint annotation the front doors
// advertise to clients: read tools are stamped readOnlyHint:true, write tools
// declare readOnlyHint:false. A tool with no hint at all is treated as a write
// (fail safe: assume a call could mutate unless explicitly told it cannot). The
// assistant chat orchestrator uses this to decide execute-inline (reads) versus
// return-as-a-confirmation-proposal (writes never auto-run).
func (t Tool) Writes() bool {
	if t.Annotations == nil || t.Annotations.ReadOnlyHint == nil {
		return true
	}
	return !*t.Annotations.ReadOnlyHint
}

// Execute is the shared choke point both front doors run every tool call
// through. It looks the tool up (ErrUnknownTool if absent), enforces its
// RequiredPerm against the acting user (PermissionError if the user lacks it or
// is nil), threads that user onto the context so the engine's actor helpers
// attribute the write, and runs the handler. It deliberately does not translate
// the handler's own error -- an unknown feature ref, a validation failure, a
// rule rejection all come back verbatim for the caller to report in its own
// protocol's shape.
func Execute(ctx context.Context, db bun.IDB, user *auth.SessionUser, name string, args json.RawMessage) (any, error) {
	t, ok := byName[name]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownTool, name)
	}
	if t.RequiredPerm != "" && (user == nil || !user.Has(t.RequiredPerm)) {
		return nil, &PermissionError{Perm: t.RequiredPerm}
	}
	ctx = auth.WithUser(ctx, user)
	return t.Handler(ctx, db, args)
}
