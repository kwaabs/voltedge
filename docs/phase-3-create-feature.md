# Phase 3 (slice 7) — draw a new feature, fill a metadata-driven form, save it

**Goal:** let a user place a new feature — point, line, or polygon — on
the map and save it through the real, generic CRUD API
(`api/internal/crud`, built in Phase 2) rather than the map staying
read-only. Editing an existing feature's attributes or geometry is
future work, see "What's still open."
**Status:** done and verified against a real Postgres/Martin/API/Vite
stack, all three geometry types — a real feature of each created
through the UI, confirmed in Postgres, in Martin's own tile bytes, and
visually on the map after saving, in both themes. See "How this was
validated" for a real bug this verification caught (the map didn't
visually update after a save) and "Try it yourself" below.

## Why this slice exists

Every previous Phase 3 slice was read-only: click a feature, see its
attributes, terminals, relationships. The backend has supported writes
since Phase 2 (`POST /features/{code}`, `PATCH /features/{code}/{id}`,
full validation against metadata), but nothing in the UI could reach
it. The user framed the next step directly: *"draw feature on map, fill
in the attribute form, then save to db?"* — and asked to scope it before
building, since the map has no idea yet what fields a feature class
even has.

## A backend gap closed first: the frontend didn't know a feature class's shape

`GET /feature-classes` (from an earlier slice) only ever returned
`code`/`name`/`geometry_type`/`network_role` — enough to label a layer,
nothing close to enough to render a form. Nothing exposed a feature
class's fields, their types, which are required for which subtype, or
coded domain values.

- **`GET /feature-classes/{code}/schema`** (new, `api/cmd/server/main.go`)
  — built on `metadata.Load`, the same loader `api/internal/crud`'s own
  validation already runs on every write. Returns subtypes (code +
  name) and one entry per field: `data_type`, `nullable`,
  `visible_on_create`/`visible_on_update`, `editable_after_publish`,
  the field's domain code and coded values if it has one, and
  `required_for_subtypes` — a flattened list of subtype codes that
  require this field, derived from `metadata.subtype_field_rules` the
  same way `api/internal/crud/validate.go`'s `validateRequired` does:
  a subtype rule with a `default_override` does *not* make the field
  client-required, since the rule is satisfied by the fallback value,
  not by demanding the client send one. Getting this derivation wrong
  would mean the form and the API disagreeing about what's actually
  required — checked directly against real POLE/DIST_XFMR metadata
  (`docs/phase-3-map-loop.md`'s seed data) via curl before any frontend
  code consumed it.
- **`src/hooks/use-feature-class-schema.ts`** fetches it through Query,
  same pattern as every other server-state hook in this app.

## What was built

- **`src/stores/draw-store.ts`** (new) — two distinct, mutually
  exclusive states, not one: `drawingFeatureClassCode` (waiting for a
  click on the map to place the geometry) and `draft` (geometry placed,
  waiting on the create form). `finishDrawing` moves straight from the
  first to the second.
- **`src/components/map/new-feature-menu.tsx`** (new) — a dropdown
  listing every feature class with real geometry (point, line, or
  polygon — `none` is excluded, since those never appear on the map at
  all), top-right next to the Layers button. Picking one calls
  `startDrawing`; while a draw is active, the button is replaced by a
  small banner with its own Cancel control, worded per geometry type
  ("Click the map to place the new feature" for a point; "Click to add
  points, double-click to finish" for a line or polygon).
- **`src/components/map/voltedge-map.tsx`** gained:
  - An unsaved-draft marker layer (a dashed emerald shape, geometry-type
    branching same as `styleForFeature` — see `draftStyleForFeature` in
    `src/lib/map-layers.ts` — deliberately a different color from every
    committed feature style, so a draft never reads as already-real
    data), created once alongside the map and cleared whenever `draft`
    is cleared.
  - A `Draw` interaction typed to match the picked feature class's
    geometry (`Point`/`LineString`/`Polygon`), added/removed based on
    `drawingFeatureClassCode`/`drawingGeometryType`. Line and polygon
    drawing (multiple clicks, finished with a double-click or Enter) is
    OL's own default `Draw` behavior — no extra interaction wiring
    needed beyond picking the right `type`. On `drawend`,
    `geometryToGeoJSON` converts whichever geometry class OL handed
    back (point/line/polygon each shape their coordinates differently)
    from the view's EPSG:3857 to the EPSG:4326 GeoJSON
    `POST /features/{code}` expects.
  - Escape cancels an in-progress draw; the click-to-select handler
    (from earlier slices) now no-ops while drawing or while a draft's
    form is open, so a click doesn't silently reselect something out
    from under an in-progress, unsaved form.
- **`src/components/map/feature-create-form.tsx`** (new) — renders in
  place of `feature-panel.tsx`'s tabs while a draft exists (a feature
  with no id yet has no terminals or relationships to show). A subtype
  picker if the feature class has subtypes, then one control per
  `visible_on_create` field: a coded domain always renders as a
  `<select>` regardless of underlying `data_type`; otherwise the
  control follows `data_type` (checkbox for boolean, number input for
  integer/numeric, date/datetime-local for date/timestamp, text
  otherwise) — the same type contract
  `api/internal/crud/validate.go`'s `validateValue` enforces
  server-side, so a value accepted by the form should never bounce off
  a 400 for its type alone. Save is disabled until every field
  `required_for_subtypes` marks for the chosen subtype has a value.
  On save: `POST /features/{code}`, then the real created feature is
  selected (`feature-panel.tsx` switches back to its normal tabs
  automatically, since `draft` clearing and `selected` being set are
  what that decision already keys on) and the map is told to refresh
  (below).
- **`src/stores/map-refresh-store.ts`** (new) — a bare monotonic
  counter. `feature-create-form.tsx` bumps it after a successful save;
  `voltedge-map.tsx` reacts. Deliberately not folded into refetching the
  catalog query — the catalog (which sources exist) doesn't change when
  a feature is added, so nothing there would otherwise signal that
  tiles themselves are now stale.

## How this was validated -- and a real bug this caught

Driven against a real stack (Postgres, a real Martin binary, the Go
API, the Vite dev server) via Playwright, not assumed from the code:
draw a new `POLE`, fill every required field from its real domains, hit
Save, and confirm the created feature (real id, real attributes, `POST
201`) shows up **on the map**, not just in the panel.

The first pass failed exactly there: the panel correctly showed the new
feature's real id and attributes (proving the create request itself
worked), and Postgres and Martin's own raw tile bytes (fetched directly
with curl) both had the new point -- but the map kept rendering the old,
empty tile until the page was fully reloaded. Two real, independent
causes, not one:

1. **`VectorTileSource.refresh()` doesn't clear OL's own tile cache.**
   Reading OL's source (`node_modules/ol/source/Tile.js`,
   `Source.js`): `refresh()` calls `clear()` then fires a `'change'`
   event -- but `VectorTile` never overrides `clear()` (unlike a raster
   `TileImage` source, which does), so the renderer's already-loaded
   tile for that coordinate is never actually discarded; `'change'`
   alone doesn't force a refetch of a tile already marked loaded. The
   fix: `setUrl()` with a cache-busting `?v=<version>` query param
   (`tileUrlTemplate`'s new `version` argument) instead of `refresh()`
   -- changing the URL changes the source's internal key
   (`UrlTile.setUrls` → `setKey`), which *is* what the renderer actually
   checks to decide whether to discard cached tiles.
2. **Martin's own in-memory tile cache doesn't key on query
   parameters.** Even after the OL-side fix, tiles for an area already
   requested earlier in the same Martin process lifetime kept coming
   back empty -- confirmed by watching the actual network requests:
   OL correctly issued a fresh request with a new `?v=` value, but
   Martin still returned the stale cached bytes for that
   `(source, z, x, y)` regardless of the query string, the same
   caching behavior already documented as a testing gotcha in
   `docs/phase-3-map-loop.md`'s peer-picker round (there, the fix was
   restarting Martin between test runs). For a system where features
   are actively being created, that's not just a testing
   inconvenience -- it's a real deployment consideration. Martin
   exposes `--cache-size <MB>`; passing `--cache-size 0` disables its
   cache outright (confirmed via its own startup log: `Caching is
   disabled`), which is what made the fix visible end-to-end. Whether
   to actually run with caching disabled in a real deployment is a
   product decision outside this slice's scope (a large territory with
   infrequent edits might reasonably prefer Martin's cache and accept
   some create-to-visible lag) -- called out here so it isn't
   rediscovered by surprise later.

With both addressed, the new point appeared on the map immediately
after save, in both light and dark mode, with real attributes visible
in the panel. Also verified: Escape cancels an in-progress draw before
any click; the form's own Cancel button (and the panel's close button)
both discard the draft, clear the marker, and leave nothing selected;
no console errors throughout.

## A second round: line and polygon drawing, and a real polygon feature class

The first pass deliberately shipped point-only, on the theory that
getting the schema endpoint, the form, and the save/refresh plumbing
right for the simple case first was worth doing before tackling a
multi-click draw session too. The user asked for the rest directly
("i can draw point feature and fill its form. but can draw line
feature. i need a polygon feature seeded also so we can test all
geometries") — and pointed at a real gap: nothing in this repo had ever
defined a polygon feature class, so there was no way to test polygon
drawing even once the code existed.

- **`seed/substation_yard_example.sql`** (new) — `SUBSTATION_YARD`, a
  polygon feature class (a substation site boundary), following the
  exact same seed pattern as `seed/pole_example.sql`: two subtypes
  (`distribution_substation`/`transmission_substation`), an
  `enclosure_type` coded field, and a reused `CONDITION_RATING` domain.
  `network_role: 'none'` -- a yard boundary doesn't participate in
  electrical connectivity the way `DIST_XFMR` does. `task
  seed-substation-yard` (new Taskfile target) loads it; `task compile
  CODE=SUBSTATION_YARD` compiles it, same two-step flow as every other
  feature class in this repo.
- **`voltedge-map.tsx`'s Draw interaction is now geometry-type-aware**
  instead of hardcoding `type: "Point"` -- see "What was built" above
  for the mechanics. The key realization: almost nothing else needed to
  change. `feature-create-form.tsx` already forwarded `draft.geometry`
  to `POST /features/{code}` without inspecting its shape, so a
  differently-shaped GeoJSON payload (a `LineString`'s array of
  coordinate pairs, a `Polygon`'s array of rings) needed zero form
  changes -- the geometry-agnostic design of the first round paid off
  immediately.

**Verified against a real stack**, all three geometry types in one
pass: a real `MV_CONDUCTOR` line (three clicks, double-click to finish,
real voltage class and condition rating filled from real domains) and a
real `SUBSTATION_YARD` polygon (four clicks, double-click to finish,
real enclosure type) were each drawn, saved, and confirmed rendering on
the map with the correct shape and the panel's real attributes --
alongside re-confirming the existing `POLE` point flow still works
unchanged. The dashed emerald draft styling correctly rendered for all
three geometry types while a form was open, in both light and dark
mode. Click-to-select on the newly created polygon (a feature class
that didn't exist in this repo before this round) worked with no
special-casing, confirming the click-to-select code from earlier
slices was already properly geometry-agnostic. All test instances were
soft-deleted and purged afterward, leaving `SUBSTATION_YARD`'s table
compiled and empty, ready for real use, and the seed `POLE`/
`MV_CONDUCTOR`/`DIST_XFMR` data unchanged.

## Decisions made

- **The schema endpoint is deliberately heavier than
  `GET /feature-classes`, and fetched separately.** Every field, every
  domain's coded values -- too much to fetch for every layer/panel
  render, so it's its own endpoint, only queried when a form is about
  to actually be shown (`useFeatureClassSchema` is `enabled` only when
  a draft or an edit target exists).
- **`required_for_subtypes` is flattened server-side, not shipped as
  raw `subtype_field_rules` rows.** The form only ever needs "is this
  required for the subtype the user picked" -- making the client
  cross-reference three tables to answer that itself would just be
  re-implementing `validateRequired`'s logic a second time, with a
  second chance to get the `default_override` exception wrong.
- **A visually distinct draft marker, not a preview of the final
  symbology.** Metadata-driven symbology resolution doesn't exist yet
  (`docs/phase-3-map-loop.md`'s "What's still open"); a plain,
  deliberately-different emerald dashed circle says "unsaved" without
  pretending to be a preview of something not built yet.
- **`--cache-size 0` is documented, not silently adopted as the
  project's default.** Disabling Martin's cache made this slice's bug
  reproducible and fixable, but whether a real deployment wants that
  tradeoff depends on territory size and edit frequency -- a decision
  for whoever configures the real environment, not baked into
  `docker-compose.yml` here.

## What's still open

- **Editing an existing feature's attributes** through the same panel
  -- this slice only covers creating a brand new feature. The
  create form's field-rendering code (`feature-create-form.tsx`) is
  reusable for this; what's missing is switching `AttributesTab` into
  an edit mode and wiring `PATCH /features/{code}/{id}`.
- **Editing an existing feature's geometry** (move a point, reshape a
  line or polygon) -- depends on an edit flow existing first.
- **Snapping** -- depends on both of the above; still open from
  `docs/phase-3-map-loop.md`.
- **Floating/resizable/minimizable panel** -- pinned by the user,
  unrelated to this slice.
- **Metadata-driven symbology** -- still open from
  `docs/phase-3-map-loop.md`; the draft marker above is a deliberate
  placeholder, not an attempt at it.

## Try it yourself

Same stack as `docs/phase-3-map-loop.md`'s "Try it yourself" --
`task infra-up && task migrate && task api-dev`. To also have a polygon
feature class to draw (not required for point/line, which `seed-pole`/
`seed-connectivity` already cover): `task seed-substation-yard && task
compile CODE=SUBSTATION_YARD`. Then `cd web && npm run dev`. Open
`http://localhost:5173`:

- Click "New feature" (top-right, next to Layers) and pick a feature
  class. The button is replaced by a banner telling you what to do next
  (a single click for a point; click to add points, double-click to
  finish for a line or polygon); press Escape any time before finishing
  to back out.
- Draw the geometry. A dashed emerald shape appears where you drew it,
  and a form opens in the bottom panel: a subtype picker (if the
  feature class has subtypes) and one control per field, required ones
  marked with a red asterisk once you've picked a subtype.
- Fill in the required fields and click Save. The panel switches to
  showing the real, newly-created feature's attributes, and the feature
  now renders on the map with the same styling as every other feature
  of its class -- no reload needed.
- Click Cancel (or the panel's close button) instead, at any point
  before saving, to discard the draft -- the shape disappears and
  nothing is written.

`npm run build && npm run lint` and `go build ./... && go vet ./... &&
gofmt -l .` should all finish clean.
