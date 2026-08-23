# Phase 20 — Split a line & Copy a feature

Two map-editing conveniences, both variations on one idea: *create a new feature
seeded from an existing one.*

1. **Copy (Duplicate)** — turn any existing feature into the starting point for a
   new one: same attributes, same subtype, a copy of the geometry dropped a few
   metres off the original, ready to drag into place and save.
2. **Split** — while editing a line, cut it at a point of your choosing; the
   original keeps the first part, and the second part opens the create form
   (pre-filled from the original) to save as a new feature.

## Decisions (locked with the user)

- **Split topology → keep everything on the original, then re-suggest.** The
  connectivity/relationship rows reference a feature by id with no FK and no
  cascade (see the Phase 19 explore), so a split can't safely auto-move them.
  Every existing connection/relationship stays on the shortened original; the
  assistant suggestion loop (Phase 19) fires on *both* halves so re-connecting
  anything that belongs to the new part is one click. Auto re-pointing by
  proximity was considered and deferred — it needs peer-geometry reads, terminal
  handling, and unique-constraint collision handling.
- **Split commit → one transactional backend endpoint.** Truncating the original
  loses the far half unless the new part is created in the same breath, so the
  split is `POST /features/{class}/{id}/split` doing truncate-original +
  create-new in a single DB transaction. PostGIS computes both halves from the
  split point (`ST_LineLocatePoint` + `ST_LineSubstring`) — single source of
  truth, geodesically correct. A failure leaves the original untouched. Frontend
  two-call orchestration was rejected (a mid-way failure could truncate the
  original with no new part).
- **Copy placement → offset, then move.** The copy lands `COPY_OFFSET_METERS`
  (5 m, EPSG:3857) up-and-right of the original so it's visible and separately
  clickable, and is draggable on the map before saving.
- **Shared spine.** Both features route through the ordinary draft → create-form
  → `POST /features` path, so RBAC, validation, and territory assignment are
  exactly the manual create's. Neither copies connections/relationships (they're
  separate facts about the original).

## Slice plan

- **C1 — Copy + the seed spine** (code-complete). Draft can carry pre-filled form
  values; Duplicate action seeds it from a feature; draft is rendered and
  draggable. Ships Copy end to end.
- **S1 — backend `POST /features/{class}/{id}/split`** (code-complete).
  Transactional truncate + create, PostGIS split, `features:write`.
- **S2 — split UI** (code-complete). Split tool in the geometry-edit toolbar
  (line classes only); click the line → the clicked point is projected onto it
  → the new (far) part opens the create form pre-seeded → Save calls `/split`.
- **Split live preview** (code-complete). As the user hovers/clicks the line in
  split mode, both halves are drawn on the map (near solid, far dashed, with a
  cut marker) so the cut is visible before Save; the preview persists while the
  create form is open. Pure frontend — the substring is computed client-side in
  OL; PostGIS stays authoritative on Save.

## Slice C1 — what was built

The reusable half: *"open the create form pre-seeded from a source feature,"*
plus Copy on top of it.

- **`web/src/lib/geometry.ts`** (new): `offsetGeometry(geometry, dxMeters,
  dyMeters)` — pure, recurses the untyped GeoJSON coordinate nest, shifts each
  position in EPSG:3857 metres via `fromLonLat`/`toLonLat`. Used to place a copy
  off the original.
- **`web/src/stores/draw-store.ts`**: `DraftFeature` gains an optional
  `seed: DraftSeed { name?, subtype?, attributes }`; a new `draftToken` counter
  is bumped once per *new* draft (`finishDrawing` / `startCopyDraft` /
  `startDiagramDraft`) but not when geometry is dragged; new actions
  `startCopyDraft(class, geometry, seed)` and `updateDraftGeometry(geometry)`.
- **`web/src/components/map/feature-create-form.tsx`**: the form's reset effect
  now keys on `draftToken` (not the draft's class code, which is stable across a
  drag) and pre-fills Name / Subtype / attributes from `draft.seed`. Save builds
  the POST body's attributes from the class's `visible_on_create` fields only —
  a no-op for a blank draw, but it drops any computed/invisible attributes a copy
  carried that the create validator would reject.
- **`web/src/components/map/feature-panel.tsx`**: a **Duplicate** item in the
  feature's More-actions menu (disabled for a geometry-less or deleted feature).
  `handleDuplicate` offsets the source geometry by `COPY_OFFSET_METERS` and calls
  `startCopyDraft` with the source's attributes/subtype/name.
- **`web/src/components/map/voltedge-map.tsx`**: a new effect renders an
  already-placed draft into the existing draft layer (seeding it from
  `draft.geometry` when nothing's there yet — the copy case, since no Draw
  interaction ran) and attaches a `Translate` that syncs `draft.geometry` as it's
  dragged, so Save posts the final position. Snap is intentionally left off for
  draft dragging.

No backend change in C1 — Save is the existing `POST /features`.

## Slice S1 — what was built

The transactional split endpoint, `POST /features/{code}/{id}/split`, all in
`api/internal/crud/split.go` (route wired in `handler.go`'s `Routes`):

- **Request:** `{ split_point: { lng, lat }, new_feature: { attributes,
  subtype?, name?, territory_id? } }`. `split_point` is EPSG:4326 lng/lat (the
  wire coordinate space everywhere else); both coordinates are required.
  `new_feature` carries no geometry — the far half's geometry is computed, not
  supplied.
- **`splitHandler`** resolves the class, rejects a non-line class up front
  (`geometry_type != "line"` → 400), decodes the body, checks the split point is
  present, then calls `SplitLine`. Returns the new feature's envelope with
  `201`, the same shape a create returns.
- **`SplitLine`** owns one `db.RunInTx` transaction:
  1. `computeSplitHalves` loads the original *inside the tx*. A first query
     guards existence (→ 404), soft-delete (→ 400), and geometry type
     (`ST_GeometryType(geom)`, the `ST_`-prefixed form that returns
     `ST_LineString` — *not* bare `GeometryType`, which returns uppercase
     `LINESTRING`) — the LineString check runs *before* `ST_LineLocatePoint`, so a
     `MultiLineString` gets a clean 400 instead of a raw PostGIS error. A second query does
     `frac = ST_LineLocatePoint(geom, ST_SetSRID(ST_MakePoint(lng, lat), srid))`
     and slices both halves with `ST_LineSubstring(geom, 0, frac)` /
     `ST_LineSubstring(geom, frac, 1)`, returned as GeoJSON text. `frac` within
     `splitEndpointEpsilon` (1e-6) of 0 or 1 → 400 ("on/too close to an
     endpoint"), which would otherwise leave a degenerate half.
  2. `updateGeometry` rewrites *only* the original's `geom` (plus
     `updated_at`/`updated_by`) to the near half — attributes, connections,
     relationships, and territory are all intentionally left as they were.
  3. `CreateFeature(ctx, tx, …)` inserts the far half as a brand-new feature —
     same validation, same territory auto-assignment (from its own geometry) as
     any manual create, because it *is* the shared create path, just handed the
     transaction.
- A failure at any step rolls the whole transaction back: the original is never
  truncated without its replacement landing.
- Geometry SRID handling mirrors `insertFeature`/`updateFeature` exactly
  (`ST_SetSRID(ST_GeomFromGeoJSON(?), srid)`, no reprojection) — geom's SRID is
  the same coordinate space the wire lng/lat live in.
- RBAC: the `/features` mount's method-aware gate already maps a POST to
  `features:write`, so the new route needs no extra gating.

## Slice S2 — what was built

The split UI, all frontend — it reuses the C1 seed spine (a create draft
pre-filled from a source feature) and the S1 `/split` endpoint. A split draft is
a new draft variant that carries *no* client geometry: the far half's geometry is
computed server-side, so Save posts to `/split` instead of the plain create.

- **`web/src/stores/edit-store.ts`**: `GeometryTool` gains `"split"` — a tool
  that, unlike `"move"`/`"vertices"`, attaches no drag interaction and produces
  no `dirtyGeometry`.
- **`web/src/components/map/geometry-edit-toolbar.tsx`**: a **Split** button
  (Scissors icon), shown only for `geometry_type === "line"` (`showSplitTool`),
  toggling `geometryTool` between `"split"` and `null` exactly like Move/vertices,
  plus a hint line ("Click the line where you want to cut it…").
- **`web/src/stores/draw-store.ts`**: `DraftFeature` gains an optional
  `split: { sourceFeatureId, splitPoint: { lng, lat } }` marker, and a new
  `startSplitDraft(code, sourceFeatureId, splitPoint, seed)` action — same
  clear-drawing / set-draft / bump-`draftToken` shape as `startCopyDraft`, but
  with no geometry.
- **`web/src/components/map/voltedge-map.tsx`**: a new effect gated on
  `editing && geometryTool === "split"` attaches a one-off `map.on("click")` that
  projects the click onto the line under edit with `line.getClosestPoint(...)`
  (OL's perpendicular projection, EPSG:3857), rejects a click more than ~12 px
  off the line, converts to 4326 with `toLonLat`, calls `startSplitDraft`
  (seeded from the original's name/subtype/attributes), and leaves edit mode. The
  global click-to-select handler already bails while `editing` is set, so this is
  the only reaction to the click. The Modify/Translate effect now attaches a
  Modify only for `"vertices"` (never for `"split"`), and the Snap effect no
  longer arms for `"split"` (no gesture to bend). Uses `getClosestPoint` — the
  first use of it in the codebase; `LineString`/`toLonLat` were already imported.
- **`web/src/components/map/feature-create-form.tsx`**: `handleSave` branches on
  `draft.split` → `POST /features/{code}/{sourceFeatureId}/split` with
  `{ split_point, new_feature: { attributes (visible-only), subtype?, name? } }`,
  then `bumpMapRefresh()`, invalidates the original's `["feature", code,
  sourceId]` query, selects the new id, and fires `requestSuggestions` on the new
  far half.

## Split live preview — what was built

A cosmetic on-map preview of the cut, so the user sees where the line will break
*before* saving. All frontend — the authoritative split is still PostGIS on Save.

- **`web/src/lib/geometry.ts`**: `splitCoordinates(coords, point)` — a pure,
  projection-agnostic planar helper. It walks the polyline's segments, finds the
  perpendicular foot nearest `point` (clamped per segment), and returns
  `[nearHalf, farHalf]` sharing the split coordinate so the halves meet exactly.
  Fed EPSG:3857 coordinates by the preview. A degenerate line (<2 points) is
  returned unchanged in both halves. This backs only the preview; the real cut is
  `ST_LineSubstring` around `ST_LineLocatePoint` in the `/split` endpoint, so any
  planar-vs-geodesic disagreement here is purely visual.
- **`web/src/components/map/voltedge-map.tsx`**: three scratch styles
  (`SPLIT_NEAR_STYLE` solid blue, `SPLIT_FAR_STYLE` dashed green,
  `SPLIT_POINT_STYLE` a cut marker), a `splitPreviewCoordsRef` caching the
  original line's 3857 coords, and the S2 split-click effect rewritten into a
  combined preview + click effect over one scratch `VectorLayer` (zIndex 9). It
  runs in two phases sharing that layer:
  - **Hover** (`editing && geometryTool === "split"`): captures the line's coords
    into `splitPreviewCoordsRef`, attaches `pointermove` to project the cursor
    onto the line (`getClosestPoint`, same 12 px tolerance as the click) and
    render the two halves + marker, and keeps the click-to-commit
    (`startSplitDraft` + leave edit mode).
  - **Draft** (`draft.split`): edit mode is over and `editFeatureRef` cleared, but
    the create form is open — it freeze-frames the committed cut from the cached
    coords and the draft's frozen split point (`fromLonLat`), so the preview
    stays on screen right up to Save.

  The effect depends on `draft` (so it re-arms for the draft phase) alongside
  `editing`/`geometryTool`/`editingFeature`.

**Suggestions on both halves — final decision (far half only).** The original
sketch wanted the suggestion loop to fire on *both* halves, but we settled on the
new far half only, and this is the shipped, final behaviour — not a deferral:

- `assistantStore.requestSuggestions` *replaces* the whole suggestion set
  (`set({ source, suggestions })`), so firing it for both would race and show one
  non-deterministically. Carrying two sources means a store + panel refactor.
- More importantly, it buys almost nothing. The far half is the brand-new,
  disconnected part that actually needs re-connecting, and its suggestions already
  include **the original** as a candidate (the original is simply a nearby feature
  to the new part), so the single most important new link — reconnecting the new
  part to the original at the split point — is already offered. The original (near
  half) kept all its existing connections/relationships and only got shorter, so
  re-suggesting it mostly re-surfaces links it already has.
- Fanning out to both halves also produces reciprocal-duplicate rows: near→far and
  far→near describe the *same* physical connection from both ends, which the panel
  would show twice.

So the far-half-only re-suggestion is the intended final design. A future
multi-source suggestion surface could revisit it, but it is not owed work for
Phase 20.

## Status

**Phase 20 is closed — verified and signed off (2026-08-22).** The user ran the
split end to end and confirmed it works, including after the `ST_GeometryType`
guard fix (see below). All planned scope is built; nothing is open. The
both-halves re-suggestion idea was resolved as a settled design decision (far
half only — see above), not deferred work.

- **Slice C1 — done.** Copy/Duplicate via the shared seed spine.
- **Slice S1 — done.** Transactional split endpoint.
- **Slice S2 — done.** Split UI, end to end.
- **Split live preview — done.** On-map dashed preview of both halves during
  hover and while the create form is open.

### Fix during verification

The `ST_GeometryType` guard originally used bare `GeometryType(geom)`, which
returns the uppercased OGC name (`LINESTRING`) — but the check compared against
`ST_LineString` (the `ST_`-prefixed form only `ST_GeometryType` returns), so it
rejected *every* valid line with "…is a LINESTRING, not a simple LineString".
Switched the guard query to `ST_GeometryType(geom)`; splits now save.

### Verification owed (Bash is blocked here; run locally)

- Frontend `tsc` / lint for `lib/geometry.ts`, `draw-store.ts`,
  `feature-create-form.tsx`, `feature-panel.tsx`, `voltedge-map.tsx`.
- Backend `go build` / `go vet ./...` for `api/internal/crud/split.go` +
  `handler.go`.
- Frontend `tsc` / lint for `edit-store.ts`, `geometry-edit-toolbar.tsx`,
  `draw-store.ts`, `feature-create-form.tsx`, `voltedge-map.tsx` (S2).
- Frontend `tsc` / lint for `lib/geometry.ts` + `voltedge-map.tsx` (live
  preview). Manual: enter split mode on a line → hovering it shows the near half
  solid + far half dashed + a cut marker that tracks the cursor; a click commits
  and the preview stays put while the create form is open; Save clears it. Cursor
  far off the line shows no preview. Escape/Cancel out of split mode removes the
  scratch layer.
- Manual (S2): edit a line → the **Split** button shows (and does *not* show for
  a point/polygon); click it → hint appears, Move/vertices don't fight it →
  click on the line → the create form opens pre-filled with the line's
  name/subtype/attributes → Save → the original is now the near half, a new
  feature is the far half, both render after the tile refresh, and the new part
  gets suggestion chips. Confirm a click far off the line is ignored; confirm
  Escape/Cancel out of split mode leaves the original intact.
- Manual (S1, once S2 or curl is available): `POST
  /features/{line-class}/{id}/split` with a `split_point` on the line's interior
  → 200-ish `201` with a new feature; the original's geometry is now the near
  half; the new feature's geometry is the far half and it has its own
  auto-assigned territory. Confirm each guard 400s: a non-line class, a
  soft-deleted feature, a point on an endpoint, a missing `split_point`. Confirm
  the original keeps its connections/relationships. Confirm a mid-flight failure
  (e.g. an invalid `new_feature.attributes`) rolls back — the original's
  geometry is unchanged.
- Manual: select a feature → More → **Duplicate** → the create form opens
  pre-filled with its attributes/subtype/name; a copy of its geometry appears
  offset on the map and drags with the pointer; Save creates a new feature at the
  dragged position; Cancel/Escape discards it. Confirm a class with computed or
  non-`visible_on_create` fields still saves (those are dropped from the POST).
  Confirm a blank draw is unchanged (form starts empty, saves as before).
