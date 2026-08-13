# Phase 4 (slice 1) — the network trace engine

**Goal:** walk `runtime.feature_connectivity` edge by edge, per a named
`metadata.trace_configurations` profile, and answer "what does this
feature reach" (or "what reaches this feature") -- the first slice of
Phase 4, scoped to the four raw trace directions
`docs/metadata-core.md` describes with real barrier-stopping against a
real switching device. Isolation and loop/mesh detection (which
compose two of these primitives) are explicitly not built this pass.
**Status:** done and verified against a real Postgres/Martin/API/Vite
stack, including toggling a real switch open and closed and confirming
the trace result changes live.

## Why this slice exists, and the scope agreed before writing code

Phase 3's map loop closed out with all four planned CRUD slices done
(`docs/phase-3-snapping.md`) plus two small open items
(`docs/phase-3-relationships-ui.md`, `docs/phase-3-floating-panel.md`).
Phase 4 -- "the trace engine, built against the terminals/network_rules/
trace_configurations metadata Phase 1 already created and the
connectivity Phase 2 establishes" -- was next on the roadmap `README.md`
set out from the start, and directly answers the user's own framing of
what tracing means here: "energizing a feature" or "opening/closing a
switching device" isn't new write infrastructure at all -- opening a
switch is just an attribute edit through the CRUD engine Phases 2-3
already built. What was actually missing was the read side: something
that walks `feature_connectivity` and respects a device's live state
while doing it. `docs/phase-2-connectivity.md` said as much outright:
"No trace engine yet -- `feature_connectivity` is exactly what Phase 4's
trace engine will walk edge by edge, but nothing reads it that way
today."

Before writing any code, three scope questions got settled:

- **Core primitives only this pass** -- the four raw directions
  (`connected`/`downstream`/`upstream`/`shortest_path`) with real
  barrier-stopping. Isolation (upstream + downstream composed) and loop
  detection (connected run from two sources) are deferred -- they need
  the primitives proven against real data first, not designed
  speculatively alongside them.
- **`cross_transformers`/`respect_phase` accepted but stubbed** --
  `trace_configurations` carries both flags, and this pass reads and
  stores them, but doesn't enforce either. What actually marks a
  transformer boundary, and how phase capability intersects hop to hop,
  are real design questions this pass deliberately didn't answer under
  time pressure -- only `stop_at_open_devices` (backed by a real
  `trace_barrier_rules` condition) is enforced.
- **A minimal map trigger + highlight**, not backend-only -- matching
  how every other slice in this app has closed the loop with something
  demoable rather than only curl-verified.

## A gap this slice had to close

`metadata.trace_configurations`/`trace_barrier_rules` existed since
Phase 1 but were empty -- no `TRACE_DIRECTION` domain, no
`DEVICE_STATE` domain, and (per `migrations/0009`/`0014`'s own
illustrative comments) no `DIST_SWITCH` feature class, the exact
example those migrations named ("on feature_class DIST_SWITCH, if
field device_state equals domain value OPEN, stop") but never actually
built. `seed/switching_device_example.sql` and
`seed/trace_configurations_example.sql` build exactly that: a real
`DIST_SWITCH` junction feature class (two terminals, T1 line-side/T2
load-side, a `device_state` field domained `CLOSED`/`OPEN`), the
`TRACE_DIRECTION` domain, and four `trace_configurations` rows (one per
raw direction) plus `trace_barrier_rules` wiring `DIST_SWITCH.device_state
= OPEN` to halt the two barrier-respecting configs.

Building a real chain to trace over -- `conductor_1 -> switch -> conductor_2
-> transformer` -- surfaced a second, more interesting gap during
verification, not a bug in the engine so much as a real modeling
question this pass hadn't confronted yet: **`feature_connectivity`'s
`from`/`to` columns carry no inherent flow direction.** They're just
"whichever side initiated the establish request" -- `docs/metadata-core.md`
never claimed otherwise, and `api/internal/connectivity` treats both
sides symmetrically. But `DOWNSTREAM`/`UPSTREAM` in this pass are
defined as a literal walk of `from`->`to` (or the reverse) through that
table -- so which side got established as `from` for each leg of a
chain determines whether a directed trace can walk through a device at
all. The first version of the seeded topology established every leg as
"conductor is from, junction terminal is to" (mirroring
`seed/connectivity_example.sql`'s own `MV_CONDUCTOR -> DIST_XFMR.T1`
convention) -- which meant `DIST_SWITCH` was never the `from` side of
any edge, so a downstream walk reached the switch and had nothing to
follow past it, with or without a barrier. The fix: a fourth
`network_rules` row, `DIST_SWITCH.T2 -> MV_CONDUCTOR` (the switch's
*load-side* terminal as the legal `from` side), and re-establishing
that one leg of the topology in that direction. `seed/switching_device_example.sql`
documents this explicitly rather than leaving it to be rediscovered --
a junction device that's meant to sit inline on a directed trace needs
its output terminal modeled as a legal `from` side, not just its input
terminal as a legal `to` side.

A second, unrelated bug came from the same verification pass: `checkBarrier`
initially read a visited feature's attribute value straight off
`crud.GetFeature`'s row map (`row[fieldName]`), which returned `nil`
every time -- `crud.GetFeature` nests every custom column under
`row["attributes"]` (`crud/sql.go`'s `formatRow`), not the map's top
level alongside `id`/`status`/`geometry`. A barrier that can never
match is worse than an obviously broken one: the switch's `device_state`
was silently ignored regardless of its real value until this was caught
and fixed by tracing through with the switch actually toggled open,
not just closed.

## What was built

- **`seed/switching_device_example.sql`** (new) -- `DIST_SWITCH`
  feature class (junction, two terminals), `DEVICE_STATE` domain, and
  four `network_rules` rows (both directions into the switch's two
  terminals, plus the load-side-as-`from` rule the directional-walk gap
  above needed).
- **`seed/trace_configurations_example.sql`** (new) -- `TRACE_DIRECTION`
  domain (`CONNECTED`/`DOWNSTREAM`/`UPSTREAM`/`SHORTEST_PATH`), four
  `trace_configurations` rows (one per direction, `DOWNSTREAM_TRACE`/
  `UPSTREAM_TRACE` with `stop_at_open_devices = true`, `CONNECTED_TRACE`/
  `SHORTEST_PATH_TRACE` without -- matching `docs/metadata-core.md`'s
  own "Feeder connectivity audit" sample: an audit shouldn't silently
  stop just because a switch happens to be open right now), and
  `trace_barrier_rules` wiring the switch's `device_state = OPEN` to
  the two barrier-respecting configs.
- **`api/internal/metadata`** -- `TraceConfiguration`/`TraceBarrierRule`
  structs and `LoadTraceConfiguration`/`LoadTraceBarrierRules` loaders,
  mirroring `NetworkRule`/`LoadNetworkRules`'s existing shape exactly.
  A small `DomainValueCode` helper resolves a domain value id (e.g.
  `trace_configurations.direction_value_id`) to its stable code.
- **`api/internal/trace`** (new package) -- the engine itself:
  - `resolveNode`/`resolveBarriers` mirror `api/internal/connectivity`'s
    own `resolveEndpoint` pattern for validating a request's start/target
    feature and pre-resolving every barrier condition for the chosen
    config, once per run.
  - `bfs` walks breadth-first from the start node, direction-filtered
    (`neighborsOf`): `DOWNSTREAM` only follows an edge where the current
    node is the `from` side, `UPSTREAM` only the reverse, anything else
    treats every edge touching a node as undirected. Barrier-checked at
    each hop (`checkBarrier`, live against the feature's *current*
    attribute value -- never baked into the graph, per
    `docs/metadata-core.md`) -- a matching node is included in the
    result but not expanded past. Always tracks parent pointers (cheap
    even when unused) so `SHORTEST_PATH` can reconstruct a real path
    once its target is discovered, rather than returning everything
    visited along the way.
  - No persistent in-memory graph -- each hop's neighbors are queried
    fresh from `runtime.feature_connectivity`, same stateless-per-request
    philosophy `api/internal/connectivity` already established, not the
    "in-memory graph" `docs/metadata-core.md` describes as a longer-term
    role. Revisit if/when trace performance on a real-sized network
    actually demands it.
  - `POST /trace` (`{trace_configuration, start, target?}` ->
    `{trace_configuration, direction, start, features, barriers,
    path_found?}`), mounted read-only, no write path at all -- a trace
    never establishes or revokes anything, it only walks what
    `api/internal/connectivity`/`relationship` have already committed.
- **`src/stores/trace-store.ts`** (new) -- the result, shared between
  the trigger and the map/banner the same cross-component pattern every
  other workflow store in this app already uses.
- **`src/components/map/feature-panel.tsx`** -- two new header icon
  buttons, "Trace downstream"/"Trace upstream", starting from whichever
  feature is selected.
- **`src/components/map/voltedge-map.tsx`** -- a dedicated trace
  highlight source/layer (not folded into the existing selected/connect
  highlight source -- that one's cleared and rebuilt synchronously every
  render off already-fetched `useFeature` data; a trace result is an
  arbitrary-length list fetched asynchronously, and sharing one source
  between a sync and an async effect would mean whichever happened to
  run last on a given commit silently clobbers the other's features).
  Every reached feature renders in a new `HighlightRole` color
  (`highlightStyleForFeature` in `map-layers.ts`, `trace` lime-green,
  `trace-barrier` red -- distinct from every existing role).
- **`src/components/map/trace-result-banner.tsx`** (new) -- a small
  floating summary (feature count, barrier count) with the Clear action,
  fixed bottom-center, clear of the floating attribute panel's own
  default corner and the geometry-edit toolbar's top-center position.

## How this was validated

Driven against a real Postgres/Martin/API/Vite stack, first via curl
directly against the trace engine (isolating the two bugs above from
any frontend concern), then via Playwright end to end.

- `curl -X POST /trace` with `DOWNSTREAM_TRACE` from `conductor_1`,
  switch `CLOSED`: reached all four features (`conductor_1`, the
  switch, `conductor_2`, the transformer), zero barriers -- confirmed
  the directional-walk fix.
- Same request with the switch `PATCH`ed to `OPEN`: reached only
  `conductor_1` and the switch, with the switch reported as a barrier --
  confirmed the `crud.GetFeature` attributes-nesting fix.
- `UPSTREAM_TRACE` from the transformer (switch closed) reached the
  same four features walking the reverse direction.
- `CONNECTED_TRACE` from the transformer, switch deliberately left
  `OPEN`: still reached all four -- confirmed `stop_at_open_devices =
  false` really does skip barrier evaluation entirely for that config,
  not just report it differently.
- `SHORTEST_PATH_TRACE` from `conductor_1` to the transformer returned
  the real reconstructed path (not just the visited set) with
  `path_found: true`.
- Error paths: an unknown `trace_configuration` code returns a real 404;
  a `SHORTEST_PATH_TRACE` request with no `target` returns a real 400
  naming exactly what's missing.
- End to end in the browser: selecting `conductor_1` and clicking
  "Trace downstream" highlighted all four features lime-green with a
  summary banner; editing the switch's `device_state` to `OPEN` through
  the existing Phase 3 edit form (no new UI needed for this -- exactly
  the point) and re-running the trace correctly stopped at the switch,
  rendered red, with the banner reporting the stop; toggling back to
  `CLOSED` and re-running reached all four again; "Trace upstream" from
  the transformer reached the same four features walking backward.
  Seed data was restored to its original `CLOSED` state before
  finishing.

## Decisions made

- **`feature_connectivity`'s `from`/`to` needs to represent real flow
  direction for a directed trace to mean anything, and that's a
  metadata-authoring responsibility, not something the trace engine
  should paper over.** A junction device meant to sit inline on a
  directed trace needs both a legal "receives on this terminal" rule
  and a legal "sends from this terminal" rule -- documented explicitly
  in `seed/switching_device_example.sql` rather than left implicit.
- **No persistent in-memory graph.** Every hop queries
  `runtime.feature_connectivity` fresh, matching
  `api/internal/connectivity`'s own stateless-per-request philosophy.
  `docs/metadata-core.md` describes an in-memory graph as the
  longer-term role; this pass's seed-sized network gives no signal yet
  that it's actually needed.
- **`cross_transformers`/`respect_phase` stubbed, not designed
  speculatively.** Both raise real questions (what marks a transformer
  boundary in a network_role-driven model that doesn't currently
  distinguish "transformer" from "any other junction"? how does phase
  capability intersect hop to hop?) that deserve their own pass once
  there's a second real motivating feature class or use case to design
  against, not answers invented under this slice's time pressure.
- **A dedicated trace highlight source, not the existing one.** The
  existing selected/connect highlight effect is synchronous
  (fixed-size, already-fetched `useFeature` results); a trace result is
  an async, arbitrary-length fetch. Sharing one `VectorSource` between
  a sync-every-render effect and an async one is a real race two
  independent effects can't safely coordinate around without one
  clobbering the other -- a second source sidesteps the problem
  entirely rather than trying to merge write ownership.

## What's still open

- **Isolation and loop/mesh detection** -- compose two of this pass's
  primitives (`docs/metadata-core.md`'s own framing); deferred until
  the primitives have more real-world exercise.
- **`cross_transformers`/`respect_phase` enforcement** -- accepted and
  stored, not enforced; see "Decisions made" above.
- **No persistent/cached graph** -- acceptable at this pass's data
  scale; revisit if a real network's size ever makes per-hop querying
  a real latency concern.
- **`GET /feature-classes` / schema endpoints have no way to discover
  which `trace_configurations` exist** -- `feature-panel.tsx`'s two
  trigger buttons hardcode `DOWNSTREAM_TRACE`/`UPSTREAM_TRACE` by code
  rather than listing configs from the metadata. Fine for this pass's
  four fixed, seeded configs; would need a real endpoint if
  `trace_configurations` ever becomes something an admin authors
  freely.
- Everything else tracked in `docs/open-items.md` is unchanged by this
  slice.

## Try it yourself

`task infra-up && task migrate && task api-dev`, then apply the two new
seed files in order and compile the new feature class:

```
psql "$DATABASE_URL" -f seed/switching_device_example.sql
psql "$DATABASE_URL" -f seed/trace_configurations_example.sql
cd api && go run ./cmd/compile DIST_SWITCH
```

Then `cd web && npm run dev`. Open `http://localhost:5173`, create a
`DIST_SWITCH` and two `MV_CONDUCTOR`s connecting it to any two other
junctions (or reuse `docs/phase-2-connectivity.md`'s own seeded
topology), establish real `POST /connectivity` rows for the chain
(mind the directional-walk note above), then:

- Select a feature in the chain, click "Trace downstream" (or
  "Trace upstream") in the panel header -- every reached feature
  highlights lime-green, with a summary banner at the bottom-center.
- Edit the switch's `device_state` to `OPEN` (the existing pencil Edit
  form -- no new UI needed) and re-run the trace -- it stops at the
  switch, which renders red instead.

`npm run build && npm run lint` and `go build ./... && go vet ./... &&
gofmt -l .` should all finish clean.
