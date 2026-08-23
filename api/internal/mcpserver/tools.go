package mcpserver

// The tool registry -- definitions, handlers, per-tool RBAC permission, and the
// shared Execute choke point -- moved to internal/agenttools in Phase 19 Slice 2
// (docs/phase-19-assistant.md), so this MCP server and the in-app assistant
// (internal/assistant) share one tool contract instead of each defining it and
// risking drift. This file intentionally registers no tools now: the transport
// in server.go builds tools/list from agenttools.All() and runs tools/call
// through agenttools.Execute.
