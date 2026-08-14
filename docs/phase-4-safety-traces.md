# Phase 4 (slice 5) — feeder/section tracing, isolation safety checks, loop detection

**Goal:** five utility-specific trace and safety capabilities the user
asked for directly by name, scoped deliberately to what the existing
engine and network model already support: feeder trace, sectionalizing
trace, a critical-facility flag on isolation, a backfeed/re-energization
check on isolation, and loop/mesh detection. Voltage-boundary tracing,
phase-aware tracing, and transformer-loading tracing were explicitly
scoped *out* of this pass -- each needs real new data modeling
(a voltage-boundary BFS mechanism, a `PHASE` domain and per-conductor
phase data, load data on `SERVICE_POINT`) that deserves its own design
conversation, not answers invented alongside these five.
**Status:** done and verified against a real Postgres/Martin/API/Vite
stack, including a manufactured backfeed scenario (established, then
revoked) to confirm the positive case, not just the negative one.

## Why this slice exists

Closing out Phase 4's original scope (`docs/phase-4-utility-traces.md`)
left one open question: what does a *real* electric distribution/
transmission utility actually need beyond the primitive-plus-target-class
mechanism already built? The user asked directly, and the answer split
into three tiers by effort and how much of the foundation already
existed:

- **Buildable now, on existing primitives** -- feeder trace, critical-
  facility flag, sectionalizing trace, a backfeed check, and loop
  detection. This slice.
- **Needs real new data modeling** -- voltage-boundary tracing (a
  different BFS mechanism entirely: compare each node's own attribute,
  not match a fixed target class), phase-aware tracing (the biggest
  gap: `terminals.phase_capable` and `network_rules.phase_rule` exist
  in the schema but were never wired to anything), transformer-loading
  tracing (needs load data that doesn't exist yet). Deliberately still
  deferred.

## What was built

### Feeder trace (`POST /trace/feeder`)

Answers "which whole feeder does this feature belong to," regardless
of which point along it you start from -- not just a downstream trace
from the substation, which would require already knowing where the
substation is. Composes two ordinary walks:

1. **Find the head(s)**: an `UPSTREAM` walk from the start feature with
   no barriers and no target class, to full exhaustion. A feeder's
   identity is a topological property, not a live-switching-state one
   -- deliberately ignoring barriers here mirrors `CONNECTED_TRACE`'s
   own `stop_at_open_devices = false` reasoning ("an audit of what's
   wired to what shouldn't silently stop at a switch just because it's
   open"). The head(s) are whichever reached nodes have no further
   upstream neighbors of their own -- the network's root(s).
2. **Union a `DOWNSTREAM` trace** (this time respecting the given
   `trace_configuration`'s real barrier settings -- the returned
   *footprint* should reflect live switching state) from each head.

### Sectionalizing trace (`POST /trace/section`)

"Everything between the nearest bounding device upstream and
downstream of a point" -- the planned-outage-notification question.
Runs the same `target_class_id` mechanism `TRACE_TO_TRANSFORMER`
already established, just in both directions from the start feature
itself rather than from a device's neighbors (unlike isolation, a
section isn't about what a device controls -- it's about what lies
between two devices, so it walks from the point itself). `boundary_class`
is optional, defaulting to `DIST_SWITCH` -- the only sectionalizing/
protective class this app's seed data models.

### Critical-facility flag + backfeed check (both folded into `POST /trace/isolate`)

Rather than three separate safety-oriented endpoints, both ride along
with an isolation call -- isolation *is* the safety-critical operation;
these are naturally part of "what do I need to know before/after
opening this device," not independent questions:

- **`critical_facilities`**: a new `is_critical` boolean on
  `SERVICE_POINT` (nullable -- an existing row already had data by the
  time this was seeded, and a `NOT NULL ALTER TABLE ADD COLUMN` with no
  default fails outright against a table with rows), cross-referenced
  against the de-energized set.
- **`possible_backfeed_paths`**: every physically-connected neighbor
  (either direction) of anything in the de-energized set that isn't
  already accounted for by the isolation result (the device itself,
  the de-energized set, or the boundary) -- a live connection the
  directed downstream walk never needed to cross, e.g. a normally-open
  tie point that's actually closed right now. Reported as a
  *possibility* for a human to verify, explicitly not a claimed
  guarantee either way -- same "checked live, never assumed" framing
  `checkBarrier` already established for barrier state.

### Loop/mesh detection (`LOOP_CHECK_TRACE` + `connect-confirm-panel.tsx`)

The one item deferred twice before this slice, and it turned out to
need almost no new code. Real definition: closing/establishing a link
between two features creates a loop exactly when a *live* path already
connects them -- and `bfs` already supports a barrier-respecting
target search (`target *node` plus `barriers`, together), because
that's exactly what `SHORTEST_PATH_TRACE` itself uses. The only
missing piece was a second `trace_configurations` row differing from
`SHORTEST_PATH_TRACE` in one setting: `stop_at_open_devices = true`.
`SHORTEST_PATH_TRACE` deliberately leaves barriers off ("a path query
between two points is about topology, not live state"); a loop check
needs the opposite -- whether a second path is *currently* live, not
merely whether one exists on paper.

Zero new Go code. `connect-confirm-panel.tsx` gained a "Check for
loop" button (connectivity workflow only, once both source and target
are picked) that runs this config as an ordinary `POST /trace` call
and surfaces `path_found` as a warning before Confirm.

## Frontend: reusing the existing result table instead of building two more

Feeder trace and section trace each hit their own dedicated endpoint
with their own response shape (`heads` / `upstream_boundary`+
`downstream_boundary` instead of `barriers`/`targets`) -- rather than
building two more result-table components, `feature-panel.tsx`
normalizes both into the same `TraceResult` shape `trace-result-table.tsx`
already renders (heads/boundaries become `targets`, so they get the
same visually-distinct gold treatment a `target_class` hit already
gets), with two new synthetic direction labels ("Feeder", "Section")
so the header reads correctly.

`isolation-result-table.tsx` gained a summary line above the table
(critical-facility and backfeed counts, so neither is easy to miss by
only skimming individual rows) and a third row treatment (red,
distinct from the violet boundary row) for backfeed candidates, which
aren't part of the de-energized set itself so are appended as their
own rows rather than folded into it. `map-layers.ts` gained a matching
`isolation-backfeed` highlight role.

## How this was validated

Backend confirmed via `curl` against the real Postgres/API stack:

- Feeder trace from the service point correctly found the Substation
  Power Transformer as the sole head and the whole 8-feature feeder
  footprint.
- Section trace from XF-1 correctly bounded to Switch S1 upstream, no
  downstream boundary (the meter is a network leaf).
- Marking the meter `is_critical = true` and re-running isolation from
  Switch S1 correctly flagged it in `critical_facilities`.
- **Backfeed, both directions confirmed**: with the clean topology,
  isolating Switch S1 correctly reported no backfeed paths at all
  (field omitted). A second live connectivity edge was then
  manufactured directly from the Substation Power Transformer into the
  meter (bypassing Switch S1 entirely) -- re-running the same
  isolation correctly flagged the power transformer as a
  `possible_backfeed_paths` entry. The manufactured edge was revoked
  immediately after, restoring the clean topology for everything else.
- **Loop check, both directions confirmed**: `LOOP_CHECK_TRACE` between
  Switch S1 and the Substation Power Transformer correctly found
  `path_found: true` (they're genuinely connected). Opening Switch S0
  (which sits on that path) and re-running the same check correctly
  flipped to `path_found: false` -- while `SHORTEST_PATH_TRACE` between
  the same two features, run in the same state, still correctly
  reported `path_found: true`, confirming the two configs really do
  differ exactly in whether live state is respected.

Frontend confirmed via Playwright against the real dev stack.

`npm run build && npm run lint` and `go build ./... && go vet ./... &&
gofmt -l .` all finish clean.

## Decisions made

- **Critical-facility and backfeed checks fold into `POST /trace/isolate`**,
  not separate endpoints -- both are naturally part of the same
  safety-critical question isolation already answers, and a caller
  needing the full picture would otherwise have to make three
  round-trips for one decision.
- **Feeder/section responses get normalized into the existing
  `TraceResult` shape client-side**, not two new result-table
  components -- the visual vocabulary (a notable row gets the gold
  "target" treatment) already generalizes to "feeder head" and
  "section boundary" without forcing a new one.
- **`is_critical` is nullable**, unlike most fields on a freshly-designed
  class -- a real backfill constraint (a `NOT NULL` `ALTER TABLE ADD
  COLUMN` fails against a table that already has rows), and arguably
  more honest semantics anyway: nothing should silently assume a
  service point is a hospital just because a value was never set.
- **Backfeed detection reports possibilities, not guarantees.** Fully
  resolving "is that external connection actually energized right now"
  would require nested traces with their own termination questions;
  surfacing every unaccounted-for live connection for a human to
  verify is the safer default than risking a false "all clear."
- **Loop detection needed a new trace_configurations row, not new Go
  code.** The existing `bfs` already supported everything a
  barrier-respecting shortest-path search needs; the gap was purely
  that no seeded config asked for that combination.

## What's still open

- **Voltage-boundary tracing** -- needs a genuinely different engine
  mechanism (compare each node's own attribute to a boundary
  condition, not match a fixed target class).
- **Phase-aware tracing** -- the biggest remaining gap: a real `PHASE`
  domain, per-conductor/device phase data, and hop-by-hop phase-set
  intersection. `terminals.phase_capable` and `network_rules.phase_rule`
  exist in the schema but are still unused.
- **Transformer-loading tracing** -- needs load data on `SERVICE_POINT`
  (doesn't exist) and a new "aggregate a value across a trace" response
  shape, not just a list.
- **No distinct LV/secondary conductor feature class** -- unchanged
  from the previous slice.
- Everything else tracked in `docs/open-items.md` is unchanged by this
  slice.

## Try it yourself

Same stack as `docs/phase-4-trace-engine.md`'s "Try it yourself". Open
`http://localhost:5173`:

- Select the meter or XF-1, open "Run trace," pick "Feeder trace" or
  "Section trace" -- compare the footprints.
- Select Switch S1, "Run trace" → "Isolate from here" -- the result
  table's summary line calls out the critical facility affected.
- From Switch S1's "+ New connection" workflow, pick the Substation
  Power Transformer as a target and click "Check for loop" -- these
  two are already connected, so it should warn that a live path
  already exists.

`npm run build && npm run lint` and `go build ./... && go vet ./... &&
gofmt -l .` should all finish clean.
