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

**Last updated:** 2026-08-15, a Phase 8 follow-up closed: the map's
`FeaturePanel` had a read-only "Rules" tab (network/relationship/
snapping rules for the selected feature's class) but nowhere to see a
*feature's own* resolved material data. A direct request to add a
Materials tab for adding/editing a feature's materials led to a design
correction: a feature's material isn't a separate thing to author --
it's already just the value of one of the feature's own domain-linked
fields, settable through the existing Attributes tab edit form exactly
like any other coded field. Building a second add/edit workflow for the
same data would duplicate that form. The real gap was narrower: nowhere
did a user see the *resolved* engineering numbers (resistance,
ampacity, ...) behind whichever value a field currently holds. The
Rules tab was removed outright (not kept alongside) and replaced with a
**read-only** Materials tab, matching how Rules was also read-only.
Closes the "reading resolved material numbers from the feature-facing
schema endpoint" item the original Phase 8 slice flagged as out of
scope: `GET /feature-classes/{code}/schema`'s `fieldSchema` gained an
`omitempty` `material_specs` map (keyed by domain value code), resolved
server-side by a new `resolveMaterialSpecs` in `api/cmd/server/main.go`
onto the same shared handler `useFeatureClassSchema` already calls, not
a second admin-only round trip through `/admin/domains`. The new tab's
"Edit in Attributes" button starts the existing edit session *and*
switches the panel to the Attributes tab in one click, landing directly
on the real field to change. Backend curl-verified (a real compiled
field, linked and unlinked domain values, `omitempty` absence
confirmed on a non-material field); frontend Playwright-verified end to
end against a real feature: confirmed Rules is gone from the tab bar,
confirmed the Materials tab renders the resolved name/resistance/
ampacity chips, and confirmed the Edit button lands on a pre-filled
Attributes form. See `docs/phase-8-materials.md`.

Before that, a new Phase 8 opened and its first row
closed: a materials catalog letting a coded domain value carry real
physical/electrical properties (resistance, reactance, ampacity), the
dependency the next planned capability (voltage drop and loading/
capacity checks along a traced path) needs before it can compute
anything real. The design question worth recording: whether a material
spec attaches to a feature class directly (a new field kind, a real
cross-schema foreign key the compiler would need to learn to emit) or
to the *domain value* a field already selects (one nullable column on
`metadata.domain_values`, zero compiler or CRUD-engine changes). The
domain-value route won -- a coded field inherits real numbers for free
the moment its domain's values happen to be material-linked, reusing
the exact admin UI a domain value already has. New Materials admin
page; a material-spec picker added to the Domains page's own value
editor. Backend curl-verified end to end (including the delete-guard
while a spec is still referenced, and the database's own numeric
`CHECK` rejecting a negative value); frontend Playwright-verified on
both surfaces. See `docs/phase-8-materials.md`.

Before that, the third and final Phase 6 row from the
same three-part walkthrough closed: seeing what network, relationship,
and snapping rules apply to a selected feature, via a new "Rules" tab
on the feature panel backed by `GET /feature-classes/{code}/rules`
(`api/internal/rules`, a new package). Unlike the other two items from
this walkthrough, this one isn't an admin *write* capability at all --
it's a read-only view, and it lives outside `api/internal/admin`
entirely. The interesting gap: `network_rules` and
`relationship_classes` have always been loaded pair-scoped (both
from/to sides required), right before validating one specific proposed
write in `api/internal/connectivity`/`api/internal/relationship`; this
needed the browsing shape instead -- every rule where a class appears
on *either* side -- so three new class-scoped loaders were added
alongside the existing pair-scoped ones rather than replacing them.
`metadata.snapping_rules` was the more interesting find: it's had zero
rows, anywhere in this project, since the table was created in Phase
1 -- no Go model had ever been written for it, and the frontend's own
snap toggle is a client-side switch, not driven by this table at all.
Seeded one real row by hand (a `SNAP_TARGET_TYPE` domain plus one
`snapping_rules` row via `psql`) to prove the read/resolve path
actually works end to end, not just that it compiles against an empty
table, then removed the scratch data afterward. Backend curl-verified
against real `network_rules`/`relationship_classes` seed data (six and
two rows respectively) from both sides of an asymmetric class pair;
frontend Playwright-verified: searched for a real feature by name,
opened the new Rules tab, confirmed all three sections rendered
correctly with zero console errors.

Before that, a second Phase 6 row closed from the same
three-part walkthrough: editing and deleting an existing domain's own
values (label/sort_order, or the value entirely), and editing a
domain's own name/range bounds, via `PATCH /admin/domains/{code}`,
`PATCH /admin/domains/{code}/values/{value_code}`, and
`DELETE /admin/domains/{code}/values/{value_code}`. The interesting
question was whether deleting a value needed the same referenced-by-a-
field precheck `deleteDomain`/`deleteFieldGroup` already use --
checked the schema and it doesn't: `fields.domain_id` is a foreign key
to a domain, never to a specific value row, so there's nothing for
Postgres to reject at the FK level either way. The real safety
question -- does real feature data already hold this code -- turned
out to already have an answer on record: `compiler/ddl.go`'s
`syncCheckConstraints` doc comment already states that a narrowing
change fails loudly on the next recompile (Postgres's own
`ADD CONSTRAINT` validates against existing rows), rather than
silently applying it. Verified that directly rather than trusting the
comment: deleted a value three real `MV_CONDUCTOR` rows still held,
recompiled, got a clean constraint-violation naming the exact
conflict, restored the value, recompiled clean again. Domain admin
page gained a pencil icon per domain (editing name/range) and per
value pill (editing label/sort_order), plus a delete "×" per pill
reusing the same confirm-dialog component the page's own domain-delete
action already used. Backend curl-verified end to end; frontend
Playwright-verified.

Before that, editing an
existing field's domain, group, `nullable`, and visibility settings in
place, via `PATCH /admin/feature-classes/{code}/fields/{name}` --
requested right alongside two other admin gaps in the same walkthrough
(editing domain values, and seeing what network/relationship/snapping
rules apply to a selected feature), scoped into three separate slices
rather than one. This one landed first because it was the smallest and
the safest: every setting the endpoint touches is one the rest of the
system already reads live from `metadata.fields` on every request (the
CRUD engine for `nullable`/visibility, a following recompile for
domain/group), never something baked into the physical column at
`ADD COLUMN` time -- so editing it in place needed no compiler change
and no recompile to take effect. `domain_code`/`group_code` use an
explicit empty-string sentinel to mean "clear the assignment," since
JSON's null-vs-omitted can't otherwise distinguish "clear" from "leave
alone" through a plain nullable string. Name and data type are still
untouchable -- that's its own, harder, still-open row (the compiler has
never had a rename/retype path for any class). Backend curl-verified
end to end against a real Postgres; frontend Playwright-verified: the
new pencil-icon edit affordance on the feature-class detail page opens
a pre-filled form, and toggling `visible_on_update` off then back on
round-trips correctly through the table.

Before that, adding a field
to an already-existing feature class, plus a real "see this class's
fields and domains together" detail page (`/admin/feature-classes/{code}`)
-- prompted by walking through what the admin UI could and couldn't do
yet, which surfaced this as the one concrete gap behind several "no"
answers (can't add a field to an existing class, so nothing downstream
of that -- attaching a domain to one, seeing it in the create/edit
forms -- was possible either). No compiler change was needed: the
existing-table `ADD COLUMN` path already handled a field present in
metadata but missing physically, it just had no admin endpoint to
insert that metadata row in the first place. Verification caught a
real, previously-latent bug: naming a new field the same as one of the
compiler's own system columns (`status`, `geom`, `id`, ...) silently
rode on the real column instead of erroring, and on recompile
`syncCheckConstraints` would overwrite that column's actual
constraint with the new field's domain -- e.g. a field literally named
`status` would replace the real `draft`/`published` CHECK with an
arbitrary domain-value one. Fixed with an explicit reserved-name
rejection in both the new endpoint and the original create-feature-
class path, re-verified clean afterward. Backend curl-verified end to
end (happy path, reserved-name rejection, duplicate-name rejection,
bad `required_for_subtypes` rejection, pre/post-recompile drift) and a
full Playwright pass on the new detail page and add-field form against
a local, non-Docker Postgres/API stack.
The Phase 5 "admin UI for DIAGRAM_TYPE
values / is_diagram_container / creating the first diagram instance"
row closed before that -- scoped down before building, the same way this log's
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
| Add a field to an already-existing class, and a way to see a class's own fields/domains together | Done | S | Neither needed compiler changes -- the existing-table `ADD COLUMN` path already picked up any field present in metadata but missing from the physical table (true since before `api/internal/admin` existed), just never reachable through the API. New `POST /admin/feature-classes/{code}/fields`, and a new `/admin/feature-classes/{code}` detail page (reusing the same `GET .../schema` response the map's own forms already read) showing every field's data type, resolved domain (values or range), visibility flags, and required-for-subtype rules in one place -- the admin list page had only ever shown a field *count*. Caught a real pre-existing landmine while verifying: naming a new field the same as a compiler system column (`status`, `geom`, ...) silently rode on the real column and let `syncCheckConstraints` overwrite its actual constraint -- fixed with an explicit reserved-name check in both this new endpoint and the original create-feature-class path. | `docs/phase-6-admin-metadata.md` |
| Edit an existing field's domain/group/nullable/visibility settings | Done | S | `PATCH /admin/feature-classes/{code}/fields/{name}` -- safe because domain/group assignment, `nullable`, and the three visibility flags are all read live from `metadata.fields` by whatever consumes them (the CRUD engine on every request, a following recompile for the domain's own `CHECK`), never baked into the physical column at `ADD COLUMN` time. Name and data type stay fixed -- the endpoint doesn't accept either, since the compiler still has no rename/retype path (next row). Edit affordance (pencil icon per row) added to the feature-class detail page. Backend curl-verified (happy path, domain clear via an explicit empty-string sentinel, bad domain code, empty-body, unknown field/class); frontend Playwright-verified. | `docs/phase-6-admin-metadata.md` |
| Column rename / data type change on an existing field | Open | M–L | The compiler's existing-table path has never supported this, on any class, at any point in this project -- a real design question (data migration on retype, dependent view/constraint handling), not a quick add. Distinct from the row above: that one edits a field's *settings*, this one is its *identity* (name, data_type). | `docs/phase-6-admin-metadata.md` |
| Edit/delete a domain's own values, and its name/range bounds | Done | S–M | `PATCH /admin/domains/{code}` (name always; range_min/range_max on a range domain only), `PATCH /admin/domains/{code}/values/{value_code}` (label/sort_order -- always safe, a CHECK is built from codes, never labels), `DELETE /admin/domains/{code}/values/{value_code}`. Deleting a value needs no referenced-by-a-field precheck the way deleting a whole domain does: `fields.domain_id` is a foreign key to `metadata.domains` itself, not to any specific value row. Safe by the same design `compiler/ddl.go`'s `syncCheckConstraints` already documents for widening a coded domain (see the "add a subtype/domain value" row above) -- if a later recompile tries to narrow a field's CHECK past real data still using the deleted code, Postgres's own `ADD CONSTRAINT` rejects it outright rather than silently narrowing. Confirmed directly: deleted a value 3 real rows still used, recompiled, got a clean constraint-violation error naming the exact row conflict, not silent data loss. Domain admin page gained a pencil icon per domain (name/range) and per value pill (label/sort_order), plus a delete "x" per value pill reusing the existing confirm-dialog component. | `docs/phase-6-admin-metadata.md` |
| See what network/relationship/snapping rules apply to a selected feature | Done | M | New `GET /feature-classes/{code}/rules` (`api/internal/rules`, new package) returns every rule where the class appears on either side, resolved to readable codes/names -- unlike the existing pair-scoped loaders `api/internal/connectivity`/`api/internal/relationship` already use to validate one proposed write, these are class-scoped browsing loaders (`LoadNetworkRulesForClass`/`LoadRelationshipClassesForClass`/`LoadSnappingRulesForClass`). `metadata.snapping_rules` had no Go model at all before this -- added `SnappingRule` and seeded a real row by hand to prove the read path, since the table has held zero rows anywhere in this project since Phase 1. New "Rules" tab on the feature panel, class-scoped (cached by `featureClassCode`, not per-instance like Terminals/Relationships) since every feature of a class gets the same answer, plus a matching "Rules" section on the admin feature-class detail page (same hook/endpoint, rendered as tables) -- both are equally natural homes since there's no specific feature instance involved either way. Read-only -- none of the three tables gained a write path. Backend curl-verified end to end; frontend Playwright-verified on both surfaces. | `docs/phase-6-rules-visibility.md` |
| Range domains in the admin UI | Done | M -- corrected from an original S, see detail | Closed with the real schema change the original sizing missed: `migrations/0020` adds `range_min`/`range_max` to `metadata.domains`; `compiler/ddl.go` emits a `CHECK (field BETWEEN min AND max)` for any field linked to a range domain (both on initial `CREATE TABLE` and a later `ADD COLUMN`); `crud/validate.go` gained the Go-side range check mirroring the existing coded-domain one; `POST /admin/domains` accepts `range_min`/`range_max` when `domain_type = range`; the domains admin page gained a Coded values/Numeric range toggle. Verified against a local (non-Docker) Postgres, backend curl end to end and frontend Playwright end to end. | `docs/phase-6-admin-metadata.md`, `api/internal/compiler/ddl.go` |
| Edit a feature class's own basic info after creation | Done | S | `PATCH /admin/feature-classes/{code}` -- `name`/`is_diagram_container` are always editable (pure metadata); `geometry_type`/`srid`/`network_role` only while the class is still draft, since those three are exactly what the compiler builds the physical table's shape from. Inline edit form on the admin feature-classes page, shown only for a draft row. | `docs/phase-6-admin-metadata.md` |
| Delete a feature class / domain / field group from the admin UI | Done | S–M | Scoped to what's provably safe: a feature class only while still draft (cascade-deletes its subtypes/fields/terminals/symbology_rules, all already `ON DELETE CASCADE`); a domain or field group only while no field currently references it (a friendly pre-check, not a raw foreign-key-violation 500). Deleting an already-compiled class, or a referenced domain/field group, both stay exactly as unresolved as the row originally flagged -- this didn't answer that question, just carved out the subset that never runs into it. | `docs/phase-6-admin-metadata.md` |
| Azure AD SSO + backend auth | Open | L | Legitimately greenfield -- no session/token handling, no user table, no frontend auth state anywhere. Deliberately kept out of this slice -- see the doc's "Why this is its own slice" section -- but still the prerequisite for any real access control on top of the metadata this slice now lets you author. | `docs/phase-6-admin-metadata.md`, `docs/phase-3-ui-shell.md` |
| Admin metadata UI: feature class catalog + create wizard | Done | L | `api/internal/admin` (new package, mounted at `/admin`) plus four new frontend routes (`/admin/feature-classes`, `.../new`, `/admin/domains`, `/admin/field-groups`) -- the first write path into `metadata.*` that goes through the API instead of hand-written SQL. Create-a-domain, create-a-field-group, and create-a-feature-class-end-to-end (subtypes, fields, terminals, one default symbology rule, in one transaction) all real; compile is a direct call to the same `compiler.Compile` the CLI has always used. Deliberately scoped to purely-additive operations on a brand-new class only -- see the doc for the CHECK-constraint finding that drew that line. Curl- and browser-verified end to end. | `docs/phase-6-admin-metadata.md` |

## Phase 7

Not started; see `README.md`'s phase list for its one-line scope
(field collection). Too far out to itemize yet.

## Phase 8 — Materials + electrical analysis

New phase, not a continuation of Phase 6/7: a user question ("what
else could benefit from the trace engine or the features we have,
beyond tracing itself?") turned into two threads -- a materials
catalog (locked and built first, since electrical analysis needs real
per-conductor numbers to compute anything) and electrical analysis
proper (voltage drop, loading/capacity checks -- not started).

| Item | Status | Size | Value | Detail |
|---|---|---|---|---|
| Materials catalog -- link a coded domain value to real engineering numbers | Done | M | New `metadata.material_specs` table (code, name, category, resistance/reactance/ampacity/diameter/weight, each `NULL or > 0`) plus one nullable `material_spec_id` FK on `metadata.domain_values` -- deliberately attached to a *domain value*, not a feature class directly, so a field like `MV_CONDUCTOR.conductor_spec` inherits real numbers for free the moment its domain's values happen to be material-linked, with zero compiler or CRUD-engine changes. `POST/GET/PATCH/DELETE /admin/material-specs`, and `material_spec_code` added to the existing domain-value create/edit requests (`updateDomainValueRequest` uses the same nil/""/value sentinel `updateFieldRequest.domain_code` already established). New Materials admin page; a material-spec picker added to the Domains page's own value editor, with a wrench-icon indicator on a linked value's pill. Follow-up (same day, prompted by a real "how do I attach a material to a feature class?" question): the feature-class detail page's own Fields table now shows the same wrench indicator per domain value, cross-referencing the already-fetched `useAdminDomains()` client-side rather than widening the shared schema endpoint -- a side effect is every domain-linked field's pills now show real labels instead of bare codes. Backend curl-verified end to end (including the delete-guard while referenced, and the database's own numeric `CHECK` rejecting a negative value); frontend Playwright-verified on all three surfaces. | `docs/phase-8-materials.md` |
| Read-only Materials tab on the map's own feature panel | Done | S | Replaced the feature panel's "Rules" tab outright with a read-only "Materials" tab, closing the "reading resolved material numbers from the feature-facing schema endpoint" gap the original catalog slice left open. `GET /feature-classes/{code}/schema`'s `fieldSchema` gained an `omitempty` `material_specs` map (keyed by domain value code), resolved by a new `resolveMaterialSpecs` in `api/cmd/server/main.go` onto the same shared handler the map/admin forms already call. A design correction from the original request (add a tab to *author* materials): a feature's material is already just the value of one of its own domain-linked fields, editable through the existing Attributes tab form -- so this tab only surfaces the resolved numbers behind whichever value is currently set, plus an "Edit in Attributes" button that starts the edit session and switches to the Attributes tab in one click, rather than duplicating that form. Backend curl-verified; frontend Playwright-verified against a real compiled field and feature instance. | `docs/phase-8-materials.md` |
| Voltage drop along a traced path | Open | M | Needs a traced path's conductor segments' `resistance_ohm_per_km` (now available via the materials catalog above) times real geometry length (`ST_Length`, already available), accumulated per segment, plus a load figure at the far end. |
| Loading/capacity check | Open | M | Compare a conductor's `ampacity_amps` (now available) against connected load summed downstream of a point via an existing trace -- flags overloaded equipment. Natural first electrical-analysis capability to build: needs the least new modeling beyond what tracing + materials already provide. |
| Transformer loading % | Open | S–M | Needs a transformer kVA rating field (not yet modeled on `DIST_XFMR`) compared against downstream connected load, same trace-sum approach as loading/capacity above. |
| Fault current / short-circuit analysis, protection coordination | Open | L | A bigger modeling lift than the three rows above -- source impedance, sequence networks. Treated as a later, separate effort rather than folded into this phase's first slice. |
