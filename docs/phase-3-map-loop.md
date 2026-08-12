# Phase 3 (slice 2) — The map loop + IA correction

**Goal:** put the map where a GIS app's home surface actually belongs —
`/`, not a fourth item in a flat nav list — render real, compiled
feature data on it via Martin, not a mock, and let a user click a
feature (including disambiguating a click that hits several
stacked/overlapping features) to see its real attributes. Deliberately
not in this slice: metadata-driven symbology (icon resolution, shape
vs. modifier) and an undockable/floating panel — see "What's still
open."
**Status:** done and verified against a **real Martin binary** run
in-sandbox against real compiled feature tables (not assumed from
documentation) — genuine MVT tiles requested, returned, and rendered,
confirmed via network capture and screenshot, in both themes, with pan/
zoom interaction working. See "Try it yourself" below.

## A second correction, caught immediately by live testing

The first version of this slice still docked the sidebar beside the
map — same fixed-width column as every other route, just with the map
filling what was left. Live testing caught it right away: *"isnt the
layer controls not supposed to be in the sidebar? ... shouldn't you be
suggesting a better ui?"* Fair, and worth fixing before calling this
slice done rather than filing it as a follow-up — a docked sidebar
permanently reserving map width is the same category of mistake this
slice's own intro describes fixing (competing for the front door
instead of getting out of the way).

The actual pattern every real map product uses: the canvas stays edge
to edge, and navigation/tools float *over* it rather than push it
aside. What changed:

- **`src/components/ui/sidebar.tsx`** gained an `overlay` prop on
  `SidebarProvider` — when set, the sidebar always renders as a `Sheet`
  drawer (the same component already used for the mobile breakpoint on
  every other route), never as a docked column, regardless of viewport
  width. This reuses the exact same nav content and component rather
  than building a second, parallel nav UI to keep in sync.
- **`src/components/layout/map-layout.tsx`** (new) — the map route's
  own layout: `SidebarProvider overlay` + the same `AppSidebar` +
  `AppHeader`, no docked column, map fills everything below the
  header. `src/app.tsx` now has two layout groups: `MapLayout` for `/`,
  `AppLayout` (unchanged, still docks) for everything else.
- **A real layers panel**, not just a fix — the old "3 layers" badge
  was read-only. `src/stores/layers-store.ts` (Zustand, persisted, only
  storing *hidden* source ids so a newly compiled feature class defaults
  to visible with no extra wiring) + `src/components/map/layers-panel.tsx`
  (a `DropdownMenuCheckboxItem` per catalog entry) give an actual
  visibility toggle per feature class. `voltedge-map.tsx` reacts to the
  store and flips each `VectorTileLayer`'s `visible` flag in place —
  toggling one layer doesn't rebuild or re-fetch the others. (Getting
  this right needed one more fix along the way: reading the store
  directly inside the layer-*creation* effect made oxlint correctly flag
  a stale-closure risk — if the catalog rebuilt the layer list on a
  render where visibility had already changed elsewhere, the new layers
  would carry whatever `hidden` looked like when the effect last ran,
  not the current value. Fixed with a ref kept in sync every render,
  read (not depended on) inside the effect — current value, no
  rebuild-on-every-toggle.)

**Verified in a real browser**, driving the actual dev server: the map
route now measures full viewport width with zero docked sidebar
present; clicking the header's menu button opens the nav as a Sheet
overlay *on top of* the map (dimmed backdrop, map untouched
underneath), with the same four nav items as before; the Layers button
opens a real dropdown, unchecking "pole" removes exactly the pole
points from the rendered map and nothing else, and re-opening the panel
shows the unchecked state persisted; navigating to `/features` (or any
other secondary route) confirmed the docked sidebar is exactly as
before there — this fix is scoped to the map route only, on purpose.

## A third round: display names, a status bar, and a corrected layers position

Three more things came out of the same live-testing pass: the layers
panel showed raw table ids (`pole`, `mv_conductor`) instead of the
admin-set display name; the Layers button made more sense top-right,
mirroring where map tools conventionally live, not stacked against the
health badge on the left; and the map needed a real status bar --
live mouse coordinates, the working projection, basemap attribution,
and a select/pan mode toggle with its own cursor feedback.

**Display names required a real backend gap to close, not a frontend
trick.** Martin's catalog only ever knows a Postgres table name --
lowercased, with underscores where the code has them (see
`api/internal/compiler/compiler.go`: `tableName :=
strings.ToLower(fc.Code)`). It has no idea `metadata.feature_classes`
even has a `name` column. Faking a display name client-side (e.g.
title-casing `mv_conductor` into "Mv Conductor") would have been
guessing at something admin-configured and possibly wrong -- the whole
point of a metadata-driven system is that the *real* configured value
wins, not a plausible-looking derivation of it. So:

- **`GET /feature-classes`** (new, `api/cmd/server/main.go`) — a small,
  read-only endpoint listing every active feature class's `code`,
  `name`, `geometry_type`, and `network_role`. Backed by
  `metadata.ListFeatureClasses` (new in `api/internal/metadata`).
  Deliberately its own response type (`featureClassResponse`), not
  `metadata.FeatureClass` serialized directly -- that type has no json
  tags and carries a couple of internal-only fields with no reason to
  leave the API.
- **`src/hooks/use-feature-classes.ts`** fetches it through Query; the
  layers panel matches each Martin source id against a feature class's
  lowercased `code` to render `MV Conductor` instead of `mv_conductor`.

**The status bar** (`src/components/map/status-bar.tsx`) replaces
OpenLayers' default `Attribution` control (disabled explicitly in
`voltedge-map.tsx` via `controls: defaultControls({ attribution:
false })`) with the same required OSM attribution text, rendered as a
link to `openstreetmap.org/copyright`, alongside:
- **Live mouse coordinates** in EPSG:4326 (lat/lon), via the map's own
  `pointermove` event reprojected with `ol/proj`'s `toLonLat` --
  written to a new `map-pointer-store.ts` (Zustand, not persisted --
  this changes on essentially every mouse move, the opposite of
  something worth remembering between sessions) so the map canvas
  (writer) and the status bar (reader), which are siblings, don't need
  a shared parent to pass this through.
- **The map's working projection**, `EPSG:3857` -- a label for now
  (OpenLayers' default view projection, not yet configurable), not
  derived from anything live.
- **A select/pan mode toggle** (`interaction-store.ts`, also not
  persisted -- which mode you're in is a moment-to-moment choice, not a
  preference). Pan mode sets the map container's cursor to `grab`;
  select mode leaves it default. This is cursor feedback only in this
  slice -- there's no selection behavior to actually gate on the mode
  yet, since feature selection itself doesn't exist until the next
  slice. Wiring the toggle now means the next slice only has to make
  `select` mode *do* something, not invent the toggle too.

## A fourth round: click-to-select, a real attribute panel, and cutting a mode toggle that added nothing

Live testing of the third round's select/pan mode toggle surfaced a
direct correction: *"i dont see the difference between select mode and
pan mode. remove the pan mode. i dont think its necessary."* Correct —
pan mode only ever changed the cursor to a grab hand; there was no
actual behavior difference between the two modes because selection
itself didn't exist yet. Rather than keep a toggle that gates nothing,
this round removes it outright and builds the thing it was a stand-in
for.

- **`interaction-store.ts` is gone.** The status bar no longer has a
  mode toggle. Instead, the map itself now shows a pointer cursor
  whenever the mouse is over a selectable feature (via
  `map.hasFeatureAtPixel` on every `pointermove`, restricted to vector
  layers) and the default cursor everywhere else — the same affordance
  the toggle was trying to fake, but driven by what's actually under
  the cursor instead of a mode the user has to remember to switch.
- **The floating "API healthy" badge moved into the status bar**
  (`ApiStatusIndicator` in `status-bar.tsx`, reusing the existing
  `useHealth` hook) — one less thing floating over the map now that the
  status bar exists to hold exactly this kind of reference info.
  `src/routes/map.tsx` no longer renders it as a separate overlay.
- **Click-to-select** (`voltedge-map.tsx`) — a `click` listener hit-tests
  every vector layer at the clicked pixel via
  `map.forEachFeatureAtPixel`, resolves the hit feature's layer back to
  a feature class code (each `VectorTileLayer` carries its Martin
  source id as an OL layer property, set when the layer is created; the
  source id lowercases a feature class's `code` the same way the
  compiler names the physical table, so matching is a straight lookup
  against `GET /feature-classes`, not a guess), and writes
  `{featureClassCode, featureId}` to a new `selection-store.ts`
  (Zustand, not persisted — a selected feature has no meaning after a
  reload). Clicking empty space clears the selection. Only the topmost
  hit is used — disambiguating stacked features is the peer-picker
  slice, still open below.
- **A real attribute panel** (`feature-panel.tsx`), docked between the
  map and the status bar, renders whatever `selection-store` currently
  holds. It fetches through `use-feature.ts` → `GET
  /features/{code}/{id}` — the same generic CRUD read path every other
  feature is fetched through — rather than reading whatever properties
  happened to ride along on the clicked MVT tile feature. That's a
  deliberate correctness choice, not just tidiness: vector tiles are
  cached client-side and by Martin, so tile properties can go stale the
  moment a feature is edited elsewhere, and this same panel is meant to
  host edit forms in a later slice, where stale data shown next to an
  edit form would be a real bug. The panel shows status, soft-delete
  state, subtype, every attribute (label derived from the column name —
  not a real per-field display label, since `metadata.fields` doesn't
  model one yet), and the last-updated timestamp. Renders nothing when
  nothing is selected, so it reserves no layout space until there's
  something to show. Fixed height, docked only — undocking into a
  floating/resizable/minimizable window is still its own later slice,
  described below.

**Verified against a real stack** — Postgres, a real Martin binary, and
the Go API all running together (the same in-sandbox setup as the
earlier rounds), driving the actual dev server with Playwright:
clicking a real `MV_CONDUCTOR` feature opened the panel showing its
actual display name ("MV Conductor"), real id, real status (`Draft`),
real attributes (`Condition rating: GOOD`, `Voltage class: 11KV`), and
a real updated timestamp — not placeholder data. The close button
cleared the selection and collapsed the panel. Clicking empty map space
(re-measuring the map's canvas bounds *after* the panel had already
shrunk it — an easy trap during testing, since the map area gets
shorter by the panel's own height the moment a feature is selected)
also correctly cleared the selection. The status bar shows "API
healthy" where the mode toggle used to be, and no pan-mode control
remains anywhere. Checked in both light and dark mode; no console
errors.

## A fifth round: a peer picker for stacked features

The round above deliberately took only the topmost hit when a click
landed on several overlapping features, leaving disambiguation as an
open follow-on. This round closes it.

- **`voltedge-map.tsx`'s click handler now collects every hit, not just
  the first.** `map.forEachFeatureAtPixel`'s callback stops iterating
  as soon as it returns something truthy -- the previous slice relied
  on exactly that to take the topmost hit. Collecting every hit instead
  means the callback must always return `undefined`, and hits get
  pushed into a local array as a side effect. Deduped by `(code, id)`,
  since a line feature clipped across an MVT tile boundary can
  otherwise appear as two separate hits for what's really one feature.
- **Zero hits clears the selection, exactly one selects it directly
  (unchanged from the previous round), and more than one now populates
  a new `candidates` field on `selection-store.ts`** instead of
  guessing which one the user meant. `selected` and `candidates` are
  mutually exclusive in the store on purpose -- a stacked click doesn't
  select anything until the user actually picks one.
- **`peer-picker.tsx`** (new) renders a small floating list at the
  click's screen position (`evt.originalEvent.clientX/clientY`, not the
  map's own pixel space, which is relative to the map container --
  positioned with `fixed` so it's correct regardless of where in the
  DOM it's mounted) showing each candidate's real feature-class display
  name and a shortened id. Picking one calls `select`, which also
  clears `candidates` -- same store action a direct single-feature
  click already used, so `feature-panel.tsx` didn't need to change at
  all to pick this up. Dismisses on an outside click or Escape, since a
  click elsewhere in the UI (the Layers button, the header, ...) never
  reaches the map's own click handler to replace `candidates` the way a
  second map click naturally would.

**Verified against a genuinely stacked scenario, not a synthetic one.**
The existing seed data doesn't have two features at the same point, so
a real `POLE` was created via `POST /features/POLE` at the exact
coordinates of the existing seed `DIST_XFMR` (through the same generic
CRUD endpoint every other feature goes through), producing a real
same-point stack of two different feature classes to click on -- then
deleted again (soft delete + purge) once verification was done, so the
seed data this doc's other screenshots depend on is unchanged. Driving
the real stack (Postgres, a restarted Martin binary so its in-memory
tile cache picked up the newly created feature, the Go API, and the
Vite dev server) via Playwright: clicking the stacked point opened the
picker showing both real names ("Pole" and "Distribution
Transformer") with their real (shortened) ids; picking one closed the
picker and opened the attribute panel with that feature's real data;
reopening the picker and clicking the Layers button, or pressing
Escape, both dismissed it without selecting anything. Checked in both
light and dark mode; no console errors.

One thing worth calling out about testing this kind of overlay: the
picker and the attribute panel both depend on screen coordinates that
shift the moment the panel opens and shrinks the map -- a stale
`getBoundingClientRect()` captured before a selection is exactly the
kind of thing that silently makes a real bug and a test artifact look
identical. Re-measuring after every state change that resizes the map
was the fix, both here and in the previous round's click-to-clear
testing.

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
  overlay badge on the map itself (`src/routes/map.tsx`), alongside the
  real layers panel described below (superseding an initial read-only
  layer-count badge).
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
- **Click-to-select, a docked attribute panel, and a peer picker for
  stacked features are all done.** Still open, scoped out with the user
  as follow-on slices:
  1. Undocking: the panel becomes a floating window over the map --
     movable, resizable, minimizable, closeable. Orthogonal to the peer
     picker -- neither depends on the other.
  2. The same panel later hosts a form, for drawing a new feature or
     editing an existing one -- deliberately not attempted until the
     selection/panel mechanics above exist to build on.
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
- A "Layers" button top-right — no docked sidebar; the map fills the
  full width below the header.
- Click "Layers" to see one checkbox per compiled feature class, named
  with its real display name (e.g. "MV Conductor", not `mv_conductor`);
  unchecking one hides exactly that layer on the map, live.
- Click the header's menu icon (top-left) to open navigation
  (`Map`/`Feature classes`/`Connectivity`/`Relationships`/`Settings`)
  as a drawer that slides over the map, not a column that pushes it
  aside. Navigating to any other route shows the normal docked sidebar
  there instead — this floating behavior is specific to the map route.
- Hover over a rendered feature and the cursor becomes a pointer; click
  it and a panel docks along the bottom of the map showing its real
  display name, id, status, subtype, every attribute, and when it was
  last updated. Click the panel's close button, or click empty map
  space, to clear the selection and collapse the panel.
- If a click lands on several stacked features at once, a small popup
  appears at the click point instead, listing each one's real display
  name for you to pick from -- picking one opens the same attribute
  panel as a direct single-feature click would. Click elsewhere in the
  UI, or press Escape, to dismiss the popup without selecting anything.
- A thin status bar along the bottom: "API healthy"/"API unreachable"
  on the left; move your mouse over the map and watch the lat/lon
  readout update live; `EPSG:3857` next to it; OSM's attribution,
  linked, on the right.
- Scroll to zoom, drag to pan, double-click to zoom in a step — all
  standard OpenLayers interactions, no custom code required for them to
  work.

`npm run build && npm run lint` should both finish clean (the build's
chunk-size warning about `ol` is expected, see "What's still open").
