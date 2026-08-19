# Phase 3 (follow-on) — symbology icons move into the database

**Goal:** take the first real step of the "real sprite pipeline" open
item (`docs/open-items.md`, `docs/phase-3-symbology.md`'s "What's still
open") by moving the SVG markup behind every `icon_ref` out of a
hardcoded frontend registry and into a `metadata.symbology_icons` table
served over the API -- so an icon can be added or changed as *data*,
without a frontend deploy. **Status:** code complete, verification
owed. This work was done in a shell-less sandbox where `bash.exe`
`EPERM`s on every invocation, so `tsc`/lint/`go build`/`gofmt` could
not be run -- statically reviewed only, flagged here rather than
claimed clean (see "Verification owed").

## Why this slice exists

`docs/phase-3-symbology.md` shipped symbology resolution with a
deliberate placeholder: the five hand-authored SVGs (circle, square,
triangle, triangle-outline, diamond) lived inline in
`web/src/lib/symbology-icons.ts`, and `docs/open-items.md`'s "Real
sprite pipeline" row named that hardcoded registry as the thing to
replace. The user asked whether real SVG artwork could replace the
generic shapes, and where that markup should live -- and chose a **DB
icon table now** (over inline code or a deferred asset pipeline), with
coloring that must **support both** single-color tintable symbols *and*
full-color artwork.

This is a self-contained swap of *where an `icon_ref` name resolves to
markup*, not a change to how rules are authored. A symbology rule still
references an icon by name via `style_def.icon_ref` (`migrations/0012`);
this only changes whether that name resolves against a JS object or a DB
row -- exactly the "a rule is metadata; the pictogram is an asset" split
`docs/metadata-core.md` already draws.

## What was built

### Database

- **`migrations/0023_create_symbology_icons.sql`** (new) --
  `metadata.symbology_icons (icon_ref text PRIMARY KEY, svg text NOT
  NULL, created_at, updated_at)`. Column comments spell out the
  `{{COLOR}}` tinting contract and the mandatory explicit
  `width`/`height` requirement (see "The two contracts an icon row must
  honor").
- **`seed/symbology_icons.sql`** (new) -- seeds the same five shapes
  that used to live only in the frontend, so the existing seeded rules
  (`seed/symbology_example.sql`) keep resolving to the *exact same*
  markup, now from the DB. Lives under `/seed`, not `/migrations`, on
  purpose (`migrations/apply.sh` only scans `/migrations`). Idempotent
  (`ON CONFLICT (icon_ref) DO NOTHING`), so re-applying never clobbers
  an icon edited in place.

### Backend

- **`api/internal/metadata/models.go`** -- `SymbologyIcon` bun model
  (`table:metadata.symbology_icons`, `icon_ref` pk, `svg`).
- **`api/internal/metadata/load.go`** -- `ListSymbologyIcons` returns
  every row ordered by `icon_ref`. The whole set at once, not per-icon:
  it's small and global, and a rule can reference any icon.
- **`api/cmd/server/main.go`** -- `GET /symbology/icons` returns
  `{icons: [{icon_ref, svg}, ...]}` via a wire-shaped `symbologyIcon`
  type (matching how this file keeps wire copies of everything else).
  No per-class scoping (unlike the per-feature-class schema endpoint) --
  the icon set is global. Martin never sees it, same as
  `symbology_rules`.

### Frontend

- **`web/src/lib/symbology-icons.ts`** -- the hardcoded set is renamed
  `FALLBACK_ICONS` and demoted to a *fallback* only. `iconDataUri` gains
  an optional `icons?: IconRegistry` argument and resolves markup as
  `icons?.[ref] ?? FALLBACK_ICONS[ref] ?? FALLBACK_ICONS[default]` --
  so a not-yet-loaded fetch or a typo'd/missing ref still renders a real
  shape, never an invisible feature. Exports `type IconRegistry`
  (`icon_ref -> svg`) and `FALLBACK_ICON_REFS`.
- **`web/src/hooks/use-symbology-icons.ts`** (new) -- `useSymbologyIcons()`
  fetches `GET /symbology/icons` once (long `staleTime`, one global
  fetch like `useAllSymbology`, memoized on the raw payload so the map's
  layer-creation effect only rebuilds when the icon set actually
  changes). `useKnownIconRefs()` unions the DB's refs with
  `FALLBACK_ICON_REFS` for admin pickers, so the fallback shapes stay
  selectable before any DB icon is authored.
- **`web/src/lib/map-layers.ts`** -- `iconStyleFor` and
  `createSymbologyStyle` thread the `icons` registry through.
  `iconStyleFor` now keys its style cache on the resolved *data URI*
  (which encodes the actual markup) plus scale, not the bare `icon_ref`
  -- so a layer built with the fallback before the DB fetch landed
  doesn't get a stale cached style once the real markup arrives (same
  ref, different URI, different key).
- **Consumers threaded**: `voltedge-map.tsx` (map layers),
  `layers-panel.tsx` (legend swatches), `diagram-canvas.tsx` (diagram
  point icons) each call `useSymbologyIcons()` and pass the registry
  into `createSymbologyStyle`/`iconDataUri`.
  `admin-feature-class-new.tsx` switches its icon picker from the
  now-removed `KNOWN_ICON_REFS` constant to the `useKnownIconRefs()`
  hook.

## The two contracts an icon row must honor

1. **Tinting is opt-in via a `{{COLOR}}` placeholder.** An icon whose
   markup contains the literal `{{COLOR}}` is a single-color, tintable
   symbol -- `iconDataUri` substitutes a rule's resolved color in
   directly (not OL's `Icon` `color` canvas trick, which wouldn't work
   for the legend's plain `<img>`). An icon with *no* placeholder
   renders as-is: the substitution is a harmless no-op, so full-color
   artwork opts out of tinting through the same single code path. This
   is exactly how "support both" cost no new branching -- presence or
   absence of `{{COLOR}}` *is* the switch.

2. **Every SVG must carry explicit `width`/`height`, not just a
   `viewBox`.** Chromium resolves a missing intrinsic size to 150×150
   for a detached `Image()`, which silently broke vector-tile icon
   rendering the one time it shipped without them
   (`docs/phase-3-symbology.md`'s bug #1). The migration's column
   comment states this so a future DB-authored icon inherits the
   requirement.

## What this does and doesn't close

This advances the "Real sprite pipeline" open item but does not finish
it. What now exists: icon markup is *data*, addable/editable per row
via SQL (or a future admin editor) without a frontend deploy, and the
render path already supports full-color artwork alongside tintable
shapes. What's still open:

- **Real utility artwork.** The seeded set is still the same five
  generic shapes; authoring an actual IEC/ANSI symbol library is a
  large, ongoing *content* task, now unblocked but not done.
- ~~**An admin UI to author/edit icons.** Rows are added via SQL today;
  no in-app editor exists yet.~~ **Done** -- a **Symbology icons** admin
  page (full CRUD with a live preview) plus a rule editor now exist; see
  `docs/phase-3-symbology-admin-ui.md`. SQL stays a valid path.
- **A sprite atlas / true asset-serving concern.** At this scale one
  `<svg>` per row over a single JSON fetch is fine; a genuine library
  might want atlasing or a CDN, deliberately not answered here.

## Verification owed

`bash.exe` `EPERM`s on every call in this sandbox, so none of the
usual gates ran. Before this can be called clean:

- `npx tsc --noEmit` and `npm run lint` (frontend) -- the icon
  threading touches five frontend files and renamed an exported
  constant (`KNOWN_ICON_REFS` -> removed; `admin-feature-class-new.tsx`
  moved to `useKnownIconRefs()`).
- `go build ./... && go vet ./... && gofmt -l .` (backend) -- new
  model, loader, handler, and route.
- Apply `migrations/0023` and `seed/symbology_icons.sql` against a real
  Postgres, then confirm `GET /symbology/icons` returns the five rows
  and the map/legend/diagram still render every icon identically to
  before (the fallback and the DB set are byte-identical by design, so
  a visual diff should be empty). Then add one full-color (no
  `{{COLOR}}`) row and confirm it renders as-is, tint ignored.
