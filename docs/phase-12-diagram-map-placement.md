# Phase 12 — placing features on a diagram from the map

An enhancement to the diagrams feature (Phase 5, `docs/phase-5-diagrams.md`).
Today a feature is added to a diagram by **searching for it by name**
(`AddFeatureButton` in `diagram-canvas.tsx`). This phase makes the primary
path **spatial**: you're already looking at the feature on the map, so you
pick it there and **draw it onto the diagram** where you want it. The map is
the palette; the diagram is the canvas. Name-search stays as a fallback.

## Decisions

- **Diagrams are electrical.** A feature can only be placed on a diagram if
  its class is flagged `is_diagram_placeable` — a new per-class metadata flag
  mirroring `is_diagram_container`. Seeded from `network_role in (edge,
  junction)` (the electrical/network set: conductors, transformers, switches,
  busbars, cutouts, service points), admin-overridable per class. Structural
  classes (poles, street lights, meter chambers, yards) are not placeable, so
  picking one from the map is rejected with a toast. `geometry_type` (point vs
  line) then decides only the *draw gesture*, not eligibility.
- **Hand-drawn geometry (option B2).** A point node is placed with one click
  (`{x, y, rotation}`, as today). A line node is **drawn** as a polyline and
  the vertices are stored: a new `path jsonb` column on
  `runtime.diagram_placements` holds an ordered `[[x, y], ...]` array in the
  diagram's local coordinate space (abstract, not geographic — so plain JSON,
  not PostGIS). `path` is null for points; when present it is what the canvas
  renders, falling back to today's icon / connectivity-derived elbow when
  absent so existing diagrams are untouched.
- **One at a time.** Single-feature placement only — no box/multi-select
  (the map selection store holds one feature). Connectivity is **not** copied
  in: wiring stays owned by the connectivity UI, not re-derived by placement.
- **Entry point is on the diagram, not the feature.** An "Add from map" button
  on the diagram arms a pick mode; the map is then the picker.

## Slices

- **Slice 1 — eligibility flag.** `is_diagram_placeable` on feature classes:
  migration + seed, Go model/API, admin UI. ✅
- **Slice 2 — placement path storage.** `path jsonb` on
  `runtime.diagram_placements`; Go model, place request/response, upsert;
  frontend types. ✅
- **Slice 3 — server-side enforcement.** `placeFeature` rejects a
  non-`is_diagram_placeable` class and validates the path-vs-point shape. ✅
- **Slice 4 — map "pick for diagram" mode.** A `diagram-place-store`
  (idle → picking → placing); "Add from map" button; the map is the picker;
  the owning canvas validates the picked feature (not placeable / already
  placed → toast) and drops it where the next canvas click lands; crosshair +
  banner + Esc. ✅
- **Slice 5 — line polyline + render.** Upgrade a line placement's gesture
  from the single click-anchor of Slice 4 to a drawn polyline (click vertices,
  double-click / Finish to commit, Esc to cancel) → POST with `path`; the
  canvas renders a stored `path` (falling back to the connectivity elbow when
  absent). ✅

## Slice 1 — eligibility flag (`is_diagram_placeable`)

- **`migrations/0032_add_diagram_placeable_flag.sql`** — adds
  `is_diagram_placeable boolean NOT NULL DEFAULT false` to
  `metadata.feature_classes` and seeds it `true` where
  `network_role in ('edge', 'junction')`.
- **`api/internal/metadata/models.go`** — `FeatureClass.IsDiagramPlaceable`.
- **`api/cmd/server/main.go`** — the public `GET /feature-classes` response
  carries `is_diagram_placeable` (this is what the map-pick validation reads).
- **`api/internal/admin/*`** — create/update/list DTOs + handlers thread the
  flag through, exactly like `is_diagram_container` (it's pure metadata, safe
  to PATCH on any class regardless of compile status).
- **`web/src/hooks/use-feature-classes.ts`, `use-admin.ts`** — the flag on
  both the public and admin feature-class types.
- **`web/src/routes/admin-feature-class-new.tsx`,
  `admin-feature-classes.tsx`** — a checkbox on both the create wizard and the
  inline edit form, plus a "diagram placeable" badge in the catalog list.
- **`web/src/routes/admin-feature-class-detail.tsx`** — a **"Diagram settings"**
  section (both flags as badges + an Edit dialog toggling
  `is_diagram_placeable` / `is_diagram_container`). This is the *only* UI path
  to change these flags on an **already-compiled** class: the list page's inline
  edit form renders its Edit pencil for draft rows only, whereas the backend
  PATCH treats both flags as pure metadata editable at any status — so the
  detail section, being always available, closes that gap. Gated on
  `metadata:write` like every other write control on the page.

**Verify (owed — Bash is blocked in this environment):**
`task migrate` (applies 0032), then `cd api && go build ./... && go vet ./...`
and `cd web && npm run typecheck && npm run lint`. Check `GET /feature-classes`
returns `is_diagram_placeable: true` for conductors/transformers/switches and
`false` for poles/street lights; toggle it on a *draft* class from **Admin →
Feature classes** (inline edit) and on an *already-compiled* class from that
class's detail page → **Diagram settings** → Edit, confirming the badge and the
PATCH round-trip in both places.

## Slices 2 & 3 — path storage + server-side enforcement

Done together since they touch the same `placeFeature` path in the diagram
engine.

- **`migrations/0033_add_diagram_placement_path.sql`** — adds a nullable
  `path jsonb` to `runtime.diagram_placements`. Null for point placements;
  for a line, an ordered `[[x, y], ...]` array in the diagram's local
  coordinate space. Backward-compatible: every existing placement stays
  null and keeps rendering exactly as before.
- **`api/internal/diagram/sql.go`**
  - `DiagramPlacement.Path json.RawMessage` (`bun:"path,nullzero"`) — an
    absent path writes SQL NULL, not an empty json literal.
  - `placeFeature` now enforces **`is_diagram_placeable`** on the resolved
    feature's class (invalid → rejected), mirroring the `is_diagram_container`
    gate `createInstance` already applies — this is the server-side backstop
    behind the map-pick UI toast (Slice 4).
  - New `validatePlacementPath` helper: a nil/JSON-null path is fine for any
    class; a non-empty path is legal only on a `geometry_type = "line"` class
    and must be ≥2 `[x, y]` pairs. The upsert's `DO UPDATE` set now also
    carries `path = EXCLUDED.path`, so moving/redrawing updates it.
- **`api/internal/diagram/types.go`** — `placeRequest.Path` and
  `placementResponse.Path` (`json.RawMessage`, `json:"path,omitempty"`), so a
  point placement neither sends nor echoes a `path` key.
- **`web/src/hooks/use-diagrams.ts`** — `DiagramPathPoint = [number, number]`
  and an optional `path?: DiagramPathPoint[]` on `DiagramPlacement`. The POST
  call sites that will actually *send* a path are inline in
  `diagram-canvas.tsx` and get touched in Slices 4–5.

**Verify (owed — Bash is blocked in this environment):**
`task migrate` (applies 0033), then `cd api && go build ./... && go vet ./...`
and `cd web && npm run typecheck`. Curl checks against a diagram instance:
(a) placing a non-placeable class (e.g. a pole) → `400 is_diagram_placeable
= false`; (b) placing a point class with a `path` → `400 path is only valid
for a line feature`; (c) placing a line class with `path: [[0,0],[10,0],[10,10]]`
→ `200`, and `GET /diagrams/{id}/placements` echoes the `path` back; (d) placing
a point class with no `path` → `200`, no `path` key in the response.

## Slice 4 — map "pick for diagram" mode

The whole gesture is coordinated by a small, unpersisted
`diagram-place-store.ts` (`idle → picking → placing`), scoped to one
`diagramId` so that with several diagrams open only the armed one reacts.

- **`web/src/stores/diagram-place-store.ts`** (new) — `startPicking(diagramId)`
  (canvas button → `picking`), `pick(feature)` (map click, only while
  `picking`), `arm(feature, geometry)` (canvas validated → `placing`),
  `cancel()`. Carries `picked` (raw map click, awaiting validation) and, once
  armed, `feature` + `geometry` (`point` | `line`, from the class's
  `geometry_type`).
- **`web/src/components/map/voltedge-map.tsx`** — the map becomes the picker:
  the click handler gets a `phase === "picking"` block (ahead of the
  connect-mode block, same shape) that routes a single hit to
  `pick` and stacked hits to the peer picker; crosshair cursor while picking;
  an Escape effect that cancels at either step.
- **`web/src/components/map/peer-picker.tsx`** — a stacked-feature click during
  a diagram pick routes the chosen entry to `diagram-place-store.pick` (and the
  header reads "pick one to place"), the same way it already special-cases
  connect-mode targets.
- **`web/src/components/map/diagram-canvas.tsx`** — the "Add from map"
  (crosshair) header button toggles the pick for this diagram. Because only
  this canvas knows the catalog *and* what's already placed, validation lives
  here: an effect watches `picked` and either toasts + cancels (non-placeable
  class, or already on this diagram) or `arm`s it. Once armed, the canvas is in
  crosshair "placing" mode and the next canvas click drops the feature there
  (`placePickedFeature` → the same `POST /placements`); a banner shows the
  current step with Cancel/Esc. An unmount cleanup cancels a pick still pointed
  at this diagram if it's closed or switched away.

Point features are fully placeable from the map now (pick → click to drop).
A line placed this way lands at its click anchor and still renders from its
connectivity; drawing its polyline `path` is Slice 5.

**Verify (owed — Bash is blocked in this environment):**
`cd web && npm run typecheck && npm run lint`. In the app: open a diagram,
click **Add from map** (crosshair), then click a transformer/switch on the map
→ click on the diagram → it lands there. Pick a pole (not placeable) → toast,
mode cancels. Pick a feature already on the diagram → toast. Stacked features →
peer picker offers "pick one to place". Esc at either step cancels; the map
cursor is a crosshair while picking.

## Slice 5 — drawn line polyline + `path` render

Slice 4 already handles the full point flow; a line picked there just dropped
at its click anchor and drew from connectivity. This slice makes a line's
gesture a real polyline and renders the stored geometry.

The geometry to draw is decided at `arm` time (Slice 4's validation effect
already passes `geometry_type === "line" ? "line" : "point"` to the store's
`arm`), so `diagram-place-store.geometry` is `"line"` exactly when the canvas
should collect a polyline rather than drop a single point.

- **`web/src/components/map/diagram-canvas.tsx`**
  - Two module helpers next to `elbowPath`: `polylinePath(points)` →
    an `M …/L …` `d` string through the vertices, and `polylineMidpoint`
    → the middle vertex, used to anchor the label / remove control.
  - Draw state: `lineDraft` (vertices committed so far) and `linePreview`
    (the cursor in diagram space, for the rubber-band segment), plus
    `isLineDrawing = isPlacingHere && placeArmedGeometry === "line"`. An
    effect clears both whenever `isLineDrawing` goes false (finish / cancel /
    switch away), so no stale sketch survives.
  - Gesture: while `isLineDrawing`, each canvas click **appends** a vertex
    (instead of placing immediately); `onPointerMove` tracks `linePreview`;
    `onDoubleClick` (dropping its duplicate trailing vertex) or the banner's
    **Finish** button commits via `finishLineDraft`, which needs ≥2 points,
    anchors x/y at the first vertex, and calls `placePickedFeature` with the
    whole polyline as `path`. Esc still cancels the whole gesture.
  - Render: the in-progress draft draws on top of the svg (dots at each
    vertex, the polyline through them, a dashed rubber-band to the pointer),
    `pointerEvents: none` so it never eats its own extending clicks. The
    conductor loop now prefers a stored `path`: `drawnPath` (path with ≥2
    points) → `polylinePath` for the line + `polylineMidpoint` for the label,
    else the old connectivity elbow. A path-backed conductor owns real
    geometry, so — exactly like a `fullyConnected` one — it's select-only
    (`hasGeometry` replaces the old `fullyConnected` interaction gates): no
    midpoint drag, click to select. `effectivePlacements` spreads the whole
    placement, so `path` survives the optimistic drag/rotate remap untouched.

**Verify (owed — Bash is blocked in this environment):**
`cd web && npm run typecheck && npm run lint`. In the app: open a diagram,
**Add from map**, pick a *line* class (e.g. a conductor) → the banner reads
"Click to add line points; double-click to finish". Click 3–4 points on the
canvas; the dashed rubber-band follows the cursor and committed vertices show
as dots. Double-click (or **Finish**) → the polyline commits and re-renders
from its `path`, label at its middle vertex, select-only (no midpoint drag).
Reload → it still draws from `path`. Pick a *point* class → unchanged
single-click drop. An existing line with no `path` still draws its
connectivity elbow. Esc mid-draw discards the sketch.

## Slice 6 — editing a drawn line's vertices

A committed line placement with its own `path` can be reshaped in place, so you
don't have to delete and redraw it to fix a bend. All three edits reuse the
same place/move `POST /placements` upsert a fresh draw uses (they only change
`path`, re-anchoring `x/y` to the first vertex), so nothing new is needed on the
backend — `validatePlacementPath` already guards the ≥2-point, line-only shape.
Handles show only while the line is **selected**, keeping the diagram clean
otherwise, and only for a path-backed line (a connectivity-elbow line has no
`path` of its own to edit).

- **`web/src/components/map/diagram-canvas.tsx`**
  - **Move a vertex** — drag a round handle at each vertex
    (`handleVertexPointerDown/Move/Up`, `VertexDragState`); the dragged vertex
    snaps to peer x/y (magnet) and the grid like a whole-placement drag.
  - **Add a vertex** — a small "+" handle at each segment midpoint;
    `insertVertex(placement, segIndex)` splices the segment's midpoint into
    `path` between its two vertices, ready to be dragged.
  - **Remove a vertex** — double-click a vertex; `removeVertex(placement,
    index)` drops it, guarded so a line never falls below two points (the
    handle's tooltip advertises this, and the "+"/vertex handles hint their
    actions).
  - `persistPath(placement, path)` is the shared optimistic writer behind all
    three (re-anchor `x/y` to `path[0]` → POST), extracted from the old
    vertex-drop path so drag, insert and remove stay identical on the wire.

**Verify (owed — Bash is blocked in this environment):**
`cd web && npm run typecheck && npm run lint`. In the app: select a drawn line
→ drag a vertex to bend it; click a "+" midpoint handle → a new vertex appears
there, drag it; double-click a vertex → it's removed (and a two-point line
refuses to go lower). Reload → the reshaped `path` persists.

## Slice 7 — header "Add" / "Connect" menus (declutter)

The diagram header had grown a long row of single-purpose icon buttons. Five of
them are folded into two dropdown menus so the header breathes; Annotate,
Stencils, Templates and Close stay as they were. Pure frontend, no backend or
schema change — every menu item fires exactly the same action its old button
did.

- **`web/src/components/map/diagram-canvas.tsx`**
  - **`AddMenu`** (Plus trigger) replaces the three "add a feature" controls —
    the old `AddFeatureButton` (search-by-name) and `DigitizeFeatureButton`
    (new off-map feature) are inlined here, plus a **From map** item that arms
    the map→diagram pick/place flow. When a digitize is armed the trigger is
    swapped for the same "Click the canvas to place X · Esc" pill the standalone
    button showed, so that feedback survives. Trigger highlights while a
    map-pick is active.
  - **`ConnectMenu`** (Cable trigger) replaces the Connect and Relate buttons —
    two items, `Connect placements` / `Relate placements`, each arming its
    authoring mode. The trigger (and the active item) highlight while that mode
    is armed.
  - `digitize-feature-tool.tsx` is now orphaned (its logic moved into
    `AddMenu`); left in the tree as harmless dead code.
  - **`DiagramViewToolbar` collapse toggle** — the floating view toolbar
    (top-left overlay: select/pan, zoom, fit, snap, grid, ortho) gained a
    `collapsed` state and a `ChevronsLeft` "Hide toolbar" button at its end;
    collapsed it renders as a single `ChevronsRight` "Show toolbar" button in
    the same corner, so it stops covering the drawing but is one click back.
    **Starts collapsed on every diagram open** — `collapsed` defaults to `true`
    and the parent keys the toolbar by `openDiagramId`, so switching to another
    diagram remounts it back to collapsed; the user expands it when they want
    the view controls.

**Verify (owed — Bash is blocked in this environment):**
`cd web && npm run typecheck && npm run lint && npm run build`. In the app: open
a diagram → the header shows Add + Connect menus. Add → From map (pick a feature
on the map, click the canvas), search by name, and (if any geometry-less classes
exist) a "New off-map feature" list that arms digitize. Connect → Connect /
Relate arm the respective two-placement flows; triggers highlight while active.

## Slice 8 — full-page diagram (maximize) + carousel

A diagram normally lives in a fixed 2/5-width column beside the geographic map
(`map.tsx`). To read or edit a schematic without the map crowding it, the header
gained a **full-page toggle** (`Maximize` / `Minimize`, next to Close): one
diagram fills the whole width and the map hides.

**Carousel when several are open.** Full-page shows a *single* diagram; every
other open diagram (across both panes) collapses into a **tab strip** at the top
of the full-page area, so maximizing turns the split into a carousel instead of
stacking. Switching a tab just re-points which diagram is full-page; the
underlying pane layout is untouched, so exiting full-page returns to the exact
prior split.

- **`web/src/stores/diagram-store.ts`** — `maximizedId: string | null` (the
  diagram shown full-page, or null for the side-by-side split) with
  `maximize(id)` / `restore()`. `maximize` also makes that diagram its pane's
  active tab, so restoring lands on a sane split. `close` drops `maximizedId`
  to another open diagram when the maximized one closes (stay full-page, like
  closing a browser tab), else null.
- **`web/src/routes/map.tsx`** — when maximized (and the id is still open) the
  map column is `hidden` (kept mounted, not unmounted, so its OpenLayers view
  survives — the map's own `ResizeObserver → updateSize()` re-fits it on
  restore) and the diagram area renders `MaximizedDiagram`: a full-width column
  with a `MaximizedTab` strip (all open diagrams, click to switch, ✕ to close)
  over the active diagram's `DiagramCanvas`. Non-maximized keeps the old
  top/bottom split.
- **`web/src/components/map/diagram-canvas.tsx`** — the header toggle is
  maximized when `maximizedId === this diagram`; it calls `maximize(id)` /
  `restore()`, highlights while active, and swaps its icon/label between "Full
  page" and "Exit full page".

**Grid fills the full canvas (fix).** The drawing grid `<rect>` was sized
exactly to the viewBox (`view.w/h`), but the SVG keeps its default
`preserveAspectRatio` (xMidYMid meet), so a canvas whose aspect ratio differs
from the viewBox — notably full-page — letterboxes the viewBox and left the
margins gridless. The rect now over-covers a large margin around `view`
(`view.x − 3·view.w … 7·view.w`, same for y), which fills those margins at any
aspect ratio; a `<pattern>` fill is a single tiled paint the browser clips to
the viewport, so the extra area costs nothing.

**Verify (owed — Bash is blocked in this environment):**
`cd web && npm run typecheck && npm run lint && npm run build`. In the app: open
a diagram → Full page hides the map, diagram fills width, grid fills the whole
canvas; Exit → map returns and re-renders (not blank). Open two diagrams →
maximize one → only it shows, the other is a tab; click the tab → it takes over
full-page; ✕ on a tab closes it; closing the shown one falls back to the other
(still full-page); closing the last exits full-page.
