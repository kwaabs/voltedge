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

## What's still open

- **Drag-to-place / drag-to-reposition on the canvas.** Placements
  render and are clickable, but nothing on the canvas itself writes a
  new position yet — moving a feature still means a direct `POST
  /diagrams/{id}/placements` call, same as the backend slice's own curl
  walkthrough below.
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
