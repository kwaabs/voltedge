# Phase 3 (slice 13) — snapping while drawing or editing geometry

**Goal:** let a user's cursor snap to a nearby existing feature's vertex
or edge while placing a brand new feature or reshaping an existing
one's geometry, toggled on/off from a toolbar button rather than a
permanent bottom-bar control — the last of four planned CRUD slices
(attribute edit, delete confirmation, geometry editing all done before
this one).
**Status:** done and verified against a real Postgres/Martin/API/Vite
stack.

## Why this slice exists, and what it needed that didn't exist yet

The user's exact ask, quoted from the original four-part CRUD request:
*"snapping. snapping should be turn on or off through a button on the
tool bar? or we should put it on the bottom bar?"* — followed, once this
slice actually started, by an explicit scope clarification: *"snapping
tool? lets do it. but when drawing a new feature, snapping should also
be available right? it should be available."* Both questions were
resolved before writing code (see `docs/phase-3-edit-geometry.md`'s
"What's still open" and the plan agreed at the start of this CRUD
initiative): a toolbar button, not a bottom-bar control, since snapping
only ever matters during active digitizing; and a plain OpenLayers
`Snap` interaction against real feature geometry, not the metadata-driven
`snapping_rules` table (`docs/metadata-core.md`) — that's a real,
separate concept (per-feature-class-pair tolerance/rule configuration)
deferred to a later, dedicated pass rather than folded into this one.

The one piece of real investigation this slice needed: OpenLayers' `Snap`
interaction only accepts a plain `ol/source/Vector` (or a `Collection` of
`ol/Feature`) to snap against — confirmed by reading
`ol/interaction/Snap.js`'s own `Options` typedef directly. Every real
feature on this map, though, lives in a `VectorTileSource` (MVT-backed,
tile-paginated, `styleForFeature`-styled) — a fundamentally different
source type with no extent-query method `Snap` could use. There is no
shortcut around this: something has to read whichever tiles are already
loaded and turn their features into a real, plain `VectorSource` for
`Snap` to point at.

## What was built

- **`src/stores/snap-store.ts`** (new) — the toggle itself: `{ enabled:
  boolean (default true), toggle }`. One store, not one per workflow,
  because `SnapToggleButton` is rendered in two different places (the
  draw banner and the geometry-edit toolbar) and both need to reflect
  and control the same setting — turning snapping off mid-session should
  stay off if the user switches from drawing to editing without an
  explicit reason to reset it. Defaults **on**: snapping to an existing
  feature is the common case while digitizing a connected utility
  network (that's the point of connectivity), so off is the exception a
  user opts into.
- **`src/components/map/snap-toggle-button.tsx`** (new) — a shared
  `Magnet`-icon button, same active-state styling convention
  (`bg-accent`) as `geometry-edit-toolbar.tsx`'s own Move/Edit vertices
  buttons, so all three toolbar toggles look and behave the same way.
- **`src/components/map/voltedge-map.tsx`**:
  - `refreshSnapSource(map, layers, snapSource)` (new, module-level) —
    the piece that makes a `VectorTileSource`-backed map usable with
    `Snap` at all. For the current view extent and resolution, and for
    each rendered `VectorTileLayer`'s source: resolves the covering tile
    coordinates (`tileGrid.forEachTileCoord`), asks the source for each
    tile (`source.getTile(z, x, y, pixelRatio, projection)`), and reads
    its already-parsed features via `renderTile.getSourceTiles()` --
    this exact call sequence mirrors what OpenLayers' own canvas
    vector-tile renderer does internally for hit-detection
    (`ol/renderer/canvas/VectorTileLayer.js`'s `getFeatures`), confirmed
    by reading that source directly rather than guessing. Because the
    underlying raw MVT tiles are cached by the source itself, keyed by
    URL, and this app's real map layer has typically already fetched
    whatever's on screen to render it, this read is synchronous and
    network-free in the common case -- it's finding data already sitting
    in memory, not issuing new requests. Each loaded tile's features are
    `ol/render/Feature`'s lightweight `RenderFeature` (this app's
    `new MVT()` never sets a `featureClass`, so nothing here has a usable
    geometry object) -- `toFeature` (also from `ol/render/Feature`, an
    exported standalone conversion function, confirmed via source
    inspection) builds a real `ol/Feature` with a real `ol/geom/*`
    geometry from each one, already in the view's own projection
    (`VectorTileSource` transforms tile-pixel coordinates into it while
    parsing, before a tile's features are ever exposed to anything).
  - A `snapSourceRef` (`VectorSource`, never added to the map as a
    rendered layer) created once alongside the draft/highlight/edit
    sources, and a `moveend` map listener (added once) that calls
    `refreshSnapSource` whenever a draw or geometry-edit session is
    active and snapping is enabled -- keeps the snap candidates current
    as the user pans/zooms mid-session, since `Snap` only ever sees
    whatever's already sitting in its source at drag time, not a live
    view of the map.
  - A new effect that attaches OL's `Snap` interaction, active whenever
    either digitizing workflow is running (`drawingFeatureClassCode` set,
    or `editing` + `geometryTool` both set) and `snap-store.ts`'s
    `enabled` is true -- calling `refreshSnapSource` once up front so a
    session that starts without first panning the map still has
    candidates immediately. **Placed textually after both the existing
    Draw effect and the existing Modify/Translate effect in the
    component**, not just logically gated behind them: OpenLayers only
    lets `Snap` influence a drag another interaction is already handling
    if `Snap` was added to the map *after* that interaction (confirmed
    via `ol/interaction/Snap.js`), and React runs a single commit's
    effects in the order they're defined in the component regardless of
    which dependency changed -- so this had to physically sit below both
    to reliably attach after them on every render that recreates any of
    the three.
- **`src/components/map/new-feature-menu.tsx`** -- the draw-mode banner
  now renders `<SnapToggleButton />` next to the existing Cancel button,
  directly answering the user's clarification that snapping needs to be
  available for a brand new feature, not only an existing one's geometry
  edit.
- **`src/components/map/geometry-edit-toolbar.tsx`** -- gained the same
  `<SnapToggleButton />`, after the Move/Edit vertices buttons.

## How this was validated

Driven against a real Postgres/Martin/API/Vite stack via Playwright,
with real seeded `POLE`, `MV_CONDUCTOR`, `DIST_XFMR`, and
`SUBSTATION_YARD` features already on the map.

- The draw banner (started from "New feature" → a point class) shows a
  Snap toggle next to Cancel, on by default (highlighted/active state).
  Toggling it off and back on updates its visual state correctly and the
  change persists across re-renders.
- The geometry-edit toolbar (Edit an existing feature) shows the same
  toggle, reflecting the same shared on/off state as the draw banner --
  changing it in one place is still in effect when the other workflow
  starts.
- With snapping on and zoomed in close to an existing feature, a new
  point placed a few pixels from that feature's exact vertex snapped
  to it precisely; with snapping off, the same imprecise click landed
  exactly where clicked, no snap. Confirmed via before/after
  screenshots, not just inferred from the code.
- Both themes render the toggle legibly in both toolbar locations.
- No drafts or edits from this pass were saved -- every test session was
  discarded via Cancel/Escape, leaving seed data unchanged.

## Decisions made

- **Plain OL `Snap`, not metadata-driven `snapping_rules`.** Confirmed
  and recorded before this slice began (see
  `docs/phase-3-edit-geometry.md`'s "What's still open"): the metadata
  table already models per-feature-class-pair snap tolerance and
  eligibility, a real, separate feature this slice deliberately doesn't
  attempt -- it needs its own design pass (which rule wins when a
  feature class pair has none, how tolerance interacts with zoom, etc.)
  rather than being folded into "does snapping exist at all."
- **One shared toggle, not one per workflow.** Both the draw banner and
  the geometry-edit toolbar read/write `snap-store.ts` directly --
  matches the mental model of one setting, not two independently
  managed ones that could drift out of sync.
- **Reading already-loaded vector tiles directly, not a new backend
  endpoint.** A "list features near this point" API endpoint would have
  been a simpler mental model, but it would mean a network round trip on
  every drag-tick's worth of candidate-refresh, plus a second code path
  for something the map's own vector tile layers already have in memory
  the moment they're on screen. Reading the already-fetched tile bytes
  directly (mirroring OpenLayers' own internal hit-detection pattern)
  keeps this entirely client-side and synchronous in the common case.
- **Refreshed on `moveend` and at session start, not on every pointer
  move.** Snap candidates only need to reflect what's currently in
  view, not update mid-drag -- a `moveend`-paced refresh is enough to
  keep them current as the user pans while digitizing, without adding
  per-frame work to a drag gesture.

## What's still open

- **Metadata-driven `snapping_rules`** -- deliberately deferred, as
  above; this slice's plain `Snap` interaction is the foundation a rules
  layer could later sit on top of (e.g. swapping the single shared
  `enabled` toggle for a source populated only with feature classes a
  rule actually permits snapping to).
- This closes the four-slice CRUD initiative the user scoped at the
  start (attribute edit, delete confirmation, geometry editing,
  snapping). Everything else already tracked as open in
  `docs/phase-3-edit-geometry.md` and `docs/phase-3-delete-confirmation.md`
  (structural relationships from the connect panel, Azure AD SSO,
  metadata-driven symbology, an undockable attribute panel, the real
  Martin/tile-filtering fix for soft-deletes) is unchanged by this
  slice.

## Try it yourself

Same stack as `docs/phase-3-edit-geometry.md`'s "Try it yourself" --
`task infra-up && task migrate && task api-dev`, then `cd web && npm run
dev`. Open `http://localhost:5173`:

- Start "New feature" → any point class -- the draw banner shows a
  magnet Snap toggle next to Cancel, on by default. Zoom in near an
  existing feature and click close to its vertex -- the new point snaps
  to it exactly. Toggle Snap off and repeat -- no snap, the point lands
  wherever you actually clicked.
- Select an existing line or polygon feature, click Edit, click Edit
  vertices -- the same Snap toggle is in the geometry-edit toolbar,
  sharing state with the draw banner's toggle. Drag a vertex near
  another feature's vertex with Snap on to see the same behavior during
  an edit.

`npm run build && npm run lint` and `go build ./... && go vet ./... &&
gofmt -l .` should all finish clean.
