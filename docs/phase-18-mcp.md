# Phase 18 — MCP (data-plane tools for an AI agent)

The architecture (docs/metadata-core.md §"MCP — data plane only") always
reserved a place for a **Model Context Protocol** server sitting beside the
REST API: it gives an AI agent tools to operate on VoltEdge *data* — never on
the metadata that governs the system. MCP and the REST API write through the
**exact same** generic, metadata-validated engine (`internal/crud`,
`internal/metadata`); the metadata compiler is reachable only from the admin
UI, with no path from MCP into schema authoring. This phase builds that server.

Slice 1 is the read-only foundation: the transport, the agent's identity, and
the four discovery/read tools — everything an agent needs to *understand and
query* the network, with no write surface at all.

## Decisions (locked with the user)

- **Transport: hand-rolled JSON-RPC 2.0 over MCP streamable-HTTP — no SDK.**
  MCP is JSON-RPC 2.0; the streamable-HTTP binding for a pure request/response
  tool server is small (POST a request, get one JSON response back). We
  implement it directly rather than take a third-party MCP SDK dependency,
  matching this codebase's "minimal, audited dependency surface" posture (the
  same reasoning as the Phase 10 auth write-up), and — practically — keeping the
  whole thing statically verifiable in an environment where the build can't run
  here. We speak the subset that matters: `initialize`, `notifications/initialized`,
  `tools/list`, `tools/call`, `ping`.
- **Agent identity: per-user opaque API tokens (`iam.api_tokens`), reusing the
  Phase 10 auth primitives.** An agent presents `Authorization: Bearer <token>`;
  the server resolves it to a real VoltEdge user and that user's effective RBAC
  permission set, then runs every tool under it. The token flows through the
  *identical* session → user → roles → permissions chain a cookie session does
  (`ResolveAPIToken` calls the same `loadPermissionsFor` the session path uses),
  so **a token can never do more than the user it belongs to** — no new
  authorization surface. Token material mirrors the session model: 256 bits of
  `crypto/rand`, stored only as its SHA-256.
- **Tokens are minted out-of-band by `authadmin`, not by an online endpoint.**
  This matches Phase 10's "admin pre-provisions, nothing shipped in a seed"
  provisioning ethos and keeps Slice 1 small. `authadmin mint-token` prints the
  raw token exactly once; `list-tokens`/`revoke-token` manage the lifecycle. An
  online token-management API (self-service minting from the web UI) is a later
  slice, not a Slice 1 gap.
- **Four genuinely-thin read tools; the rest deferred honestly.** The design
  table lists `suggest_*`, `validate_feature`, `run_trace`, and the mutating
  `establish_*`/`create_`/`update_` tools. Of the read set, only
  discovery/search/get have an already-exported engine function to adapt thinly
  — `suggest_connections`/`suggest_relationships` need a ranked-candidate engine
  that doesn't exist yet, and `validate_feature` needs an exported validate-only
  entrypoint. Rather than reimplement engine logic inside the MCP layer (which
  would violate "one validated engine, two front doors"), Slice 1 ships the four
  tools that are truly thin adapters and defers the rest to the slices that build
  their engines.

## Architecture

```
AI agent ──POST /mcp──▶ cmd/mcpserver ──▶ internal/mcpserver.Server
  (Bearer token)         (own process)      │
                                            ├─ authenticate: auth.ResolveAPIToken(token) → *SessionUser
                                            ├─ dispatch JSON-RPC: initialize / tools/list / tools/call / ping
                                            └─ tools/call: RBAC gate (user.Has(perm)) → handler
                                                                                         │
                                                        internal/crud + internal/metadata (the shared engine)
```

- **Own process, shared engine.** `cmd/mcpserver` is a sibling binary of
  `server`/`exportworker`/`compile`/`authadmin`. It imports the same
  `internal/crud` and `internal/metadata` packages the REST API does, so tool
  results are byte-for-byte the engine's own output — there is no second query
  path to keep in sync. Config is all env (`DATABASE_URL`, `MCP_PORT`); `/healthz`
  proves DB reachability for orchestration probes.
- **One trust boundary.** The MCP server reaches `internal/auth` for exactly two
  things: resolving the bearer token (`ResolveAPIToken`) and attaching the
  resolved user to the request context (`WithUser`) so the engine's actor helpers
  (`auth.ActorID`) see who is acting — identical to how the HTTP session
  middleware carries the user. Every `tools/call` re-checks `user.Has(requiredPerm)`
  as defense in depth over the bearer gate.
- **Read-only, by construction.** All four tools declare `requiredPerm =
  features:read` and call only read functions. There is no code path from this
  package to a write, to the metadata compiler, or to schema authoring.

### Wire behavior (the JSON-RPC subset)

- **POST only.** No server-initiated SSE stream and no session lifecycle in this
  slice, so GET/DELETE are refused with `405`. Body is capped at 1 MiB.
- **Auth before parse.** Every POST — *including* `initialize` — must carry a
  live token; an anonymous caller gets `401` with a `WWW-Authenticate: Bearer`
  challenge and can't even handshake.
- **Notifications.** A message with no `id` (e.g. `notifications/initialized`,
  sent right after the handshake) gets `202 Accepted` and no JSON-RPC body.
- **No batching.** MCP 2025-06-18 dropped JSON-RPC batching; a top-level `[`
  array is refused with a clear `invalid-request` rather than silently
  mishandled.
- **Tool errors are in-band.** A well-formed tool call that fails at execution
  (unknown feature class, feature not found, permission denied) returns a normal
  result with `isError: true` and a readable message — *not* a JSON-RPC transport
  error — so the agent reads the reason instead of seeing a dropped call.
  Protocol-level faults (bad params, unknown method) use JSON-RPC error codes.

### The four tools (all `features:read`)

| Tool | Backing engine call | Returns |
|---|---|---|
| `list_feature_classes` | `metadata.ListFeatureClasses` | every compiled feature class: code, name, geometry type, network role |
| `describe_feature_class` | `metadata.Load` | one class's schema: subtypes, fields (type, nullability, coded-domain values), terminals |
| `search_features` | `crud.SearchFeatures` | a page of feature envelopes (id/name/status/geometry/attributes); name/status filter, sort, paging; ≤ 500/call |
| `get_feature` | `crud.GetFeature` | one feature by id (including a soft-deleted row, so "deleted" is distinguishable from "missing") |

`SearchFeatures` (`crud/search.go`) is a thin exported wrapper over the engine's
existing unexported `listFeatures`/`listOptions` — the MCP tool reimplements no
query logic.

## Files

- **`migrations/0037_create_api_tokens.sql`** — `iam.api_tokens` (opaque,
  revocable, per-user bearer tokens). Modeled on `iam.sessions`: SHA-256-only
  storage, optional `expires_at`, `revoked_at`, `last_used_at`, `token_prefix`
  for display, FKs to `iam.users`. Fully commented.
- **`api/internal/auth/apitoken.go`** — `MintAPIToken` / `ResolveAPIToken` /
  `RevokeAPIToken` / `ListAPITokens` + the `APIToken` model. `ResolveAPIToken`
  returns the same `*SessionUser` a cookie session yields (minus SessionID/CSRF,
  which a bearer client has no use for) and loads perms via the shared
  `loadPermissionsFor`.
- **`api/internal/auth/session.go`** — refactored `loadPermissions` to delegate
  to a package-level `loadPermissionsFor(ctx, db, userID)` so both the session
  path and the token path load permissions through one query.
- **`api/internal/auth/context.go`** — exported `WithUser` wrapper so a
  non-HTTP-middleware caller (the MCP server) can establish the same
  authenticated context the session middleware does.
- **`api/internal/crud/search.go`** — `SearchFeatures` / `SearchOptions`, the
  thin export of `listFeatures`.
- **`api/internal/mcpserver/server.go`** — the JSON-RPC 2.0 transport: request
  parsing, auth, dispatch, the RBAC gate, and result serialization.
- **`api/internal/mcpserver/tools.go`** — the four tool definitions (input
  schemas + descriptions) and their handlers.
- **`api/cmd/mcpserver/main.go`** — the process: `POST /mcp`, `/healthz`, env
  config.
- **`api/cmd/authadmin/main.go`** — added `mint-token` / `list-tokens` /
  `revoke-token` subcommands.
- **`.env.example`**, **`Taskfile.yml`** — `MCP_PORT` (dev `3705`), `task
  mcp-dev`, and `task authadmin-{mint,list,revoke}-token` wrappers.

## Slices

- **Slice 1 — read-only tools + agent identity.** ✅ *Code complete
  (2026-08-21, verification owed — Bash blocked).* The transport
  (`internal/mcpserver`), the token identity (`iam.api_tokens` +
  `internal/auth/apitoken.go`), out-of-band minting (`authadmin mint-token`),
  and the four `features:read` tools (`list_feature_classes`,
  `describe_feature_class`, `search_features`, `get_feature`). No write path, no
  metadata authoring path.

  *Verify:* `go build ./...` / `go vet ./...`; `task migrate` (0037). Mint a
  token: `task authadmin-mint-token EMAIL=agent@example.com NAME=claude-desktop`
  (copy the printed token — shown once). Then against `POST /mcp` with
  `Authorization: Bearer <token>`: `initialize` → capabilities+serverInfo;
  `notifications/initialized` → `202`; `tools/list` → the four tools in stable
  order; `tools/call` each — `list_feature_classes` (no args), then
  `describe_feature_class`/`search_features`/`get_feature` against a real class
  (e.g. `POLE`) and a real feature id from the search result. Confirm: no
  `Authorization` header → `401` + `WWW-Authenticate`; a revoked/expired token →
  `401`; a token whose user lacks `features:read` → a `tools/call` result with
  `isError:true` ("permission denied…"), not a `403`; an unknown feature class or
  a missing feature id → `isError:true` with a readable message; a `[`-batch body
  → JSON-RPC `invalid-request`; a GET → `405`. Revoke and re-list:
  `task authadmin-list-tokens EMAIL=…`, `task authadmin-revoke-token EMAIL=… ID=…`,
  and confirm the revoked token then fails auth.

## Deferred to later slices

- **`suggest_connections` / `suggest_relationships`** (read-only) — need a
  ranked terminal/relationship candidate engine (evaluate `network_rules` /
  `relationship_classes` for a feature, propose ranked candidates), which does
  not exist yet.
- **`validate_feature`** (read-only) — needs an exported validate-only entrypoint
  (dry-run against `subtype_field_rules` / `network_rules`, no write).
- **`run_trace`** (read-only) — wraps the Go graph/trace service over
  `feature_connectivity`.
- **`establish_connection` / `establish_relationship` / `create_feature` /
  `update_feature`** (mutating) — the same validated write path as the REST API,
  plus the **user-confirmation model**: per the architecture, `establish_*`/write
  tools map to a user's *explicit* confirmation and an agent must never call them
  on its own judgment. The confirmation mechanism (how a tool surfaces a
  proposed write for human approval before it commits) is its own design
  decision, still open.
- **Online token management** — a self-service endpoint / web UI for minting and
  revoking one's own tokens, replacing the CLI-only path. Slice 1 is deliberately
  admin-CLI-only.
