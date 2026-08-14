# Open items log

A single, current list of what's deliberately left undone in each phase
-- and, right alongside it, what's already been closed -- so "what's
outstanding" is answerable by glancing at one **Status** column instead
of cross-referencing a separate section or rereading a dozen phase
docs. Replaces the old approach of only ever recording an open item in
prose at the bottom of whichever phase doc happened to close that
slice; those per-slice "What's still open" sections still exist and
still carry the full *why* for each item, this doc is just the index.

**Status** is `Open` or `Done`. A row never gets deleted when it
closes -- it flips to `Done` and stays, so scrolling a phase's table
shows its whole recent trajectory, not just a snapshot. Update this
doc, not just the closing slice's own doc, whenever an item here gets
built or a new one is deliberately deferred. Open rows are listed
first within each phase, Done rows after (most recently closed first)
-- so scanning top-to-bottom surfaces what's actionable before what's
already finished.

Each item still carries a rough size (S/M/L) and a one-line value
note for open rows, same framing used to prioritize what to pick up
next; a closed row keeps its size/value for context but it's no longer
load-bearing. This log's own rows start once a phase has *some* mix of
open and closed items worth tracking side by side -- it's not a full
changelog back to each phase's original build (that history is
already in `README.md` and each phase's own write-up doc).

**Last updated:** 2026-08-13, after Phase 4's fifth slice (feeder/
section tracing, critical-facility + backfeed checks on isolation, and
loop/mesh detection -- Phase 4 now closed except `respect_phase`
enforcement, voltage-boundary tracing, and phase-aware tracing, all
deliberately deferred pending their own data-modeling passes).

## Phase 1 — Metadata core

| Item | Status | Size | Value | Detail |
|---|---|---|---|---|
| Multi-class atomic publish | Open | M | Compiling two related feature classes today is two separate `metadata_versions` rows/transactions; a real "validate everything, compile everything, apply as one unit" workflow doesn't exist yet. | `docs/phase-1-metadata-core.md` |
| Publish-time validation beyond individual `CHECK` failures | Open | S–M | E.g. "every feature class needs a default `symbology_rules` row before publish" isn't enforced anywhere. | `docs/phase-1-metadata-core.md` |
| Computed fields (`is_computed = true`) | Open | M | Compiles to a plain nullable column today, not a generated one (e.g. `length_m` via `ST_Length(geom)`) -- nowhere in the metadata yet to store the expression. | `docs/phase-1-metadata-core.md` |

## Phase 2 — CRUD / connectivity / relationships engine

| Item | Status | Size | Value | Detail |
|---|---|---|---|---|
| No filtering or sorting on list | Open | S | List is a bounded, offset-paginated scan ordered by `created_at, id` only -- fine for demo data, not for a real feature count. | `docs/phase-2-crud-engine.md` |
| Attachments (RustFS-backed) | Open | L | A genuinely different design surface (upload flow, size/type limits) not yet worked through in enough detail to build. | `docs/phase-2-crud-engine.md`, `docs/phase-2-relationships.md` |
| Diagrams | Open | L | Same status as attachments -- still ahead per the original Phase 2 plan. | `docs/phase-2-crud-engine.md` |
| `numeric` fields round-trip through Go `float64` | Open | S | Not exact-decimal-safe; worth revisiting if a field ever needs currency-grade precision. | `docs/phase-2-crud-engine.md` |
| No `PATCH` for a connection or relationship | Open | S–M | Both are re-established or revoked, never edited in place -- fine for now, but a wrong terminal/kind pick means delete-and-redo. | `docs/phase-2-relationships.md` |
| `ST_DWithin` plausibility check for relationships | Open | S | `docs/metadata-core.md`'s POLE sample mentions this as a soft geometric-plausibility check alongside the explicit establish requirement; not implemented -- nothing warns when two related features are implausibly far apart. | `docs/phase-2-relationships.md` |
| Direction-agnostic `network_rules` matching | Done | S | `matchRule` required an exact `from`/`to` feature-class match with no reverse fallback -- since `from`/`to` on a rule is an authoring convention (edge vs. terminal-bearing junction), not real current direction, establishing the same physical link succeeded or failed purely based on which endpoint the UI's connect workflow happened to start from. `matchRuleEitherDirection` now tries both the request's pair and the swapped pair, always writing the connection in the rule's own canonical direction. | `docs/phase-2-connectivity.md` |

## Phase 3 — Map loop

| Item | Status | Size | Value | Detail |
|---|---|---|---|---|
| Metadata-driven symbology resolution | Done | M–L | Closed the core "looks like a real GIS" gap -- `metadata.symbology_rules` now resolves client-side into real per-class, per-attribute SVG icon shapes (points) and color/width (lines/polygons), most-specific-wins over subtype + domain-value. Zoom-gating, `render_context = 'diagram'`, and a real sprite pipeline remain deliberately deferred -- see "What's still open" below. | `docs/phase-3-symbology.md` |
| Symbology zoom-gating, diagram context, real sprite pipeline | Open | S–L | Follow-ons deferred from the symbology resolution slice above: `min_zoom`/`max_zoom` exist on `symbology_rules` and are loaded but unused by `resolveSymbology`; `render_context = 'diagram'` rules are loadable but nothing renders them (blocked on Phase 5's diagram system); `symbology-icons.ts` is a small hardcoded SVG registry, not a real asset-serving system. | `docs/phase-3-symbology.md` |
| Azure AD SSO + backend auth | Open | L | Legitimately greenfield -- no session/token handling, no user table, no frontend auth state anywhere (`README.md`: "Auth and permissions are deliberately not modeled yet"). A prerequisite for Phase 6 access control more than something the map loop itself needs. | `docs/phase-3-ui-shell.md` |
| Real Martin soft-delete-aware fix | Open | L, unresolved unknown | Today's fix (`deleted-features-store.ts`) is purely client-side and session-local -- a fresh tab or a different user still sees a soft-deleted feature. The real fix (compiler-generated `deleted_at IS NULL` view, Martin serving from it) is blocked on an unconfirmed question: Martin auto-discovers tiles by table name, so the view would need to be published under the same name the table currently uses -- needs a spike before it can be sized honestly. | `docs/phase-3-delete-confirmation.md` |
| Metadata-driven `snapping_rules` | Open | M | `docs/phase-3-snapping.md` shipped a plain OL `Snap` interaction against real feature geometry; the metadata table (per-feature-class-pair tolerance/eligibility) is still schema-only, deliberately deferred as a distinct, later layer on top of what exists now. | `docs/phase-3-snapping.md` |
| Peer-picker disambiguation for connect-mode target-picking | Done | S | Connect-mode's target pick now reuses the same peer-picker normal selection already had for stacked/overlapping features, instead of always taking the topmost hit. Picking one routes to `connect-store`'s `pickTarget` rather than normal `select`; a `clearSelected: false` option on `showCandidates` keeps the workflow's source feature visible in `FeaturePanel` throughout, a real regression caught before shipping. | `docs/phase-3-connectivity-ui.md` |
| Revoking a connection from the UI | Done | S | Each connection row in the Terminals tab now has a small unlink icon opening an `AlertDialog` confirm (same pattern as feature delete), calling the existing `DELETE /connectivity/{id}`; a broad `["connections"]`-prefix cache invalidation refreshes both ends of the connection without a reload. | `docs/phase-3-connectivity-ui.md` |
| No loading affordance on the highlight halo | Open | S | A connection/relationship row click that targets an unfetched feature takes a beat before its halo appears on the map, no spinner in the meantime (the attribute panel's own tab already shows one for the same fetch). | `docs/phase-3-map-aesthetics.md` |
| Floating panel resize/minimize | Open | S–M | The panel undocked into a movable floating window (see the Done row below); resizing and minimizing it were flagged together as later scope when undocking was first pinned, and remain out of scope -- fixed `w-96 h-64` for now. | `docs/phase-3-floating-panel.md` |
| Polygon vertex editing, real re-verification | Open | S | `docs/phase-3-edit-geometry.md` shipped `Modify`-based vertex editing for line/polygon alike but only exercised it against a line (no seeded polygon existed at the time). A real `SUBSTATION_YARD` polygon now exists in the demo dataset -- worth a quick pass to actually confirm the polygon path, not just the shared-code argument for it. | `docs/phase-3-edit-geometry.md` |
| Undockable/floating attribute panel | Done | S–M | The attribute panel adopted the connect-confirm-panel's drag pattern and became a `fixed`-positioned floating window instead of a docked, full-width row. | `docs/phase-3-floating-panel.md` |
| Structural relationships from the connect panel | Done | S | The connect panel now branches on mode to call `POST /relationships` (no terminal pickers, an optional `kind`) instead of always assuming connectivity; a matching "New relationship" trigger was added to the Relationships tab. | `docs/phase-3-relationships-ui.md` |

## Phase 4 — Network model

| Item | Status | Size | Value | Detail |
|---|---|---|---|---|
| Voltage-boundary tracing | Open | M | Needs a genuinely different engine mechanism than `target_class_id` -- comparing each node's own attribute value against a boundary condition (e.g. "stop where voltage class changes"), not matching a fixed target class. | `docs/phase-4-safety-traces.md` |
| Phase-aware tracing | Open | L | The biggest remaining gap: needs a real `PHASE` domain, per-conductor/device phase data, and hop-by-hop phase-set intersection. `terminals.phase_capable` and `network_rules.phase_rule` exist in the schema but are still completely unused. | `docs/phase-4-safety-traces.md` |
| `respect_phase` enforcement | Open | M–L | Accepted on a `trace_configurations` row and threaded through, not enforced -- subsumed by phase-aware tracing above; both need the same underlying phase data model before either can move. (`cross_transformers` is no longer in this row -- it has real, enforced meaning now, tied to `target_class_id`.) | `docs/phase-4-trace-engine.md`, `docs/phase-4-utility-traces.md` |
| Transformer-loading trace | Open | M | Needs load data on `SERVICE_POINT` (doesn't exist) and a new "aggregate a value across a trace" response shape, not just a feature list. | `docs/phase-4-safety-traces.md` |
| No distinct LV/secondary conductor feature class | Open | M | `SERVICE_POINT` connects directly to a `DIST_XFMR`'s secondary terminal for now -- a real secondary network (multiple service points sharing an LV run) would need its own conductor class between them. | `docs/phase-4-utility-traces.md` |
| No persistent/cached graph | Open | S–M, revisit only if needed | Every hop queries `runtime.feature_connectivity` fresh, matching `api/internal/connectivity`'s own stateless-per-request style -- fine at current seed-data scale; `docs/metadata-core.md` describes an in-memory graph as a longer-term role, not something the current data size gives any signal is actually needed yet. | `docs/phase-4-trace-engine.md` |
| Feeder/section tracing, isolation safety checks, loop detection | Done | — | `POST /trace/feeder` finds a feature's whole feeder (network root via an unbounded upstream walk, then downstream from there) regardless of where you start; `POST /trace/section` finds the section between the nearest bounding devices in each direction, reusing the `target_class_id` mechanism bidirectionally. Isolation gained a critical-facility cross-reference (a new `is_critical` flag on `SERVICE_POINT`) and a backfeed check (every live connection out of the de-energized set this isolation didn't already account for, surfaced for a human to verify, not claimed as a guarantee). Loop/mesh detection needed no new Go code at all -- a second `SHORTEST_PATH` config (`LOOP_CHECK_TRACE`) that, unlike `SHORTEST_PATH_TRACE`, respects live barrier state, plus a "Check for loop" trigger in the connect panel. Closes Phase 4's originally-deferred loop/mesh item. | `docs/phase-4-safety-traces.md` |
| Utility trace types + isolation | Done | — | A generalized `target_class_id` on `trace_configurations` (a walk stops at, but records, the first node of a given class) powers trace-to-transformer, transformer-to-source, and LV-network tracing from one mechanism; `cross_transformers` finally has real enforced meaning, tied to it. `POST /trace/isolate` composes a downstream trace (from the device's neighbors, not the device itself, to simulate "open" without mutating data) with an upstream target-class search for the nearest protective boundary. A new `SERVICE_POINT` feature class and an extended seeded topology (a second `DIST_XFMR` standing in for a substation source, a second `DIST_SWITCH`) make all of it real to run. The floating panel's two hardcoded trace buttons became a "Run trace" dropdown listing every config by name, plus "Isolate from here" for junction features. | `docs/phase-4-utility-traces.md` |
| Trace configuration discovery, name display everywhere, search by name | Done | — | `GET /trace/configurations` lets a client resolve a trace by raw direction instead of a hardcoded code; `connect-confirm-panel.tsx`'s `EndpointPicker` and the Terminals/Relationships tabs' counterpart rows now promote a per-feature name the same way the floating panel header already did; a new "Search by name…" box in the map toolbar fans a name query out across every feature class and merges results client-side. Closes the three items previously tracked here: no endpoint to discover trace configs, name not shown in connect panel/rows, no search by name. | `docs/phase-4-trace-discovery-and-search.md` |
| Universal feature name + trace result table | Done | — | `name` is now a system column every compiled feature class gets (same precedent as `deleted_at`), settable from the create/edit forms, shown as the floating panel header's primary label once set. The trace result banner became a real data table (feature class, id, name per row, barrier rows visually distinct, clickable). | `docs/phase-4-feature-names.md` |
| Core trace engine primitives | Done | — | `api/internal/trace` walks `runtime.feature_connectivity` per a named `trace_configurations` profile (`connected`/`downstream`/`upstream`/`shortest_path`), real barrier-stopping against a seeded `DIST_SWITCH`'s live `device_state`. Two "Trace downstream"/"Trace upstream" buttons trigger it and highlight the result on the map. | `docs/phase-4-trace-engine.md` |

## Phases 5-7

Not started; see `README.md`'s phase list for their one-line scope
(diagrams/attribute groups/MCP; domain expansion + hardening; field
collection). Too far out to itemize yet.
