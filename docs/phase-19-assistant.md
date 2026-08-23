# Phase 19 — In-app AI assistant (frontend copilot)

Phase 18 gave an **external** agent (Claude Code, Cursor) tools over the data
plane through an MCP server: a separate process, a bearer token, JSON-RPC over
HTTP. This phase builds the **in-app** counterpart — an assistant that lives on
the VoltEdge web frontend, watches what the user is doing on the map, and
proactively offers the action they're most likely about to take. The headline
example, in the user's words: *"when [the user has] snapped a line to a pole or
vice versa, suggest the establishment of a relationship. User clicks OK, [it]
executes."*

Crucially, this is a **second front door onto the same tools**, not a competing
tool system. Where MCP wraps the engine functions in JSON-RPC and authenticates
with a token, the assistant calls the *same exported engine functions* in-process
under the user's ordinary cookie session — so every suggestion and every write is
attributed to the logged-in user and bounded by exactly their RBAC, with no
second auth path. (See docs/phase-18-mcp.md for the tool set this shares.)

## Decisions (locked with the user)

- **No LLM in the hot path.** The snap→suggest→confirm→establish loop is fully
  deterministic: the editor already knows the feature involved, the `suggest_*`
  engines already rank the rule-legal candidates by the domain rules, and the
  establish routes already exist. There is no natural-language step anywhere in
  it, so putting a model there would only make it slower and non-deterministic.
  Slice 1 ships this loop with **zero model dependency**.
- **The LLM is a later, separate surface, and a small local one.** A model is
  only needed when the user *types* a freeform request ("connect this transformer
  to the nearest feeder"). Even then it does two cheap jobs — parse intent, pick
  a tool + fill args — while the deterministic engines do the actual domain
  reasoning. So a **small local model on Ollama / LM Studio** suffices, behind an
  `LLMProvider` interface that can later be pointed at Claude or OpenAI. This is
  Slice 3, deferred until the deterministic loop is in.
- **Execution reuses the existing establish routes.** `POST /connectivity` and
  `POST /relationships` already exist, session-authed, RBAC-gated on
  `connectivity:commit` / `relationships:commit`, and the frontend already calls
  them from `connect-confirm-panel.tsx`. The assistant doesn't add a write path;
  it *prebuilds the request body* for one of those routes and the frontend
  re-POSTs it on confirm. One write path, re-enforced RBAC, existing actor
  attribution — nothing new to secure.
- **`suggest_*` needs a new browser-reachable endpoint; nothing else does.** The
  suggest engines (`connectivity.SuggestConnections`,
  `relationship.SuggestRelationships`) were wired only into MCP in Phase 18 —
  they have no REST route. So Slice 1's one piece of new backend surface is
  `POST /assistant/suggestions`, which runs those engines and returns their
  candidates as executable actions.
- **Suggestions are gated on the *commit* permissions, not the read ones.** The
  suggest engines only read, but the endpoint exists to propose *executable*
  actions — there's no point suggesting an action the user could never carry out.
  So `/assistant/suggestions` checks `connectivity:commit` / `relationships:commit`
  (independently, so a user with one but not the other gets exactly the half they
  can act on). A user holding only `:read` sees no chips and can browse candidates
  through the MCP suggest tools instead.
- **Writes never auto-execute.** Every proposed action surfaces as a confirmation
  chip; the human's click is the commit — mirroring the Phase 18 client-side
  `readOnlyHint` write-confirm contract.
- **Shared tool registry (Slice 2).** Rather than redefine the tool schemas for
  the LLM surface and risk drift from MCP, the tool contract (name, schema,
  required perm, handler) will be extracted into one package both `mcpserver` and
  `assistant` consume. Deferred to Slice 2 — Slice 1's deterministic suggestions
  don't touch the full registry.

## Slice plan

- **Slice 1 — Reflexive suggestions (deterministic, no LLM). ← this slice.**
  `POST /assistant/suggestions` + the frontend confirmation chips + the
  snap/edit trigger. Delivers the whole headline loop.
- **Slice 2 — Shared tool registry.** Extract the Phase 18 tool defs into a
  package `mcpserver` and `assistant` both consume, so the two front doors
  provably share one tool contract.
- **Slice 3 — Chat orchestrator + `LLMProvider` interface.** `POST /assistant/chat`
  runs a single-turn tool-router over a small local model (OpenAI-compatible
  adapter → Ollama/LM Studio). Read tool-calls execute inline; write tool-calls
  come back as a proposed action through the *same* confirmation chip as Slice 1.
- **Slice 4 — More providers.** Anthropic + hosted-OpenAI adapters behind the
  interface; tool/model polish.

## Slice 1 — what was built

### Backend

`api/internal/assistant/suggest.go` (new package) exposes one route,
`POST /assistant/suggestions`, mounted at `/assistant` behind `RequireAuth`
(`api/cmd/server/main.go`) — not a single RBAC gate, because the endpoint reads
two capabilities at once and gates each independently inside the handler.

Request:

```json
{ "feature_class": "mv_conductor", "feature_id": "…",
  "kinds": ["connection", "relationship"],   // optional; empty = both
  "target": { "feature_class": "pole", "feature_id": "…" },  // optional narrowing
  "limit": 8, "radius_meters": 1000 }         // optional; pass through to engines
```

For each capability the session user holds the `:commit` permission for, the
handler runs the matching suggest engine, then maps every candidate into a
**proposed action**:

```json
{ "kind": "relationship", "tool": "establish_relationship",
  "title": "attached_to Pole 12", "detail": "Pole attachment",
  "target": { "feature_class": "pole", "feature_id": "…", "name": "Pole 12" },
  "distance_meters": 4.2,
  "args": { "origin": {…}, "destination": {…}, "kind": "attached_to" } }
```

The `args` field is the exact body the establish route accepts, prebuilt from the
winning rule — for a connection, `from`/`to` with the rule-implied terminals
(a nil terminal is omitted, not sent as null); for a relationship, `origin`/
`destination` assigned so the source and candidate land on the sides the
relationship class expects (the engine reports the *candidate's* role, so the
handler flips them when the candidate is the origin), plus the `kind` to
disambiguate. Actions from both engines are merged, ranked nearest-first
(geometric candidates before non-geometric, closer before farther, stable within
ties), and capped to 8.

Errors from the suggest engines (almost always an unknown feature ref) map to
`400` with the engine's message — a client condition, not a server fault.

### Frontend

- `web/src/stores/assistant-store.ts` — holds the current `source` feature and
  its `suggestions`; `requestSuggestions(source)` POSTs to the endpoint and
  swallows failures silently (proactive help must never interrupt the user; a
  user lacking commit perms just gets an empty list). `remove(i)` / `clear()`
  prune chips.
- `web/src/components/map/assistant-suggestions.tsx` — a floating stack of chips
  (bottom-left), self-contained like `connect-confirm-panel.tsx`. **Establish**
  POSTs the prebuilt `args` to `/connectivity` or `/relationships`, invalidates
  the connections/relationships queries for both features, toasts success (and
  any relationship `plausibility_warning` as a warning, same as the manual
  panel), then removes the chip. **Dismiss** just drops it.
- **Trigger:** two entry points, since a feature reaches the map two ways.
  `feature-edit-form.tsx`'s Save, after a successful geometry change
  (`dirtyGeometry` was set), fire-and-forgets `requestSuggestions` for the saved
  feature (an attribute-only edit skips it — only new geometry moves a feature
  near new candidates). `feature-create-form.tsx`'s Save does the same after a
  successful create when the draft carries map geometry (`draft.geometry`), so a
  *just-drawn* feature — e.g. an LV conductor snapped to a pole — surfaces
  suggestions the first time it lands, not only on a later edit. A diagram-only
  draft (no map geometry) skips it. Both render through `assistant-suggestions.tsx`,
  mounted in `web/src/routes/map.tsx` beside `ConnectConfirmPanel`.

Because suggestions read the *committed* geometry, the trigger fires on save
(when the new geometry is persisted), not on the in-map snap (which only produces
a candidate geometry) — so the engines rank against what's really in the
database. The just-snapped feature is the nearest candidate, so it naturally
surfaces as the top chip; the optional `target` filter can pin results to one
specific snapped feature when a future trigger knows it.

## Slice 2 — what was built

The point of this slice is to make the "second front door onto the *same*
tools" claim structurally true rather than aspirational: before it, the MCP
server (`internal/mcpserver`) defined the tool set — names, JSON schemas, RBAC
permissions, handlers — and the assistant would have had to redefine it for the
LLM surface, free to drift. So the whole tool contract was extracted into one
package both consume.

### The shared package: `internal/agenttools`

`api/internal/agenttools` now owns the tool contract and its execution, split
the same way the old `mcpserver` files were:

- `registry.go` — the shared types (`Tool{Name, Description, InputSchema,
  Annotations, RequiredPerm, Handler}`, `Annotations`, the `Handler` func type),
  the built-once ordered `registry`, and the public surface: `All()` (the tools
  in advertised order), `Get(name)`, and **`Execute(ctx, db, user, name, args)`**
  — the single choke point that looks a tool up, enforces its `RequiredPerm`
  against the acting user, threads that user onto the context (`auth.WithUser`,
  so the engines attribute the write), and runs the handler. It returns
  `ErrUnknownTool` (wrapped) and `*PermissionError` so a caller can map them onto
  its own protocol's failure shapes.
- `tools.go` — the 16 definitions (`readTools()` + `writeTools()`), moved intact.
  The old `markReadOnly` stamping is baked into `buildRegistry`: a read tool with
  no annotations of its own gets `readOnlyHint:true`, write tools keep the
  annotations they declare — so **both** front doors advertise identical hints.
- `handlers.go` — the `toolXxx` handlers + their output wire types, moved
  verbatim (each still a thin adapter over the same exported engine function).

`Tool`'s JSON tags match the MCP tool object exactly (`RequiredPerm`/`Handler`
are `json:"-"`), so a `Tool` serializes straight into a tools/list response.

### `mcpserver` now consumes it (transport only)

`mcpserver` keeps everything transport — JSON-RPC framing, the MCP payload
shapes, bearer auth, the streamable-HTTP binding — and drops everything it used
to duplicate: the `tool`/`toolDef`/`toolAnnotations` types, the
`registerReadTools`/`registerWriteTools`/`markReadOnly` registration, the
`orderedToolNames` list, and the `Server.tools` map are all gone. `Server` now
holds just the `*bun.DB`. `handleToolsList` returns `agenttools.All()` verbatim;
`handleToolsCall` calls `agenttools.Execute` and maps its errors onto MCP's two
failure shapes (`ErrUnknownTool` → JSON-RPC invalid-params, `PermissionError`
and any execution error → in-band `isError`). `mcpserver/tools.go` is now just a
pointer comment to `agenttools`. Behavior is unchanged — same tools, same order,
same permissions, same error surfaces.

### `assistant` now consumes it too

To make "both consume" literal today (not just once Slice 3 lands), the
assistant gained `GET /assistant/tools` (`internal/assistant/tools.go`) — the
shared registry served to the frontend, but **scoped to the session user**: only
tools whose `RequiredPerm` the user holds (plus any needing none) are returned,
so the assistant never advertises an action the user couldn't run. This is the
catalog the Slice 3 chat orchestrator will hand the model. The Slice 1
suggestions endpoint deliberately still calls the suggest engines directly
rather than through `Execute`, because it needs their typed candidate structs to
prebuild each establish body; `Execute` is for the freeform LLM path.

## Slice 3 — what was built

The freeform-chat surface: the first place a language model enters the assistant,
and deliberately the *only* place. A small local model routes a typed request to
the tools the session user already holds; the deterministic engines behind those
tools still do all the domain reasoning. The model is never a security boundary —
`agenttools.Execute`'s permission gate is, exactly as for the MCP front door — and
never a writer: it can *propose* a write, but the human's confirm click is the
commit, the same contract as the Slice 1 chips.

### The model seam: `internal/assistant/llm`

One interface the orchestrator depends on, `llm.Provider` (`Complete(ctx, Request)
(*Completion, error)` + `Name()`), plus the transport-neutral types that cross it
(`Message`, `ToolSpec`, `ToolCall`, `Completion`). `llm.NewFromEnv()` follows the
codebase's per-package `NewXFromEnv` convention:

- `ASSISTANT_LLM_BASE_URL` — OpenAI-compatible root (Ollama `…:11434/v1`, LM Studio
  `…:1234/v1`). **Unset ⇒ chat disabled**: `NewFromEnv` returns `(nil, nil)`, the
  server boots fine, and `/chat` reports itself unconfigured.
- `ASSISTANT_LLM_MODEL` (default `llama3.1`, must be tool-calling capable),
  `ASSISTANT_LLM_API_KEY` (optional; local servers ignore it), `ASSISTANT_LLM_TIMEOUT`
  (default 60s). A malformed *set* value (bad duration) is the only hard error.

Slice 3 ships one implementation, `openai.go` — the OpenAI Chat Completions dialect,
which is what a small local model exposes natively. It maps our types to/from the
wire (`tool_choice:"auto"`, temperature 0 for deterministic routing, parsing each
tool call's JSON-string arguments into raw JSON), one request per turn, no
streaming/retries. Hosted Anthropic/OpenAI adapters are Slice 4 and only have to
satisfy `Provider` — nothing above this package changes.

### The orchestrator: `POST /assistant/chat` (`chat.go`)

The frontend owns the visible transcript and resends it each turn (the backend
keeps no chat state). One call runs a short tool-routing loop over the provider:

- The model is offered only `scopedTools(user)` — the shared registry filtered to
  what this user may run — so it can never propose an action outside their RBAC.
- A **read** tool call executes inline via `agenttools.Execute` and its result is
  fed back so the model can answer or chain another read, bounded by
  `maxToolRounds` (4) and each fed-back result trimmed to a byte budget so a broad
  search can't blow a small model's context. If the loop hits its cap mid-tool-use,
  one final tool-less turn forces a spoken answer.
- A **write** tool call (detected via `Tool.Writes()`, the new `readOnlyHint`
  helper on the shared registry) short-circuits the loop: it is *not* executed but
  returned as a `proposedWrite{tool, title, detail, args}` — mirroring the Slice 1
  action shape — for the user to confirm.

Response: `{ reply, actions[], tool_runs[] }` — the spoken reply, any writes
awaiting confirmation, and (for transparency) the reads it ran.

### The commit path: `POST /assistant/execute` (`execute.go`)

Where a proposal is confirmed. It runs one named tool with the exact args the
proposal carried through the *same* `agenttools.Execute` choke point, so the write
is re-checked against the user's commit permission and attributed to them — no
more powerful than the REST route the tool wraps. Keeping confirm a single generic
endpoint (rather than mapping each write tool to its own REST route on the
frontend) means any chat-proposed write — `create_feature`, `update_feature`,
`establish_connection`, `establish_relationship` — commits the same way. Typed
errors map to statuses: unknown tool → 404, missing permission → 403, engine
failure → 400.

`POST /chat` and `POST /execute` carry `RequireCSRF` on top of the mount's
`RequireAuth` (they're state-changing/model-invoking); the read-only `/suggestions`
and the GETs keep the Slice 1 posture. `GET /assistant/status` reports
`{chat_enabled, model}` so the frontend knows whether to offer chat at all.

### Frontend

- `web/src/stores/assistant-chat-store.ts` — the transcript + `send(text)` (POSTs
  `/assistant/chat`, appends the reply), `resolveAction(msg, action)` (prunes a
  proposal once confirmed/dismissed), `toggle`/`open`. Not persisted.
- `web/src/hooks/use-assistant.ts` — `useAssistantStatus()` reads `GET /status`
  (cached indefinitely) so the toolbar only shows the chat entry when a model is
  configured.
- `web/src/components/map/assistant-chat.tsx` — a floating chat window (opened from
  the map toolbar's **Assistant** button). Renders the transcript; a reply's
  proposed writes render as inline confirmation cards whose **Confirm** POSTs to
  `/assistant/execute`, then invalidates the connections/relationships/features
  query roots and toasts (surfacing a relationship `plausibility_warning`). Mounted
  in `map.tsx` beside `AssistantSuggestions`.

## Slice 4 — what was built

More providers behind the same `llm.Provider` interface, so a deployment can
point the chat surface at a hosted model without touching anything above the seam
— the orchestrator, the tool routing, the confirm contract are all unchanged.

- **Hosted OpenAI needs no new code.** The Slice 3 `openai.go` adapter already
  speaks the OpenAI Chat Completions dialect, so hosted OpenAI is just a config:
  `ASSISTANT_LLM_BASE_URL=https://api.openai.com/v1` + `ASSISTANT_LLM_API_KEY`.
- **Anthropic adapter (`anthropic.go`).** A second `Provider` for the Claude
  Messages API. It absorbs the three ways that dialect differs from OpenAI's: the
  system prompt is a top-level field (not a message), tool calls/results are
  content *blocks* (`tool_use` / `tool_result`) rather than a side channel, and a
  tool result rides on a *user* message — so the adapter lifts `RoleSystem` out,
  emits assistant `tool_use` blocks, and **coalesces consecutive `RoleTool`
  results into one user message** (the API requires one turn's tool results to
  arrive together). Everything above the seam still sees only the neutral types.
- **Provider selection (`NewFromEnv`).** New `ASSISTANT_LLM_PROVIDER`
  (`openai` default / `anthropic`). Empty still means "openai if a base URL is
  set, else disabled" (Slice 3 behaviour preserved). openai requires a base URL;
  anthropic requires an API key and defaults its base URL and a small Claude
  model (`claude-haiku-4-5-20251001`) with `ASSISTANT_LLM_MAX_TOKENS` (default
  1024). An unknown provider or a backend missing its required var is a hard
  boot error; an unconfigured assistant is still `(nil, nil)` → chat disabled.

No frontend or orchestrator change — the model is swapped entirely under
`llm.Provider`. `.env.example` gains the provider var and an Anthropic example.

## Follow-up fix — soft-deleted features on the map (Martin tile functions)

Surfaced while testing Slice 1: drawing an LV conductor, then later clicking a
feature that turned out to be soft-deleted, made `POST /assistant/suggestions`
return `400 feature is deleted` (the suggest engine's `resolveEndpoint` reads the
real row and refuses a deleted one). The deeper cause wasn't the assistant at
all — **the map was still drawing soft-deleted features**, so a deleted feature
was clickable in the first place.

Root cause: Martin auto-published each compiled feature class's **raw `gis.<code>`
table** directly, and a raw table source has no `WHERE` clause, so it served
every row including soft-deleted ones (`deleted_at` set). The CRUD read path
filters `deleted_at IS NULL`, but tiles never went through it. The frontend's
`deleted-features-store` was only a **client-side, non-persisted mask** — a
reload (or any other user's browser) wiped it and the deleted feature came
straight back. This had been recorded as open since Phase 3
(`docs/phase-3-delete-confirmation.md`).

The fix makes Martin serve tile **functions** instead of tables:

- **`migrations/0038_add_tile_functions.sql`** adds `gis.sync_tile_function(table,
  srid)`, which generates `gis.<code>(z,x,y) RETURNS bytea` — an `ST_AsMVT` over
  the table filtered on `deleted_at IS NULL`. It introspects the table's columns
  so the MVT carries every attribute as a property exactly as the raw table
  source did, minus `geom` (which becomes the tile geometry) and `deleted_at`.
  The function is named **exactly like the table**, so its Martin source id (the
  bare, lowercased class code) is unchanged. The migration backfills every
  already-compiled geometric class.
- **`api/internal/compiler`** calls the same helper on every compile (a fresh
  `CREATE TABLE` or a field `ADD COLUMN` alike), so the helper is the single
  source of truth for a tile function's shape and the compiler keeps it in step
  with the table.
- **`infra/martin/config.yaml`** (new, mounted by `docker-compose.yml`) sets
  `auto_publish.tables:false` and publishes functions from schema `gis` with
  `source_id_format:'{function}'`; `connection_string:${DATABASE_URL}` keeps the
  existing env wiring. The `--config` flag is added alongside the existing
  `--cache-size 0`.
- **No frontend change.** `new MVT()` parses all layers and the catalog/source
  ids are identical, so the per-class `VectorTileLayer` wiring is untouched. The
  `deleted-features-store` stays as a pure instant-feedback optimization (its
  doc comment updated to say so), no longer load-bearing for correctness.

`gis.territories` was also an auto-published table (already filtered out of the
feature-class catalog by `TERRITORY_TILE_SOURCE_ID`); with `tables:false` it is
simply no longer published, which is correct — territories render through their
own `/territories/boundaries` overlay, never Martin tiles.

## Follow-up polish — suggestion surface UX (compact panel + noise reduction)

The first cut of the suggestion surface (Slice 1) rendered up to eight *tall*
cards in an unbounded floating column, so a real edit buried the left half of the
map under a stack of chips — and the stack itself was redundant: the relationship
engine returns several distinct rule kinds against the *same* candidate (e.g. a
support **and** a composite), which listed flat read as duplicate rows, and the
nearest-N fallback surfaced candidates 600–750 m away as "suggestions". Two-part
fix, decided with the user (compact collapsible panel + backend dedupe/cap):

- **Backend (`api/internal/assistant/suggest.go`).** `rankAndCap` → `groupAndCap`.
  It first drops geometric candidates past `suggestMaxDistanceMeters` (200 m,
  tunable — nil-distance non-geometric candidates are never distance-filtered),
  then collapses every remaining action onto its target feature into a
  `suggestionGroup{target,label,distance_meters,actions[]}` (nearest distance
  across the group; actions ordered connection-first then nearest-first), ranks
  the groups nearest-first, and caps to `suggestLimit` (now a cap on candidate
  *features*, not raw actions). No legal action is hidden — just grouped.
- **Frontend.** `assistant-store.ts` gains `SuggestionGroup` and swaps `remove`
  for `remove` (dismiss a whole group) + `removeAction` (drop one established
  action, pruning the group when its last action goes).
  `assistant-suggestions.tsx` is rebuilt as one **compact, height-bounded
  (`max-h-64`), scrollable, collapsible** panel: a header with a live group count
  and a collapse toggle, then one row per candidate feature. A single-action
  group establishes straight from its row (a `+` button); a multi-action group's
  row expands to list its options, each with its own Establish. The map stays
  visible.
- **Click-to-highlight (follow-up).** Clicking a suggestion row's feature label
  now highlights that candidate on the map — it calls
  `selectionStore.select(target, { focus: true })`, so the map draws the
  selection halo on it and pans to it if it's off-screen, exactly like a
  feature-panel connection/relationship counterpart row. It's a "show me which
  feature this is" affordance only: it establishes nothing and leaves the panel
  up (visibility keys off the assistant store's source, not selection). The
  establish `+` and the expand chevron stay separate; a multi-action row both
  highlights and expands on click.

No new permission surface, no route change — same `POST /suggestions`, same
establish routes on confirm.

## Status

**Phase 19 is closed — the user verified the suggestion surface and signed off
(2026-08-22).** The compact grouped panel + dedupe/cap + click-to-highlight land
the deterministic snap→suggest→establish loop, and the tile-function fix keeps
soft-deleted features off the map. All slices are built:

- **Slice 1 — done.** Reflexive deterministic suggestions (endpoint + store +
  chips + save trigger), later reworked into the compact collapsible grouped
  panel and given click-to-highlight (see follow-ups above).
- **Slice 2 — done.** Shared `agenttools` package (registry + `Execute`),
  `mcpserver` refactored to consume it, `assistant` via the session-scoped
  `GET /tools`.
- **Slice 3 — done.** `llm` provider seam + OpenAI-compatible adapter,
  `POST /chat` orchestrator, `POST /execute` commit path, `GET /status`, wiring.
- **Slice 4 — done.** Anthropic adapter + provider selection in `NewFromEnv`;
  hosted OpenAI via the existing adapter (config only).
- **Tile-function fix — done.** Migration 0038 + compiler wiring +
  `infra/martin/config.yaml` + compose mount.

### Config caveat (not owed work)

The **LLM chat** path (Slices 3–4) is inert unless `ASSISTANT_LLM_*` env is set:
with it unset the API boots fine ("assistant chat disabled"),
`GET /assistant/status` reports `chat_enabled:false`, and the toolbar Assistant
button stays hidden. Nothing is pending here — to exercise chat later, point
`ASSISTANT_LLM_BASE_URL` at a tool-calling model (Ollama/LM Studio) or set
`ASSISTANT_LLM_PROVIDER=anthropic` + `ASSISTANT_LLM_API_KEY`, then a read question
answers via inline tool runs and a write proposal comes back as a confirm card
that commits only through `/assistant/execute` (a `:read`-only user's write is
refused 403 at execute time). The tile-function fix rides on migration 0038 +
`docker compose up -d martin` picking up the config mount, both part of a normal
deploy.
