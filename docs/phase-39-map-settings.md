# Phase 39 — Admin-configurable default map view

## Problem

The geographic map opened on a hardcoded view: `voltedge-map.tsx`'s
`DEFAULT_CENTER = fromLonLat([-0.127, 51.507])` and `DEFAULT_ZOOM = 15` — a
London placeholder centered on the seed data, never a real service territory.
An admin had no way to point a deployment at its own network without a code
change and rebuild. This phase makes the default view runtime configuration.

## Design

A default view is three scalars — center longitude, center latitude, zoom —
so it reuses **`iam.system_settings`**, the one global key/value store every
runtime toggle already lives in (`local_auth_enabled` 0024,
`territory_enforcement_enabled` 0029). Three keys, not one JSON blob: they
mirror the client's three primitives and each is parsed independently with a
fallback, so a missing or corrupt row degrades to the placeholder rather than
breaking map startup.

- `map_default_center_lng` (seed `-0.127`)
- `map_default_center_lat` (seed `51.507`)
- `map_default_zoom` (seed `15`)

The seed keeps the exact placeholder the client shipped, so a fresh install
behaves identically until an admin changes it.

### Backend — `api/internal/mapsettings`

`Routes(db)` mounted at `/map-settings`:

- `GET /` — reads the three keys, returns `{center_lng, center_lat, zoom}` as
  floats. Called by **every** map session at startup.
- `PUT /` — validates (`lng ∈ [-180,180]`, `lat ∈ [-90,90]`, `zoom ∈ [0,22]`,
  matching OpenLayers' `maxZoom: 22`), upserts all three keys in one insert
  (`ON CONFLICT (key) DO UPDATE`), stamps `updated_by` from `auth.ActorID`.

**Method-aware RBAC gate** at the mount:
`RequirePermission(PermFeaturesRead, PermMetadataWrite)` — GET needs only
`features:read` (so any map user gets the default), the PUT that changes it
needs `metadata:write` (admin config, same gate as `/admin`). The page lives
under `/admin/map-settings` while the API sits top-level, the same split as
external-systems and field-packs.

### Frontend

- `hooks/use-map-settings.ts` — `useMapSettings()` (Query, `["map-settings"]`,
  5-min staleTime) + `useUpdateMapSettings()` (mutation, invalidates the key).
- `routes/admin-map-settings.tsx` — a flat lon/lat/zoom form seeded from the
  loaded setting, client-side validation mirroring the server, `Save` via the
  mutation with a success toast.
- `app.tsx` — route `admin/map-settings` wrapped in
  `RequirePermission perm="metadata:write"` (pure-write page).
- `app-sidebar.tsx` — nav item `Map settings` (MapPin), `perm: "metadata:write"`.
- `voltedge-map.tsx` — new optional props `initialCenter?: [lon, lat]` /
  `initialZoom?`. Captured into **refs at mount** (the View is built once) so a
  later settings refetch never yanks a live map's viewport; absent props fall
  back to the `DEFAULT_CENTER`/`DEFAULT_ZOOM` placeholder (still the client's
  last-line default).
- `map.tsx` — fetches `useMapSettings()` and **holds the `<VoltEdgeMap>` mount
  until the query settles** (success or error) rather than mounting on the
  placeholder and re-centering — that would flash and could race an early user
  pan. On error it still mounts, falling back to the client default.

## Migration

`migrations/0066_seed_map_default_view.sql` — seeds the three keys with
`ON CONFLICT DO NOTHING` (idempotent, preserves an operator's chosen value).

## Verification owed (Bash blocked in this environment)

- API: `go build ./...` / `go vet ./...` (new `mapsettings` package + main.go
  mount + import).
- Run migration 0066; confirm the three `iam.system_settings` rows.
- Web: `tsc` / lint / `vite build` (new hook, page, route, sidebar item, map
  props/wiring).
- Manual: as admin, open **Map settings**, change lon/lat/zoom, Save (success
  toast); reload the map → it opens on the new view. As a non-admin
  (`features:read` but not `metadata:write`), the map still opens on the
  configured default, the sidebar item is hidden, and `/admin/map-settings` by
  URL hits the 403 page. With the API's `/map-settings` GET failing, the map
  still mounts on the placeholder.

## Deferred / not built

- **"Use current map view" capture** on the admin form (read the live map's
  center/zoom into the inputs). The admin page is a separate route from the map,
  so it has no map instance to read — would need the map to persist its last
  view to a store the form can read. Simple numeric inputs ship first.
- A **per-territory / per-user** default view. This is a single global default;
  a territory- or role-scoped default is a larger data-model change, not needed
  yet.
