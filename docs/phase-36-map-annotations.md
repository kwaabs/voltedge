# Phase 36 — Map annotations (redlining)

Free-form markup drawn directly on the **geographic** map: text notes, boxes/
areas, lines, and arrows/leaders — each **individually styled** (this note red,
that box dashed). A review/redlining layer, not assets.

## Why a bespoke table (not a feature class)

A text note looks like "a point with a label," which the metadata-driven feature
system could model cheaply. But that system styles **per class**
(`symbology_rules` / `label_def`), never **per instance** — so a feature class
gives uniform notes, never "make *this* one red and *that* one bigger."
Redlining needs per-annotation styling, which is exactly what
`runtime.diagram_elements.style` already models on the diagram side. So map
annotations are the **geographic sibling of `diagram_elements`**: same idea
(non-feature furniture with a typed core + heterogeneous `style` jsonb), but
geo-referenced (a real PostGIS geometry in SRID 4326) instead of the diagram's
local x/y space.

They live in `runtime` alongside `diagram_elements` and the other generic,
non-compiled infrastructure — furniture, not a compiled feature: no traces, no
connectivity, no place in the feature catalog.

## Slice 1 — schema foundation (code-complete)

[migrations/0060_create_map_annotations.sql](../migrations/0060_create_map_annotations.sql):

- `kind` ∈ `text | box | line | arrow` (CHECK) drives rendering/semantics; a
  single `geom geometry(Geometry, 4326)` column holds the matching shape
  (text→Point, box→Polygon, line/arrow→LineString).
- `text` (nullable — the note content, required in spirit for `text`, an
  optional caption otherwise), `style` jsonb (NOT NULL default `{}`, per-
  annotation appearance), `z_index`, `territory_id` (plain uuid, no FK — the
  feature `territory_id` convention from migration 0028), audit +
  `created_by`/`updated_by` identity placeholders.
- GIST index on `geom` (bbox/territory reads) + a `territory_id` index.
- Geometry wire format mirrors [api/internal/crud](../api/internal/crud):
  `ST_AsGeoJSON` out, `ST_GeomFromGeoJSON` in.

## Slice 2 — API (code-complete)

A new [api/internal/mapannotation](../api/internal/mapannotation) package,
mounted at `/map-annotations`, modelled on the diagram engine's shape
(`Routes(db)` + per-handler closures + `writeJSON`/`writeError`, its own tiny
`validationError`/`notFoundError` copy):

- `GET /?territory_id=&bbox=minx,miny,maxx,maxy` — list, newest-drawn last,
  optionally scoped to a territory and/or the current lon/lat viewport
  (`ST_Intersects` against `ST_MakeEnvelope(...,4326)`); ordered by `z_index`
  then `created_at`.
- `POST /` — create. Validates `kind` against the closed set, requires
  geometry, checks the GeoJSON type matches the kind (`text`→Point,
  `box`→Polygon, `line`/`arrow`→LineString — the generic geom column can't, so
  Go does), requires non-empty text for `kind='text'`, defaults `style` to
  `{}`, and **spatially assigns a territory** from the geometry via
  `territory.Assign` (override honoured), mirroring the CRUD create path.
- `GET /{id}` — one annotation.
- `PATCH /{id}` — partial update of geometry/text/style/z_index. `kind` is
  immutable, so a new geometry is validated against the row's existing kind; a
  geometry move re-runs territory assignment. Absent fields are left unchanged.
- `DELETE /{id}` — 204, or 404 if the row is gone.

Geometry in/out is GeoJSON (`ST_AsGeoJSON(geom)::text` out,
`ST_SetSRID(ST_GeomFromGeoJSON(?),4326)` in), same wire convention as
`api/internal/crud`; SRID is a fixed `4326` (the redline layer is always
geographic, unlike a metadata-configurable feature class). `style` is validated
to a JSON object.

Auth: a new `annotations:read` / `annotations:write` capability pair — Go
constants `auth.PermAnnotations{Read,Write}` in
[rbac.go](../api/internal/auth/rbac.go), catalog rows + role grants seeded by
[migrations/0061_seed_map_annotation_permissions.sql](../migrations/0061_seed_map_annotation_permissions.sql)
(read: viewer/analyst/editor + admins; write: editor + admins, mirroring
`features:write`). Mounted under a method-aware
`auth.RequirePermission(PermAnnotationsRead, PermAnnotationsWrite)` gate in
[cmd/server/main.go](../api/cmd/server/main.go).

## Slice 3 — map render (code-complete)

A dedicated OpenLayers `VectorLayer` reading annotations (GeoJSON) and styling
each feature from its own `style` jsonb, with a Layers-panel toggle. Read-only
(create/edit is Slice 4).

- **Data**: [use-map-annotations.ts](../web/src/hooks/use-map-annotations.ts) —
  `useMapAnnotations(enabled)` fetches `GET /map-annotations` (the `enabled`
  gate is the overlay toggle, mirroring `useTerritoryBoundaries`), typing
  `MapAnnotation` (geometry as raw GeoJSON, `style` as `AnnotationStyle`).
- **Store**:
  [map-annotations-store.ts](../web/src/stores/map-annotations-store.ts) — a
  single persisted `visible` toggle (the master switch that also gates the
  fetch), the redline sibling of `territory-overlay-store`. Defaults **on**
  (annotations are the point of the layer, unlike the large territory overlay).
- **Renderer**:
  [`annotationStyleForFeature`](../web/src/lib/map-layers.ts) — branches on the
  feature's `kind` property (not geometry type — arrow and line share
  LineString), reading the per-instance `AnnotationStyle`: `text` →
  font/colour/halo `Text`; `box` → fill (`fill`/`fill_opacity`) + stroke;
  `line` → stroke (`color`/`stroke_width`/`dash`, reusing `lineDashArray`);
  `arrow` → the line stroke **plus a `RegularShape` triangle** at the last
  vertex, rotated to the final segment's bearing (map bearing → OL's
  clockwise-from-up: `π/2 − atan2(dy,dx)`). Defaults to a warm redline red
  (`#dc2626`), distinct from every other role in the file.
- **Layer wiring**:
  [voltedge-map.tsx](../web/src/components/map/voltedge-map.tsx) — one
  client-side `VectorLayer`/`VectorSource` at **zIndex 7** (above features,
  territory, imported overlays, and the analysis highlights 0–6 so markup stays
  readable on top; below the active-authoring draft/edit layers 8/10), created
  in the setup effect and repopulated by a load effect that reprojects the
  4326 geometry to the view CRS and tags each feature with its kind/text/style.
  The layer is in **no** click/hover `layerFilter` (those target tile layers,
  or the specific territory/imported layers), so it never intercepts feature
  selection — Slice 4 adds explicit annotation hit-testing.
- **Toggle**:
  [layers-panel.tsx](../web/src/components/map/layers-panel.tsx) — an
  "Annotations" row (red `PencilLine`) in the Overlays section beside
  Territories.

## Slice 4 — draw/edit UX

Split into two sub-slices for reviewability: **4a** (draw-create, select,
text/z-index edit, delete) and **4b** (full style editor + in-map geometry
reshape).

### Slice 4a — draw, select, edit text, delete (code-complete)

Redlining is faster than feature placement: an annotation has no attribute form,
so a draw creates the row immediately and hands it to a small editor.

- **Store**:
  [annotation-edit-store.ts](../web/src/stores/annotation-edit-store.ts) — a
  thin, non-persisted session store: `drawingKind` (which kind is armed) +
  `selectedId` (which existing annotation is open). Drawing and selecting are
  mutually exclusive (each action clears the other). The redline sibling of
  draw-store + edit-store, but far thinner (no draft/attribute-form staging).
- **Mutations**: [use-map-annotations.ts](../web/src/hooks/use-map-annotations.ts)
  gains `useCreateAnnotation` (resolves to the created row so the map can select
  it), `useUpdateAnnotation`, `useDeleteAnnotation` — all invalidating
  `["map-annotations"]` so the overlay repopulates. Bodies `CreateAnnotationBody`
  (kind + GeoJSON geometry + optional text/style/z_index) and
  `UpdateAnnotationBody` (all optional; `kind` immutable so never sent).
- **Menu**: [new-annotation-menu.tsx](../web/src/components/map/new-annotation-menu.tsx)
  — an "Annotate" dropdown (Text note / Box / Line / Arrow) beside "New
  feature", with an active-draw banner + Cancel, mirroring
  new-feature-menu.tsx. **Gated on `annotations:write`** via `usePermission` —
  a viewer never sees it.
- **Draw wiring**: [voltedge-map.tsx](../web/src/components/map/voltedge-map.tsx)
  — an effect keyed on `annotationDrawingKind` attaches an OL `Draw` (kind →
  Point/Polygon/LineString via `OL_ANNOTATION_DRAW_TYPE`) on a throwaway scratch
  layer; on drawend it converts to 4326 GeoJSON (`geometryToGeoJSON`) and POSTs
  immediately (text kind seeded with placeholder `"New note"` the editor
  replaces, satisfying the server's non-empty-text rule), then selects the new
  row. Escape cancels.
- **Selection**: the click handler gains an annotation branch **before** the
  imported-overlay branch (annotations are topmost at zIndex 7), gated on
  `annotations:write` + layer visible, hit-testing only the annotation layer;
  a hit selects it (the OL feature id is the row id, set by the load effect's
  GeoJSON reader) and returns. A viewer's clicks fall straight through to
  feature selection, so a redline they can't edit never swallows the feature
  under it. A guard also early-returns while a draw is armed.
- **Editor**: [annotation-edit-panel.tsx](../web/src/components/map/annotation-edit-panel.tsx)
  — a compact bottom-left card shown while `selectedId` is set: edits the note
  text (required non-empty for `text`, optional caption otherwise) and stacking
  order, Saves (PATCH), and Deletes. Reads the selected row straight from the
  already-fetched overlay data (no second request). Mounted unconditionally in
  map.tsx but only reachable through the write-gated selection path.

### Slice 4b — style editor + geometry reshape (code-complete)

The editor panel grows the full per-instance style controls and in-map geometry
gestures; the renderer gains text rotation.

- **Renderer**: [map-layers.ts](../web/src/lib/map-layers.ts) — `AnnotationStyle`
  gains `rotation` (degrees clockwise, text only); `annotationText` applies it
  (degrees → OL radians, `rotateWithView` left default-false so a label keeps its
  screen angle). The redline fall-back values are exported as
  `ANNOTATION_DEFAULTS` so the editor seeds its controls from the same numbers
  the renderer uses.
- **Store**: [annotation-edit-store.ts](../web/src/stores/annotation-edit-store.ts)
  gains a geometry-gesture state — `tool` (`"move"` → Translate, `"vertices"` →
  Modify, `"rotate"` → a drag-to-spin gesture, or null) + `dirtyGeometry` (the
  staged GeoJSON) + `dirtyRotation` (the staged text angle in degrees) +
  `setTool` / `setDirtyGeometry` / `setDirtyRotation` / `clearGeometry`. Staged,
  not PATCHed per drag, so a background refetch can't clobber the bound feature
  mid-gesture (the territory-edit pattern).
- **Editor**: [annotation-edit-panel.tsx](../web/src/components/map/annotation-edit-panel.tsx)
  — Move plus a second gesture button that is Reshape for shapes (box corners /
  line endpoints) or **Rotate for a text note** (a lone Point has no vertices to
  reshape). Then per-kind style controls: colour for all; stroke-width +
  line-style (the shared `LINE_DASH_STYLES`) for line/arrow/box; fill colour +
  fill opacity for a box; font size + bold/italic + a **read-only rotation
  readout** for text (the angle now comes from the in-map Rotate drag, not a
  slider — a `dirtyRotation` sync effect folds the live angle into the style
  state). Everything stages locally and commits in one PATCH on Save (with the
  `dirtyGeometry` when a gesture produced one); Save's success clears the
  geometry state so the overlay repopulates with the saved shape.
- **Map wiring**: [voltedge-map.tsx](../web/src/components/map/voltedge-map.tsx)
  — a geometry-edit effect attaches `Translate` (move) / `Modify` (reshape) to
  the selected annotation's own feature in the overlay source and stages the
  result; for **Rotate** it attaches a `PointerInteraction` that computes the
  screen-space bearing from the note's anchor pixel to the cursor (`atan2` on
  y-down pixels = clockwise degrees, matching OL `Text.rotation`), live-writes it
  onto the feature's `style` prop for preview, and stages it via
  `setDirtyRotation`; the load effect holds off repopulating while a `tool` is
  armed (its dependency) so the feature — and its live rotation preview — survive
  the gesture; the click handler's guard also bails while a gesture is armed, so
  a stray click can't reselect out from under it.

> Fix (this iteration): a **caption on a box / line / arrow is now rendered**.
> The editor always let those kinds carry free-form text, but
> `annotationStyleForFeature` only drew text for `kind === "text"`, so an arrow's
> caption was saved yet invisible. Added `annotationCaption()` — a label at the
> geometry's label point (OL point-places text at a line's midpoint / a polygon's
> interior point), lifted a line-height above the stroke — appended to the box /
> line / arrow style arrays. (Reminder: the caption only persists after **Save**.)

> Follow-up (this iteration): the text **rotation slider was replaced by a
> drag-to-rotate map gesture** at the user's request ("move to rotate instead of
> using the bar?"). Rotation of a text note is a style angle on a Point, so there
> is no native OL interaction — the Rotate tool is a bespoke `PointerInteraction`
> that spins the note to face wherever you drag. The panel keeps only a read-only
> degree readout.

## Slice 5 — export (planned)

Include annotations in the KMZ and DXF exports (they already carry real 4326
geometry + a style, so they map onto KML placemarks / DXF entities alongside
features).

## Verification owed (Bash blocked here — run locally)

- Slice 1: apply migrations; confirm `runtime.map_annotations` exists with the
  `kind` CHECK, a `geometry(Geometry,4326)` `geom`, the GIST + territory
  indexes, and the `style` default `{}`.
- Slice 3: `tsc`/`lint` in `web/`; toggle Annotations in the Layers panel;
  with a few annotations seeded via the Slice 2 API, confirm each kind renders
  (text note, filled box, dashed line, arrow with a head pointing the right
  way), the layer hides/shows with the toggle, redlines sit above features but
  clicking one still selects the feature underneath (no interception), and the
  visibility choice survives a reload.
- Slice 4b: `tsc`/`lint` in `web/`; select an annotation, then — Move drags the
  whole thing, Reshape drags box corners / line endpoints (and is disabled for a
  text note), colour/stroke/dash/fill/opacity/font/bold/italic change the mark,
  the Rotate tool (text only) spins the note to follow a drag on the map with a
  live degree readout, and Save commits all of it in one PATCH (including a
  moved/reshaped geometry and the rotation angle); confirm a second Move after a
  Save still works (the feature isn't clobbered by the refetch), and that the
  rotation preview survives across the drag (the load effect's `tool` guard).
- Slice 4a: `tsc`/`lint` in `web/`; as a writer — draw each kind (text places
  a "New note" you can rename, box/line/arrow draw and open the editor), click
  an existing annotation to reopen it, edit text + stacking order and Save,
  Delete one, Escape mid-draw cancels. As a viewer — no "Annotate" button, and
  clicking an annotation selects the feature underneath (no interception, no
  editor).
- Slice 2: `go build ./...` / `go vet ./...` in `api/`; apply migration 0061
  and confirm the `annotations:read`/`annotations:write` rows + role grants;
  smoke the routes — `POST /map-annotations` a text/box/line/arrow (each with
  the matching GeoJSON type; a mismatch must 400), `GET /?bbox=...`, `PATCH`
  the geometry (territory re-resolves), `DELETE` (missing id → 404); confirm a
  session without `annotations:write` is 403 on the mutating verbs but can GET.
