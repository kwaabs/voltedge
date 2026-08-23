# VoltEdge Field (Phase 21)

Offline-first field tool. Flutter app that downloads a **region pack** from the
`cmd/fieldsync` engine and works fully offline against it.

- **Display** — `tiles.mbtiles` (vector tiles, rendered by MapLibre Native) — *Slice 2b*.
- **Edit truth** — `data.sqlite` (server-built SQLite pack: features as OGC WKB +
  attributes JSON, plus the connectivity/relationship graph and per-class
  metadata). Opened read-only and queried directly — no import step.

## What's here

**Slice 2a — data layer.** The full server→device contract, end to end without
the map:

- `lib/src/api/` — `FieldsyncClient` (list / create / poll / cancel packs) +
  wire models matching `internal/fieldpack/routes.go`.
- `lib/src/pack/pack_store.dart` — download both artifacts via the presigned
  manifest URLs into `<app-docs>/packs/<id>/`.
- `lib/src/pack/pack_database.dart` — open `data.sqlite` read-only; class
  counts + geometry types, paged feature lists, by-id lookup.
- `lib/src/geo/wkb.dart` — minimal OGC WKB decoder.
- `lib/src/ui/` — browse UI (device packs, server packs, pack summary,
  per-class feature list, feature detail).

**Slice 2b — the map.** `tiles.mbtiles` rendered offline in MapLibre Native,
tap-to-select, responsive layout:

- `lib/src/map/mbtiles_tile_server.dart` — an in-app `dart:io` loopback HTTP
  server that reads `tiles.mbtiles` (SQLite) and serves `/{z}/{x}/{y}.pbf`
  (TMS→XYZ row flip, `Content-Encoding: gzip` for tippecanoe's gzipped PBF).
  This is how we render fully offline **without** re-downloading into
  maplibre_gl's ambient cache.
- `lib/src/map/pack_style.dart` — builds the MapLibre GL style JSON: one vector
  source, one draw layer per feature class (fill / line / circle by geometry
  type; polygons under lines under points). Class ids ride in the tile `id`
  property, so a tap resolves tile → id → SQLite row.
- `lib/src/map/pack_map_view.dart` — the `MaplibreMap` widget; `onMapClick` →
  `queryRenderedFeatures` over the pack layers → `PackDatabase.featureById` →
  `onFeatureTap`. Selection is highlighted via one GeoJSON overlay source.
- `lib/src/ui/pack_map_page.dart` — responsive host: master/detail split at
  ≥720dp (tablet), dismissable bottom sheet below (phone). Reuses
  `FeatureDetailView`. Reached from the ⛁ **Open map** action on a downloaded
  pack (home menu + pack-detail app bar).

### Why maplibre_gl (Slice 2b plugin choice)

Both `maplibre_gl` and the newer `maplibre` wrap **MapLibre Native**, so tiles
render on the GPU either way — equally efficient for a 100k pack. `maplibre_gl`
wins on the query/projection surface **snapping** (Slice 3) will need:
`queryRenderedFeatures`, `toLatLng(screenPoint)`, `toScreenLocation(latLng)` are
all mature. It's also the design-locked choice. Offline tiles come from our own
loopback server (above), not maplibre_gl's download-to-cache path.

## Regenerating the platform folders

`android/` + `ios/` are generated, not committed. If you need to (re)create them:

```bash
cd mobile
flutter create --org com.voltedge --platforms=android,ios .   # NO --overwrite
flutter pub get
```

> Do **not** pass `--overwrite`: it resets `pubspec.yaml` / `lib/main.dart` /
> `README.md` to Flutter templates. Plain `flutter create` fills in only the
> missing platform dirs and leaves the committed files alone.

## Running on a real Android device over USB (adb reverse)

No firewall changes — tunnel the two ports so the device reaches them at its own
`localhost`:

```bash
adb reverse tcp:3706 tcp:3706   # fieldsync API
adb reverse tcp:3702 tcp:3702   # RustFS (pack downloads via presigned URLs)
adb reverse --list
```

With adb reverse, the presigned download host stays the default `localhost:3702`
(so leave `RUSTFS_PUBLIC_ENDPOINT` unset / `localhost:3702`).

Android blocks cleartext `http://` by default, so `<application>` in
`android/app/src/main/AndroidManifest.xml` carries
`android:usesCleartextTraffic="true"` (dev-only) — this also covers the map's
loopback tile server on `127.0.0.1`. maplibre_gl needs `minSdkVersion >= 21`,
which is already Flutter's default.

Then:

```bash
flutter run
```

In-app **⚙ Connection**:

- **Base URL**: `http://localhost:3706`
- **API token**: an `iam.api_tokens` bearer —
  `task authadmin-mint-token EMAIL=you@example.com NAME=field-tablet`.

## Smoke test

**Slice 2a (data):**

1. Set base URL + token, tap **New pack** (bbox + class codes, e.g.
   `POLE, MV_CONDUCTOR, DIST_XFMR` — UPPERCASE + case-sensitive).
2. Pull-to-refresh until it shows **succeeded**, tap **download**.
3. Open the pack → confirm the class counts, drill into a class, open a feature,
   check the decoded geometry (type + vertex count + first vertex) and attributes.

**Slice 2b (map):**

4. On a downloaded pack tap **⋮ → Open map** (or the ⛁ icon in pack detail).
5. The map should frame the pack bbox and draw the features (points/lines/polys
   in per-class colors) with no network.
6. Tap a feature → it highlights and its record appears (side panel on a tablet,
   bottom sheet on a phone). Rotate the tablet to check the layout switch.
