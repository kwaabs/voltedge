# Admin-managed basemaps

Turns the geographic map's basemap list from a hardcoded frontend constant into
admin-managed data: an operator adds/edits/reorders/disables basemaps from the
Map settings admin page with no rebuild, and the map reads the list from the
server at runtime.

Builds directly on the Phase 39 pattern (docs/phase-39-map-settings.md): the
default map view became admin config in `iam.system_settings`; basemaps become
admin config in a new `iam.basemaps` table, curated through a CRUD API + a
section on the same `/admin/map-settings` page.

## Two parts

**Part 1 -- more built-in basemaps (shipped first, in code).** Before the
server work, the hardcoded set grew from OSM + Carto Voyager + Blank to also
include Carto Positron (light), Carto Dark Matter, and Satellite (Esri World
Imagery). These are the five basemaps the migration then seeds, so the map is
unchanged the instant the server starts reading the table.

**Part 2 -- admin-managed (this doc).** The server-backed catalog + API + admin
UI, with the map refactored to read the list.

## Data model -- `iam.basemaps` (migration 0077)

One raster XYZ tile source per row:

- `code` (unique) -- the stable client id the browser persists as the user's
  chosen basemap and looks a row up by; survives label edits. Lowercase kebab.
- `label`, `url_template` -- the tile URL with `{x}/{y}/{z}` placeholders in
  whatever order the provider uses (Esri is `{z}/{y}/{x}`, cartocdn/OSM
  `{z}/{x}/{y}`); OpenLayers' XYZ source substitutes whichever are present.
- `attribution` (jsonb `[{label,href}]`) -- the required credit line(s) the
  map's status bar renders (OL's own attribution control is disabled).
- `max_zoom` (nullable -- no cap), `min_zoom`, `tms` (flipped-Y scheme).
- `sort_order`, `is_enabled`, `is_builtin`.

Two deliberate non-rows / protections:

- **Blank is NOT a row.** The no-basemap choice has no tile source; the client
  always appends it as a built-in picker option (`BLANK_BASEMAP` in
  `basemap-store.ts`) and hides the base layer when it's selected. The code
  `"blank"` is reserved (the API rejects it).
- **Built-ins can't be deleted.** The five seeded rows carry `is_builtin=true`;
  the API refuses to DELETE them (an admin disables/edits/reorders them
  instead) so the shipped set can't be lost by accident. A custom basemap an
  admin adds has `is_builtin=false` and deletes freely.

## Server -- `api/internal/basemap`

Mirrors `api/internal/circuit` in shape (InvalidError→400 split, bun model,
chi Routes). `models.go` (Basemap + AttributionLink), `basemap.go`
(List/Get/Create/Update/Delete + validation), `http.go` (Routes + handlers).

- `GET /basemaps` -- enabled rows in sort order (the map); `?all=true` returns
  disabled rows too (the admin table).
- `POST /basemaps` -- add a custom basemap (always `is_builtin=false`).
- `PATCH /basemaps/{id}` -- every field optional; `code` immutable;
  `clear_max_zoom` removes the cap. Built-ins are editable here.
- `DELETE /basemaps/{id}` -- custom only; built-ins 400.

Validation: code shape (lowercase/digits/hyphens, not "blank"), non-empty
label, `url_template` an http(s) URL carrying `{z}/{x}/{y}`, zoom bounds
0..24 with `min_zoom <= max_zoom`, attribution blank-label rows dropped.

Mounted in `api/cmd/server/main.go` with the same method-aware gate as
map-settings: `RequirePermission(features:read, metadata:write)` -- GET needs
features:read (every map viewer builds its picker) while the mutating verbs
need metadata:write (admin).

## Client

- `hooks/use-basemaps.ts` -- `useBasemaps()` (enabled list, for the map, 5-min
  staleTime), `useAllBasemaps()` (`?all=true`, for the admin table),
  create/update/delete mutations invalidating `["basemaps"]`.
- `stores/basemap-store.ts` -- gutted to just the persisted chosen `code`
  (string) + `BLANK_BASEMAP`; the catalog now lives on the server, so the old
  `BASEMAP_OPTIONS`/`basemapOption`/`BasemapId` are gone.
- `lib/basemaps.ts` -- `resolveBasemap(code, list)` (code→record; unknown code
  falls back to the first enabled basemap so a stale localStorage value never
  leaves the map empty), `createBasemapSource(record|null)` (generic XYZ from
  the record; `{y}`→`{-y}` for TMS), `applyBasemap(layer, record|null)`.
- `components/map/voltedge-map.tsx` -- base layer created empty/hidden; a
  `[basemap, basemaps]` effect applies the resolved source once the catalog
  loads and on every change (waits while `basemaps` is undefined rather than
  flashing blank).
- `components/map/status-bar.tsx` -- picker + attribution read the fetched
  list; Blank appended as a client built-in.
- `components/admin/basemaps-manager.tsx` -- the CRUD section (list with
  built-in/disabled badges + quick enable-toggle, create/edit FormDialog with
  a repeatable attribution editor, delete via ConfirmDeleteDialog), rendered on
  `routes/admin-map-settings.tsx` below the default-view form.

## Degradation

If `/basemaps` errors or the table is empty (e.g. migration not yet applied),
`basemaps` stays undefined/empty and the base layer stays hidden (blank map);
the status bar and every other control keep working. Applying migration 0077
(which seeds the five built-ins) restores the map to OSM-by-default.
