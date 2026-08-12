# Phase 3 (slice 2) — The map loop + IA correction

**Goal:** put the map where a GIS app's home surface actually belongs —
`/`, not a fourth item in a flat nav list — and render real, compiled
feature data on it via Martin, not a mock. Deliberately not in this
slice: click-to-select/attribute panels and metadata-driven symbology
(icon resolution, shape vs. modifier) — both real subsystems on their
own; see "What's still open."
**Status:** done and verified against a **real Martin binary** run
in-sandbox against real compiled feature tables (not assumed from
documentation) — genuine MVT tiles requested, returned, and rendered,
confirmed via network capture and screenshot, in both themes, with pan/
zoom interaction working. See "Try it yourself" below.

## Why this slice exists

Slice 1 shipped a shell with `/`, `/features`, `/connectivity`,
`/relationships`, `/settings` as flat, equal-weight nav items — a
reasonable IA for an admin/data-management tool, but the wrong one for
a GIS app. The user caught this directly: *"i was expecting to see a
map... gis is straight to map after successful login."* Correct call —
in Esri Field Maps, G/Technology, and every comparable tool, the map
*is* the app; everything else is either a panel that opens off a
selected feature, or a secondary escape hatch, never the front door.

## What was built

- **IA reshuffle** — `/` now renders `MapPage`. `Feature classes`,
  `Connectivity`, `Relationships` moved into a visually secondary
  "Data" sidebar group below the map, and `AppHeader`'s title now
  reflects the current route instead of a hardcoded "Dashboard". The
  old `DashboardPage` (slice 1's health-check-and-cards page) is gone —
  its one genuinely useful signal, live API health, is now a small
  overlay badge on the map itself (`src/routes/map.tsx`), alongside a
  layer count from Martin's catalog.
- **Layout fix for a full-bleed route** — `AppLayout` no longer
  wraps every route in a padded div; that padding moved into
  `PlaceholderPage` itself (`src/routes/placeholder-page.tsx`), so the
  map is the one route that can fill its container edge to edge while
  every other route keeps the padded-card look unchanged.
- **`src/components/map/voltedge-map.tsx`** — the actual OpenLayers
  map: an `ol/Map` created once in a `useEffect` (cleaned up via
  `map.setTarget(undefined)` on unmount), an OSM raster base layer, and
  a second effect that adds one `VectorTileLayer` per entry in Martin's
  live catalog.
- **`src/lib/martin.ts`** + **`src/hooks/use-catalog.ts`** — fetches
  Martin's `/catalog` (proxied at `/tiles/catalog`, see below) through
  TanStack Query. Martin auto-discovers every geometry table in the
  `gis` schema and publishes it as a tile source named after the table
  (migrations/0015's own comment says as much) — so the catalog *is*
  the live list of compiled, tile-servable feature classes. Reading it
  at runtime instead of hardcoding feature class codes keeps the map in
  step with whatever's actually been compiled, one level down from the
  rest of this app's metadata-driven spirit: Martin's catalog reflects
  compiled physical tables, which themselves come from metadata.
- **`src/lib/map-layers.ts`** — geometry-type-only styling (point =
  copper circle, line = teal stroke, polygon = slate fill), branching on
  the *decoded* MVT feature's geometry type at render time. No
  metadata lookup needed for this — deliberately not real symbology
  resolution yet, see "What's still open."
- **Dev proxy for tiles** (`vite.config.ts`) — `/tiles/*` → Martin's
  port (`3701`), the same pattern slice 1 already used for `/api/*` →
  the Go API. The browser never talks to Martin's actual port directly.

## How this was validated — a real Martin binary, not just a documented contract

This sandbox has no Docker daemon, so the `ghcr.io/maplibre/martin`
image from `docker-compose.yml` couldn't run here directly. Rather than
write the OpenLayers/Martin wiring against Martin's documented API and
call it done, a real Martin release binary
(`martin-x86_64-unknown-linux-gnu`, fetched directly from a GitHub
release — reachable even though `ui.shadcn.com` and
`tile.openstreetmap.org` are proxy-blocked in this sandbox, confirmed
via the proxy status endpoint) was run standalone against a real
Postgres database with `POLE`, `MV_CONDUCTOR`, and `DIST_XFMR` already
compiled:

- **Auto-discovery, unprompted** — Martin's own startup log shows it
  finding all three tables on its own: `Discovered source dist_xfmr
  from table gis.dist_xfmr with geom column (POINT, SRID=4326)`, and
  likewise for `mv_conductor` and `pole` — exactly the mechanism
  `fetchCatalog()` depends on, observed actually happening, not assumed
  from Martin's docs.
- **A real MVT tile, fetched directly with curl** before any frontend
  code ran against it: 458 bytes of protobuf, the layer name `pole`
  visible in the raw bytes, for a tile computed to cover the seed data's
  actual coordinates.
- **Through the full stack, in a real browser** — with the Vite dev
  server, the real Martin binary, and the real Go API all running
  together: navigated to `/`, captured every network request matching
  `/tiles/*`, and confirmed real `200` responses with
  `content-type: application/x-protobuf` for tiles that actually
  contain geometry, and `204`s for tiles outside the data's extent
  (correct Martin behavior, not an error).
- **The rendered result** — a screenshot showing real copper points and
  a real teal line at the correct relative positions for the actual
  seed data, in both light and dark mode, confirming `styleForFeature`
  branches correctly per decoded geometry type.
- **Interaction** — double-click zoom was exercised and the rendered
  features shifted/scaled exactly as expected, confirming the map is a
  live, interactive `ol/Map`, not a static image.
- **The one thing this sandbox genuinely can't verify**: the OSM base
  layer's raster tiles. `tile.openstreetmap.org` is explicitly
  proxy-blocked here (`403`, confirmed via the same proxy status
  endpoint used to diagnose the `ui.shadcn.com` block in slice 1) — the
  base layer renders as blank canvas in every screenshot in this doc.
  `ol/source/OSM` is a standard, extremely well-established OpenLayers
  source with nothing project-specific about it; this is a sandbox
  network limitation, not a code path this project should be less
  confident about. Confirm it on a real machine per "Try it yourself."
- **Secondary pages still work correctly** after the layout change —
  navigated to all four (`Feature classes`, `Connectivity`,
  `Relationships`, `Settings`) and confirmed the padded-card look is
  unchanged from slice 1, and the header title/sidebar active-state
  both track the current route correctly.

## Decisions made

- **Map as `/`, not `/map`** — the map isn't *a* view among equals,
  it's the app's home surface; giving it the bare root path reflects
  that, and matches "straight to map after login" once real auth lands
  (the login redirect target becomes trivially `/`).
- **Catalog-driven layers, not hardcoded feature class codes** — see
  above; this also means adding a new compiled feature class shows up
  on the map with zero frontend changes, which is the whole point of a
  metadata-driven system extended one more layer into the client.
- **Geometry-type styling now, real symbology resolution later** —
  docs/metadata-core.md's "Symbology resolution" section describes a
  real subsystem (resolution order by specificity, shape vs. modifier,
  `render_context` for map vs. diagram) that deserves its own slice, not
  a rushed first pass bolted onto getting *something* to render.
  Branching on decoded geometry type is enough to prove the pipeline
  and tell feature classes apart visually in the meantime.
  `map-layers.ts` says so explicitly in its own comment, so it reads as
  a deliberate placeholder, not an oversight, to whoever builds real
  symbology resolution next.
- **Style colors are duplicated constants, not CSS variable
  references** — OpenLayers paints to `<canvas>`, which can't consume
  `var(--primary)` the way DOM-rendered Tailwind classes can. The
  constants in `map-layers.ts` echo the theme's `--primary`/`--chart-2`
  tokens by eye, documented as such, rather than pretending there's a
  clean way to share them.
- **A real binary in-sandbox over trusting documented behavior** — this
  project's whole testing discipline (Phase 2's every slice) has been
  "run it for real, don't take correctness on faith." Martin was the
  one piece of infrastructure this sandbox couldn't normally reach the
  same way (no Docker), so fetching a standalone release binary rather
  than skipping straight to "should work per the docs" kept that
  discipline intact for this slice too.

## What's still open

- **Metadata-driven symbology resolution** — icon library, resolution
  order (feature_class + subtype + domain_value → ... → feature_class
  default), shape-vs-modifier split, `render_context`. Current styling
  is geometry-type-only, a deliberate placeholder (see "Decisions
  made").
- **Click-to-select + attribute panel** — selecting a feature on the
  map and seeing/editing its attributes in a side panel, with its
  connectivity and relationships surfaced contextually (per the IA
  discussion this slice grew out of). This is the natural next slice.
- **Snapping** — depends on an edit/digitize flow existing first, which
  depends on the selection/attribute panel above.
- **No default-extent concept** — the map's initial center/zoom is a
  hardcoded constant matching the seed data used to validate this
  slice, not derived from anything real (there's no "service territory"
  concept in the metadata model yet). Fine for now, worth revisiting
  once real data exists.
- **The `Feature classes`/`Connectivity`/`Relationships` pages are
  still slice-1 placeholders** — demoted in the IA now, but not yet
  built out as real bulk-browse/table views.
- **`ol` adds meaningfully to the JS bundle** (build now warns past the
  500kB chunk threshold). Not code-split, on purpose: the map is the
  home route, so nearly every visit needs this bundle immediately —
  deferring it wouldn't actually improve time-to-interactive for the
  common case. Revisit if that stops being true (e.g. an auth gate
  that's genuinely map-independent lands in front of it).

## Try it yourself

```
task infra-up
task migrate
task seed-pole
task seed-connectivity
task compile CODE=POLE
task compile CODE=MV_CONDUCTOR
task compile CODE=DIST_XFMR
task api-dev
```
Martin is already part of `task infra-up` (it's in `docker-compose.yml`)
and auto-discovers the tables you just compiled — no separate step
needed on a real machine with Docker, unlike the manual binary this
sandbox needed.

From a second terminal:
```
cd web
npm install
npm run dev
```
Open `http://localhost:5173`. You should see:
- A real OpenStreetMap base layer (this sandbox couldn't verify this
  part — it's the one thing to specifically check on your machine).
- Copper points and teal lines at the real coordinates of whatever
  `POLE`/`MV_CONDUCTOR`/`DIST_XFMR` rows you've created via the API
  (see docs/phase-2-crud-engine.md's "Try it yourself" if you need to
  create some first).
- A small "API healthy" + "N layers" status overlay, top-left of the
  map.
- The sidebar showing `Map` as the first, primary item, with `Feature
  classes`/`Connectivity`/`Relationships` grouped separately under
  "Data" below it.
- Scroll to zoom, drag to pan, double-click to zoom in a step — all
  standard OpenLayers interactions, no custom code required for them to
  work.

`npm run build && npm run lint` should both finish clean (the build's
chunk-size warning about `ol` is expected, see "What's still open").
