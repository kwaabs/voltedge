# Phase 4 (slice 4) — closing out the trace engine: target-class stops, isolation, a real service point

**Goal:** close out Phase 4 with the utility-specific trace types a
real electric utility GIS needs on top of the four raw primitives
slice 1 built (`docs/phase-4-trace-engine.md`) -- trace to nearest
transformer, transformer to source, LV network, and isolation -- by
generalizing the engine rather than special-casing each one, and by
adding the one feature class (a meter/service point) the demo network
was missing to make any of them meaningful to run.
**Status:** done and verified against a real Postgres/Martin/API/Vite
stack, including toggling a real switch open to confirm barrier
interaction with the new capabilities.

## Why this slice exists

The user's own framing, closing out Phase 4: a real utility GIS needs
trace types beyond generic downstream/upstream/connected walks --
trace to nearest transformer, trace a transformer's LV/secondary
network, trace a transformer back to its source, isolation, outage
impact, and service connection tracing. Two design questions got
settled before writing code:

- **Engine generalization + minimal network model expansion**, not
  engine changes alone -- several of the six requested trace types
  (service connection trace, trace to transformer, outage-to-customer)
  are meaningless without a consumer/meter feature class, which didn't
  exist in the demo network at all; and "transformer to source" needs
  something at the far end to actually find. Building only the engine
  mechanism without that data would have been a trace to nowhere.
- **Isolation = de-energized set + upstream protective boundary**,
  computed together in one call -- not just "what loses power" alone.

## The core generalization: `target_class_id`

Every one of the utility-specific asks except isolation and outage
reduces to the same shape: *walk until you reach a node of a given
feature class*. `migrations/0018_add_trace_target_class.sql` adds an
optional `target_class_id` to `metadata.trace_configurations`; `bfs`
(`api/internal/trace/graph.go`) now stops expanding through (but still
records) any node matching it, other than the trace's own start node.

That single mechanism, combined with just *where* a trace starts,
covers three of the six asks with one seeded config
(`TRACE_TO_TRANSFORMER`, `UPSTREAM`, `target_class = DIST_XFMR`):

- Started from a **service point**, it finds the nearest transformer
  upstream -- a service connection trace.
- Started from a **transformer itself**, the start-node exclusion means
  it finds the *next* transformer upstream instead of itself -- which,
  in a radial distribution network, is exactly that transformer's
  source.

A second seeded config (`LV_NETWORK_TRACE`, `DOWNSTREAM`, same
`target_class`) covers a fourth: started from a transformer, it walks
its downstream/secondary footprint, and would stop at (while still
recording) any *other* transformer it might otherwise cross into.

`cross_transformers` -- accepted but never enforced since slice 1 --
finally has real, working meaning: `false` (every config's default)
stops the walk at a target-class node the way a barrier would; `true`
lets it continue past while still recording the hit. It was previously
a stub because nothing marked which classes counted as "a transformer"
independent of being a specific trace's own target; tying its
enforcement to `target_class_id` sidesteps that without inventing a
second, separate marking mechanism.

The **outage trace** ask needed no new mechanism at all -- it's exactly
what `DOWNSTREAM_TRACE` already computes (everything that loses power
past a fault point, respecting live barrier state). Seeding a second,
mechanically-identical config under a different name would have been
redundant metadata; the existing config just gets used and framed that
way.

## Isolation

`api/internal/trace/isolate.go` (`POST /trace/isolate`) composes two
ordinary traces rather than walking the graph itself -- exactly the
"isolation... composes two of the four primitive traces" framing
`docs/phase-4-trace-engine.md` used when explicitly deferring it:

1. **De-energized set**: a `DOWNSTREAM` trace from each of the device's
   own downstream neighbors, not from the device itself. Starting past
   it, rather than mutating its live `device_state` to fake "open," is
   what lets this answer "if I opened this right now" without ever
   writing fake data -- `checkBarrier` always reads a feature's real,
   current attribute value.
2. **Upstream boundary**: an `UPSTREAM` walk from the device using the
   same `target_class_id` mechanism above, targeting a boundary class
   that defaults to the device's own feature class (a switch's natural
   boundary is the *nearest other switch*), overridable via
   `boundary_class` in the request.

## The missing piece: a real service point

`seed/service_point_example.sql` adds `SERVICE_POINT` -- a minimal
meter/consumer feature class, one terminal, connected directly to a
`DIST_XFMR`'s secondary (T2). There's no distinct LV/secondary
conductor feature class yet (`docs/open-items.md`), so this
deliberately simplifies "a short service drop off the transformer's LV
bus" into a direct terminal-to-terminal connection -- worth revisiting
if an LV conductor class is ever added.

The same file adds one more network_rule -- `DIST_XFMR T2 ->
MV_CONDUCTOR` -- letting a transformer's secondary feed an onward
conductor instead of only ever a service point. That's what let the
seeded topology gain a second `DIST_XFMR` instance ("Substation Power
Transformer") standing in for the source at a substation, without
inventing a separate "power transformer" feature class: which role a
given `DIST_XFMR` plays is purely a function of how it's wired, not
anything the metadata model distinguishes.

The extended seeded topology, in full:

```
Substation Power Transformer (DIST_XFMR, 33KV primary)
  --T2--> Feeder source to S0 (MV_CONDUCTOR)
    --> Switch S0 (DIST_SWITCH, closed)
      --T2--> Feeder tap to switch S1 (MV_CONDUCTOR)
        --> Switch S1 (DIST_SWITCH, closed)
          --T2--> Feeder from S1 to XF-1 (MV_CONDUCTOR)
            --> XF-1 (DIST_XFMR, 11KV primary)
              --T2--> Meter 4471 - 12 Elm Court (SERVICE_POINT)
```

Two switches, not one, was a deliberate addition beyond the minimum --
isolating Switch S1 with no other switch anywhere upstream would never
exercise the upstream-boundary half of isolation at all (nothing to
find), only the override-`boundary_class` path. Switch S0 makes the
*default* boundary lookup (same class as the device) demonstrable too.

## Frontend

- **`feature-panel.tsx`**'s two hardcoded downstream/upstream arrow
  buttons became one "Run trace" dropdown, built directly off `GET
  /trace/configurations` (no more resolving a raw direction to a code --
  the dropdown lists every runnable config by name; `SHORTEST_PATH`
  configs are filtered out, since they need a second target feature, not
  a one-click start). A junction-role feature's dropdown gains an
  "Isolate from here" item below a divider.
- **`isolate-store.ts`** (new) -- same shape as `trace-store.ts`, kept
  separate rather than folded in since an isolation result has a
  genuinely different shape (two named lists, not one feature list plus
  subsets of it) and the two workflows are mutually exclusive in the UI
  (running one clears the other's store).
- **`isolation-result-table.tsx`** (new) -- same floating-table pattern
  as `trace-result-table.tsx`, its own header/icon, the boundary device
  (if any) rendered as a distinctly-colored row.
- **`trace-result-table.tsx`** -- rows matching a trace's `target_class`
  now render with their own distinct color, alongside the existing
  barrier-row styling.
- **`map-layers.ts`**/**`voltedge-map.tsx`** -- three new `HighlightRole`
  values (`trace-target`, `isolation-deenergized`, `isolation-boundary`),
  each its own color; a dedicated `isolateSource`/layer, same
  async-fetch-into-its-own-source pattern `traceSourceRef`'s own comment
  already established, since isolation and trace results are fetched
  independently and could otherwise race each other's clear/populate
  timing across the (mutually exclusive, but still async) transition
  between them.

## How this was validated

Backend confirmed via `curl` against the real Postgres/API stack before
any frontend work, including the full seeded topology above built up
live via the CRUD/connectivity endpoints (not a migration):

- `TRACE_TO_TRANSFORMER` from the service point found XF-1; from XF-1
  itself found the Substation Power Transformer (skipping XF-1 as its
  own target, correctly walking through both intervening switches).
- `LV_NETWORK_TRACE` from XF-1 found only the service point, confirming
  `target_class` scoping in the downstream direction too.
- Opening Switch S0 and re-running `TRACE_TO_TRANSFORMER` from XF-1
  confirmed the trace stops at S0 (a real barrier), never reaching the
  power transformer -- barrier-stopping and target-class-stopping
  compose correctly.
- `POST /trace/isolate` from Switch S1 with no `boundary_class`
  override correctly found Switch S0 as the default same-class
  boundary (added specifically to make this path demonstrable, not
  just the override); an explicit `boundary_class: "DIST_XFMR"`
  override correctly found the power transformer instead.

Frontend confirmed via Playwright against the real dev stack (Vite +
Go API + Postgres + Martin, no mocking) -- the "Run trace" dropdown
listing every real config, trace-to-transformer/LV-network/
transformer-to-source all producing correct result tables with target
rows visually distinct, and isolation producing its own separate result
table and map highlight, distinct from an ordinary trace result.

`npm run build && npm run lint` and `go build ./... && go vet ./... &&
gofmt -l .` all finish clean.

## Decisions made

- **One mechanism (`target_class_id`), not one config per named use
  case.** "Trace to transformer" and "transformer to source" are the
  literal same `trace_configurations` row -- the difference is entirely
  in what feature the trace starts from, not anything encoded in the
  profile itself. Modeling them as separate rows would have meant two
  configs that could silently drift apart in behavior for no reason.
- **`cross_transformers` enforcement tied to `target_class_id`**,
  not a standalone "is this class a transformer" marker. The narrower
  scope was a deliberate tradeoff: it makes every one of this slice's
  real use cases work, without inventing a second metadata concept this
  pass didn't have a concrete enough need for yet.
- **Isolation composes existing primitives at the API layer**, not a
  new BFS mode of its own -- keeps the one real graph-walking
  implementation (`bfs`) as the single place traversal logic lives.
- **A direct terminal-to-terminal connection for `SERVICE_POINT`**,
  not a new LV conductor class -- the narrowest change that makes
  service-point tracing real, deliberately deferring the more complete
  secondary-network model as a separate, later concern.
- **No new `OUTAGE_TRACE` config row** -- `DOWNSTREAM_TRACE` already is
  an outage trace; seeding a mechanically-identical row under a
  different name would have been redundant metadata for no behavioral
  gain.

## What's still open

- **Loop/mesh detection** -- the one item from Phase 4's original scope
  still deliberately deferred; unlike isolation, it needs a *connected*
  run from two sources and set intersection, a different shape of
  composition this slice didn't need to build.
- **`respect_phase` enforcement** -- still a stub; phase-aware tracing
  needs its own design pass (how phase capability intersects hop to
  hop), not answered here.
- **No persistent/cached graph** -- unchanged; still fine at this data
  scale.
- **No distinct LV/secondary conductor feature class** -- `SERVICE_POINT`
  connects directly to a transformer's secondary terminal for now; a
  real secondary network (multiple service points sharing an LV run)
  would need one.
- Everything else tracked in `docs/open-items.md` is unchanged by this
  slice.

## Try it yourself

Same stack as `docs/phase-4-trace-engine.md`'s "Try it yourself". Open
`http://localhost:5173`:

- Select the meter ("Meter 4471 - 12 Elm Court") or XF-1, open "Run
  trace," pick "Trace to transformer" -- watch which transformer it
  finds depending on where you started from.
- Select XF-1, run "LV network trace" -- a much shorter result than any
  upstream trace from the same feature.
- Select a switch (Switch S1 or S0), open "Run trace," pick "Isolate
  from here" -- a distinct result table shows what would de-energize
  and the nearest upstream protective device.
- Open Switch S0 (edit its `device_state` to `OPEN`) and re-run
  "Trace to transformer" from XF-1 -- confirm it now stops at S0
  instead of reaching the power transformer.

`npm run build && npm run lint` and `go build ./... && go vet ./... &&
gofmt -l .` should all finish clean.
