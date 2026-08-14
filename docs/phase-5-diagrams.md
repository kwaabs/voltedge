# Phase 5 — diagram containers, backend and frontend canvas

**Goal:** the first two slices of Phase 5 (README: "Diagrams, attribute
groups, MCP") — a real data model and API for placing features on a
schematic diagram, independent of their real map geometry, then a real
canvas to render one. The backend slice was deliberately built first,
no canvas view yet, matching how connectivity, relationships, and the
trace engine all shipped in this project — the data model proven solid
via curl against a real Postgres/API stack before any UI is built on
top of it. See "Frontend canvas, added later" below for the second
slice.
**Status:** both slices done. Backend verified against a real
Postgres/API stack — create/get/list/delete a diagram instance,
place/move/list/remove a feature on it, both zero-trust gates
(non-container feature class, unknown diagram type), cascade delete,
and 404 handling all confirmed with real curl calls, not assumed from
the code. Frontend canvas verified live in the browser, light and dark
mode, including click-to-select and real connectivity edges between
placed features.

## Why this slice exists, and why backend-only

The user asked "what is next? diagrams?" and, once the actual size of
the surface became clear (a new data model, a new API, and a genuinely
new frontend canvas view — the biggest jump in scope of any slice this
session), was asked how to approach it rather than treating it as
another quick item. Two decisions came back explicit:

- **Backend first**, matching how every other major feature in this
  project started (connectivity, relationships, the trace engine all
  shipped backend-first, curl-verified, before any UI) — the diagram
  placement model and `render_context = 'diagram'` symbology (unused
  since Phase 3) both deserve their own focused UI slice once the data
  model is proven, not built and verified for the first time in the
  same pass as a canvas.
- **Flip `SUBSTATION_YARD` to a diagram container** for the first real
  diagram, rather than also proving `geometry_type = 'none'` (a
  feature that only ever exists via a diagram placement, never on the
  map) in the same slice — that path is real and already supported end
  to end (see "What was already there"), just not exercised with an
  actual feature class yet.

## What was already there

Before writing anything, the actual code was checked rather than
assumed: `docs/metadata-core.md`'s "Diagram containers" section had
already designed this in Phase 1, and most of the foundation was
already built and working, just never exercised:

- `metadata.feature_classes.geometry_type` has allowed `'none'` since
  the very first migration (`0007_create_feature_classes.sql`) — a
  feature class with no `geom` column at all, whose only visual
  representation is ever a diagram placement.
- `metadata.feature_classes.is_diagram_container` has existed since
  that same migration, already commented as "true for feature classes
  that can host a one-line/schematic diagram of the features inside
  them."
- The schema compiler (`api/internal/compiler/ddl.go`) already skips
  generating a `geom` column when `geometry_type = 'none'` --
  `buildCreateTable` guards the call to `geometryType()` behind exactly
  that check.
- The CRUD engine (`api/internal/crud/validate.go`) already rejects a
  geometry on a `geometry_type = 'none'` feature class, and requires
  (and type-checks) one otherwise.

None of that needed to change. What was actually missing was the
`diagram_instances`/`diagram_placements` tables themselves and
everything above them.

## What was built

- **`migrations/0019_create_diagram_tables.sql`** — `runtime.diagram_instances`
  and `runtime.diagram_placements`, in the `runtime` schema
  `migrations/0016` already reserved and already named ahead of time
  ("`feature_connectivity`/`feature_relationships`, attachments,
  diagram tables"). `diagram_instances` ties one diagram to a
  container feature (polymorphic `feature_class_id` + `feature_id`,
  same pattern as `feature_connectivity`'s `from`/`to`) and a
  `diagram_type_value_id` — a `domain_values` row, "just another row in
  domains, not a new lookup table," same as trace direction.
  `diagram_placements` puts any feature at an `x`/`y`/`rotation` inside
  one diagram instance, unique per (diagram, feature) so placing the
  same feature twice moves it rather than duplicating it. No `is_valid`
  gate on placement itself — the only legality question this whole
  model has is whether the container's feature class has
  `is_diagram_container = true`, enforced by the API before ever
  writing a `diagram_instances` row.
- **`metadata.ResolveDomainValueByCode`** (new, `api/internal/metadata/load.go`)
  — `DomainValueCode`'s inverse: given a domain code and a value code
  (e.g. `DIAGRAM_TYPE`/`ONE_LINE`), returns the `domain_values` row.
  Generically useful, not diagram-specific — the existing
  `DomainValueCode` only ever went id → code, and this is the first
  place in the system that needed the reverse (a human-typed code from
  a request body, resolved to the id a foreign key actually needs).
- **`api/internal/diagram`** (new package) — deliberately not shaped
  like `connectivity`/`relationship`: there's no rule table to match a
  placement against and no cardinality, so it's closer to a small,
  explicit CRUD surface than a zero-trust rule engine:
  - `POST /diagrams` — create a diagram instance for a container
    feature. Resolves the container, rejects if its feature class
    isn't a diagram container, resolves `diagram_type` against the
    `DIAGRAM_TYPE` domain, writes. No upsert/idempotency here unlike
    connectivity/relationship's `establish` — a container can host more
    than one diagram (a one-line and a control-scheme diagram for the
    same substation is a real, expected case per
    `docs/metadata-core.md`), so there's no natural key to upsert
    against.
  - `GET /diagrams?container_feature_class=&container_feature_id=` —
    list every diagram instance hosted by one container feature.
  - `GET /diagrams/{id}`, `DELETE /diagrams/{id}` (cascades to its
    placements via `ON DELETE CASCADE`).
  - `POST /diagrams/{id}/placements` — place or move a feature: an
    idempotent upsert on the table's own unique constraint, same
    pattern connectivity/relationship's own upsert-on-conflict
    establish already uses.
  - `GET /diagrams/{id}/placements`, `DELETE /diagrams/{id}/placements/{placementID}`.

## How this was validated

Driven against a real Postgres + Go API, not assumed from the code:

1. Applied migration 0019 (`runtime.diagram_instances`/`diagram_placements`
   created with all FKs, indexes, and the explicitly-named unique
   constraint — its natural Postgres-derived name would have exceeded
   the 63-byte identifier limit and been silently truncated, so it's
   named explicitly instead).
2. Seeded a `DIAGRAM_TYPE` domain + `ONE_LINE` value, and flipped
   `SUBSTATION_YARD.is_diagram_container` to `true` (`seed/diagram_example.sql`
   — schema-level only, same discipline every other `runtime.*` seed
   file in this project follows: the actual instance rows always come
   from a real API call, never a raw `INSERT`, so nothing is ever
   speculative even in demo data).
3. Created a real `SUBSTATION_YARD` feature ("Main Substation Yard")
   via `POST /features/SUBSTATION_YARD`, then a real diagram instance
   for it via `POST /diagrams`, then placed the real "Substation Power
   Transformer" (`DIST_XFMR`) and "Switch S0" (`DIST_SWITCH`) features
   on it via `POST /diagrams/{id}/placements`.
4. Confirmed `GET /diagrams` (by container), `GET /diagrams/{id}`, and
   `GET /diagrams/{id}/placements` all return the expected data.
5. Confirmed placing the transformer a second time at new
   `x`/`y`/`rotation` moved it (same placement `id`, same total
   placement count) rather than creating a duplicate.
6. Confirmed both rejection gates: `POST /diagrams` against `DIST_XFMR`
   (a real feature class, just not a diagram container) rejected with
   `is_diagram_container = false`; against an unknown `diagram_type`
   rejected with a clear "not a valid DIAGRAM_TYPE value" message.
7. Confirmed `DELETE .../placements/{id}` removes one placement without
   touching others, and `DELETE /diagrams/{id}` cascades to remove
   every placement on it (tested against a disposable throwaway
   instance, not the real demo one).
8. Confirmed `GET /diagrams/{nonexistent-id}` returns a real 404 with a
   clear error message, not a generic failure.

`go build ./... && go vet ./... && gofmt -l .` all finish clean.

## Decisions made

- **Backend-only this slice.** See "Why this slice exists" above.
- **`SUBSTATION_YARD` as the first container, not a new
  `geometry_type = 'none'` feature class.** Proves the placement
  model with features that already exist; the geometry-less path is
  real and already supported by the compiler/CRUD engine (see "What
  was already there") but deliberately not exercised with an actual
  feature class in this slice.
- **No upsert on `POST /diagrams`.** Unlike connectivity/relationship's
  `establish`, a container hosting more than one diagram is expected,
  not a duplicate to collapse.
- **`ResolveDomainValueByCode` added to the shared `metadata` package,
  not kept local to `diagram`.** The reverse-of-`DomainValueCode`
  lookup (human-typed code → id) is a generically useful capability any
  future package accepting a coded value in a request body would also
  need, not something specific to diagrams.

## Frontend canvas, added later

The user asked "front end canvas, lets go" and, before any code, three
design questions: which library, and where does it live relative to the
map (floating panel? resizable? movable? minimizable?). The library
question got a real answer, not a default: React Flow was the initial
recommendation (a node+edge graph fits a one-line diagram, pan/zoom and
custom nodes come built in), but the user pushed back ("svg. react
flow? are you sure? fabric?"). Re-evaluated honestly rather than
defended — the actual scope is one substation's handful of devices, not
a large flow chart; this project has hand-rolled every other
interaction (dragging, connect-mode, the peer picker) rather than
reaching for a library; and `symbology-icons.ts`'s `iconDataUri` SVG
strings plug directly into a hand-rolled SVG canvas with zero
translation layer. Fabric.js was considered too (canvas/raster-based,
no native edge concept, no first-party React bindings) and rejected as
the weakest fit. Landed on: **hand-rolled SVG, no new dependency**. For
placement, "once its not on a new page and its on the map, or side by
side on the map, i'm ok with it" ruled out a dedicated route; the final
choice was **side-by-side split view**, not a floating overlay — the
map keeps its own full interaction surface, the diagram gets its own
panel alongside it.

- **Backend groundwork.** Two fields the schema endpoint had been
  deliberately withholding needed to reach the frontend before any
  canvas code could work: `featureClassResponse.is_diagram_container`
  (so the UI can know which selected feature's class can even host a
  diagram) and `symbologySchema.render_context` (previously filtered to
  `"map"` only inside `resolveSymbologySchema`, back when diagrams
  didn't exist yet — now both contexts are sent, and each consumer
  filters client-side, same "every icon decision happens client-side"
  principle `docs/metadata-core.md` already commits to for symbology).
- **`web/src/lib/symbology.ts`'s `resolveSymbology`** gained a required
  `context: "map" | "diagram"` parameter — now that both contexts'
  rules arrive together, every call site has to say which one it's
  rendering, or a diagram-context rule could silently start matching on
  the map. `map-layers.ts` and `layers-panel.tsx`'s legend both pass
  `"map"`; the new diagram canvas passes `"diagram"`, falling back to
  `"map"` when no diagram-context rule exists yet (none does in the
  current seed data — `render_context = 'diagram'` rows are still
  unauthored, same open item as before, just no longer blocked on
  having a consumer).
- **`web/src/hooks/use-diagrams.ts`** (new) — `useDiagramInstances`
  (list by container feature, backs the "View diagram" trigger) and
  `useDiagramPlacements` (list by diagram id, backs the canvas itself).
  Plain `useQuery` wrappers over the existing endpoints, same shape as
  every other data hook in this project.
- **`web/src/stores/diagram-store.ts`** (new) — one field,
  `openDiagramId: string | null`, same minimal-Zustand-store pattern as
  `edit-store.ts`/`connect-store.ts`. Not persisted, not a stack — opening
  a second diagram replaces whichever was open.
- **`web/src/components/map/diagram-canvas.tsx`** (new) — the canvas
  itself. Fetches placements, then per-placement feature records
  (`useQueries`, same pattern `use-all-symbology.ts` already uses for
  per-class schema fetches — a placement only ever stores
  `x`/`y`/`rotation`, not a feature's name/subtype/attributes) and
  per-placement connectivity, so edges can be derived: a real
  `feature_connectivity` row renders as a line only when *both* its
  ends are placed on this same diagram (a feature can have real
  connectivity reaching outside the diagram entirely — a conductor
  leaving the yard — which has nowhere sensible to draw to). Icons
  resolve through the same `resolveSymbology` + `iconDataUri` path the
  map itself uses, just with `context: "diagram"`. Clicking a placed
  feature calls `useSelectionStore`'s `select`, reusing the exact same
  feature panel the map's own click-to-select already opens — a
  diagram is another way to reach a feature, not a separate feature
  viewer.
  - **Icon/text sizing is extent-relative, not a fixed pixel-like
    constant.** Placement `x`/`y` is an arbitrary local coordinate
    space chosen by whoever placed each feature — there's no fixed
    "one unit = one pixel" relationship the way the map's real geometry
    has. A first pass used fixed constants (`ICON_SIZE = 40`,
    `fontSize: 11`) against a viewBox sized to the placements' own
    bounding box, and looked fine in isolation but broke visibly once
    live-verified: the seed data's placements span roughly 100x50
    units, the panel is much taller than wide, so the effective
    px-per-unit scale factor came out large enough that the text
    dwarfed the icons and overflowed the panel edge. Fixed by deriving
    icon size, padding, and font size as fractions of the placements'
    own extent (`Math.max(width, height)`, floored at 100 units) instead
    of literal pixel-like numbers — legible regardless of what
    coordinate scale a given diagram happens to be authored in.
- **`web/src/routes/map.tsx`** — the map area became a flex row: the
  existing map `div` (`flex-1`) plus, only while
  `diagramStore.openDiagramId` is set, a `w-2/5` panel hosting
  `DiagramCanvas`. Closing (its own header `X` button) removes the
  panel and the map reclaims full width.
- **`web/src/components/map/voltedge-map.tsx`** gained a
  `ResizeObserver` on the map's container `div`, calling
  `map.updateSize()` on every resize. OL sizes its canvas off the
  target element's dimensions at creation/window-resize time only — it
  has no idea the container itself can shrink/grow without a window
  resize, which is exactly what happens when the split view opens or
  closes. Without this the map kept rendering at its pre-split width
  until the next window resize; confirmed fixed by watching the map
  visibly resize on open/close in the live verification pass.
- **`web/src/components/map/feature-panel.tsx`** — a "View diagram"
  trigger (`Workflow` icon) in the header, next to the existing trace
  dropdown. Only fetches `useDiagramInstances` at all when the selected
  feature's own class `is_diagram_container` is true (most classes
  never host one). A single diagram opens directly on click; more than
  one (a container can legitimately host several, per
  `docs/metadata-core.md`) shows a small dropdown to pick from, same
  `DropdownMenu` pattern the trace picker already uses. Zero diagrams
  for an otherwise-container-capable feature shows no trigger at all —
  there's no admin UI yet to create one, so a dead button pointing at
  nothing would be worse than no button.

Verified live in the browser (Playwright), not just build/typecheck:
the trigger appears only for `SUBSTATION_YARD` and only once its real
diagram instance exists, opening it shows the switch/conductor/
transformer placements as their resolved icons with real edges between
them (an extra placement of the shared `MV_CONDUCTOR` feature was added
to the running dev diagram through the real `POST /diagrams/{id}/
placements` endpoint specifically to exercise the edge-rendering path,
same "real API, never a raw INSERT" discipline every other runtime
instance in this project follows), the map visibly shrinks and the
resize observer keeps it correctly sized, clicking a placed icon
selects that feature and opens the same feature panel the map's own
selection uses, closing the panel returns the map to full width, and
the trigger is correctly absent for a non-container feature class
(`DIST_SWITCH`). Confirmed in both light and dark mode — the SVG's
edge/text colors are set via the same `--foreground`/`--muted-foreground`
CSS custom properties the rest of the app themes with, not hardcoded.
`npx tsc --noEmit`, `npm run lint` (oxlint), and `npm run build` all
finish clean.

## Adding existing features from the canvas, added later

The canvas above could render a diagram, but there was still no way to
put a feature onto one from the UI at all -- every placement so far had
come from a direct curl call. Asked "new features or existing
features?", the answer was existing features, for less effort: a new,
geometry-less feature needs its own creation flow (the normal
draw-on-map form doesn't apply when there's nothing to draw), while
adding an already-mapped feature is just picking one and calling the
placement endpoint that already exists.

- **`web/src/hooks/use-feature-name-search.ts`** (new) -- the
  cross-class, debounced, fan-out-by-feature-class name search
  extracted out of `feature-search.tsx` (the map toolbar's own search
  box), which now calls this hook instead of carrying its own copy.
  Needed in two places once the diagram canvas wanted the same "find a
  feature by name across every class" capability; extracting it was
  less code than a second copy, not a speculative abstraction.
- **`diagram-canvas.tsx`'s `AddFeatureButton`** (new, colocated in the
  same file) -- a `+` icon in the panel header opens a small popover:
  type a name, get cross-class results via the hook above, click one to
  place it. Results already on this diagram are filtered out (re-adding
  one would just move it, which is a different, still-open capability –
  see "What's still open"). Placement position is computed by
  `nextPlacementPosition` -- just outside the current bounding box,
  offset by a fraction of its own extent, so successive additions
  spread out left-to-right instead of stacking exactly on top of each
  other; an empty diagram's first addition lands at a fixed `(200,
  150)`. A real `POST /diagrams/{id}/placements` call, then
  `queryClient.invalidateQueries({queryKey: ["diagram-placements",
  diagramId]})` -- no client-side optimistic insert, the canvas just
  re-renders off the real refetched list, same pattern every other
  mutation in this project follows.

Verified live: searching for a named `SERVICE_POINT` ("Meter 4471 - 12
Elm Court") from an empty search returns it, clicking it places a new
icon at the expected offset position and the popover closes; a real
`GET /diagrams/{id}/placements` confirms the new row landed in Postgres
with the expected `x`/`y`; reopening the diagram after a full page
reload shows the addition persisted; searching the same name again
correctly returns "No matching features not already on this diagram."
instead of offering a duplicate; confirmed in dark mode too.

## Drag-to-reposition and routed edges, added later

Shown a real single-line diagram as a reference, the canvas as it
stood didn't hold up: every placement landed at an auto-computed spot
(`nextPlacementPosition`) that the user never chose, and edges were
raw diagonal `<line>`s between two points -- a diagram that was
*plotted*, not *drawn*. The library question came up again (React
Flow? Fabric.js?) and the answer stayed the same both times: neither
actually solves what a real one-line diagram needs (multi-segment bus
routing, a real ANSI-device symbol library, grouped/boxed annotations,
dual line semantics for power vs. control/trip circuits -- all real
gaps, still open below) -- and for the two things asked for here,
manual placement and routed wires, hand-rolled SVG was never the
limiting factor. The gap was that the drag interaction and the
elbow-routing math simply hadn't been written yet.

- **`elbowPath`** (new, `diagram-canvas.tsx`) -- every edge renders as
  a three-segment right-angle `<path>` (out horizontally from the
  source to the horizontal midpoint, down/up to the target's row,
  across to the target) instead of a straight `<line>`, matching real
  schematic convention. Bends once, at a fixed midpoint -- not the
  multi-segment bus-tap routing a dense protection-scheme diagram
  would eventually want (see "What's still open").
- **Drag-to-reposition** -- `pointerdown` on a placed icon starts
  tracking; `pointermove` (captured via `setPointerCapture`, so the
  drag keeps tracking even if the cursor leaves the icon) updates a
  local `dragState`; `pointerup` either selects the feature (no real
  movement -- a plain click still works exactly as before) or writes
  the new position through the same `POST /diagrams/{id}/placements`
  upsert every other placement write already uses. Screen-pixel
  pointer movement is converted into the SVG's own viewBox coordinate
  space via `getScreenCTM().inverse()` before being applied -- a raw
  `clientX`/`clientY` delta would drag a feature faster or slower than
  the cursor depending on how zoomed a given diagram's extent-relative
  viewBox happens to be (see the sizing comment from the first canvas
  slice).
  - The node being dragged, and any edge attached to it, both render
    from a `dragState`-aware `effectivePlacements` list rather than
    the raw query data -- bounds, `placementByKey`, and the rendered
    `<g>` all read from it, so a wire visibly follows its feature while
    it's still mid-drag, not just once the drag ends.
  - On drop, the query cache is updated optimistically
    (`queryClient.setQueryData`) *before* `dragState` clears, so there
    is no visible snap-back-then-forward flicker while the real
    request is in flight; a failed write reverts by invalidating the
    query instead.
- **`nextPlacementPosition`'s comment updated** -- it's no longer the
  only way a feature's position gets set, just where a newly *added*
  feature starts before the user drags it somewhere deliberate.

Verified live: dragging a placed icon (the switch, in the demo
diagram) to a new spot moves it, the elbow-routed wire to the adjacent
conductor follows it live during the drag, and a real `GET
/diagrams/{id}/placements` afterward confirms the new `x`/`y` landed
in Postgres (rotation unchanged); a plain click with no movement still
selects the feature and opens the same feature panel, without writing
any position change; confirmed in dark mode. The demo diagram's switch
was moved back to its original position afterward via the same real
API, same "leave the demo data as found" discipline every other
manual-verification pass in this project follows.

## Conductors as wires, naming, rotate, and delete, added later

Shown a real reference one-line diagram and asked pointed questions
about the result, four real gaps came back at once, not a single
follow-on: `MV_CONDUCTOR` -- a `geometry_type = 'line'` feature, a
physical wire -- was rendering through the exact same point-icon path
every junction device used, putting a circle in the middle of what
should be one continuous line; a second, separate elbow was
auto-drawn for *every* underlying `feature_connectivity` row
regardless of whether a real conductor was even placed, so adding an
unrelated device could make an unrequested line appear; the diagram's
own `name` (in every API response since the backend slice) was never
shown, and there was no way to set one; and there was no way to rotate
a placement (drag only ever changed position) or remove one from a
diagram (the backend `DELETE .../placements/{id}` route has existed
since the very first slice, just never called from the UI).

- **Conductors render as wires, not point icons.** A placement whose
  feature class is `geometry_type = 'line'` no longer gets an icon at
  all. It resolves its own real connectivity endpoints (via the same
  per-placement `connectionResults` the canvas already fetches) and
  renders *as* the routed line between them, styled with its own
  resolved map/diagram `color` (not an icon -- `MV_CONDUCTOR`'s seeded
  style is `{"color": "#3f8fa6", "width": 3}`, and that teal is exactly
  what the wire now renders in). With fewer than two endpoints placed
  on this diagram, it falls back to a small draggable marker at its
  own stored `x`/`y` -- a loose end waiting for its other side, still
  selectable and removable, just not yet "wired in." Once a second
  endpoint appears, it snaps into wire form automatically on the next
  render, no action needed.
- **No more generic auto-drawn connectivity edges.** The old `edges`
  computation -- one elbow per `feature_connectivity` row where both
  ends had *any* placement, regardless of what was between them -- is
  gone entirely. Every network_rule in this system already connects an
  edge-role class to a junction-role class (docs/metadata-core.md), so
  in practice every real wire *is* a placed conductor; removing the
  generic fallback means a wire only ever appears when its own
  conductor is actually on the diagram, matching how a real one-line
  diagram works -- if it isn't drawn, it isn't shown.
- **`PATCH /diagrams/{id}`** (new, backend) -- `renameInstance`
  (`api/internal/diagram/sql.go`) updates a diagram instance's `name`
  column; `Name *string` so an explicit `null` clears it, same
  pointer convention `createInstanceRequest.Name` already uses.
  `web/src/hooks/use-diagrams.ts` gained `useDiagramInstance` (fetch
  one diagram by id, the canvas's own header needs its name) alongside
  the existing per-container list fetch.
- **`DiagramTitle`** (new, `diagram-canvas.tsx`) -- the header's
  hardcoded "Diagram" text is now the real name (falling back to the
  `diagram_type` code when unnamed). Click to edit; Enter or blur
  commits via the new `PATCH`, Escape cancels without writing anything.
- **Drag-to-rotate** -- a small handle appears above a placement only
  while it's the app's own globally-selected feature (`useSelectionStore`),
  reusing the exact same selection state clicking a placement already
  sets, not a separate canvas-local concept. Deliberately rendered
  *outside* the icon's own `rotate()` sub-group so it's always found in
  the same predictable spot regardless of the feature's current
  orientation. Dragging computes the new angle from the pointer's
  position relative to the placement's true center (`atan2`, converted
  from screen pixels via the same `getScreenCTM` used for
  drag-to-reposition), snapped to 15-degree increments for a
  deliberate, schematic-appropriate feel rather than free-angle drift.
  Writes back through the same placement upsert, independently of
  `x`/`y` -- confirmed by curl that a rotate leaves position untouched
  and a position drag leaves rotation untouched.
- **`RemoveControl`** (new) -- a small "×" badge, hidden until hover
  (Tailwind `group`/`group-hover`), on every placement (point or
  conductor). Calls the pre-existing `DELETE
  /diagrams/{id}/placements/{placementID}` with an optimistic
  cache removal, same pattern every other mutation here uses.
  Deliberately no confirm dialog, unlike `RevokeConnectionDialog` or
  `DeleteFeatureDialog`: un-placing a feature only loses its cosmetic
  position on *one* diagram, trivially restored with one search-and-add
  through `AddFeatureButton` -- not a real network fact like a
  connection, and not the feature itself like a real delete.

Verified live: the demo diagram's `MV_CONDUCTOR` now renders as a
teal routed wire directly between the switch and transformer icons,
no circle in the middle; the panel header shows "Main Substation Yard
- One-Line" instead of the literal word "Diagram"; clicking the title
opens an editable input pre-filled with the current name, Escape
reverts it (confirmed via a real `GET` that nothing was written);
selecting the meter shows a rotate handle, dragging it to a real
DOM-located position changes its stored `rotation` from 0 to 90 while
leaving `x`/`y` byte-for-byte unchanged (confirmed via `GET`); hovering
the meter reveals its remove control, clicking it deletes the real
placement row (confirmed via `GET` showing three placements
remaining), and the meter was re-added afterward via `AddFeatureButton`
to leave the demo data as found. Confirmed in dark mode throughout.
`go build/vet/gofmt`, `npx tsc --noEmit`, `npm run lint`, and `npm run
build` all finish clean.

## What's still open

- **Multi-segment bus routing, not just a single-bend elbow.** Real
  protection-scheme diagrams route wires off a shared vertical/
  horizontal bus with T-junction taps, not just a point-to-point
  right-angle connector -- `elbowPath` is a real improvement over a
  straight line but still only connects exactly two placements.
- **A real electrical-schematic symbol library.** The current icon set
  (circle/square/triangle/diamond) is generic; a diagram like a
  protection one-line needs ANSI device-number circles (27, 59, 50/51N,
  86, 87, ...), CTs, PTs, disconnect switches, ground studs, and more --
  a large, ongoing content task independent of the canvas's rendering
  technology.
- **Grouping/annotation boxes.** Real diagrams label logical groupings
  (e.g. a relay panel) with a dashed box around several placements --
  there's no such freeform, non-feature-backed annotation primitive in
  the current data model (`diagram_placements` only ever points at a
  real feature).
- **Dual line semantics (power wiring vs. dashed control/trip
  lines).** Every edge the canvas draws today comes from a real
  `feature_connectivity` row. A protection scheme's dashed trip lines
  with arrowheads aren't real network connectivity at all -- they're
  schematic-only annotations, which the current model has no place for.
- **`render_context = 'diagram'` symbology rules.** The canvas now
  resolves and renders them (falling back to `'map'` symbology when
  none exists), but no diagram-specific rule has actually been authored
  in seed data yet — every placement in the demo currently renders its
  map-context icon.
- **`geometry_type = 'none'` with a real feature class.** The compiler
  and CRUD engine already support it (see "What was already there"),
  but nothing in the seeded data actually exercises it yet (e.g. a
  `PROTECTIVE_RELAY` that only ever appears via a diagram placement).
- **No admin UI for authoring `DIAGRAM_TYPE` values, flipping
  `is_diagram_container`, or creating the first diagram instance for a
  container.** Still real metadata/data changes made by hand today
  (seed SQL / a migration / a direct curl call). Adding an *existing*
  feature to an *already-created* diagram is now a real UI capability
  (see "Adding existing features from the canvas, added later" above);
  creating that first diagram instance itself, and placing a brand-new
  `geometry_type = 'none'` feature with no prior map presence, are not.

## Try it yourself

Against a running API (`DATABASE_URL=... go run ./cmd/server`,
migrations 0001-0019 applied, `seed/diagram_example.sql` applied) and,
for the frontend part, `npm run dev` in `web/`:

```bash
# Create a container feature
curl -X POST http://localhost:3704/features/SUBSTATION_YARD \
  -H "Content-Type: application/json" \
  -d '{"subtype": 1, "name": "Main Substation Yard",
       "geometry": {"type": "Polygon", "coordinates": [[[-0.1315,51.5085],[-0.1285,51.5085],[-0.1285,51.5095],[-0.1315,51.5095],[-0.1315,51.5085]]]},
       "attributes": {"condition_rating": "GOOD", "enclosure_type": "FENCED"}}'

# Create a diagram instance for it -- note the container's real id from above
curl -X POST http://localhost:3704/diagrams \
  -H "Content-Type: application/json" \
  -d '{"container": {"feature_class": "SUBSTATION_YARD", "feature_id": "<id>"},
       "diagram_type": "ONE_LINE", "name": "Main Substation Yard - One-Line"}'

# Place a feature on it -- note the diagram's real id from above
curl -X POST http://localhost:3704/diagrams/<diagram-id>/placements \
  -H "Content-Type: application/json" \
  -d '{"feature_class": "DIST_XFMR", "feature_id": "<xfmr-id>", "x": 100, "y": 200, "rotation": 0}'

# List what's on it
curl http://localhost:3704/diagrams/<diagram-id>/placements
```

Then in the browser: select the `SUBSTATION_YARD` feature just created
(search by name or click it on the map), click the "View diagram"
button (`Workflow` icon) in the feature panel's header, and the diagram
opens in a side-by-side panel next to the map showing every feature
just placed.

`go build ./... && go vet ./... && gofmt -l .` should all finish clean,
and so should `npx tsc --noEmit`, `npm run lint`, and `npm run build`
in `web/`.
