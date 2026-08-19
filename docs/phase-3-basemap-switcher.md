# Phase 3 — a basemap switcher in the status bar

**Goal:** let the user swap the map's underlying basemap from the status
bar, between exactly three choices -- OpenStreetMap (what the map has
always shown), Carto Voyager, and Blank (no basemap at all) -- a small,
self-contained addition picked up right after the same day's floating-
panel resize/minimize work.
**Status:** built, not yet browser-verified (see "How this should be
validated" -- the session that built it couldn't spawn a shell).

## Why this slice exists

The map has only ever rendered a single, hardcoded OpenStreetMap tile
layer, and the status bar carried a single hardcoded OSM attribution link
to match. The request was a plain one: a "select" in the bottom bar to
change the basemap, with three specific options. The interesting part
wasn't the picker -- it was keeping the concern split cleanly so the
OL-free status bar doesn't pull OpenLayers tile-source classes into its
bundle just to name the choices, and so swapping the source doesn't
disturb the map's layer z-order.

## What was built

Four pieces, one new module each plus the two existing files they wire
into:

- **`src/stores/basemap-store.ts`** (new) -- the single source of truth
  for *which* basemaps exist and which one is active. A persisted zustand
  store (`useBasemapStore`, name `voltedge-basemap`, default `osm`) holding
  just the chosen `BasemapId` (`"osm" | "carto-voyager" | "blank"`), plus a
  `BASEMAP_OPTIONS` list pairing each id with a display label and its
  required attribution links. **Deliberately no OpenLayers import** here:
  it's pure metadata, exactly the split `theme-store.ts` uses for the
  theme (the store holds the choice; the side effect of applying it lives
  elsewhere). Adding a fourth basemap is a one-line change here plus a new
  `case` in the source factory.
- **`src/lib/basemaps.ts`** (new) -- the OpenLayers side. `createBasemapSource(id)`
  builds the tile source (`new OSM()` for osm; an `XYZ` source at Carto's
  non-subdomain raster host for carto-voyager; `null` for blank, which has
  no tiles). `applyBasemap(layer, id)` applies a choice to the map's single
  base tile layer *in place* -- a real `setSource` swap for osm/carto
  (keeping the same layer object, so it stays at the bottom of the z-order
  under every feature layer), or `setVisible(false)` for blank rather than
  juggling a null source through OL's types. Idempotent.
- **`src/components/map/voltedge-map.tsx`** (wired) -- the base tile layer
  is now created from the persisted basemap (`createBasemapSource(useBasemapStore.getState().basemap)`)
  and held in a new `baseLayerRef`, instead of an inline `new OSM()`. A new
  effect watches the store's `basemap` and calls `applyBasemap` on a
  change; the mount run is skipped (a `didApplyBasemapRef` guard) because
  the creation effect already applied the initial choice, so there's no
  pointless source rebuild / tile re-request on first render. OL's default
  Attribution control stays disabled (`defaultControls({ attribution: false })`),
  since the status bar renders attribution itself.
- **`src/components/map/status-bar.tsx`** (wired) -- the actual "select".
  A new `BasemapPicker` (a `DropdownMenu` + `DropdownMenuRadioGroup`, one
  `DropdownMenuRadioItem` per `BASEMAP_OPTIONS`, `side="top"` so it opens up
  into the map instead of off the bottom edge of the window) with a compact
  trigger showing a map icon, the current label, and a chevron. Alongside
  it, a new `BasemapAttribution` that renders the *active* basemap's
  required links off `BASEMAP_OPTIONS` (OSM's wording for osm, OSM + CARTO
  for carto voyager, nothing for blank) -- replacing the formerly-hardcoded
  OSM link. Both sit in an `ml-auto` group at the right end of the bar.

## Decisions made

- **A DropdownMenu radio group, not a `<Select>`.** The user asked for a
  "select component," but the app has no `@radix-ui/react-select`
  dependency and this sandbox can't run `npm install` to add one. The
  DropdownMenu radio-group primitive is already in the app and already used
  as a picker (the layers panel), gives the same single-choice behavior and
  a checkmark on the active item, and matches the existing design language
  -- so it's the right call here regardless, not just a workaround.
- **Store holds metadata only; OL source lives in `lib/basemaps.ts`.** So
  the status bar (which has no other reason to touch OpenLayers) can render
  the picker and attribution off pure data without dragging tile-source
  classes into its bundle, and so the swap side effect stays in the one
  place that owns the map instance (`voltedge-map.tsx`), mirroring the
  theme store's split.
- **Swap the source on one persistent layer, don't rebuild the map.**
  Keeping a single base `TileLayer` in a ref and calling `setSource` on it
  preserves its position at the bottom of the layer stack under every
  feature/highlight layer -- rebuilding the layer, or the map, would risk
  z-order and lose the point.
- **Blank hides the layer rather than carrying a null source.** OL's types
  want a source; `setVisible(false)` is the clean way to render no basemap,
  and switching back off blank re-shows the layer with the newly chosen
  source.
- **Carto's non-subdomain raster host.** Carto's subdomained tile URLs are
  deprecated; the single-host `basemaps.cartocdn.com` URL is fine over
  HTTP/2. `maxZoom: 20` on the source lets OL over-zoom the last level past
  z20 rather than 404, matching how OSM behaves under the view's `maxZoom: 22`.

## How this should be validated

**Not yet browser-verified.** The session that built this couldn't spawn a
shell at all (`EPERM ... uv_spawn ... bash.exe` -- the exact
git-backed-`bash` block `README.md` documents as why Windows uses `task`
over `make`), so neither `tsc`/`lint` nor a Playwright pass could run
here. It's a self-contained frontend edit reviewed statically instead.
Before it closes for real it still needs a pass against the live Vite
stack:

- Open the map on its default -- OpenStreetMap tiles render, the status
  bar shows the OSM attribution and the picker reads "OpenStreetMap".
- Switch to Carto Voyager -- tiles change in place with no flicker of the
  feature layers, and the attribution shows OSM + CARTO.
- Switch to Blank -- the basemap disappears, feature layers stay, and the
  attribution area goes empty.
- Reload -- the last-picked basemap persists (localStorage), the map opens
  straight onto it without a flash of OSM first.
- Both themes render the picker trigger and attribution legibly.

## Try it yourself

Same stack as the rest of Phase 3 -- `task infra-up && task migrate &&
task api-dev`, then `cd web && npm run dev`, open `http://localhost:5173`.
The picker is at the right end of the bottom status bar; pick each of the
three options and watch the base tiles change.

`npm run build && npm run lint` should finish clean (still owed here --
see above).
