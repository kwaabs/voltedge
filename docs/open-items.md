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

**Last updated:** 2026-08-15, the Phase 5 "admin UI for DIAGRAM_TYPE
values / is_diagram_container / creating the first diagram instance"
row closed -- scoped down before building, the same way this log's
other recent updates did: checking the actual code found two of the
three already true (`is_diagram_container` editable since Phase 6
slice 1, `DIAGRAM_TYPE` an ordinary coded domain already authorable via
the domains admin page), leaving only "creating the first diagram
instance" as a real gap, even though `POST /diagrams` itself had been a
real, generic, curl-verified endpoint since Phase 5's own backend
slice -- it just had no UI trigger. A new `useCreateDiagramInstance`
hook plus `create-diagram-form.tsx` closes that, wired into
`feature-panel.tsx` as a header trigger shown only when the selected
feature is a diagram container with zero diagrams yet, taking over the
panel's tab body the same way an attribute-edit session already does.
Verified against a local, non-Docker Postgres 16 + PostGIS install,
the real Go API, and a real (non-Docker) Martin binary, driven end to
end with Playwright: searched for a real container feature by name,
triggered "Create diagram," picked a type and name, and confirmed the
split-view canvas opened immediately on the real new instance.
Before that, range domains closed -- the second half of
what was originally asked alongside subtype/domain-value editing (the
update before that). `migrations/0020` finally gives
`metadata.domains` somewhere to store a range domain's bounds
(`range_min`/`range_max`, both nullable, `range_min <= range_max`
enforced by its own `CHECK`) -- `api/internal/compiler/ddl.go` had
carried its own comment flagging this exact gap since Phase 6 slice 1
("nothing to compile into a CHECK for domain_type = 'range' until
that's modeled"). `columnDefinition` now emits a `BETWEEN` `CHECK` for
any field linked to a range domain, on both a fresh `CREATE TABLE` and
a later `ADD COLUMN`; `crud/validate.go` gained the matching Go-side
range check (mirroring the existing coded-domain membership check) so
an out-of-range value gets a clean 400 instead of a raw constraint
violation; `POST /admin/domains` accepts `range_min`/`range_max` when
`domain_type = range`; the domains admin page gained a Coded values/
Numeric range toggle. This session's sandbox couldn't reach the Docker
registry (same 403 policy block as the previous update), so
verification ran against a local, non-Docker Postgres 16 + PostGIS
install already present in the sandbox instead -- migrations applied
for real via `migrations/apply.sh`, backend curl-verified end to end
(valid/invalid bound combinations, a compiled `BETWEEN` CHECK matching
exactly, pre- and post-compile feature create/update rejection and
acceptance), and this time the frontend got a real Playwright pass too,
not just typecheck/lint -- created a range domain through the UI,
confirmed its bounds displayed correctly, confirmed the pre-existing
coded-domain flow still worked, zero console errors.
The Phase 6 "edit an already-compiled
class's subtypes / an already-referenced domain's values" gap closed
for its safe half before that: *adding* a new subtype or domain value to an
already-compiled class is now real (`api/internal/compiler`'s
`syncCheckConstraints` rebuilds the affected `CHECK` constraint on
every recompile; `POST /admin/feature-classes/{code}/subtypes` and
`POST /admin/domains/{code}/values` insert the metadata, an existing
recompile call applies it), backend curl-verified extensively
(pre-recompile rejection, post-recompile acceptance, idempotency,
no regressions across every other compiled class). *Editing or
removing* an existing subtype/value stays out of scope -- only ever
additive, matching what Postgres's constraint-widening safety
argument actually covers. The frontend (an inline "add subtype"/"add
value" row per class/domain, plus a recompile trigger) is
typechecked/linted clean but this session's sandbox couldn't pull the
Docker images needed to browser-verify it -- flagged explicitly in
`docs/phase-6-admin-metadata.md` rather than claimed done. Range
domains was the other half of what was originally asked alongside this
-- see the newer update above for its own close-out.
Before that, two more Phase 6 quick wins closed:
editing a feature class's own basic info (`PATCH
/admin/feature-classes/{code}` -- name/is_diagram_container always,
geometry_type/srid/network_role only while still draft) and deleting a
draft feature class or an unreferenced domain/field group, both scoped
to the provably-safe subset the same way the original Phase 6 slice
drew its own line around the compiler's real CHECK-constraint limits.
Verifying it also caught a real, pre-existing, cross-cutting bug: the
app's `<Toaster />` was mounted only inside the map route's own page
component, so `pushError`/`pushWarning` on every *other* route
(`/features`, `/admin/*`, `/connectivity`, `/relationships`,
`/settings`) silently had nowhere to render -- fixed by mounting it
once in `app.tsx`, above every route, instead.
The day before, a batch of six low-effort open items
closed at once: `GET /features/{code}` gained real `?sort=`/`?order=`/
`?status=` query params; establishing a relationship now runs a soft
`ST_Distance`-based plausibility check (never blocking, just a new
amber warning toast when two related features' real geometries are
implausibly far apart); `symbology_rules.min_zoom`/`max_zoom` are now
sent to the frontend and actually enforced against the map's real zoom
level; a small loading badge shows while the highlight halo's feature
fetch is in flight; and polygon vertex editing was re-verified against
a real polygon (no bugs found). A seventh item, range domains in the
admin UI, was investigated and found to need a schema migration plus
compiler/CRUD work before an admin form would have anything real to
write to -- re-sized from S to M and left open rather than half-built.
Earlier the same day, Phase 6 slice 1 shipped: a real admin UI
(`api/internal/admin`, `/admin/feature-classes`, `/admin/domains`,
`/admin/field-groups` in the frontend) for authoring `metadata.*`
end to end -- a brand-new feature class with subtypes, fields,
terminals, and default symbology in one wizard, plus domains and field
groups -- without writing SQL, the first write path into any of those
tables that goes through the API rather than a hand-written seed file
or migration. Deliberately scoped to purely-additive operations on a
brand-new class only; see the Phase 6 section below for why editing an
already-compiled class's subtypes or an already-referenced domain's
values stays out of scope. Earlier the same day: a round of direct
feedback on the digitizing tool and the rest of the diagram canvas in
practice -- connect/relate target picking now works from the diagram,
placements and conductors highlight when selected or acting as a
connect source/target, the canvas has its own independent pan/zoom
camera and a Select/Pan tool toggle, placements snap-align to each
other while dragging, an unconnected conductor's loose-end marker is
no longer a visible circle, and digitizing a new feature now opens the
real `FeaturePanel`/`feature-create-form.tsx` instead of a second,
bespoke form -- on top of the same day's earlier rounds (a confirm
dialog before removing a diagram placement, seeding the first real
`geometry_type = 'none'` feature class, and the digitizing tool itself)
and the diagram-canvas fixes before that (conductors as wires, diagram
naming, rotate, remove).

## Phase 1 — Metadata core

| Item | Status | Size | Value | Detail |
|---|---|---|---|---|
| Multi-class atomic publish | Open | M | Compiling two related feature classes today is two separate `metadata_versions` rows/transactions; a real "validate everything, compile everything, apply as one unit" workflow doesn't exist yet. | `docs/phase-1-metadata-core.md` |
| Publish-time validation beyond individual `CHECK` failures | Open | S–M | E.g. "every feature class needs a default `symbology_rules` row before publish" isn't enforced anywhere. | `docs/phase-1-metadata-core.md` |
| Computed fields (`is_computed = true`) | Open | M | Compiles to a plain nullable column today, not a generated one (e.g. `length_m` via `ST_Length(geom)`) -- nowhere in the metadata yet to store the expression. | `docs/phase-1-metadata-core.md` |

## Phase 2 — CRUD / connectivity / relationships engine

| Item | Status | Size | Value | Detail |
|---|---|---|---|---|
| No filtering or sorting on list | Done | S | `GET /features/{code}` now takes real `?sort=`/`?order=`/`?status=` query params (whitelisted sort columns via bind params, `id` always appended as a stable tiebreaker), replacing the old `created_at, id`-only ordering. An unrecognized `sort` column is silently ignored, matching this endpoint's existing lenient handling of `limit`/`offset`. | `docs/phase-2-crud-engine.md` |
| Attachments (RustFS-backed) | Open | L | A genuinely different design surface (upload flow, size/type limits) not yet worked through in enough detail to build. | `docs/phase-2-crud-engine.md`, `docs/phase-2-relationships.md` |
| Diagrams (backend) | Done | L | Superseded by Phase 5's own slice -- `runtime.diagram_instances`/`diagram_placements`, a new `api/internal/diagram` package (create/list/get/delete an instance, place/move/list/remove a feature on it), gated on `is_diagram_container`. See the Phase 5 section below for the frontend canvas, now also done. | `docs/phase-5-diagrams.md` |
| `numeric` fields round-trip through Go `float64` | Open | S | Not exact-decimal-safe; worth revisiting if a field ever needs currency-grade precision. | `docs/phase-2-crud-engine.md` |
| No `PATCH` for a connection or relationship | Open | S–M | Both are re-established or revoked, never edited in place -- fine for now, but a wrong terminal/kind pick means delete-and-redo. | `docs/phase-2-relationships.md` |
| `ST_DWithin` plausibility check for relationships | Done | S | A soft check (never blocks the write), run after a relationship is already committed: `ST_Distance` on both features' real geometries, transformed to EPSG:4326/cast to `geography` so the result is real meters regardless of either class's SRID. Over 500m, the response carries a new `plausibility_warning`; the frontend surfaces it as a new amber "warning" toast, distinct from the existing red "error" ones. | `docs/phase-2-relationships.md` |
| Direction-agnostic `network_rules` matching | Done | S | `matchRule` required an exact `from`/`to` feature-class match with no reverse fallback -- since `from`/`to` on a rule is an authoring convention (edge vs. terminal-bearing junction), not real current direction, establishing the same physical link succeeded or failed purely based on which endpoint the UI's connect workflow happened to start from. `matchRuleEitherDirection` now tries both the request's pair and the swapped pair, always writing the connection in the rule's own canonical direction. | `docs/phase-2-connectivity.md` |

## Phase 3 — Map loop

| Item | Status | Size | Value | Detail |
|---|---|---|---|---|
| Metadata-driven symbology resolution | Done | M–L | Closed the core "looks like a real GIS" gap -- `metadata.symbology_rules` now resolves client-side into real per-class, per-attribute SVG icon shapes (points) and color/width (lines/polygons), most-specific-wins over subtype + domain-value. Zoom-gating and a real sprite pipeline remain deliberately deferred -- see "What's still open" below. `render_context = 'diagram'` is now consumed too, by Phase 5's diagram canvas. | `docs/phase-3-symbology.md` |
| Real sprite pipeline | Open | L | `symbology-icons.ts` is a small hardcoded SVG registry, not a real asset-serving system -- fine at five icons, would need real design work to scale to a genuine utility symbol library. | `docs/phase-3-symbology.md` |
| Symbology zoom-gating | Done | S | `min_zoom`/`max_zoom` were already loaded server-side but never sent to the frontend. Now included in the schema response and enforced in `resolveSymbology`/`createSymbologyStyle`, converting the map's real `resolution` to a zoom level via `View.getZoomForResolution` -- a rule outside the current zoom is skipped, falling back to a less-specific in-range rule or the plain geometry-type default, same fallback behavior as "no rule matches." | `docs/phase-3-symbology.md` |
| Azure AD SSO + backend auth | Open | L | Legitimately greenfield -- no session/token handling, no user table, no frontend auth state anywhere (`README.md`: "Auth and permissions are deliberately not modeled yet"). A prerequisite for Phase 6 access control more than something the map loop itself needs. | `docs/phase-3-ui-shell.md` |
| Real Martin soft-delete-aware fix | Open | L, unresolved unknown | Today's fix (`deleted-features-store.ts`) is purely client-side and session-local -- a fresh tab or a different user still sees a soft-deleted feature. The real fix (compiler-generated `deleted_at IS NULL` view, Martin serving from it) is blocked on an unconfirmed question: Martin auto-discovers tiles by table name, so the view would need to be published under the same name the table currently uses -- needs a spike before it can be sized honestly. | `docs/phase-3-delete-confirmation.md` |
| Metadata-driven `snapping_rules` | Open | M | `docs/phase-3-snapping.md` shipped a plain OL `Snap` interaction against real feature geometry; the metadata table (per-feature-class-pair tolerance/eligibility) is still schema-only, deliberately deferred as a distinct, later layer on top of what exists now. | `docs/phase-3-snapping.md` |
| Peer-picker disambiguation for connect-mode target-picking | Done | S | Connect-mode's target pick now reuses the same peer-picker normal selection already had for stacked/overlapping features, instead of always taking the topmost hit. Picking one routes to `connect-store`'s `pickTarget` rather than normal `select`; a `clearSelected: false` option on `showCandidates` keeps the workflow's source feature visible in `FeaturePanel` throughout, a real regression caught before shipping. | `docs/phase-3-connectivity-ui.md` |
| Revoking a connection from the UI | Done | S | Each connection row in the Terminals tab now has a small unlink icon opening an `AlertDialog` confirm (same pattern as feature delete), calling the existing `DELETE /connectivity/{id}`; a broad `["connections"]`-prefix cache invalidation refreshes both ends of the connection without a reload. | `docs/phase-3-connectivity-ui.md` |
| No loading affordance on the highlight halo | Done | S | A small "Loading feature…" badge (top-left of the map) now shows while the halo's underlying feature fetch is genuinely in flight -- gated on TanStack Query's `isLoading` (no cached data yet + a real fetch running), not shown on a cache hit. | `docs/phase-3-map-aesthetics.md` |
| Floating panel resize/minimize | Open | S–M | The panel undocked into a movable floating window (see the Done row below); resizing and minimizing it were flagged together as later scope when undocking was first pinned, and remain out of scope -- fixed `w-96 h-64` for now. | `docs/phase-3-floating-panel.md` |
| Polygon vertex editing, real re-verification | Done | S | Verified against the real `SUBSTATION_YARD` polygon: dragged a vertex, saved, confirmed via `GET` the new coordinate actually persisted (not just moved on screen), then restored the original geometry exactly. No bugs found -- the shared line/polygon `Modify` code held up unchanged. | `docs/phase-3-edit-geometry.md` |
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
| `geometry_type = 'none'` with a real feature class | Done | S | `PROTECTIVE_RELAY` (`seed/protective_relay_example.sql`) is the first feature class to actually exercise the no-map-geometry path the compiler and CRUD engine have supported since Phase 1 -- `srid = NULL`, no `geom` column, an ANSI device-number coded field, and its own `render_context = 'diagram'` symbology rule (the first ever authored in this project). | `docs/phase-5-diagrams.md` |
| Conductors render as wires + no more generic auto-drawn edges | Done | M | A `geometry_type = 'line'` placement (e.g. `MV_CONDUCTOR`) no longer renders through the point-icon path -- it resolves its own real connectivity endpoints and draws *as* the line between them, styled with its own map/diagram color, falling back to a small draggable marker when fewer than two endpoints are placed. The old generic "one elbow per connectivity row" pass is gone entirely -- a wire only appears when its own conductor is actually placed, matching real one-line diagram convention. | `docs/phase-5-diagrams.md` |
| Diagram naming: display + rename | Done | S | `runtime.diagram_instances.name` was already in every API response but never shown; the canvas header now shows the real name (falling back to `diagram_type`) and a new `PATCH /diagrams/{id}` endpoint lets it be renamed by clicking the title. | `docs/phase-5-diagrams.md` |
| Rotate a placement on the diagram canvas | Done | M | A drag handle appears above the app's currently-selected placement (reusing the existing global selection state); dragging computes the new angle from the pointer's position relative to the placement's center, snapped to 15-degree increments, and writes back through the existing placement upsert independently of position. | `docs/phase-5-diagrams.md` |
| Remove a placement from the diagram canvas | Done | S | A hover-revealed "×" control on every placement calls the `DELETE /diagrams/{id}/placements/{placementID}` endpoint that has existed since the backend slice, now behind a real `AlertDialog` confirm step (`RemovePlacementDialog`, same pattern as revoking a connection or deleting a feature) -- shipped without one first, reversed after direct feedback that a hover-revealed "×" alone was too easy to hit by accident. | `docs/phase-5-diagrams.md` |
| Admin UI for `DIAGRAM_TYPE` values / `is_diagram_container` / creating the first diagram instance | Done | M -- turned out to be S once scoped, see detail | Rescoped before building: two of the three were already true (`is_diagram_container` via `PATCH /admin/feature-classes/{code}` and the create wizard since Phase 6 slice 1; `DIAGRAM_TYPE` is an ordinary coded domain, already authorable via `admin-domains.tsx`). The one real gap -- creating the first `diagram_instances` row for a container -- is now closed: a new `useCreateDiagramInstance` hook wraps the already-generic `POST /diagrams`, and `feature-panel.tsx` gained a "Create diagram" trigger shown only when the selected feature is a container with zero diagrams. Verified against a local, non-Docker Postgres/API/Martin stack, end to end with Playwright. | `docs/phase-5-diagrams.md` |
| Diagram containers, data model + API | Done | L | `runtime.diagram_instances`/`diagram_placements` (a diagram belongs to one container feature, a placement puts any feature at an x/y/rotation inside it, independent of real geometry), a new `api/internal/diagram` package, gated on `metadata.feature_classes.is_diagram_container`. `SUBSTATION_YARD` is the first real container. Curl-verified end to end against a real Postgres/API stack, including both rejection gates and cascade delete. | `docs/phase-5-diagrams.md` |
| Diagram frontend canvas | Done | M–L | A hand-rolled SVG canvas (no new dependency, reuses `symbology-icons.ts`'s `iconDataUri` directly) in a side-by-side split view next to the map, not a floating overlay or a new route. Renders placements as symbology-resolved icons plus labels, and real `feature_connectivity` edges between placements that are both on the same diagram. Triggered from `FeaturePanel` via a "View diagram" button, shown only when the selected feature's class is a diagram container with at least one diagram instance. First real consumer of `render_context = 'diagram'` symbology (falls back to `'map'` symbology when no diagram-context rule exists yet). | `docs/phase-5-diagrams.md` |
| Add existing feature to a diagram, from the canvas | Done | S | A `+` button in the diagram panel opens a cross-class name search (the same fan-out `feature-search.tsx`'s map toolbar box uses, extracted into `use-feature-name-search.ts` so both share it) and places the picked feature via the existing `POST /diagrams/{id}/placements` endpoint -- no new backend work needed. New placements land just outside the current bounding box so repeated additions spread out instead of stacking; already-placed features are filtered out of the search results. Placing a brand-new, geometry-less feature is now a separate, also-done capability -- see the "Digitize a geometry-less feature" row below. | `docs/phase-5-diagrams.md` |
| Digitize a geometry-less feature straight onto a diagram | Done | M | A new header trigger next to "Add feature" lists only `geometry_type = 'none'` classes (e.g. `PROTECTIVE_RELAY`) -- the one hard rule, since there's no way to give a spatial class real map geometry from inside a diagram. Arming it and clicking the canvas starts a `draw-store.ts` draft with no geometry and a `diagramPlacement` instead, which the real `FeaturePanel`/`feature-create-form.tsx` picks up and renders -- not a second, bespoke form. Saving does two real API calls for one user action: `POST /features/{class}` with no `geometry` key, then `POST /diagrams/{id}/placements` at the clicked point -- no backend changes needed. Escape cancels cleanly at both the armed and form-open stages (the latter now a global handler, fixing the same gap for the map's own draw flow too). | `docs/phase-5-diagrams.md` |
| Connect/relate target picking from the diagram canvas | Done | S | A plain click on a placement or fully-connected conductor picks it as the waiting connect/relate target (`connect-store.ts`'s `pickTarget`) when a workflow is active, mirroring `voltedge-map.tsx`'s own click handler exactly -- `ConnectConfirmPanel` needed no changes, since it already reads the same store regardless of which panel called it. | `docs/phase-5-diagrams.md` |
| Selection/connect-role highlight on the diagram canvas | Done | S | A placement or conductor now gets a colored ring/halo when selected or acting as a connect-mode source/target, reusing `map-layers.ts`'s `HIGHLIGHT_COLORS` (now exported) so the colors match the map's own highlight layer exactly instead of a second palette. | `docs/phase-5-diagrams.md` |
| Pan/zoom + Select/Pan tool on the diagram canvas | Done | M | The viewBox no longer recomputes from placement bounds on every render -- a `camera` rect fits once per diagram and then stays put under manual pan/zoom (a new toolbar: MousePointer2/Hand tool toggle, zoom in/out, fit-to-view). Wheel zooms centered on the cursor; the Hand tool drags to pan via the SVG's own CTM scale, same technique drag-to-reposition already used. | `docs/phase-5-diagrams.md` |
| Placement-to-placement snap alignment on the diagram canvas | Done | S | A Magnet toggle (on by default) in the new view toolbar. While dragging, a placement's x and y each independently snap to any other placement's exact x or y within a threshold scaled to the diagram's own current extent -- alignment to another device, not a fixed grid. | `docs/phase-5-diagrams.md` |
| Round marker on an unconnected conductor's loose end | Done | XS | The visible dot read as a stray symbol rather than "this wire is waiting for its other end" -- it's now an invisible-but-still-interactive hit target (`fill: transparent`, `pointerEvents: all`) instead of a painted circle. | `docs/phase-5-diagrams.md` |
| Drag-to-reposition + routed elbow edges on the diagram canvas | Done | M | Shown a real one-line diagram, the auto-placed/straight-line canvas didn't hold up. Placements now drag via pointer events (screen coordinates converted into the SVG's own viewBox space via `getScreenCTM`), writing back through the existing placement upsert on drop with an optimistic query-cache update to avoid flicker; a plain click still selects, same as before. Edges now render as a right-angle elbow `<path>` instead of a straight `<line>`. Neither React Flow nor Fabric.js was reconsidered as the fix -- both were re-evaluated and rejected again, since neither ships schematic routing or symbol conventions either. | `docs/phase-5-diagrams.md` |

## Phase 6 — Domain expansion + hardening

| Item | Status | Size | Value | Detail |
|---|---|---|---|---|
| Add a subtype / domain value to an already-compiled class | Done | M | `syncCheckConstraints` (`api/internal/compiler/ddl.go`) now rebuilds the subtype/coded-domain `CHECK` constraint on every recompile of an existing table, via `information_schema`-driven `DROP CONSTRAINT`/`ADD CONSTRAINT` -- safe because every operation the new `POST /admin/feature-classes/{code}/subtypes` and `POST /admin/domains/{code}/values` endpoints can produce is strictly widening, which Postgres's default `ADD CONSTRAINT` validation always accepts against existing rows. Editing or removing an existing subtype/value is still not exposed anywhere -- only adding. Backend curl-verified; frontend typechecked/linted but not yet browser-verified (see the doc). | `docs/phase-6-admin-metadata.md` |
| Column type change / rename / delete on an existing field | Open | M–L | The compiler's existing-table path has never supported this, on any class, at any point in this project -- a real design question (data migration on retype, dependent view/constraint handling), not a quick add. | `docs/phase-6-admin-metadata.md` |
| Range domains in the admin UI | Done | M -- corrected from an original S, see detail | Closed with the real schema change the original sizing missed: `migrations/0020` adds `range_min`/`range_max` to `metadata.domains`; `compiler/ddl.go` emits a `CHECK (field BETWEEN min AND max)` for any field linked to a range domain (both on initial `CREATE TABLE` and a later `ADD COLUMN`); `crud/validate.go` gained the Go-side range check mirroring the existing coded-domain one; `POST /admin/domains` accepts `range_min`/`range_max` when `domain_type = range`; the domains admin page gained a Coded values/Numeric range toggle. Verified against a local (non-Docker) Postgres, backend curl end to end and frontend Playwright end to end. | `docs/phase-6-admin-metadata.md`, `api/internal/compiler/ddl.go` |
| Edit a feature class's own basic info after creation | Done | S | `PATCH /admin/feature-classes/{code}` -- `name`/`is_diagram_container` are always editable (pure metadata); `geometry_type`/`srid`/`network_role` only while the class is still draft, since those three are exactly what the compiler builds the physical table's shape from. Inline edit form on the admin feature-classes page, shown only for a draft row. | `docs/phase-6-admin-metadata.md` |
| Delete a feature class / domain / field group from the admin UI | Done | S–M | Scoped to what's provably safe: a feature class only while still draft (cascade-deletes its subtypes/fields/terminals/symbology_rules, all already `ON DELETE CASCADE`); a domain or field group only while no field currently references it (a friendly pre-check, not a raw foreign-key-violation 500). Deleting an already-compiled class, or a referenced domain/field group, both stay exactly as unresolved as the row originally flagged -- this didn't answer that question, just carved out the subset that never runs into it. | `docs/phase-6-admin-metadata.md` |
| Azure AD SSO + backend auth | Open | L | Legitimately greenfield -- no session/token handling, no user table, no frontend auth state anywhere. Deliberately kept out of this slice -- see the doc's "Why this is its own slice" section -- but still the prerequisite for any real access control on top of the metadata this slice now lets you author. | `docs/phase-6-admin-metadata.md`, `docs/phase-3-ui-shell.md` |
| Admin metadata UI: feature class catalog + create wizard | Done | L | `api/internal/admin` (new package, mounted at `/admin`) plus four new frontend routes (`/admin/feature-classes`, `.../new`, `/admin/domains`, `/admin/field-groups`) -- the first write path into `metadata.*` that goes through the API instead of hand-written SQL. Create-a-domain, create-a-field-group, and create-a-feature-class-end-to-end (subtypes, fields, terminals, one default symbology rule, in one transaction) all real; compile is a direct call to the same `compiler.Compile` the CLI has always used. Deliberately scoped to purely-additive operations on a brand-new class only -- see the doc for the CHECK-constraint finding that drew that line. Curl- and browser-verified end to end. | `docs/phase-6-admin-metadata.md` |

## Phase 7

Not started; see `README.md`'s phase list for its one-line scope
(field collection). Too far out to itemize yet.
