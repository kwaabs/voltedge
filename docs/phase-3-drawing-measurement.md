# Phase 3 — drawing constraints and measurement tools

**Goal:** answer a cluster of drawing-and-measurement asks in one pass --
how to draw an orthogonal (right-angle) line or polygon, how to measure an
ad-hoc distance/area on the map, and how to see an existing feature's own
length/area -- plus a shared metric/imperial units toggle for every
read-out. Picked up right after the same week's basemap-switcher and
resize/minimize work.
**Status:** built, not yet browser-verified (see "How this should be
validated" -- the session that built it couldn't spawn a shell).

## Why this slice exists

Three related questions came up together while drawing on the map:

1. **Straight / orthogonal lines.** A straight line already worked --
   that's OpenLayers' default Draw behavior. What was missing was a
   *right-angle constraint*: laying down a run of segments that are each
   axis-aligned (or diagonal) relative to the previous one, the way a
   utility trench or a rectangular yard boundary actually goes in.
2. **Measuring something ad hoc.** "I want to draw a line/box and just know
   its length/area" -- a throwaway measurement, not a feature to save.
3. **Measuring an existing feature.** "I click a line feature and know its
   length" -- which needs no tool to arm at all: the geometry is already
   fetched, so the number can just be shown.

The design decision worth recording: **ortho is a toggle, not a new
toolbar** (it sits next to Snap, which is the same kind of drawing
modifier), and **measurement splits in two** -- an ad-hoc *tool* for (2)
and an always-visible *computed read-out* for (3). A single shared units
store keeps every number in one system.

## What was built

Three new zustand stores, one measurement lib, three small UI components,
and the map wiring:

- **`src/stores/ortho-store.ts`** (new) -- session-only (`useOrthoStore`,
  `{ enabled, toggle }`, default off). Session-only because an angular
  drawing constraint has no meaning across a reload, same as
  `draw-store.ts`.
- **`src/stores/measure-store.ts`** (new) -- session-only
  (`useMeasureStore`, `mode: "distance" | "area" | null`, `setMode`). Arms
  the ad-hoc tool; measurements it produces are ephemeral, so nothing to
  persist.
- **`src/stores/units-store.ts`** (new) -- **persisted** (`useUnitsStore`,
  `units: "metric" | "imperial"`, `toggle`/`setUnits`, name
  `voltedge-units`, metric default). Persisted, unlike the two above:
  which units a user thinks in is a lasting preference, not a per-session
  one. Shared by *every* read-out so flipping it anywhere flips it
  everywhere.
- **`src/lib/measure.ts`** (new) -- the formatting + geodesic-measurement
  helpers. `formatLength(m, units)` / `formatArea(m², units)` roll m->km
  and ft->mi, m²->ha->km² and ft²->ac at their natural thresholds.
  `geoJSONLengthMeters(geometry)` / `geoJSONAreaMeters(geometry)` measure a
  fetched feature's EPSG:4326 GeoJSON (LineString length or Polygon
  outer-ring perimeter; Polygon area), passing `{projection:"EPSG:4326"}`
  to `ol/sphere` so it doesn't mis-transform the already-lon/lat coords.
- **`src/components/map/ortho-toggle-button.tsx`** (new) -- an "Ortho"
  ghost button (highlighted when on) that reads/toggles the ortho store,
  placed in the drawing banner (`new-feature-menu.tsx`) beside Snap, only
  for line/polygon draws (a point has no segment to constrain).
- **`src/components/map/units-toggle-button.tsx`** (new) -- a ghost button
  reading "Metric" / "Imperial" that toggles the units store.
- **`src/components/map/measure-toolbar.tsx`** (new) -- mirrors
  `new-feature-menu.tsx` exactly: a **Measure** dropdown (Distance / Area)
  when idle; an active banner with the current mode, a units toggle, and a
  stop button while measuring. Placed in the map toolbar between the
  feature search and the New-feature menu (`routes/map.tsx`).
- **`src/components/map/voltedge-map.tsx`** (wired) -- the map-side work:
  - **Ortho:** two module-scope `GeometryFunction`s
    (`orthoLineGeometryFunction` / `orthoPolygonGeometryFunction`) attached
    to the draw's `geometryFunction` for line/polygon. Each reads
    `useOrthoStore.getState().enabled` **live** on every pointer move and,
    when on, snaps the last segment to the nearest 45° (`snapLastOrtho`)
    off the previous vertex; when off, it reproduces OL's own default
    line/polygon geometry (including polygon ring-closing) byte for byte.
    Reading the flag live is what lets the toggle take effect mid-sketch
    without a dependency change that would tear the Draw down and drop the
    in-progress geometry.
  - **Measure tool:** an effect keyed on the measure mode that stands up a
    throwaway scratch `VectorSource` + `VectorLayer` (a dashed copper
    `MEASURE_STYLE`) and a `Draw` (LineString for distance, Polygon for
    area) that never touches the real feature sources. A live tooltip
    `Overlay` trails the sketch, re-reading `getLength`/`getArea` on each
    geometry `change`; drawend freezes that tooltip in place (pushed onto a
    `measureOverlaysRef`) and arms a fresh one, so several measurements can
    be laid down in a row. Escape or the banner's stop clears the mode,
    whose cleanup removes the interaction, layer, and every overlay.
    `handleClick` gained an early bail while a mode is armed, so clicks
    feed the Draw, not selection.
  - **Units re-format:** a small effect keyed on `units` rewrites each
    frozen overlay's label from the raw meters it stashed -- a units flip
    is a pure re-format, never a re-measure.
- **`src/components/map/feature-panel.tsx`** (wired) -- the Attributes tab
  now computes a **Length** row for a LineString feature and **Area +
  Perimeter** rows for a Polygon, straight off the feature's fetched
  geometry via `lib/measure.ts`, formatted against the units store. No tool
  to arm -- the geometry is already loaded for the highlight halo. (This
  landed alongside the same tab's row-border/tint pass.)

## Decisions made

- **Ortho as a toggle beside Snap, not a new toolbar.** It's a drawing
  modifier of exactly the same kind as Snap -- an active-only switch that
  changes how the next click lands -- so it belongs in the same banner, not
  a separate surface. No extra chrome for what's a one-flag behavior.
- **Read the ortho flag live inside the geometryFunction.** Closing over
  the flag (or adding it as an effect dependency) would mean toggling ortho
  mid-draw rebuilds the Draw interaction and discards the in-progress
  sketch. `useOrthoStore.getState().enabled` read on each pointer move
  makes the toggle instant and non-destructive.
- **Measurement is geodesic (`ol/sphere`), not planar.** A naive length in
  the distorted EPSG:3857 working projection is wrong by the secant of the
  latitude. `getLength`/`getArea` measure on the sphere. The two callers
  differ only in projection handling: the feature panel's GeoJSON is
  already lon/lat so it passes `{projection:"EPSG:4326"}`; the measure
  tool's sketch is view-projection EPSG:3857 so it uses the default.
- **Ad-hoc tool vs. always-on read-out.** Measuring a *new* shape you don't
  want to keep is a tool you arm and disarm (the Measure dropdown);
  measuring an *existing* feature is just a fact about it, so it's a plain
  row in the panel, no mode to enter. Folding both into one tool would have
  made the common case (glance at a feature's length) needlessly modal.
- **Units store is persisted; the draw/measure stores aren't.** An
  in-progress draw or measurement is meaningless across a reload; a units
  preference is exactly the kind of thing that should survive one. So
  `units-store` persists and the other two don't, matching how
  `basemap-store` (a lasting choice) persists while `draw-store` doesn't.
- **One units store, every read-out.** The live measure tooltips and the
  panel's Length/Area rows both format against the same flag, so the units
  toggle in the measure banner also flips the panel -- no per-surface unit
  state to keep in sync.

## How this should be validated

**Not yet browser-verified.** The session that built this couldn't spawn a
shell at all (`EPERM ... uv_spawn ... bash.exe` -- the git-backed-`bash`
block `README.md` documents as why Windows uses `task` over `make`), so
neither `tsc`/`lint` nor a Playwright pass could run here. It's a
self-contained frontend edit reviewed statically instead. Before it closes
for real it still needs a pass against the live Vite stack:

- Start a line draw, toggle **Ortho** on -- each new segment snaps to a
  right angle / 45° off the previous vertex; toggle it off mid-draw and the
  next segment is free again, with the in-progress sketch intact. Same for
  a polygon, and the ring still closes correctly on finish.
- **Measure -> Distance**, click a few points, double-click -- the live
  tooltip shows a growing length and freezes on finish; start another
  without disarming and both stay on screen. **Measure -> Area** shows a
  filled sketch with a live area. Escape / the stop button clears them all.
- Open a real **line** feature -- the Attributes tab shows a **Length** row
  matching the measure tool run over the same geometry. Open a **polygon**
  -- **Area + Perimeter** rows appear. A **point** shows neither.
- Flip **Metric -> Imperial** in the measure banner -- every frozen
  measurement *and* the open feature panel's rows re-label (m->ft/mi,
  m²->ft²/ac) in place. Reload -- the units choice persists.
- Both themes render the Ortho/Measure/units controls and the measure
  tooltips legibly.

## Try it yourself

Same stack as the rest of Phase 3 -- `task infra-up && task migrate &&
task api-dev`, then `cd web && npm run dev`, open `http://localhost:5173`.
Ortho is in the drawing banner (start a line/polygon from New feature);
Measure is in the top toolbar; the feature panel's Length/Area rows show
whenever a line/polygon feature is selected.

`npm run build && npm run lint` should finish clean (still owed here --
see above).
