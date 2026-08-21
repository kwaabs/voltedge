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
- **Write safety is client-side, via MCP annotations (Slice 6).** When the write
  surface opened, the question was how a mutating tool gets human approval before
  it commits. Decision: the idiomatic MCP pattern — tools carry
  `annotations.readOnlyHint` (read tools `true`, write tools `false`, plus
  `destructiveHint` where apt), and the human's MCP *client* is responsible for
  surfacing a write for confirmation. The server stays stateless: no server-side
  two-phase `confirm_write` token/round-trip. The annotations are hints; the
  `:write`/`:commit` RBAC gate is the real boundary, and `validate_feature` gives
  a no-write pre-flight. (The rejected alternative — a server-enforced pending-
  write store keyed by a confirmation token — would be client-agnostic but adds
  state, a TTL, and a second call for no gain over a client that honors the hint.)

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
- **Read-only, by construction.** Every tool calls only read/pure functions;
  there is no code path from this package to a write, to the metadata compiler,
  or to schema authoring. The feature tools declare `features:read`, the trace
  tools `trace:run`, `validate_feature` `features:write` (not because it writes —
  it doesn't — but because its REST equivalent is the create route), and the
  suggestion tools `connectivity:read` / `relationships:read` (they read those
  domains' rules and existing links to propose candidates). Every gate is exactly
  the permission the tool's REST equivalent requires.

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

### The tools

Slice 1 — feature discovery/read (all `features:read`):

| Tool | Backing engine call | Returns |
|---|---|---|
| `list_feature_classes` | `metadata.ListFeatureClasses` | every compiled feature class: code, name, geometry type, network role |
| `describe_feature_class` | `metadata.Load` | one class's schema: subtypes, fields (type, nullability, coded-domain values), terminals |
| `search_features` | `crud.SearchFeatures` | a page of feature envelopes (id/name/status/geometry/attributes); name/status filter, sort, paging; ≤ 500/call |
| `get_feature` | `crud.GetFeature` | one feature by id (including a soft-deleted row, so "deleted" is distinguishable from "missing") |

Slice 2 — network tracing (all `trace:run`):

| Tool | Backing engine call | Returns |
|---|---|---|
| `list_trace_configurations` | `trace.ListConfigurations` | every available trace profile: code (→ `run_trace`), name, direction (CONNECTED/DOWNSTREAM/UPSTREAM/SHORTEST_PATH) |
| `run_trace` | `trace.RunTrace` | features reached from a start feature (each `feature_class`+`feature_id`), the barrier subset, and — for SHORTEST_PATH — whether the target was found |

Slice 3 — write pre-flight (`features:write`):

| Tool | Backing engine call | Returns |
|---|---|---|
| `validate_feature` | `crud.ValidateFeature` | `{valid:true}` or `{valid:false, error:<first problem>}` — a dry-run of the exact create-time validation (subtype, geometry, required fields, known/editable fields, per-value type/domain/range), writing nothing |

Slice 4 — ranked candidate proposals (read-only):

| Tool | Backing engine call | Perm | Returns |
|---|---|---|---|
| `suggest_connections` | `connectivity.SuggestConnections` | `connectivity:read` | ranked candidate features this one could legally connect to (rule-legal, not already connected, proximity-ranked); each with the candidate, the rule's implied terminals per side, the authorizing rule, and distance in meters |
| `suggest_relationships` | `relationship.SuggestRelationships` | `relationships:read` | ranked candidate features this one could legally relate to (class-legal, cardinality-satisfying, not already related, proximity-ranked); each with the candidate, its role (origin/destination), the relationship class + kind + cardinality, and distance |

`crud.SearchFeatures` (`crud/search.go`) and `trace.RunTrace` /
`trace.ListConfigurations` (`trace/export.go`) are thin exported wrappers over
those packages' existing unexported engine functions (`listFeatures`, `runTrace`,
`listConfigurations`) — the MCP tools reimplement no query, traversal, or
validation logic. The trace tools require `trace:run`, not `features:read`,
because that is exactly the permission the REST `/trace` subtree enforces.

`crud.ValidateFeature` (`crud/validatefeature.go`) is different in kind: there
was no existing validate-only entrypoint to export. Instead, the create-time
validation sequence was factored out of `createHandler` into an unexported,
DB-free `validateForCreate` (`crud/validate.go`) that both the real create path
and `ValidateFeature` call — so the dry-run can never disagree with an actual
create about what "valid" means. It is a metadata-level pre-flight only: it does
*not* see database-enforced constraints (uniqueness, referenced-feature
existence, PostGIS coordinate validity) and does *not* run territory
assignment, so `valid:true` means "passes all in-memory rules", not "guaranteed
insert". It reports the first blocking error (fail-fast), matching the create
endpoint.

`connectivity.SuggestConnections` (`connectivity/suggest.go`) and
`relationship.SuggestRelationships` (`relationship/suggest.go`) are the first
genuinely *new* engines the MCP work added — there was no ranked-candidate logic
to adapt. They live inside the `connectivity` / `relationship` packages (not a
wrapper) precisely so they can reuse the establish path's own zero-trust oracle:
`idMatches` / `ruleSpecificity` over `LoadNetworkRules` for connections,
`checkCardinality` over `LoadRelationshipClasses` for relationships — so every
candidate is one a real establish would accept at the moment of suggestion.
Ranking is spatial proximity (PostGIS `ST_Distance` over geography, the same
pattern as `relationship/plausibility.go`), nearest-first, bounded by a search
radius; when either class is non-geometric there is no spatial signal, so
candidates come back name-ordered with a null distance. Candidates always
exclude the feature itself, soft-deleted rows, and anything already
connected/related. This realizes the "suggested vs. established" split
(`docs/metadata-core.md`): a matching rule yields a *candidate, not a fact*.
Deliberately *not* pre-checked (still enforced at establish time): a phase-gated
rule's live phase compatibility, and all database constraints — so a suggestion
is "rule-legal and nearby", not a guaranteed establish.

Slice 5 — composite trace analyses (all `trace:run`):

| Tool | Backing engine call | Returns |
|---|---|---|
| `run_isolation_trace` | `trace.Isolate` | what would de-energize if a switching device were opened now (without opening it): the de-energized set, the upstream protective boundary, the critical facilities within it, and possible backfeed paths |
| `run_feeder_trace` | `trace.FeederTrace` | the whole feeder a feature belongs to: the head(s) found by walking upstream to the root, then everything reached downstream from there |
| `run_section_trace` | `trace.SectionTrace` | the section a feature sits in: everything between the nearest bounding device upstream and downstream, plus each boundary device |

`trace.Isolate` / `trace.FeederTrace` / `trace.SectionTrace` (`trace/export.go`)
are thin exported wrappers over the engine's existing unexported composites
(`isolate`, `feederTrace`, `sectionTrace`) — exactly the Slice 2 pattern, adding
no traversal logic of their own. These composites already *compose* the four
primitive walks (an isolation is a downstream trace past the device unioned with
an upstream boundary search, etc.), so the MCP tools are one more mapping layer
on top, not new graph code. They are gated `trace:run` because that is precisely
the permission the REST `/trace` mount enforces on `/isolate`, `/feeder`, and
`/section`. All three are read-only: an isolation trace computes what opening a
device *would* do without ever writing its state (the engine starts the
downstream leg from the device's neighbors rather than mutating `device_state`),
so the tool name says *analyze*, not *operate*.

Slice 6 — the write surface (`features:write`):

| Tool | Backing engine call | Returns |
|---|---|---|
| `create_feature` | `crud.CreateFeature` | the created feature's full envelope (id, name, status, geometry, attributes) — the same validated write the REST create route performs (metadata validation → territory assignment → insert) |

`crud.CreateFeature` (`crud/create.go`) is the write counterpart to Slice 3's
`validateForCreate`: the create body (validate → assign territory → insert) was
factored out of `createHandler` into one exported function that both the REST
handler and the MCP tool call, so the two front doors can't drift on what a
create validates, how a territory is assigned, or what is written. The acting
user — stamped into `created_by`/`updated_by` — is read from the context
(`auth.ActorID`), which `handleToolsCall` populates from the bearer token via
`auth.WithUser` exactly as the HTTP session middleware does for a cookie
session, so an agent's write is attributed to the token's real user and can
never exceed that user's RBAC.

**Write safety is client-side, by MCP annotation** (the decision recorded
below). `create_feature` advertises `annotations.readOnlyHint:false` (plus
`destructiveHint:false` — a create is additive — and `idempotentHint:false`),
while every read tool advertises `readOnlyHint:true` (stamped centrally by
`markReadOnly`). A human's MCP client uses those hints to decide what to run
freely and what to surface for confirmation before calling — the idiomatic MCP
human-in-the-loop pattern. The server itself stays stateless: it does **not**
implement a second server-side confirm round-trip. The annotations are hints,
not enforcement — the `features:write` RBAC gate in `handleToolsCall` is the
actual boundary, and `validate_feature` remains available as a no-write
pre-flight before a create.

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
- **`api/internal/trace/export.go`** (Slices 2 + 5) — Slice 2: `RunTrace` /
  `RunTraceInput` / `RunTraceResult` and `ListConfigurations` /
  `TraceConfigurationSummary`, the thin exported wrappers over the trace engine's
  unexported `runTrace` / `listConfigurations` (mapping to exported DTOs so the
  internal package's handler types stay untouched). Slice 5 adds the composite
  wrappers `Isolate` / `IsolationInput` / `IsolationResult`, `FeederTrace` /
  `FeederInput` / `FeederResult`, and `SectionTrace` / `SectionInput` /
  `SectionResult` over the engine's `isolate` / `feederTrace` / `sectionTrace`
  (plus a `toExportedRefPtr` helper for the optional boundary fields).
- **`api/internal/crud/validate.go`** (Slice 3) — extracted `validateForCreate`,
  the DB-free create-time validation sequence now shared by `createHandler` and
  `ValidateFeature` (the single source of truth for "valid to create").
- **`api/internal/crud/validatefeature.go`** (Slice 3) — `ValidateFeature` /
  `ValidateFeatureInput` / `ValidateFeatureReport`, the exported read-only
  dry-run over `validateForCreate` (copies attributes so `default_override`
  injection stays side-effect-free).
- **`api/internal/crud/create.go`** (Slice 6) — `CreateFeature` /
  `CreateFeatureInput`, the exported full create path (validate → assign
  territory → insert) extracted from `createHandler` so REST and the MCP
  `create_feature` tool share one write path. `createHandler` (`crud/handler.go`)
  now just decodes the body and calls it.
- **`api/internal/connectivity/suggest.go`** (Slice 4) — `SuggestConnections` /
  `SuggestInput` / `ConnectionSuggestion`, the new ranked-candidate engine
  reusing this package's own `idMatches` / `ruleSpecificity` oracle and the
  PostGIS proximity pattern. No write path.
- **`api/internal/relationship/suggest.go`** (Slice 4) — `SuggestRelationships` /
  `SuggestInput` / `RelationshipSuggestion`, the parallel engine reusing
  `checkCardinality` and `LoadRelationshipClasses`.
- **`api/internal/mcpserver/server.go`** — the JSON-RPC 2.0 transport: request
  parsing, auth, dispatch, the RBAC gate, and result serialization. Slice 6 adds
  the `toolAnnotations` type + `markReadOnly` (stamps `readOnlyHint:true` on the
  read tools) and calls `registerWriteTools`.
- **`api/internal/mcpserver/tools.go`** — the tool definitions (input schemas +
  descriptions + annotations) and handlers: four feature tools (Slice 1) + two
  trace tools (Slice 2) + `validate_feature` (Slice 3) + the two suggestion tools
  (Slice 4) + the three composite trace tools (Slice 5) + `create_feature`
  (Slice 6, in `registerWriteTools`).
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

- **Slice 2 — network tracing (read-only).** ✅ *Code complete (2026-08-21,
  verification owed — Bash blocked).* Two tools — `list_trace_configurations`
  (discover trace profiles) and `run_trace` (walk connectivity from a start
  feature) — over the existing Phase 4 trace engine. As in Slice 1 the engine
  was entirely unexported, so this slice first added `trace/export.go`: thin
  exported wrappers (`ListConfigurations`, `RunTrace`) mapping to exported DTOs,
  leaving the internal handler types and all traversal/barrier logic untouched.
  Both tools require `trace:run` (the same permission the REST `/trace` subtree
  enforces), not `features:read`. The specialized `isolate` / `feeder` /
  `section` composites (also read-only, also unexported) are deferred — Slice 2
  ships the two general-purpose trace tools.

  *Verify:* `go build ./...` / `go vet ./...` (no migration — reuses Phase 4's
  schema and the existing `trace:run` permission from migration 0025). With a
  Bearer token whose user holds `trace:run`: `tools/list` now shows six tools
  (the four feature tools + these two); `tools/call list_trace_configurations`
  (no args) → the seeded configs with their directions; `tools/call run_trace`
  with a real `trace_configuration` code and a real `start` feature
  (`{feature_class, feature_id}`) → features/barriers. Confirm: a SHORTEST_PATH
  config without `target` → `isError:true` ("target is required…"); an unknown
  `trace_configuration` → `isError:true` ("… does not exist"); a token whose
  user lacks `trace:run` (but has `features:read`) → the four feature tools work
  but both trace tools return `isError:true` "permission denied".

- **Slice 3 — `validate_feature` (write pre-flight).** ✅ *Code complete
  (2026-08-21, verification owed — Bash blocked).* A single tool that dry-runs
  the exact create-time validation without writing. Unlike Slices 1–2 there was
  no existing entrypoint to thinly export, so this slice refactored the
  create-validation block out of `createHandler` into an unexported, DB-free
  `validateForCreate` (`crud/validate.go`) and added the exported
  `crud.ValidateFeature` (`crud/validatefeature.go`) over it — the create path
  and the dry-run now share one implementation and cannot drift. Gated by
  `features:write` (its REST equivalent is the create route). It is a
  metadata-level pre-flight: no DB-only constraints (uniqueness, FK existence,
  PostGIS validity), no territory assignment; fail-fast (first error), matching
  the create endpoint. Update-mode validation and collect-all-errors reporting
  are possible future enhancements.

  *Verify:* `go build ./...` / `go vet ./...` (no migration). Confirm the
  create-path refactor is behavior-preserving: creating a feature via the REST
  API still validates identically (same first error). With a Bearer token whose
  user holds `features:write`: `tools/list` now shows seven tools;
  `tools/call validate_feature` with a real `feature_class` and a well-formed
  `{subtype, geometry, attributes}` → `{valid:true}`; with a missing required
  field, an unknown field, a bad coded-domain value, a wrong geometry type, or a
  missing/spurious subtype → `{valid:false, error:…}` naming the problem. Confirm
  a token whose user lacks `features:write` (read-only) → `validate_feature`
  returns `isError:true` "permission denied" while the feature/trace tools still
  work; an unknown `feature_class` → `isError:true`.

- **Slice 4 — `suggest_connections` / `suggest_relationships` (ranked candidate
  proposals).** ✅ *Code complete (2026-08-21, verification owed — Bash blocked).*
  The first *new* engine work (no existing function to adapt): two read-only
  tools that, given a feature, propose ranked candidate features it could legally
  connect to / relate to. Built inside the `connectivity` / `relationship`
  packages so they reuse those packages' own establish-time oracle
  (`idMatches`/`ruleSpecificity`; `checkCardinality`) — a suggestion is one a
  real establish would accept. Ranked by PostGIS proximity (nearest-first, radius
  bounded; non-geometric classes fall back to name order, null distance);
  excludes self, soft-deleted, and already-linked features. Gated by
  `connectivity:read` / `relationships:read`. *Not* pre-checked (still enforced at
  establish): phase-gated rules' live phase data and DB constraints — a candidate
  is not a fact.

  *Verify:* `go build ./...` / `go vet ./...` (no migration — reads Phase 2's
  `network_rules` / `relationship_classes` / `runtime.feature_connectivity` /
  `runtime.feature_relationships`). With a Bearer token holding
  `connectivity:read`: `tools/list` now shows nine tools;
  `tools/call suggest_connections` with a real `{feature_class, feature_id}`
  (e.g. an `MV_CONDUCTOR` from the connectivity seed) → nearby, rule-legal
  candidates (e.g. `DIST_XFMR` at terminal `T1`), nearest-first, with
  `network_rule_name` and `distance_meters`; confirm a feature already connected
  to a candidate no longer suggests it, and the `is_valid=false` T3 rule never
  produces a suggestion. With a `relationships:read` token,
  `tools/call suggest_relationships` on a `POLE` → nearby `MV_CONDUCTOR`
  candidates (role `destination`, kind `spatial_support`); confirm a
  `one_to_one`/`one_to_many` class whose cap is exhausted drops those candidates.
  Confirm a token lacking `connectivity:read` (resp. `relationships:read`) →
  `isError:true` "permission denied" on that tool while the others still work;
  an unknown/deleted feature → `isError:true`.

- **Slice 5 — `run_isolation_trace` / `run_feeder_trace` / `run_section_trace`
  (composite trace analyses).** ✅ *Code complete (2026-08-21, verification owed —
  Bash blocked).* Three read-only tools over the trace engine's existing
  composites — an isolation trace (what a switch de-energizes, plus the upstream
  boundary, critical facilities, and possible backfeed paths), a feeder trace
  (the whole feeder a feature belongs to), and a section trace (what lies between
  the two devices bounding a feature). Thin `trace.Isolate` / `trace.FeederTrace`
  / `trace.SectionTrace` wrappers (the Slice 2 pattern), so no traversal logic is
  reimplemented. All gated `trace:run` — the exact permission the REST `/trace`
  mount enforces on `/isolate`, `/feeder`, `/section`. Read-only by construction:
  the isolation analysis simulates "opened" by starting downstream from the
  device's neighbors, never writing `device_state`.

  *Verify:* `go build ./...` / `go vet ./...` (no migration — reuses Phase 4's
  trace schema + `trace:run` from migration 0025). With a Bearer token holding
  `trace:run`: `tools/list` now shows **twelve** tools;
  `tools/call run_isolation_trace` with a DOWNSTREAM `trace_configuration` and a
  switching `device` → a `de_energized` set, an `upstream_boundary` (or absent at
  a network edge), and `critical_facilities` / `possible_backfeed_paths` when
  present; confirm a non-DOWNSTREAM configuration → `isError:true` "isolation
  requires a DOWNSTREAM trace_configuration". `run_feeder_trace` from any feature
  on a feeder → the feeder head(s) + full downstream footprint;
  `run_section_trace` on a feature between two `DIST_SWITCH`es → the bounded
  section plus both boundary devices (override `boundary_class` to test another).
  Confirm a token lacking `trace:run` → `isError:true` "permission denied" on all
  three; an unknown/deleted feature or configuration → `isError:true`.

- **Slice 6 — `create_feature` (the write surface opens).** ✅ *Code complete
  (2026-08-21, verification owed — Bash blocked).* The first mutating tool: it
  creates a feature through the exact same validated write path as the REST
  create route, via the shared `crud.CreateFeature` (validate → territory →
  insert) factored out of `createHandler`. Gated `features:write`. Write safety
  is client-side by MCP annotation (decided with the user): `create_feature`
  advertises `readOnlyHint:false`, every read tool `readOnlyHint:true`, so the
  human's client surfaces a write for confirmation before calling — the server
  adds no confirm round-trip. The write is attributed to the token's user
  (`created_by` from `auth.ActorID`, populated by `auth.WithUser` in
  `handleToolsCall`).

  *Verify:* `go build ./...` / `go vet ./...` (no migration — writes through the
  existing feature tables; reuses `features:write` from migration 0025). Confirm
  the REST create still behaves identically after the `createHandler` refactor
  (POST `/features/{code}` with and without geometry/subtype; territory
  auto-assign vs override; a non-geometric class rejecting `territory_id`). With a
  Bearer token holding `features:write`: `tools/list` now shows **thirteen**
  tools, and `create_feature`'s entry carries `annotations.readOnlyHint:false`
  while a read tool (e.g. `get_feature`) carries `readOnlyHint:true`;
  `tools/call create_feature` with a valid `{feature_class, ...}` → the new
  feature envelope with an `id`, and a follow-up `get_feature` finds it with
  `created_by` = the token's user; an invalid body (bad subtype, missing required
  field, wrong geometry type) → `isError:true` carrying the first validation
  error, having written nothing. Confirm a token lacking `features:write` →
  `isError:true` "permission denied" while the read tools still work.

## Deferred to later slices

- **`establish_connection` / `establish_relationship` / `update_feature`**
  (mutating) — the remaining writes, each over its own already-exported engine
  path (`connectivity`/`relationship` establish; `crud.updateFeature`, which
  would first need extracting into an exported `UpdateFeature` the way Slice 6
  did for create). The **write-confirmation model is now settled** (Slice 6):
  client-side by MCP annotation — each new write tool advertises
  `readOnlyHint:false` (and the apt `destructiveHint`, e.g. a delete would set it
  true) and relies on the human's client to confirm, plus its `:commit`/`:write`
  RBAC gate. `create_feature` shipped in Slice 6; these are the same pattern.
- **Online token management** — a self-service endpoint / web UI for minting and
  revoking one's own tokens, replacing the CLI-only path. Slice 1 is deliberately
  admin-CLI-only.
