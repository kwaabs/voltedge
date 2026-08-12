# Phase 3 (slice 7) — draw a new feature, fill a metadata-driven form, save it

**Goal:** let a user place a new point feature on the map and save it
through the real, generic CRUD API (`api/internal/crud`, built in Phase
2) rather than the map staying read-only. Point geometry only this
slice — line/polygon drawing and editing an existing feature's geometry
are both future work, see "What's still open."
**Status:** done and verified against a real Postgres/Martin/API/Vite
stack — a real feature created through the UI, confirmed in Postgres,
in Martin's own tile bytes, and visually on the map after saving, in
both themes. See "How this was validated" for a real bug this
verification caught (the map didn't visually update after a save) and
"Try it yourself" below.

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
  listing every *point* feature class (line/polygon excluded, since
  drawing them isn't built yet), top-right next to the Layers button.
  Picking one calls `startDrawing`; while a draw is active, the button
  is replaced by a small banner ("Click the map to place the new
  feature") with its own Cancel control.
- **`src/components/map/voltedge-map.tsx`** gained:
  - An unsaved-draft marker layer (a dashed emerald circle, see
    `draftPointStyle` in `src/lib/map-layers.ts` — deliberately a
    different color from every committed feature style, so a draft
    never reads as already-real data), created once alongside the map
    and cleared whenever `draft` is cleared.
  - A `Draw` interaction (point-only), added/removed based on
    `drawingFeatureClassCode`. On `drawend`, the drawn coordinate is
    converted from the view's EPSG:3857 to EPSG:4326 and handed to
    `finishDrawing` as GeoJSON — the exact shape `POST /features/{code}`
    expects.
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

## Decisions made

- **Point geometry only, this slice.** `Draw`'s `type: "Point"` is
  hardcoded in `voltedge-map.tsx`; the "New feature" menu only lists
  point feature classes for exactly this reason. Line/polygon drawing
  needs its own interaction handling (a draw session that isn't a
  single click) and isn't attempted alongside getting the schema
  endpoint, the form, and the save/refresh plumbing right for the
  simple case first.
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
- **Line/polygon drawing** -- see "Decisions made."
- **Editing an existing feature's geometry** (move a point, reshape a
  line) -- depends on an edit flow existing first.
- **Snapping** -- depends on both of the above; still open from
  `docs/phase-3-map-loop.md`.
- **Floating/resizable/minimizable panel** -- pinned by the user,
  unrelated to this slice.
- **Metadata-driven symbology** -- still open from
  `docs/phase-3-map-loop.md`; the draft marker above is a deliberate
  placeholder, not an attempt at it.

## Try it yourself

Same stack as `docs/phase-3-map-loop.md`'s "Try it yourself" --
`task infra-up && task migrate && task api-dev`, then `cd web && npm
run dev`. Open `http://localhost:5173`:

- Click "New feature" (top-right, next to Layers) and pick a point
  feature class. The button is replaced by a banner telling you to
  click the map; press Escape any time before clicking to back out.
- Click anywhere on the map. A dashed emerald marker appears there, and
  a form opens in the bottom panel: a subtype picker (if the feature
  class has subtypes) and one control per field, required ones marked
  with a red asterisk once you've picked a subtype.
- Fill in the required fields and click Save. The panel switches to
  showing the real, newly-created feature's attributes, and the point
  now renders on the map with the same styling as every other feature
  of its class -- no reload needed.
- Click Cancel (or the panel's close button) instead, at any point
  before saving, to discard the draft -- the marker disappears and
  nothing is written.

`npm run build && npm run lint` and `go build ./... && go vet ./... &&
gofmt -l .` should all finish clean.
