# Phase 15 — data import (map overlay)

Imports external GIS/CAD files and **overlays them on the map** — **GeoJSON**,
**KMZ**, **Shapefile**, and **DXF** — carrying geometry and attributes. This is
the mirror of Phase 14 export, but with one hard rule: **imported data is an
overlay, never written to the database.** It's a read-only reference layer the
user drops on top of the network to compare, trace visually, or measure
against; it lives in the browser and the user removes it when done.

Unlike export (which is server-side — the dialog just POSTs and downloads),
import is **entirely client-side**: files never leave the browser, there's no
upload endpoint, and nothing is stored server-side. Parsing, reprojection, and
rendering all happen in the browser.

## Decisions

Locked with the user before building:

- **Formats: GeoJSON, KMZ, Shapefile, DXF — all parsed client-side.** These four
  are all browser-parseable (GeoJSON/KML via OpenLayers' bundled readers,
  Shapefile via `shpjs`, DXF via a DXF parser). **DWG is deferred** (see Open
  items): it's a proprietary binary with no viable JS parser, so it would force
  a server + GDAL/ODA path that the other four don't need. DXF covers the
  CAD-interchange case for now (AutoCAD exports DXF).
- **A real reprojection engine (`proj4`), not an assume-4326 shortcut.** Imported
  files come in arbitrary CRSs, so the app gains a proper reprojection step:
  everything is reprojected to the canonical storage CRS (EPSG:4326) on import
  and to the view CRS (EPSG:3857) for display. This is a new frontend capability
  — the app currently only knows OL's built-in 3857↔4326.
  - The **source CRS** is auto-detected where the format carries it (a
    Shapefile's `.prj`, a GeoJSON `crs` member) and **user-supplied for DXF**,
    which has no georeference. The import dialog has a source-CRS control,
    pre-filled from detection and editable, required for DXF.
- **Persisted in the browser (localStorage), surviving refresh.** Overlays are
  not ephemeral — they stay across reloads until the user explicitly removes
  them. Stored as canonical 4326 GeoJSON (compact, reprojection-neutral). A size
  guard protects the ~5 MB quota; oversized imports load for the session but
  report that they can't be persisted (IndexedDB is the fallback if we outgrow
  localStorage — see Open items).
- **Read-only, but measurable and snappable.** Imported features are display
  only — not editable, not part of the network/connectivity/trace model, never
  saved to the DB. But the **measure tool and snapping work against imported
  geometry** (you can measure an imported boundary, or snap a new DB feature to
  an imported reference line). Clicking an imported feature shows its attributes
  in a lightweight read-only popup (the full feature panel assumes a persisted
  DB feature, so imported features get their own popup).

## Format realities (why the parsers differ)

| | GeoJSON | KMZ | Shapefile | DXF |
| --- | --- | --- | --- | --- |
| Parser | `ol/format/GeoJSON` (already imported) | unzip (`jszip`) → `ol/format/KML` (bundled) | `shpjs` (`.shp`+`.dbf`+`.prj`, usually zipped) | `dxf` (`Helper.toPolylines()`) |
| CRS | 4326 by spec (rare `crs` member) | always 4326 | from `.prj` → proj4 def | **none** → user picks |
| Attributes | `properties` | `ExtendedData` | `.dbf` columns | none carried in v1 (geometry only) |
| New dependency | none | `jszip` | `shpjs` (+ `proj4`) | `dxf` (+ `proj4`) |

## What the overlay reuses (grounded in the current map)

The rendering half is a well-worn pattern here, not net-new:

- **Client `VectorLayer` + `VectorSource` overlays** already back 9+ features
  (trace/isolation/load-flow/fault/query highlights, the territory boundary
  overlay, the measure scratch layer) in `voltedge-map.tsx`. DB feature classes
  render as Martin **vector tiles**; an imported overlay is instead a client
  `VectorLayer` populated from the parsed features — the same shape as the
  existing overlays.
- The **Layers panel already has an "Overlays" section** (the territory boundary
  overlay, driven by its own store). "Imported overlays" mirrors that: its own
  store, its own `VectorLayer`, listed and toggled/removed there — independent of
  the catalog-derived `layers-store.hidden` map.
- The **attribute list** in `feature-panel.tsx` already renders an arbitrary
  `Record<string, unknown>`, so an imported feature's properties display with no
  schema. Only the surrounding panel is DB-coupled, hence the separate popup.
- **GeoJSON reading is already proven** (the territory overlay uses
  `new GeoJSON().readFeatures(..., { dataProjection, featureProjection })`), so
  Slice 1 adds zero dependencies.

What's genuinely new: `proj4` + an EPSG registry, `jszip`/`shpjs`/a DXF parser,
and **all** file-ingest (there's no `<input type=file>`, FileReader, drag-drop,
or zip handling anywhere today).

## Slices

Built GeoJSON-first, so the whole pipeline (ingest → parse → reproject → overlay
→ persist → toggle/remove) is proven end-to-end with zero dependencies before
any format-specific parser is added.

- **Slice 1 — overlay skeleton + GeoJSON.** The imported-overlays store
  (localStorage-persisted), the `VectorLayer` wired into the map, the "Imported"
  section in the Layers panel (toggle + remove), an Import button + dialog, and
  GeoJSON ingest via OL's bundled reader. Proves the whole path, no new deps. ✅
- **Slice 2 — KMZ.** Add `jszip` to unzip the KMZ to its KML, then OL's bundled
  `ol/format/KML` reader. Still 4326, no reprojection needed. ✅
- **Slice 3 — Shapefile + the reprojection engine.** Add `proj4` and `shpjs`;
  read the `.prj`, register/resolve the source CRS, reproject to 4326. This is
  where the CRS machinery and the source-CRS control land. ✅
- **Slice 4 — DXF.** Add a DXF parser; translate entities to features; since DXF
  has no CRS, the source-CRS control is required (defaulting sensibly), routed
  through the Slice 3 reprojection engine. ✅
- **Slice 5 — attribute popup + drag-drop.** A lightweight read-only popup for a
  clicked imported feature, and drag-a-file-onto-the-map ingest alongside the
  dialog. Measure/snap-against-imported wiring lands here (or folded into Slice 1
  if cheap).

## Open items (deferred, tracked)

- **DWG.** Deferred from v1. No viable client-side parser; would require a
  server `/import` endpoint backed by GDAL or the ODA File Converter (upload →
  convert to GeoJSON → return). A candidate later server-side slice, decoupled
  from the client formats above.
- **EPSG catalog scope.** `proj4` ships only 4326/3857; the reprojection engine
  needs a curated set of projected CRSs preloaded. **Which national grid /
  projected system this utility actually operates in is unknown from the repo**
  and should be confirmed — it decides the default preload set. Interim plan:
  bundle WGS84 + Web Mercator + the UTM zones, and let the user paste a
  proj4/WKT definition for anything not preloaded.
- **Storage quota.** localStorage is ~5 MB; a large shapefile/DXF overflows it.
  v1 caps persisted size and reports oversized imports as session-only.
  IndexedDB is the fallback if we need to persist larger overlays.
- **Export target-CRS addon (backend).** The mirror of import reprojection, on
  the export side (Phase 14). Add an `srid`/CRS param to the `/export` request
  and wrap the geometry query in PostGIS `ST_Transform`; the dialog gains a
  target-CRS dropdown fed by the shared `CRS_CATALOG`, with the backend
  validating the SRID against `spatial_ref_sys`. Separate task — reuses the
  catalog, not the frontend engine (see Slice 3's "two engines, one catalog").
- **Snap source composition.** The existing snap interaction reads DB features;
  feeding imported vector features into it (Slice 5 / measure+snap) needs the
  imported `VectorSource` added to the `Snap` interaction's sources.

## Slice 1 — overlay skeleton + GeoJSON

The full ingest → overlay → persist → toggle/remove pipeline, GeoJSON only, zero
new dependencies (OpenLayers' bundled `GeoJSON` reader was already imported).

### Store (`web/src/stores/imported-overlays-store.ts`)

`useImportedOverlaysStore` — a `persist`ed list of `ImportedOverlay`
(`id`, `name`, `format`, `featureCount`, canonical-4326 `geojson`, `visible`,
`importedAt`, optional `ephemeral`). Actions: `add` (assigns id/importedAt,
defaults visible), `remove`, `toggle`, `rename`.

- **Quota handling is two-layer.** Per-overlay: an overlay whose serialized size
  exceeds `MAX_PERSIST_BYTES` (4 MB) is flagged `ephemeral` and `partialize`
  drops it from what's written to localStorage — it loads for the session but
  won't survive a reload (the dialog says so in the success toast). Aggregate:
  the store wraps localStorage in a `safeStorage` whose `setItem` swallows a
  `QuotaExceededError`, so several medium overlays together overflowing the
  ~5 MB ceiling degrades to session-only instead of throwing.
- A separate `useImportStore` (`import-store.ts`) holds just the dialog-open
  bit, mirroring `export-store` (not persisted).

### Dialog (`web/src/components/map/import-dialog.tsx`)

`ImportDialog` — a file picker (`.geojson,.json`) + an editable name (defaults to
the filename). On import it reads the file text, `JSON.parse`s it, and
`toFeatureCollection` normalizes any GeoJSON root (a `FeatureCollection`, a lone
`Feature`, or a bare geometry) into a `FeatureCollection`; empties and non-JSON
are rejected with a toast. **Slice 1 assumes the file is already 4326** (the
reprojection engine arrives in Slice 3); the dialog states that assumption.

### Map (`web/src/components/map/voltedge-map.tsx`)

One client `VectorLayer`/`VectorSource` pair (`importedStyleForFeature`:
amber, **dashed** so a foreign layer never reads as saved data), at **zIndex 4**
— above the feature tiles and territory overlay (both zIndex 0), below the
selection/analysis highlights (zIndex 5+). A population effect keyed on the store
clears and repopulates the source from every *visible* overlay, reprojecting the
stored 4326 GeoJSON to the view CRS (3857) via `featureProjection`, and tags each
feature with `__overlayId`/`__overlayName` for the Slice 5 click popup.

### Layers panel (`web/src/components/map/layers-panel.tsx`)

An "Imported" section (shown only when ≥1 overlay exists) mirroring the existing
"Overlays" section: one row per overlay with a visibility checkbox, its feature
count, and a trash button that removes it (`stopPropagation` so it doesn't also
toggle visibility).

### Files

- `web/src/stores/imported-overlays-store.ts` (new) — persisted overlay list +
  `safeStorage` quota wrapper.
- `web/src/stores/import-store.ts` (new) — dialog-open flag.
- `web/src/components/map/import-dialog.tsx` (new) — `ImportDialog`.
- `web/src/lib/map-layers.ts` — `importedStyleForFeature` (amber dashed).
- `web/src/components/map/voltedge-map.tsx` — imported layer + population effect.
- `web/src/components/map/layers-panel.tsx` — "Imported" section.
- `web/src/routes/map.tsx` — Import button + mounts `ImportDialog`.

### Verification owed (frontend)

The Bash tool is unavailable here, so this was **not** run — please run it:

```sh
cd web && npm run typecheck && npm run lint
```

Manual smoke test: Import a `.geojson` → it appears amber/dashed on the map and
as a row in Layers ▸ Imported; toggle hides/shows it; trash removes it; reload
the page and a small overlay is still there (a >4 MB one is gone, as warned).

## Slice 2 — KMZ

Adds KMZ (and bare `.kml`) ingest behind the same dialog, plus the first
extraction of parsing into its own module so the dialog stays format-agnostic.

### Parsing module (`web/src/lib/import-formats.ts`)

New. All format ingest now lives here, normalizing every format into one
canonical shape — a 4326 GeoJSON `FeatureCollection` — so the store, the map's
population effect, and the layers panel only ever see GeoJSON:

- `detectFormat(filename)` maps an extension to an `ImportFormat` (both `.kml`
  and `.kmz` → `"kmz"`, the shared KML family). `IMPORT_ACCEPT` is the picker's
  `accept` list (`.geojson,.json,.kml,.kmz`).
- `parseFile(file)` dispatches on the detected format and returns
  `{ format, geojson, featureCount }`. GeoJSON and DXF/Shapefile guards throw
  user-facing errors (the latter until their slices land).
- **KMZ path:** `jszip` loads the archive; `readKmzKml` picks the main KML entry
  (`doc.kml` by convention, else a root-level `.kml`, else the first one; dirs
  and icons ignored) and returns its text. A bare `.kml` skips the unzip.
- The KML text is read with OL's bundled `KML` reader (`extractStyles: false` —
  imported overlays get our own amber/dashed style, not the file's) at 4326,
  then written straight back out as a 4326 GeoJSON object via `GeoJSON`'s
  `writeFeaturesObject`. **KML is WGS84 by spec, so no reprojection** — the
  reprojection engine still doesn't arrive until Slice 3.
- `toFeatureCollection` (the GeoJSON-root normalizer) moved here from the dialog.

### Dialog (`web/src/components/map/import-dialog.tsx`)

Slimmed to call `parseFile` — it no longer knows about GeoJSON specifically.
The size guard now measures the **serialized GeoJSON** (`JSON.stringify(geojson)
.length`, what actually gets persisted) rather than the source file's text
length, so it's accurate for a binary KMZ too. Accept list and helper text
updated for KMZ/KML.

### Dependency

`jszip` (`^3.10.1`) added to `web/package.json`. It ships its own TypeScript
declarations, so no `@types/jszip`. **`npm install` is owed** (the Bash tool is
unavailable here, so it was not run).

### Files

- `web/src/lib/import-formats.ts` (new) — format dispatch + KMZ/KML + GeoJSON
  normalization.
- `web/src/components/map/import-dialog.tsx` — uses `parseFile`; serialized-size
  guard; KMZ/KML copy.
- `web/package.json` — `jszip` dependency.

### Verification owed (frontend)

```sh
cd web && npm install && npm run typecheck && npm run lint
```

Manual smoke test: Import a `.kmz` (and a bare `.kml`) → features appear
amber/dashed on the map and as a Layers ▸ Imported row; a KMZ with no `.kml`
inside, or an empty one, is rejected with a toast; GeoJSON import still works
unchanged.

## Slice 3 — Shapefile + the reprojection engine

Adds zipped-shapefile ingest and the frontend reprojection engine the plan's
been building toward. Import-side CRS handling now works end to end; the same
CRS *catalog* is the shared piece a future export target-CRS control will reuse.

### Two engines, one catalog (import vs export reprojection)

Reprojection lives on **both** sides of the app, deliberately, because the two
data paths run in different runtimes over different data:

- **Import → frontend (`proj4`).** Imported files are parsed entirely in the
  browser and never uploaded, so their reprojection *must* be client-side. This
  is the engine in `crs.ts`.
- **Export → backend (PostGIS `ST_Transform`).** Exported data lives in PostGIS
  (already 4326) and the encoders already run server-side, so a target CRS is
  best applied there: add an `srid` to the export request and wrap the geometry
  query in `ST_Transform`. PostGIS knows every EPSG via `spatial_ref_sys` — no
  bundled defs, and no shipping geometry to the browser just to reproject and
  re-encode (which would duplicate the server encoders).
- **Shared: `CRS_CATALOG`.** The one thing both reuse is the *picker list* — the
  set of CRSs offered in a dropdown. The import source-CRS control uses it now;
  the export target-CRS control will draw from the same list, with the backend
  validating the chosen SRID against `spatial_ref_sys`.

**The export target-CRS addon is a separate (backend) task**, tracked in Open
items — it reuses the catalog but not the engine.

### Reprojection engine (`web/src/lib/crs.ts`)

New. `proj4` ships only 4326/3857, so the engine registers a curated
`CRS_CATALOG` — WGS 84, Web Mercator, and the 120 WGS 84 UTM zones (generated,
not hand-listed) — with both proj4 and OpenLayers, and accepts a pasted
proj4/WKT definition for anything outside it (registered under a synthetic
`IMPORT:*` code).

- `ensureProjection(input)` resolves a catalog code or a raw proj4/WKT string to
  a registered OL projection code (lazy — the catalog registers once on first
  use).
- `reprojectGeoJSONTo4326(fc, sourceDef)` reprojects a FeatureCollection into
  4326 via OL's `GeoJSON` reader/writer (a no-op when the source is already
  4326).
- `detectCrsFromWkt(wkt)` identifies a `.prj`'s CRS: it matches the last
  `AUTHORITY["EPSG","<code>"]` tag and prefers the catalog entry, falling back to
  the raw WKT as the proj4 def.
- `CRS_GROUPS` groups the catalog into optgroups (Geographic/Web, UTM N, UTM S)
  for the picker.

### Shapefile ingest + the inspect step (`import-formats.ts`)

- **`.zip` → shapefile**, parsed with `shpjs` (`shp(arrayBuffer)`), merging
  features if the zip holds several shapefiles.
- **CRS handling leans on shpjs for the common case, our engine for the rest:**
  when the zip has a readable `.prj`, shpjs reprojects to WGS 84 itself, so the
  result is already 4326. When there's **no `.prj`**, shpjs returns the raw
  source coordinates and we reproject them from a user-chosen CRS via the
  engine. (Overriding a *present* `.prj` isn't supported in this slice — it'd
  need shpjs's low-level API to get raw coordinates; noted below.)
- New **`inspectFile(file)`** runs on file-pick (before import): it reports the
  format, any auto-detected CRS (peeking at the shapefile's `.prj` via `jszip`),
  and whether a CRS is *required* (no georeference found). GeoJSON/KMZ are 4326
  by spec, so they never need one.

### Dialog (`import-dialog.tsx`)

On pick it calls `inspectFile` and then either shows the detected projection
read-only ("Projection: WGS 84 / UTM zone 30N (detected from .prj)") or, when
none was found, a **required source-CRS `<select>`** (the `CRS_GROUPS`
optgroups, defaulting to WGS 84) with the explainer "This file has no projection
info — choose the CRS its coordinates are in." Import is disabled while
inspecting. This is the same control DXF (Slice 4) will always show.

### Dependencies

`proj4` + `shpjs` (runtime), `@types/proj4` + `@types/shpjs` (dev). **Confirm the
resolved versions on `npm install`** — the ranges here (`proj4 ^2.11`,
`shpjs ^4.0.4`) were pinned conservatively to pair cleanly with their
DefinitelyTyped types without a live registry to check against.

### Files

- `web/src/lib/crs.ts` (new) — reprojection engine + CRS catalog.
- `web/src/lib/import-formats.ts` — shapefile parse, `inspectFile`, `ParseOptions`.
- `web/src/components/map/import-dialog.tsx` — inspect-on-pick + source-CRS control.
- `web/package.json` — `proj4`, `shpjs`, `@types/proj4`, `@types/shpjs`.

### Verification owed (frontend)

```sh
cd web && npm install && npm run typecheck && npm run lint
```

Manual smoke test: import a zipped shapefile **with** a `.prj` (a UTM one) →
features land in the right place, amber/dashed, no CRS prompt; import one
**without** a `.prj` → the source-CRS picker appears, and choosing the right
zone places the features correctly (choosing 4326 leaves the raw coordinates,
which land in the wrong place — the expected "assume already lat/lon" behavior).

### Risks flagged (unverified — Bash blocked)

- **shpjs reproject-vs-raw assumption:** the no-`.prj` path assumes shpjs
  returns *raw source* coordinates (which we then reproject). If a future shpjs
  version reprojects/relabels differently, this needs revisiting.
- **`shpjs`/`proj4` versions + types** as noted above.
- **Present-`.prj` override** deferred (needs shpjs low-level API).

## Slice 4 — DXF

Adds DXF (AutoCAD interchange) ingest — the last of the four formats, and the
first with **no georeference at all**, so it exercises the required side of the
Slice 3 source-CRS control and always reprojects through the proj4 engine.

### Parser choice + ambient types (`dxf`)

Parsed with the `dxf` package's `Helper.toPolylines()` rather than a
structured-entity parser. `toPolylines()` interpolates arcs/circles into
segments and — crucially — **expands block `INSERT`s into flat world-coordinate
polylines**, so a block-heavy drawing (common in utility CAD) renders whole
instead of dropping its symbols. The trade-off is that it yields geometry, not
rich entity attributes.

`dxf` ships no reliable TypeScript types, so `web/src/dxf.d.ts` is a **minimal
ambient declaration** of just the sliver we use (`Helper` + `toPolylines()`),
keeping the typecheck self-contained rather than depending on an uncertain
`@types` package.

### Ingest (`import-formats.ts`)

- **`.dxf` → dxf**, added to `EXT_FORMAT`/`IMPORT_ACCEPT`.
- `parseDxf` runs `toPolylines()`, maps each polyline to a `LineString` (a
  degenerate single-vertex one to a `Point`), builds a raw FeatureCollection in
  the drawing's own coordinates, then **requires** `reprojectFrom` and runs it
  through `reprojectGeoJSONTo4326`. No CRS → hard error.
- `inspectFile` returns `requiresCrs: true` for DXF (no detection possible), so
  the dialog always shows the source-CRS picker.

### Dialog

No structural change — DXF reuses the Slice 3 required-CRS `<select>`. The
picker's helper text is now format-aware ("DXF drawings carry no coordinate
system — pick the CRS the drawing was made in."), and the accept list/copy
include `.dxf`.

### Limitations (v1, noted in code)

Only polyline geometry is carried: isolated **text, hatch fills, and per-entity
attributes** (layer, color) aren't rendered. This is a display overlay, so
geometry-only is acceptable; entity attributes would mean switching to (or
combining with) a structured-entity parser, a later refinement if the Slice 5
popup needs them.

### Dependency

`dxf` (`^5.3.1`, resolved on install) runtime; no `@types` (local ambient
d.ts).

### Files

- `web/src/dxf.d.ts` (new) — ambient types for the `dxf` package.
- `web/src/lib/import-formats.ts` — `.dxf` mapping, `parseDxf`, DXF inspect branch.
- `web/src/components/map/import-dialog.tsx` — DXF copy + format-aware CRS hint.
- `web/package.json` — `dxf` dependency.

### Verification owed (frontend)

```sh
cd web && npm install && npm run typecheck && npm run lint
```

Manual smoke test: import a `.dxf` → the source-CRS picker appears (required);
choosing the drawing's CRS lands the geometry in the right place, amber/dashed;
a block-heavy drawing renders its symbols (INSERTs expanded); an empty/geometry-
less DXF is rejected with a toast.

### Risks flagged (unverified — Bash blocked)

- **`dxf` `toPolylines()` return shape** — the ambient d.ts assumes
  `{ polylines: [{ vertices: [[x,y], …] }] }`. If the installed version differs,
  the ambient types and `parseDxf` need adjusting (smoke test will surface it).
- **`dxf` version** as noted above.

## Slice 5 — attribute popup

Clicking an imported overlay feature now shows its attributes. Imported features
live only in the browser (never the DB), so they can't open the DB-coupled
`FeaturePanel` — they get their own lightweight, read-only floating card
instead, mirroring `PeerPicker`'s positioning pattern.

### Store (`web/src/stores/imported-feature-store.ts`)

New. `useImportedFeatureStore` holds the one transient selection —
`{ overlayName, properties, position }` — set via `show`, cleared via `clear`.
Not persisted: it's UI state, dropped on the next map click, an outside click,
or Escape. The map's click handler drives it via `getState()`, the same pattern
every other click branch already uses (`selection-store`, `connect-store`, …).

### Popup (`web/src/components/map/imported-feature-popup.tsx`)

`ImportedFeaturePopup` — a `fixed`-positioned card at the click's viewport
coordinates, clamped on-screen (same math as `PeerPicker`). Header shows the
source overlay's name; the body is a `<dl>` of the feature's own properties
(each value rendered as-is for primitives, compact JSON for nested
objects/arrays, an em dash for empties). A feature with no attributes (a DXF
polyline, whose v1 carries none) shows "This feature carries no attributes."
Dismissed by an outside `pointerdown`, Escape, or the ✕ button — and by the next
map click, which the handler clears on any non-imported hit.

### Map (`web/src/components/map/voltedge-map.tsx`)

The click handler gained an imported hit-test **before** the DB selection block.
The imported layer is a plain `VectorLayer`, so it's deliberately excluded from
`isVectorLayer` (which every DB hit-test filters on) — the new test targets that
one layer directly (`layerFilter: (l) => l === importedLayerRef.current`). Being
the topmost thing under the cursor (zIndex 4), an imported feature wins the
click: the handler reads the clicked feature's properties, strips `geometry` and
the internal `__overlayId`/`__overlayName` tags (the name feeds the popup
header), and calls `show`, then returns. Any non-imported click first calls
`clear()`, so opening another feature or clicking empty space dismisses the
popup. The hover-cursor check was widened to include the imported layer so
imported features read as clickable.

### Files

- `web/src/stores/imported-feature-store.ts` (new) — the transient selection.
- `web/src/components/map/imported-feature-popup.tsx` (new) — the card.
- `web/src/components/map/voltedge-map.tsx` — imported hit-test in the click
  handler + widened hover cursor.
- `web/src/routes/map.tsx` — mounts `ImportedFeaturePopup`.

### Verification owed (frontend)

```sh
cd web && npm run typecheck && npm run lint
```

Manual smoke test: import an overlay with attributes (a GeoJSON/KML with
`properties`, or a shapefile with `.dbf` columns) → clicking a feature floats a
card of its attributes named after the overlay; clicking another imported
feature moves the card; clicking a DB feature or empty space dismisses it and
runs normal selection; Escape/outside-click/✕ close it; a DXF polyline (no
attributes) shows the empty-state line.

### Still pending in this slice

Drag-a-file-onto-the-map ingest and measure/snap-against-imported geometry (the
snap interaction needs the imported `VectorSource` added to its sources — see
Open items) remain to be built; the popup was the user-reported gap and landed
first.

## Status

Slices 1–4 ✅ and Slice 5's attribute popup ✅ (all pending the owed
typecheck/lint — Bash unavailable here). Remaining Slice 5 work — drag-drop
ingest and measure/snap-against-imported — is still pending.
