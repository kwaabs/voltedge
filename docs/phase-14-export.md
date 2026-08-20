# Phase 14 — geographic export

Exports the map's **current view** to a downloadable GIS/CAD file — **KMZ**,
**ESRI Shapefile**, or **AutoCAD DXF** — carrying **geometry and attributes**.
Today the only export is the query panel's client-side CSV, which drops
geometry entirely. This adds a real spatial export: pick the visible layers,
optionally clip to the current viewport, choose a format, download.

## Decisions

Locked with the user before building:

- **All three formats in WGS84 / EPSG:4326.** Every feature class is stored in
  4326 and the API's GeoJSON contract already is too, so no coordinate
  transform is needed anywhere. Trade-off accepted: **DXF ends up in lon/lat
  degrees**, so in CAD the drawing is geographically correct but tiny and
  distorted (fine for import-by-coordinates, poor for drafting). A projected-CRS
  option can be added later without breaking the wire shape.
- **DXF attributes as XDATA.** DXF has no attribute table; attributes ride as
  per-entity **extended entity data (XDATA)**, the CAD-native approach — it
  round-trips in AutoCAD/civil tools, though lightweight viewers may not show
  it. (Slice 3.)
- **Scope = current view, whole-dataset, or by territory.** By default the
  export is clipped to the map's current bbox and to the layers you have
  visible; a whole-dataset choice drops the bbox to export each selected class's
  entire extent; a territory choice clips to a set of chosen territories instead.
  Territories are **multi-select** and **subtree-inclusive** — picking one
  includes everything beneath it — because a feature is stamped to the *deepest*
  territory containing it, so a district's features may carry a sub-area/plot id
  (Slice 5).

## Format realities (why the three encoders differ)

| | KMZ | Shapefile | DXF |
| --- | --- | --- | --- |
| Container | zip of one `doc.kml` | zip of `.shp/.shx/.dbf/.prj` **per geometry type** | single `.dxf` |
| Mixed geometry types | one file, a Folder per class | **one geometry type per `.shp`** → a shapefile per class | one file, a layer per class |
| Attributes | `ExtendedData` (string name/value) | `.dbf` table — **field names ≤ 10 chars**, typed columns | XDATA per entity (no real table) |
| CRS | must be 4326 (we comply) | ships a `.prj` (we emit WGS84) | any; we emit 4326 (see trade-off) |

## Slices

- **Slice 1 — backend pipeline + KMZ.** Spatial (bbox) read + `POST /export`
  endpoint + the KMZ encoder, proving the whole path end to end (multi-class
  bbox read → normalized layers → format writer → streamed download). ✅
- **Slice 2 — Shapefile.** `.shp/.shx/.dbf/.prj` per geometry type, bundled in a
  zip; DBF attribute mapping (10-char names, typed columns). ✅
- **Slice 3 — DXF.** One `.dxf`, a layer per class, geometry entities, XDATA
  attributes. ✅
- **Slice 4 — frontend.** A map-view store exposing the live bbox, plus an
  export dialog (format + layer checkboxes defaulting to visible, whole-dataset
  toggle) that POSTs and downloads the blob. ✅
- **Slice 5 — territory scope.** A third scope alongside view/whole: pick a set
  of territories (multi-select, subtree-inclusive). Server-side a recursive CTE
  expands each to its subtree and the read filters `territory_id` to the set;
  the dialog gains a 3-way scope radio and an indented territory picker. ✅

## Slice 1 — backend pipeline + KMZ

### Endpoint

`POST /export`, mounted behind the **`features:read`** gate (an export is a
read, like trace/analysis; POST only because the filter and scope don't fit
query params). Request body:

```json
{
  "format": "kmz",
  "feature_classes": ["pole", "mv_conductor"],
  "bbox": [-0.14, 51.50, -0.11, 51.52]
}
```

- `format` — `"kmz"`, `"shp"`, or `"dxf"` (an unknown format is a clean `400`).
- `feature_classes` — the class **codes** to export (same codes the CRUD
  endpoints key on). Empty is a `400`.
- `bbox` — optional `[minX, minY, maxX, maxY]` in **EPSG:4326**. Omitted or
  empty means the **whole-dataset** export; present clips every class to that
  envelope. Swapped min/max are normalized rather than rejected.

The response is the file itself — `Content-Type` per format,
`Content-Disposition: attachment; filename="voltedge-export.kmz"` — streamed,
not a JSON envelope. Every fallible read happens **before** any byte is
written, so a read error still becomes a clean JSON `4xx`/`5xx`; only once all
layers are in hand are the headers sent and the file streamed.

### The read (`api/internal/crud/export.go`)

`crud.ExportFeatures` reuses the exact list/query read path
(`selectColumns`/`scanRows`/`formatRow`), so each row already carries its
GeoJSON geometry (4326) and its attribute map — no re-fetch, mirroring the
query builder. It adds:

- an optional **bbox spatial filter**:
  `ST_Intersects(geom, ST_Transform(ST_MakeEnvelope(?,?,?,?,4326), <class SRID>))`
  — the envelope is built in 4326 (the client's coordinates) and transformed to
  the class's own SRID so the compare uses the stored geometry's spatial index
  (a no-op transform for the 4326 classes we have);
- the same always-on `deleted_at IS NULL` exclusion as list;
- a `maxExportFeatures = 50000` per-class cap (an export holds every feature in
  memory to encode it) and a stable `id` sort so repeated exports of one extent
  are byte-identical;
- a guard that a **geometry-less class can't be exported** (nothing to place on
  a map).

### The encoder (`api/internal/export/`)

A new package, deliberately dependency-free — Go's stdlib `archive/zip` +
`encoding/json` + string building do all the work (no GIS/shapefile/KML/DXF
library added; `go.mod` is untouched):

- **`feature.go`** — the format-neutral model every encoder consumes:
  `Feature` (id, name, status, raw GeoJSON geometry, attribute map) and `Layer`
  (a class's features + its code/name/geometry-type). `featureFrom` converts one
  `formatRow` envelope; `Layer.AttributeKeys()` is the sorted union of attribute
  keys — the stable column set the tabular encoders will share.
- **`kml.go`** — `writeKMZ` (a zip wrapping one `doc.kml`) and the KML writer:
  a `Folder` per class, a `Placemark` per feature with attributes (status first,
  then the layer's sorted keys) in `ExtendedData`, and geometry translated from
  GeoJSON. Handles Point/LineString/Polygon and their `Multi*` variants;
  features with no geometry are skipped, an unknown geometry type errors. KML is
  inherently 4326, matching the stored data exactly.
- **`export.go`** — the `POST /export` handler, request validation, the
  `formats` dispatch table (content-type + extension + encoder per format), and
  `gatherLayers` (load + read each class, mapping a missing class to `404`, a
  geometry-less one to `400`).

### Files

- `api/internal/crud/export.go` (new) — `ExportFeatures`, `ExportBBox`,
  `maxExportFeatures`.
- `api/internal/export/feature.go`, `kml.go`, `export.go` (new package).
- `api/internal/export/export_test.go` (new) — pure tests: KML for every
  geometry type (incl. a polygon with a hole and the `Multi*` variants), the
  geometry-less-feature skip, the unknown-geometry error, the KMZ-is-a-valid-zip
  check, `xmlEscape`, `Layer.AttributeKeys`, `featureFrom`, and `parseBBox`
  (nil, wrong arity, min/max normalization). No DB needed.
- `api/cmd/server/main.go` — mounts `export.Routes(db)` at `/export` under the
  `features:read` gate.

## Slice 2 — Shapefile

Adds `"shp"` to the `formats` table (content-type `application/zip`, extension
`zip`) and a hand-rolled ESRI Shapefile encoder — **no shapefile dependency**,
just `archive/zip` + `encoding/binary`. Each selected class becomes its own
shapefile (a `.shp/.shx/.dbf/.prj` quartet), all bundled into one zip, because
a `.shp` holds exactly one geometry type.

### `api/internal/export/shapefile.go`

- **`writeShapefiles`** — opens a `zip.Writer`, and per layer writes the four
  files in a fixed order (`.shp`, `.shx`, `.dbf`, `.prj`) so a given export is
  byte-identical run to run.
- **Geometry (`.shp` / `.shx`).** `shapeTypeFor` maps the class geometry type to
  a shape type: `point → Point(1)`, `line → PolyLine(3)`, `polygon → Polygon(5)`
  (anything else is an error). Both files share the 100-byte header (file code
  9994, version 1000, shape type, overall bbox); the `.shp` carries each
  feature's record and the `.shx` a fixed 8-byte `(offset, length)` index entry,
  so record counts stay in lockstep with the DBF. A feature with no/empty
  geometry is written as a **Null** shape rather than dropped, keeping the
  geometry and attribute tables aligned.
- **Polygon winding is enforced, not assumed.** The shapefile spec requires the
  outer ring **clockwise** and holes **counter-clockwise** — the opposite of
  GeoJSON, and PostGIS makes no guarantee — so `orientPolygon` computes each
  ring's shoelace signed area and reverses as needed, closing any open ring.
- **Attributes (`.dbf`, dBASE III).** A leading `name` and `status` text column,
  then one typed column per attribute field: `integer → N(18,0)`,
  `numeric → N(19,6)`, `boolean → L`, everything else (text, uuid, dates,
  timestamps, jsonb) → text sized to the data (capped at the DBF max of 254) so
  no value is lost to a too-narrow column. DBF field names are capped at **10
  chars** and **de-duplicated** on collision so no column silently overwrites
  another. A numeric value too wide for its column becomes `*`-fill (the dBASE
  overflow marker), never a silently-wrong truncation.
- **CRS (`.prj`).** Every layer ships the WGS84 WKT (`wgs84WKT`); the data is
  already 4326, so no transform.

`format` values are unchanged (`"shp"` reuses the existing streaming path); the
handler, bbox read, and `gatherLayers` from Slice 1 are untouched. `gatherLayers`
now also populates each `Layer.Fields` (name + `data_type`) from the loaded
metadata, which the DBF encoder needs to pick typed columns.

### Files

- `api/internal/export/shapefile.go` (new) — `writeShapefiles` + the encoder
  (`shapeTypeFor`, `buildSHP`, `shapeRecord`/`partsShape`, `orientPolygon`/
  `signedArea`/`closeRing`, `buildDBF` + column/renderer helpers, `toFloat`,
  `safeName`, `wgs84WKT`).
- `api/internal/export/export.go` — `"shp"` registered in `formats`;
  `gatherLayers` populates `Layer.Fields`.
- `api/internal/export/feature.go` — `AttributeField` type + `Layer.Fields`.
- `api/internal/export/export_test.go` — shapefile tests: `shapeTypeFor`, the
  four-file bundle + WGS84 `.prj`, the `.shp` header/point-record byte layout
  (file code, shape type, self-consistent file length, coordinates), polygon
  ring re-orientation (outer CW, hole CCW), `closeRing`, DBF field-name
  truncate/uniquify, DBF record byte shape (version, count, header/record
  lengths, EOF marker), `toFloat`, `padN` overflow.

## Slice 3 — DXF

Adds `"dxf"` to the `formats` table (content-type `application/dxf`, extension
`dxf`) and a hand-rolled AutoCAD DXF encoder — **no CAD dependency**, just
string building. One `.dxf` holds every selected class.

### `api/internal/export/dxf.go`

DXF is a flat list of `(group-code, value)` line pairs grouped into sections.
`writeDXF` emits four:

- **HEADER** — just `$ACADVER = AC1015` (AutoCAD 2000, the first version with
  `LWPOLYLINE`).
- **TABLES** — an **APPID** table registering the `VOLTEDGE` application (so the
  XDATA is valid), and a **LAYER** table with one layer per feature class (reusing
  the shapefile `safeName`, so a CAD user can toggle classes independently).
- **ENTITIES** — the geometry, each entity placed on its class's layer:
  `Point → POINT`, `LineString → open LWPOLYLINE`, `Polygon ring → closed
  LWPOLYLINE`. DXF has **no polygon-with-hole and no multipart entity**, so a
  polygon's rings, a `MultiPolygon`'s polygons, and a `MultiLineString`'s lines
  each become their own entity, all carrying the same feature's attributes. A
  closed ring drops GeoJSON's duplicate closing vertex and sets the closed flag
  instead (no zero-length segment). An unknown geometry type is an error;
  geometry-less features emit nothing.
- **EOF**.

**Attributes as XDATA (the locked decision).** DXF has no attribute table, so
each entity carries its attributes as extended entity data under the `VOLTEDGE`
app: `name` and `status` first, then the layer's sorted attribute keys, each a
`key=value` string (group 1000). Values are flattened to a single line (a
newline would corrupt the line-oriented pair stream) and clipped to the format's
255-byte XDATA limit. XDATA round-trips in AutoCAD/civil tools; lightweight
viewers may not surface it.

**CRS.** Coordinates are written as plain decimal EPSG:4326 degrees (never
scientific notation, which some readers reject). Per the locked decision this
means the drawing is geographically correct but, in a CAD editor, tiny and
distorted — fine for import-by-coordinates, poor for drafting.

### Files

- `api/internal/export/dxf.go` (new) — `writeDXF` + the encoder (`writeDXFHeader`/
  `writeDXFTables`/`writeDXFEntities`, `dxfFeature` dispatch, `dxfPoint`/
  `dxfPolyline`, `writeDXFXData`/`dxfXField`/`dxfClean`/`dxfClip`, `dxfFloat`,
  `dxfPair`).
- `api/internal/export/export.go` — `"dxf"` registered in `formats`.
- `api/internal/export/export_test.go` — DXF tests: full point-document
  structure (sections, APPID, per-class layer, POINT + coords + XDATA fields),
  open-polyline for a line, closed-polyline-with-duplicate-dropped for a polygon
  ring, unknown-geometry error, geometry-less skip, XDATA clean/clip.

## Slice 4 — frontend

An **Export** button in the map's top-right control cluster (beside Query /
Layers, in [routes/map.tsx](../web/src/routes/map.tsx)) opens a dialog that
POSTs to `/export` and saves the streamed file.

### The map-view bbox (the one piece of new wiring)

The OpenLayers `Map` lives in a component-local ref in `voltedge-map.tsx` and is
never lifted into a store, so a sibling dialog can't read the viewport directly.
The map now **publishes** its viewport: on `moveend` (and once on the first
`rendercomplete`) it transforms the view's calculated extent from the view CRS
(EPSG:3857) to EPSG:4326 and pushes it into a small store,
[stores/map-view-store.ts](../web/src/stores/map-view-store.ts)
(`useMapViewStore`, `bbox: [minX, minY, maxX, maxY] | null`). This mirrors how
the map already feeds `useMapPointerStore` from `pointermove`. `bbox` is null
until the first render, so the dialog degrades to a whole-dataset export if it's
somehow opened first.

### The dialog

[components/map/export-dialog.tsx](../web/src/components/map/export-dialog.tsx)
(`ExportDialog`, toggled by
[stores/export-store.ts](../web/src/stores/export-store.ts), the query-store
pattern):

- **Format** — radio: KMZ / Shapefile / DXF.
- **Scope** — a "whole dataset (ignore current view)" checkbox. Unchecked
  (default) clips to the map-view store's current bbox; checked omits the bbox.
  If no bbox is available yet the checkbox is forced on and disabled.
- **Layers** — one checkbox per geometry-bearing feature class
  (`geometry_type !== "none"`, from `useFeatureClasses`), pre-checked to whatever
  is **currently visible** on the map. Visibility comes from `useLayersStore`'s
  `hidden` map, keyed by tile source id, which is the lowercased class code — so
  a class is visible when `!hidden[code.toLowerCase()]`. The checkboxes are
  seeded once per open (not re-seeded on later `hidden` changes, so an edit here
  isn't clobbered), with a select-all/clear-all shortcut.
- **Download** — the selected class **codes** (already the uppercase codes the
  endpoint keys on, straight from `useFeatureClasses`) plus the format and
  optional bbox are POSTed via a new binary helper, `postForBlob` in
  [lib/api.ts](../web/src/lib/api.ts) — the same same-origin-cookie +
  double-submit-CSRF handling as `apiFetch` (a raw `fetch` would 403), but
  returning a `Blob` + the `Content-Disposition` filename instead of JSON. The
  blob is saved with the throwaway-anchor trick (mirroring the query panel's CSV
  download); a failed export surfaces its JSON `{error}` as a toast, and the
  dialog stays open.

Since the UI kit has no Checkbox/Radio/Select components, the form uses styled
native inputs (the existing convention).

### Files

- `web/src/stores/map-view-store.ts` (new) — `useMapViewStore`, `Bbox4326`.
- `web/src/stores/export-store.ts` (new) — `useExportStore` (open flag).
- `web/src/components/map/export-dialog.tsx` (new) — `ExportDialog`.
- `web/src/lib/api.ts` — `postForBlob` (binary POST helper).
- `web/src/components/map/voltedge-map.tsx` — publishes the 4326 viewport bbox
  on `moveend`/`rendercomplete`.
- `web/src/routes/map.tsx` — Export trigger button + mounts `ExportDialog`.

### Verification owed (frontend)

```sh
cd web && npm run typecheck && npm run lint
```

## Slice 5 — territory scope

Adds a third export scope beside "current view" and "whole dataset": clip to a
chosen set of **territories**. The request gains an optional `territories`
field — an array of territory ids:

```json
{
  "format": "kmz",
  "feature_classes": ["pole", "mv_conductor"],
  "territories": ["b3f1…", "c7a2…"]
}
```

`bbox` and `territories` are independent, optional filters; sent together they
compose as an AND, but the dialog sends at most one. With neither, the export is
the whole dataset.

### Why the subtree expansion is mandatory (not a nicety)

A feature is stamped with the id of the **deepest** territory that geometrically
contains it (`territory.Assign`/`ResolveContaining`), so a feature physically
inside a district can carry the id of a drawn sub-area or plot beneath it.
Filtering `territory_id` against the *selected* ids alone would silently drop
those features. So each selected territory is expanded to its **whole subtree**
and the filter matches the full set — restoring the intuitive "everything within
this territory."

### The expansion (`api/internal/territory/descendants.go`)

`DescendantIDs(ctx, db, ids)` returns the given ids plus every territory beneath
them, deduplicated, via a downward recursive CTE (the inverse of
`wouldCreateCycle`'s upward ancestor walk):

```sql
WITH RECURSIVE subtree AS (
    SELECT id FROM gis.territories WHERE id IN (…)
    UNION
    SELECT t.id FROM gis.territories t
    JOIN subtree s ON t.parent_id = s.id
)
SELECT id::text FROM subtree
```

Ids that don't exist contribute nothing (the base case only selects present
rows), so an all-unknown input yields an **empty** slice — which the read path
treats as "match nothing", never a silent fall-back to the whole dataset.

### The read (`api/internal/crud/export.go`)

`ExportFeatures` takes a `territoryIDs []string` whose three states map to three
behaviours: **nil** → no territory filter; a **non-empty** slice → keep only
rows whose `territory_id::text` is in the set (each id a positional bind param,
same as the bbox); a **non-nil empty** slice → `1 = 0`, match nothing. The cast
to text compares against the canonical `id::text` the expansion emits (the
column is unindexed either way).

### The handler (`api/internal/export/export.go`)

`resolveTerritories` turns the request's `territories` into that expanded set:
no ids → nil (no filter); otherwise `territory.DescendantIDs`, with a nil result
normalised to an empty (non-nil) slice so unresolvable ids stay "match nothing".
It threads the result through `gatherLayers` → `ExportFeatures`.

### The dialog (`web/src/components/map/export-dialog.tsx`)

The whole-dataset checkbox becomes a **3-way scope `<select>`** — *Current view*
/ *Whole dataset* / *Territories* (view option disabled until the map reports a
bbox, Territories disabled when none are defined). Choosing *Territories* reveals
a **collapsed multi-select dropdown** (a `DropdownMenu` of
`DropdownMenuCheckboxItem`s, styled to read like the `<select>` triggers): its
trigger shows an *N selected* summary, and opening it lists the territories. The
flat `GET /territories` adjacency list (`useTerritories`) is flattened
depth-first (`flattenTerritories`, roots first, siblings by name, `depth` driving
the indent), each row a checkbox tagged with its level name; `onSelect`
`preventDefault` keeps the menu open so several can be ticked in one visit. The
Export button is disabled until at least one territory is picked (an empty
territory export would resolve to "match nothing"). On submit, `territories:
[ids]` is sent only in territory scope; the server does the subtree expansion.

To keep the growing dialog compact, the single-choice **Format** and **Scope**
controls are native `<select>`s and the multi-select **Territories** picker is a
collapsed checkbox dropdown (all sharing the `inputClass` styling) rather than
always-visible radio/checkbox stacks; only the **Layers** list stays an inline
checkbox list.

### Files

- `api/internal/territory/descendants.go` (new) — `DescendantIDs`.
- `api/internal/crud/export.go` — `ExportFeatures` gains `territoryIDs []string`
  (nil = no filter, empty = match nothing) + the `territory_id::text IN (…)`
  clause.
- `api/internal/export/export.go` — `exportRequest.Territories`,
  `resolveTerritories`, threaded through `gatherLayers`.
- `web/src/components/map/export-dialog.tsx` — Format + 3-way Scope `<select>`s +
  `flattenTerritories` collapsed checkbox-dropdown territory picker; `territories`
  in the POST body.

No migration — the filter reads the existing `territory_id` column.

## Verification owed

The Bash tool is unavailable here, so these were **not** run — please run them:

```sh
cd api && go build ./... && go vet ./... && go test ./internal/export/... ./internal/crud/...
```

Quick manual smoke test once the server is up (KMZ opens in Google Earth; the
`.shp.zip` opens in QGIS/ArcGIS):

```sh
curl -X POST localhost:8080/export \
  -H 'Content-Type: application/json' \
  --cookie '<session cookie>' \
  -d '{"format":"kmz","feature_classes":["pole"]}' \
  -o voltedge-export.kmz

curl -X POST localhost:8080/export \
  -H 'Content-Type: application/json' \
  --cookie '<session cookie>' \
  -d '{"format":"shp","feature_classes":["pole","mv_conductor"]}' \
  -o voltedge-export.zip

curl -X POST localhost:8080/export \
  -H 'Content-Type: application/json' \
  --cookie '<session cookie>' \
  -d '{"format":"dxf","feature_classes":["pole","mv_conductor"]}' \
  -o voltedge-export.dxf

# territory-scoped (Slice 5): only features in these territories + their subtrees
curl -X POST localhost:8080/export \
  -H 'Content-Type: application/json' \
  --cookie '<session cookie>' \
  -d '{"format":"kmz","feature_classes":["pole"],"territories":["<territory-id>"]}' \
  -o voltedge-territory.kmz
```

No migration is needed — the endpoint reads the existing compiled tables.
