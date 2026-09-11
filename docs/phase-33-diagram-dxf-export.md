# Phase 33 — Diagram DXF export

Export a one-line / schematic **diagram** as a DXF a CAD tool can open. This
is distinct from the existing GIS export (`api/internal/export`, which writes
map feature classes in EPSG:4326 as KMZ/SHP/DXF/CIM): a diagram lives in its
own local, unitless x/y space and is drawn by a hand-rolled SVG canvas, not
by Martin tiles.

## Why client-side

The canvas ([web/src/components/map/diagram-canvas.tsx](../web/src/components/map/diagram-canvas.tsx))
resolves everything a faithful export needs **in the browser**:

- **Symbology matching is client-side** (`resolveSymbology`, [web/src/lib/symbology.ts](../web/src/lib/symbology.ts)) —
  the server never sees a rule or a resolved colour.
- **Conductor endpoints are connectivity-derived here** (`conductorItems`) —
  a `geometry_type = 'line'` placement draws as the elbow between its two
  resolved endpoint placements, not from any stored geometry.
- **Glyph SVG markup + icon/font sizing** (`iconSize`, `fontSize`, extent) are
  all computed per render.

A server-side exporter would have to port the symbology matcher, re-derive
elbow routing, and hand-write an SVG-path parser/flattener in Go — the hardest
part — with none of it verifiable against what the browser actually drew.
Client-side reuses the exact render model, and (Slice 2) gets native
`DOMParser` + SVG path sampling for the glyph geometry for free. Decided with
the user 2026-08-31 ("Client-side").

Trade-off accepted: the export is a **UI action**, not reachable via REST/MCP.

## Format: DXF R12 (AC1009)

The most portable interchange dialect — no handles, no object model, read by
essentially every CAD/GIS tool. Cost: R12 is **ACI-only** (no true colour), so
each symbology colour maps to the nearest of the seven standard colour indices
(`nearestAci`). Geometry, layering and text carry across exactly.

The drawing is authored **Y-down** (screen space); DXF/CAD is **Y-up**, so
every coordinate is flipped once on emit (`DxfBuilder.emitXY` negates Y). SVG
`rotate` is clockwise in Y-down space; after the flip that reads as
clockwise-negated in CAD's CCW `group 50` convention, so text rotation is
emitted as `-rotationDeg`.

## Slice 1 — structural geometry (this slice)

[web/src/lib/diagram-dxf.ts](../web/src/lib/diagram-dxf.ts): a pure
`buildDiagramDxf(input)` + a `downloadDiagramDxf(input)` browser-download
wrapper. A **Download** button on the diagram header
([diagram-canvas.tsx](../web/src/components/map/diagram-canvas.tsx),
`handleExportDxf`) assembles the input from the same memoised values it
renders from (`conductorItems`, `pointItems`, `elements`, `featureResults`,
`rules`, `symbologyIcons`, `iconSize`, `fontSize`) and triggers the download.

Emitted, each on its own CAD layer so a user can toggle them:

| Layer | Content |
| --- | --- |
| `CONDUCTORS` | line-class placements: drawn `path`, else connectivity elbow (`elbowPoints`); loose ends (no geometry) skipped, matching the canvas |
| `DEVICES` | point placements — **real glyph** (Slice 2: BLOCK/INSERT); marker fallback if the SVG can't be flattened |
| `SYMBOLS` | stencil `symbol` elements — **real glyph** (Slice 2: BLOCK/INSERT); marker fallback |
| `BUSES` / `TAPS` | bus/tap polylines; tap junction dot at the last vertex |
| `ANNOTATIONS` | boxes (closed polyline + optional caption) and schematic lines |
| `TITLEBLOCK` | border, row dividers, key/value text |
| `LEGEND` | border, header divider, title text |
| `SLOTS` | unbound template slots (circle + "+" + class label) |
| `TEXT` | free text elements |
| `LABELS` | every feature/conductor name label, honouring `label_dx/dy/rotation/scale` and the glyph-rotation transform chain (`transformPoint`) |

Colours: symbology colour → nearest ACI on conductors/devices; furniture is
ByLayer (prints black).

## Slice 2 — full symbol shapes (this slice)

Devices and stencil `symbol` elements now export as **real glyph geometry**,
not markers. [web/src/lib/svg-flatten.ts](../web/src/lib/svg-flatten.ts)
(`flattenSvg`) parses a glyph's `0 0 24 24` SVG with `DOMParser` and flattens
it to plain polylines: `<path>` data (`M/L/H/V/C/S/Q/T/A/Z`, absolute and
relative, curves and arcs sampled to segments) plus `line/polyline/polygon/
rect/circle/ellipse`, applying element/group `transform` attributes and
mapping every point through the root `viewBox` onto a fixed `0..24` frame (so a
glyph authored at any viewBox exports at the scale the `<image>` renders it).

`diagram-dxf.ts` resolves each placement's `icon_ref` (from `resolveSymbology`)
and colour, flattens its glyph **once** (cached per ref), writes it as a DXF
`BLOCK` — geometry centred on the glyph centre, pre-Y-flipped via `emitXY`,
entities `BYBLOCK` — and emits one `INSERT` per placement: insertion point =
placement centre (Y-flipped), uniform scale `iconSize/24`, group-50 rotation
`-deg` (the same SVG-clockwise → CAD-CCW negation the text uses), colour = the
placement's nearest ACI on the INSERT (so `BYBLOCK` entities tint per feature).
The single-flip + `-deg` pairing reproduces the canvas's
`translate · rotate · scale` transform exactly. A glyph that flattens to no
geometry (parse failure / empty SVG) falls back to the Slice 1 marker circle,
so a symbol never silently vanishes.

Resolved once via `resolveIconMarkup` (new export on
[symbology-icons.ts](../web/src/lib/symbology-icons.ts), the same fallback
chain `iconDataUri` uses: DB set → hardcoded fallback → default shape).

## Slice 3 — feature attributes as XDATA

Geometry alone gives a CAD user shapes with no data behind them. Every
feature-backed entity (conductor `POLYLINE`, device `INSERT`, or the marker
`CIRCLE` fallback) now carries the feature's attributes as **XDATA** under a
registered application id `VOLTEDGE` (`featureXData`, `DxfBuilder.emitXData`):
`feature_class`, `feature_id`, `name`, `subtype`, `status`, then every
non-null scalar attribute, each a `1000` string `key=value` (newline-stripped,
capped at DXF's 255-byte limit; nested object/array values skipped). The
`APPID` table is registered in `TABLES` (required in R12) so a reader accepts
the extended data. XDATA rides the `POLYLINE` header (before its vertices) and
the tail of simple entities.

XDATA is the one attribute mechanism that attaches to *any* entity type, so
conductors and devices carry data uniformly; it's read back by AutoCAD,
BricsCAD, ODA-based tools and FME. (Block `ATTRIB`/`ATTDEF` — the visible
alternative — only rides `INSERT`s, so it couldn't cover conductor polylines;
not used.) Furniture (boxes/text/title-block/legend/slots) and stencil
`symbol`s are not feature-backed and carry no XDATA.

## Verification owed (Bash blocked here — run locally)

- `cd web && npm run build` (`tsc -b`) and `npm run lint` (oxlint) clean.
- Open a diagram with conductors + devices (a switch, a transformer) + a
  title block/legend, click **Download**, open the `.dxf` in a CAD tool (or
  re-import via the existing `dxf` reader): conductors/buses/taps/boxes/text/
  title-block/legend and all labels present, on their layers, right-side-up
  (Y-flip correct), labels not mirrored.
- **Slice 2:** each device/symbol is its actual shape (a triangle exports as a
  triangle, a circle as a circle), correctly scaled, centred and rotated —
  matching the on-screen glyph, not a placeholder marker. Rotate a device on
  the canvas and confirm the exported glyph turns the same way.
- Confirm a green feeder exports green-ish, a red isolator red-ish (nearest
  ACI), furniture black.
- **Slice 3:** select a conductor/device in a CAD tool (or read the file) and
  confirm its XDATA under `VOLTEDGE` lists `feature_class`/`feature_id`/`name`/
  `subtype` and the feature's attributes (e.g. voltage, material).
