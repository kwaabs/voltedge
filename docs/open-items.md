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

**Last updated:** 2026-08-14, after a round of real diagram-canvas
fixes prompted by a reference one-line diagram: conductors now render
as their own wires instead of point icons (with the old generic
auto-drawn connectivity edges removed entirely), diagrams have a
visible, editable name, and placements can be rotated and removed from
the canvas.

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
| Diagrams (backend) | Done | L | Superseded by Phase 5's own slice -- `runtime.diagram_instances`/`diagram_placements`, a new `api/internal/diagram` package (create/list/get/delete an instance, place/move/list/remove a feature on it), gated on `is_diagram_container`. See the Phase 5 section below for the frontend canvas, now also done. | `docs/phase-5-diagrams.md` |
| `numeric` fields round-trip through Go `float64` | Open | S | Not exact-decimal-safe; worth revisiting if a field ever needs currency-grade precision. | `docs/phase-2-crud-engine.md` |
| No `PATCH` for a connection or relationship | Open | S–M | Both are re-established or revoked, never edited in place -- fine for now, but a wrong terminal/kind pick means delete-and-redo. | `docs/phase-2-relationships.md` |
| `ST_DWithin` plausibility check for relationships | Open | S | `docs/metadata-core.md`'s POLE sample mentions this as a soft geometric-plausibility check alongside the explicit establish requirement; not implemented -- nothing warns when two related features are implausibly far apart. | `docs/phase-2-relationships.md` |
| Direction-agnostic `network_rules` matching | Done | S | `matchRule` required an exact `from`/`to` feature-class match with no reverse fallback -- since `from`/`to` on a rule is an authoring convention (edge vs. terminal-bearing junction), not real current direction, establishing the same physical link succeeded or failed purely based on which endpoint the UI's connect workflow happened to start from. `matchRuleEitherDirection` now tries both the request's pair and the swapped pair, always writing the connection in the rule's own canonical direction. | `docs/phase-2-connectivity.md` |

## Phase 3 — Map loop

| Item | Status | Size | Value | Detail |
|---|---|---|---|---|
| Metadata-driven symbology resolution | Done | M–L | Closed the core "looks like a real GIS" gap -- `metadata.symbology_rules` now resolves client-side into real per-class, per-attribute SVG icon shapes (points) and color/width (lines/polygons), most-specific-wins over subtype + domain-value. Zoom-gating and a real sprite pipeline remain deliberately deferred -- see "What's still open" below. `render_context = 'diagram'` is now consumed too, by Phase 5's diagram canvas. | `docs/phase-3-symbology.md` |
| Symbology zoom-gating, real sprite pipeline | Open | S–L | Follow-ons deferred from the symbology resolution slice above: `min_zoom`/`max_zoom` exist on `symbology_rules` and are loaded but unused by `resolveSymbology`; `symbology-icons.ts` is a small hardcoded SVG registry, not a real asset-serving system. | `docs/phase-3-symbology.md` |
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

## Phase 5 — Diagrams, attribute groups, MCP

| Item | Status | Size | Value | Detail |
|---|---|---|---|---|
| Multi-segment bus routing on the diagram canvas | Open | M | Edges now route as a single right-angle elbow (see the "Done" row below), not the multi-segment bus-tap routing a dense protection-scheme diagram needs -- every edge still only connects exactly two placements. | `docs/phase-5-diagrams.md` |
| Electrical-schematic symbol library | Open | L | The current icon set (circle/square/triangle/diamond) is generic; a real protection one-line needs ANSI device-number circles, CTs, PTs, disconnect switches, ground studs, and more -- a large, ongoing content task independent of the canvas's rendering technology. | `docs/phase-5-diagrams.md` |
| Grouping/annotation boxes on the diagram canvas | Open | M | Real diagrams label logical groupings (e.g. a relay panel) with a dashed box around several placements -- no such freeform, non-feature-backed annotation primitive exists in the data model yet. | `docs/phase-5-diagrams.md` |
| Dual line semantics (power vs. dashed control/trip lines) | Open | M | Every edge today comes from a real `feature_connectivity` row (or, for a conductor, its own resolved endpoints); a protection scheme's dashed trip lines with arrowheads are schematic-only annotations with no real network connectivity behind them, which the current model has no place for. | `docs/phase-5-diagrams.md` |
| `geometry_type = 'none'` with a real feature class | Open | S | The compiler and CRUD engine already support a feature class with no map geometry at all (whose only visual representation is a diagram placement) -- proven by code inspection, not yet exercised by any actual seeded feature class (e.g. a `PROTECTIVE_RELAY`). | `docs/phase-5-diagrams.md` |
| Conductors render as wires + no more generic auto-drawn edges | Done | M | A `geometry_type = 'line'` placement (e.g. `MV_CONDUCTOR`) no longer renders through the point-icon path -- it resolves its own real connectivity endpoints and draws *as* the line between them, styled with its own map/diagram color, falling back to a small draggable marker when fewer than two endpoints are placed. The old generic "one elbow per connectivity row" pass is gone entirely -- a wire only appears when its own conductor is actually placed, matching real one-line diagram convention. | `docs/phase-5-diagrams.md` |
| Diagram naming: display + rename | Done | S | `runtime.diagram_instances.name` was already in every API response but never shown; the canvas header now shows the real name (falling back to `diagram_type`) and a new `PATCH /diagrams/{id}` endpoint lets it be renamed by clicking the title. | `docs/phase-5-diagrams.md` |
| Rotate a placement on the diagram canvas | Done | M | A drag handle appears above the app's currently-selected placement (reusing the existing global selection state); dragging computes the new angle from the pointer's position relative to the placement's center, snapped to 15-degree increments, and writes back through the existing placement upsert independently of position. | `docs/phase-5-diagrams.md` |
| Remove a placement from the diagram canvas | Done | S | A hover-revealed "×" control on every placement calls the `DELETE /diagrams/{id}/placements/{placementID}` endpoint that has existed since the backend slice -- purely a missing frontend affordance until now. Deliberately no confirm dialog: un-placing is cosmetic and one search-and-add away from reversed, unlike a real connection revoke or feature delete. | `docs/phase-5-diagrams.md` |
| Admin UI for `DIAGRAM_TYPE` values / `is_diagram_container` / creating the first diagram instance | Open | M | All real metadata/data changes made by hand today (seed SQL / a migration / a direct curl call), same as every other metadata-level change in this project pre-admin-UI. Adding an *existing* feature to an *already-created* diagram is now a real UI capability -- see the "Add existing feature to a diagram" row below -- creating that first diagram instance itself is not. | `docs/phase-5-diagrams.md` |
| Diagram containers, data model + API | Done | L | `runtime.diagram_instances`/`diagram_placements` (a diagram belongs to one container feature, a placement puts any feature at an x/y/rotation inside it, independent of real geometry), a new `api/internal/diagram` package, gated on `metadata.feature_classes.is_diagram_container`. `SUBSTATION_YARD` is the first real container. Curl-verified end to end against a real Postgres/API stack, including both rejection gates and cascade delete. | `docs/phase-5-diagrams.md` |
| Diagram frontend canvas | Done | M–L | A hand-rolled SVG canvas (no new dependency, reuses `symbology-icons.ts`'s `iconDataUri` directly) in a side-by-side split view next to the map, not a floating overlay or a new route. Renders placements as symbology-resolved icons plus labels, and real `feature_connectivity` edges between placements that are both on the same diagram. Triggered from `FeaturePanel` via a "View diagram" button, shown only when the selected feature's class is a diagram container with at least one diagram instance. First real consumer of `render_context = 'diagram'` symbology (falls back to `'map'` symbology when no diagram-context rule exists yet). | `docs/phase-5-diagrams.md` |
| Add existing feature to a diagram, from the canvas | Done | S | A `+` button in the diagram panel opens a cross-class name search (the same fan-out `feature-search.tsx`'s map toolbar box uses, extracted into `use-feature-name-search.ts` so both share it) and places the picked feature via the existing `POST /diagrams/{id}/placements` endpoint -- no new backend work needed. New placements land just outside the current bounding box so repeated additions spread out instead of stacking; already-placed features are filtered out of the search results. Placing a brand-new, geometry-less feature is a separate, still-open capability. | `docs/phase-5-diagrams.md` |
| Drag-to-reposition + routed elbow edges on the diagram canvas | Done | M | Shown a real one-line diagram, the auto-placed/straight-line canvas didn't hold up. Placements now drag via pointer events (screen coordinates converted into the SVG's own viewBox space via `getScreenCTM`), writing back through the existing placement upsert on drop with an optimistic query-cache update to avoid flicker; a plain click still selects, same as before. Edges now render as a right-angle elbow `<path>` instead of a straight `<line>`. Neither React Flow nor Fabric.js was reconsidered as the fix -- both were re-evaluated and rejected again, since neither ships schematic routing or symbol conventions either. | `docs/phase-5-diagrams.md` |

## Phases 6-7

Not started; see `README.md`'s phase list for their one-line scope
(domain expansion + hardening; field collection). Too far out to
itemize yet.
