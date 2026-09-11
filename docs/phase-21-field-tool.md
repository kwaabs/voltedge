# Phase 21 — Field Tool (offline-first Flutter mobile app + `fieldsync` engine)

A native mobile field tool for iOS + Android (Android first), phone **and** tablet
layouts, **offline-first**. It is a fourth front door onto the VoltEdge data plane
(after the web app, the MCP server of [phase-18-mcp], and the in-app assistant of
[phase-19-assistant]): a field tech pulls a bounded work area onto the device,
views + edits it with **no network**, and syncs changes back when a connection
returns.

Built in Flutter (Dart). The server side is a new Go binary, `cmd/fieldsync`, a
sibling of `server` / `exportworker` / `mcpserver` / `compile` / `authadmin` in the
same `api` module — **not** a separate stack — so it reuses `internal/crud`,
`internal/metadata`, `internal/export` (the gather + RustFS plumbing), and the
`iam.api_tokens` trust boundary directly, and owns no schema of its own beyond the
pack-job table.

## Why a new binary, not a separate service or an extension of `server`

The field engine's job is (1) **build region packs** (async, CPU-heavy —
tippecanoe) and (2) **replay field edits** through the *exact* shared write path
the web/MCP/assistant use (`crud.UpdateFeature`, territory + RBAC gates). A
separate stack would re-implement validation/RBAC/territory and drift. Extending
`server` would put tippecanoe + long pack builds on the API hot path. A new `cmd/`
binary in the same module gets code reuse **and** process isolation — the same
call the export worker already makes.

## Locked decisions (with user, 2026-08-22)

- **Offline edit scope (v1): attributes + move + photos only.** No offline
  *create* → **no client-generated feature UUIDs to reconcile**; the outbox is
  updates keyed by existing feature ids. This is the single biggest simplifier.
- **Connectivity + relationships: read-only context + offline trace in v1.** They
  ship in the pack and power on-device tracing (isolation/feeder/section), but
  establishing/breaking edges stays online. Keeps the sync path to attribute+move
  and avoids edge-conflict resolution.
- **Region = draw-a-box / GPS-radius bbox.** The pack builder reuses the Phase 17
  export gather, which already takes a `[minX,minY,maxX,maxY]` EPSG:4326 bbox.
- **Conflict policy: last-write-wins, with a free overwrite audit log.** Edits are
  attribute+move on existing features; field/office rarely touch the same asset the
  same day. Replay stamps `updated_by`/`updated_at` (already present) and logs
  every overwrite (who clobbered whom + the prior value) — the safety net without a
  merge UI.
- **"100k" = the editable universe in a pack** (queryable/traceable offline), with
  a *small dirty set per session*. The whole no-lag architecture is built around
  exactly that shape.
- **Map: MapLibre Native** (`maplibre_gl`). Divergence from web's OpenLayers is
  unavoidable (OL is browser-only); symbology is re-expressed once as a MapLibre GL
  style JSON.
- **On-device formats: MBTiles (display) + one server-built SQLite pack (edit
  truth).** No GeoJSON on disk or on the wire. See "Formats" below.

## The no-lag architecture (the load-bearing decision)

At 100k editable features + a connectivity/relationship graph, **the data-flow
decides lag, not the file format.** The invariant: *nothing that scales with 100k
ever touches the render or interaction hot path.*

| Job | Mechanism | Cost at 100k |
|---|---|---|
| Mass display | MBTiles vector tiles, GPU-rendered by MapLibre | O(viewport), not O(features) |
| Authoritative storage | Local SQLite pack (WKB geometry + attrs + 2 edge tables), indexed | 100k rows is nothing; by-id lookup <1ms |
| Tap / select | MapLibre `queryRenderedFeatures` → feature id → SQLite by id | GPU hit-tests; DB does one indexed lookup |
| Edit feedback | Tiny **dirty overlay** (only features changed this session) | Dozens–hundreds, never 100k |
| Trace | **In-memory adjacency graph**, built once on pack open | ~200k edges ≈ 2–4 MB RAM; BFS sub-ms |

Three techniques make it work:

1. **Dirty-overlay + base-layer filter.** The base MBTiles is static/read-only. On
   an edit, write SQLite + add the feature (new geometry) to a small MapLibre
   GeoJSON overlay source + add its id to a base-layer filter
   `["!", ["in", ["get","id"], ["literal", dirtyIds]]]` so the base stops drawing
   it. Edits look instant over a static tile base, **zero tile regeneration**. The
   overlay never grows to 100k.
2. **GPU hit-testing, DB record lookup.** Never scan 100k in Dart at tap time.
   MapLibre returns the feature id from the tile; pull the full-precision record
   from SQLite by id.
3. **Graph in RAM.** On pack open, load connectivity + relationship edges into
   `Map<int,List<int>>` adjacency (UUIDs mapped to dense int indices for
   compactness). Traces run in memory — no per-hop DB round-trips.

## Formats

- **Display: MBTiles** (MVT from **tippecanoe**), **content-pruned** — only the
  fields drawn into tiles (label/symbol codes); full attributes live in the SQLite
  pack. Content pruning + zoom range + `--drop-densest-as-needed` are the real
  size levers (10× the container choice). MBTiles over PMTiles is chosen for
  mature Flutter/MapLibre offline support, not efficiency (a wash).
- **Edit truth: one server-built SQLite pack.** Chosen over GeoPackage at this
  scale/complexity: GPKG's feature-table format doesn't naturally hold the
  connectivity/relationship graph or the sync/dirty/outbox columns, and Dart has no
  mature GPKG *editing* library (you hand-roll WKB either way). So the server builds
  a plain SQLite file — `features`, `connectivity`, `relationships`, `metadata`,
  `manifest` — **with indexes built server-side and shipped in the file**; the
  device opens it with Drift, queryable on first frame, no import step. (Revisit
  GPKG only if "open the pack in QGIS" becomes a hard requirement.)
- Both artifacts land in RustFS; the device downloads each via a presigned URL
  (resumable HTTP range), transported zstd-compressed.

**Rough size at 100k:** pruned MBTiles ≈ 5–30 MB; SQLite pack ≈ 30–100 MB
(attribute-dependent), less after zstd. A ~50–120 MB pack pulled over wifi before
heading out. This is why **delta refresh** (below) matters later.

## The engine — `cmd/fieldsync`

Mirrors `cmd/exportworker` + `cmd/mcpserver`:

- **Pack-build job** on a new `ops.field_packs` queue (SKIP LOCKED, lease +
  heartbeat, cancel, RustFS delivery) — structurally identical to `ops.export_jobs`
  (Phase 17). A build: resolve scope → **reuse `export.gatherLayers`** under a
  `REPEATABLE READ` snapshot (records `snapshot_at` for later delta refresh) →
  (a) write per-class GeoJSON → shell out to **tippecanoe** → `.mbtiles`;
  (b) write the SQLite pack (features WKB + attrs, connectivity, relationships,
  metadata) → upload both to RustFS → mark succeeded with two object keys + sizes.
- **HTTP surface** (POST /mcp-style, own port `FIELDSYNC_PORT`), all under
  `iam.api_tokens` bearer auth (reuse Phase 18 `internal/auth`), every call under
  the token-owner's RBAC:
  - `POST /packs` — enqueue a build for a bbox + class set → `{pack_id}`.
  - `GET /packs/{id}` — poll status; on success returns the **manifest** (two
    presigned URLs, feature_count, bbox, `snapshot_at`, pack version).
  - `GET /packs` — the caller's recent packs.
  - `POST /sync` — replay an edit batch (attribute/move deltas, idempotent by
    client op-id) through `crud.UpdateFeature`; LWW + overwrite log. Returns
    per-op results (applied / conflict-overwritten / rejected).
  - `POST /attachments` (Slice 4) — mint a presigned PUT for a photo → device PUTs
    bytes straight to RustFS, then records the row.
- **Auth mint:** reuse `authadmin mint-token`; a device registers by pasting/
  scanning a minted token (Slice 3 may add an OIDC-PKCE → token exchange).

## Sync model

- **Outbox** (device SQLite): append-only `{op_id (client uuid), feature_class,
  feature_id, changed_attrs (sparse), new_geom (WKB, if moved), base_updated_at,
  created_at}`. Attribute + move only in v1.
- **Replay**: batched `POST /sync`; each op → `crud.UpdateFeature`. **Idempotent**
  by `op_id` (server dedupes so a retried batch never double-applies). LWW: apply
  regardless of `base_updated_at`, but when the server row moved since, write an
  **overwrite log** row (feature, who, when, prior value) for later audit.
- **Photos**: compressed on device (JPEG/WebP), PUT straight to RustFS via presigned
  URL — image bytes never transit the API.

## Auth

Reuse `iam.api_tokens` bearer (Phase 18) end to end. Device stores the token in
Keychain/Keystore (`flutter_secure_storage`). Read (pack download) and write (sync)
both flow through the token → user → RBAC chain. OIDC-PKCE-in-app → token exchange
is a later-slice nicety; a minted token unblocks everything before it.

## Flutter app (`mobile/` in this repo)

Lives in a new top-level `mobile/` dir, versioned with `api/` + `web/` so contracts
stay together. Stack: **MapLibre Native** (map) · **Drift/SQLite** (local store, opens
the pack directly) · **Riverpod** (state) · `flutter_secure_storage` · `geolocator`
· `image_picker` · `connectivity_plus` + `workmanager` (background sync).
Responsive: `LayoutBuilder` + breakpoints — master-detail (map + feature panel
side-by-side) on tablet, bottom-sheet on phone.

## Slice plan

- **Slice 0 — spike (local, user-run; Bash blocked here).** Prove the two real
  integration risks before committing: (a) MBTiles renders offline in
  MapLibre-Flutter; (b) reading/writing WKB from Dart/Drift against a sample SQLite
  pack. De-risk first.
- **Slice 1 — engine.** `ops.field_packs` migration + `internal/fieldpack` (job
  model + queue, mirror of `export/job.go`) + pack builder (dual-encoding read →
  tippecanoe → SQLite pack → RustFS) + `cmd/fieldsync` + HTTP routes + bearer auth.
  *(This slice is the backend; statically verifiable here.)* **Done — see the Slice 1
  section below.**
- **Slice 2 — offline read.** Flutter: download a pack, render MBTiles + tap-select
  via SQLite, phone + tablet layouts. Split into **2a** (data layer: download +
  open the SQLite pack + WKB decode + browse UI, no map — **done**, see below) and
  **2b** (MapLibre render + tap-select + responsive layout — **code-complete
  2026-08-23**, see below; verification owed).
- **Slice 3 — offline edit + sync.** Dirty-overlay edits (attributes + move) →
  outbox → `POST /sync` replay → LWW + overwrite log.
- **Slice 4 — photos + GPS.** `runtime.feature_attachments` migration + presigned
  PUT upload + capture UI.
- **Slice 5 — offline trace.** In-memory graph over the pack's connectivity/
  relationship tables; isolation/feeder/section trace UI (read-only).
- **Slice 6+ — delta refresh** (server returns features changed since `snapshot_at`
  instead of a full re-download), then richer conflict handling / online
  connectivity edits if wanted.

## Open items (deferred, tracked)

- **Delta refresh** vs full pack re-download (Slice 6) — the one place v1 is
  knowingly not size-optimal.
- **Online connectivity/relationship editing** from the field (would reopen the
  edit-scope + edge-conflict decisions).
- **OIDC-PKCE in-app** token exchange (vs pasted minted token).
- **Basemap source** — Protomaps/OSM extract packaged as PMTiles/MBTiles (watch
  ODbL attribution) vs bring-your-own imagery.
- **Pack size caps** — enforce a max bbox area / feature count at build (reuse the
  `maxExportFeatures` guard); show a size estimate before download.

## Slice 1 — engine (code-complete 2026-08-22, verification owed)

Backend only; the Flutter side is Slice 2+. What landed:

- **`migrations/0039_create_field_packs.sql`** — `ops.field_packs` queue, modeled on
  `ops.export_jobs` but carrying **two** artifacts (`tiles_object_key` +
  `data_object_key`, `tiles_bytes` + `data_bytes`) and `snapshot_at`.
- **`internal/fieldpack/pack.go`** — `FieldPack` row model + `PackScope` (bbox,
  classes, tri-state territory ids, **frozen** `IncludeConnectivity` /
  `IncludeRelationships`) + queue ops (`enqueue`/`claim`/`heartbeat`/`complete`/
  `fail`/`cancel`/`get`/`list`), a structural twin of `export/job.go`.
- **`internal/fieldpack/reader.go`** — per-class streaming read. Each feature is read
  **once** in **two encodings** side by side: `ST_AsBinary(geom)` (OGC WKB — the
  on-device edit truth) and `ST_AsGeoJSON(geom)` (tippecanoe's tile input). This
  deliberately does **not** reuse `crud.ExportFeatures` (GeoJSON-only); it
  hand-mirrors `crud.exportQuery`'s WHERE (soft-delete + optional territory + optional
  bbox) because that builder is unexported and the pack needs a different column list.
  Note: the earlier "reuse `export.gatherLayers`" sketch was superseded by this
  dual-encoding reader — WKB can't be recovered from GeoJSON without a geometry lib.
- **`internal/fieldpack/builder.go`** — the build core (analog of
  `export.runClaimedJob`). One `REPEATABLE READ`, read-only tx reads `SELECT now()`
  as `snapshot_at`, loops the classes writing WKB+attrs rows into a **server-built
  SQLite pack** (`modernc.org/sqlite`, pure Go) and per-class newline-delimited
  GeoJSON, then loads the connectivity/relationship edges **only when the scope's
  frozen flags allow it** (both endpoints must be in the packed set — dangling edges
  dropped). Indexes are built after the bulk load and shipped in the file; then
  tippecanoe (`os/exec`, one `-L code:file` layer per class) builds the MBTiles. Both
  artifacts upload to RustFS under `packs/<id>/`.
- **`internal/fieldpack/worker.go`** — claim loop + lease heartbeat (heartbeat at
  `lease/3`, abort the build on a lost lease), a twin of `export.RunWorker` minus the
  retention sweep.
- **`internal/fieldpack/routes.go`** — chi router behind `iam.api_tokens` bearer +
  `features:read`: `POST /packs` (graph-inclusion frozen from the requester's
  `connectivity:read`/`relationships:read`), `GET /packs`, `GET /packs/{id}` (presigns
  both artifact URLs into the manifest on success), `POST /packs/{id}/cancel`.
  `POST /sync` deferred to Slice 3.
- **`cmd/fieldsync/main.go`** — wiring: DB + RustFS store, `RunWorker` goroutine, HTTP
  server on `FIELDSYNC_PORT` (default `8091`), graceful shutdown draining in-flight
  builds.
- **`api/Dockerfile.fieldsync` + compose `fieldsync` service** — its **own** image
  (not the shared `api/Dockerfile`), because only fieldsync needs tippecanoe and the
  server/exportworker/mcpserver image shouldn't carry a C++ tile compiler. There is
  **no official tippecanoe image** (Mapbox archived the repo; Felt maintains the fork;
  Docker Hub community images are unpinned/untrusted), so stage 1 **builds tippecanoe
  from a pinned `felt/tippecanoe` release tag** (`ARG TIPPECANOE_VERSION`) on alpine,
  stage 2 builds the Go binary, stage 3 ships both with only the runtime libs
  (`sqlite-libs zlib libstdc++ libgcc`). Compose service mirrors `exportworker`'s
  RustFS env + adds `FIELDSYNC_PORT` (host `3706`); `.env.example` documents the vars.

**Build prerequisites (Bash blocked here — owed to the user):**
- `cd api && go get modernc.org/sqlite` — the pure-Go SQLite driver is a **new
  go.mod dependency**; both `go build ./...` **and** the Docker build's `go mod
  download` fail until go.mod/go.sum include it (commit them).
- **tippecanoe**: the compose image builds it in (nothing to install); only host-run
  `task fieldsync-dev` needs it on `PATH` (or `TIPPECANOE_BIN`). Pinned to
  `felt/tippecanoe` **2.79.0** (latest, Jul 2025) via `TIPPECANOE_VERSION` in
  `api/Dockerfile.fieldsync`; bump the tag to upgrade.
- Apply migration `0039`.
- All new Go was statically cross-checked (signatures/imports against `crud`,
  `metadata`, `territory`, `auth`, `export`); no compiler/vet run was possible here.

## Slice 2a — offline read: data layer (code-complete 2026-08-22, verification owed)

The Flutter side begins. Slice 2a lands the whole server→device contract **without
the map** (that's 2b), so the download → open-pack → decode chain is provable on its
own — and it absorbs what the Slice 0 spike would have proven for the WKB/SQLite side.
Lives in the new top-level **`mobile/`** dir. Two decisions taken with the user
(AskUserQuestion, 2026-08-22): stage as **2a (data) → 2b (map)**; use the **`sqlite3`
package directly** (raw SQL, read-only) for the pack rather than Drift — no
`build_runner` codegen for a fixed, server-built, read-only schema. This is a
**deliberate deviation from the design's locked "Drift/SQLite"**; recommend adopting
Drift in Slice 3, where the reactive dirty-overlay + outbox + migrations earn the
codegen. What landed:

- **`mobile/pubspec.yaml`** — `flutter_riverpod`, `http`, `sqlite3` +
  `sqlite3_flutter_libs` (bundles native libsqlite3), `path_provider`, `path`,
  `flutter_secure_storage`. (`geolocator`/`image_picker`/`connectivity_plus`/
  `workmanager` deferred to the slices that use them.)
- **`lib/src/api/`** — `models.dart` (`PackSummary`/`PackManifest`, field names
  mirroring the Go `packView`/`manifestView`; tolerant RFC3339 parse that trims Go's
  nanoseconds to Dart's 6-digit limit) + `fieldsync_client.dart` (`FieldsyncClient`:
  list/get/create/cancel over the bearer, surfaces the `{"error":…}` envelope).
- **`lib/src/config/settings_repository.dart`** — base URL + bearer token in the
  platform keystore, loaded once at startup so reads are synchronous. Default base URL
  `http://10.0.2.2:3706` (Android-emulator → host).
- **`lib/src/pack/pack_store.dart`** — downloads both presigned artifacts into
  `<app-docs>/packs/<id>/` (streamed to a `.part` then renamed), writes a `pack.json`
  sidecar, lists/deletes downloaded packs.
- **`lib/src/pack/pack_database.dart`** — opens `data.sqlite` **read-only**; class
  counts (joined to `metadata.name`), paged `featuresOfClass`, `featureById` (the
  tap-select query 2b reuses), table counts. Every lookup rides the server-built
  indexes.
- **`lib/src/geo/wkb.dart`** — minimal OGC WKB decoder (Point/Line/Polygon/Multi/
  Collection → GeoJSON-style coordinates; tolerates EWKB high-bit flags + ISO Z/M
  defensively, ignores any SRID — `metadata.srid` is authoritative).
- **`lib/src/ui/`** — `home_screen` (device packs + server packs with build/download/
  cancel, pull-to-refresh), `settings_screen`, `new_pack_screen` (bbox + comma classes
  — a text form; 2b's map gives draw-a-box), `pack_detail_screen` (manifest summary +
  per-class counts + paged feature list), `feature_detail_screen` (decoded geometry +
  attributes), `format.dart` helpers.

**Verification owed (Bash blocked — the user runs Flutter):** the platform folders
aren't committed — generate with `flutter create --platforms=android,ios` then restore
the committed `lib/`+`pubspec` (README documents the exact steps), `flutter pub get`,
`flutter run`. Smoke test in `mobile/README.md`. No `dart analyze`/compile was possible
here; the Dart was written against the documented package APIs
(`sqlite3`/`riverpod`/`http`/`flutter_secure_storage`) but not compiler-checked.

## Slice 2b — offline read: the map (code-complete 2026-08-23, verification owed)

The map goes on top of the 2a data layer: `tiles.mbtiles` rendered offline, a tap
resolved back to the SQLite row, phone + tablet layouts.

**Plugin choice — `maplibre_gl`** (design-locked, and re-confirmed against the user's
two criteria: *efficient?* + *can I snap?*). Both `maplibre_gl` and the newer
`maplibre` wrap MapLibre Native, so GPU render of a 100k pack is equivalent.
`maplibre_gl` wins on the query/projection surface **Slice 3 snapping** needs —
`queryRenderedFeatures`, `toLatLng(screenPoint)`, `toScreenLocation(latLng)` are all
mature there.

**Offline tiles without the ambient cache.** maplibre_gl's own offline story is
"download a region into a cache", but the pack already ships the tiles. So
`src/map/mbtiles_tile_server.dart` is an in-app `dart:io` loopback HTTP server that
opens `tiles.mbtiles` (SQLite) read-only and serves `/{z}/{x}/{y}.pbf` — TMS→XYZ row
flip (`tms = 2^z−1−y`), `Content-Encoding: gzip` when the tile bytes start with the
gzip magic (tippecanoe gzips its PBF). The style just points a vector source at
`http://127.0.0.1:<ephemeral>/{z}/{x}/{y}.pbf`. Render path stays plugin-agnostic.

**Style** (`src/map/pack_style.dart`): one vector source, one draw layer per class —
fill+outline / line / circle keyed off the class's `geometry_type` (from the pack
`metadata` table), z-ordered polygons→lines→points, deterministic per-code colors (a
real symbology table is a later slice). tippecanoe wrote each class as its own tile
layer named by the class code (`-L code:file`), so a layer's `source-layer` **is** the
code and the feature `id` rides in the tile properties.

**Tap-select** (`src/map/pack_map_view.dart`): `onMapClick` → `queryRenderedFeatures`
scoped to the pack draw layers → first hit's `id` property → `PackDatabase.featureById`
→ `onFeatureTap`. Selection is owned by the page (single source of truth) and drawn via
one GeoJSON overlay source (`setGeoJsonSource`) with fill/line/circle highlight layers —
only the layer matching the picked geometry paints.

**Responsive** (`src/ui/pack_map_page.dart`): `LayoutBuilder` at a 720dp breakpoint —
master/detail split (map + 360dp panel) on a tablet, dismissable bottom sheet (≤45%
height) on a phone. Both reuse the new `FeatureDetailView` (extracted from
`feature_detail_screen` so the record renders inline without a page push). Entry points:
⛁ **Open map** on the home downloaded-pack menu + the pack-detail app bar.

Also: `PackDatabase.classMetas()` added (code + name + geometry_type per class);
AndroidManifest gained `usesCleartextTraffic="true"` (covers both the API over adb
reverse and the loopback tile server).

**Genuine integration risk (the skipped Slice 0 spike):** rendering a **local**
MBTiles in maplibre_gl was never de-risked. The loopback-server approach is the
lowest-risk path, but it's unproven on-device here (Bash blocked). If MapLibre Native
double-decompresses gzipped tiles or rejects `noContent` for empty tiles, the fixes are
local to `mbtiles_tile_server.dart` (drop the gzip header / return 404). Verify the map
draws **before** building Slice 3 edit UI on top.

**Runtime-verified 2026-08-24.** On the SM-T395 the map draws the pack offline and
tap-select works — the on-device MBTiles-in-MapLibre render (the skipped Slice 0 spike)
is now proven. Two bring-up fixes:
- **Blank map ≠ tile delivery.** logcat showed only GPU init, zero tile errors — the
  camera opened *below* tippecanoe's minzoom 6 because a wide-bbox pack's scope box put
  the data off-screen. Added `PackDatabase.featureExtent()` (an on-device O(features) WKB
  scan for the real min/max) and framed the map to **that**, not the manifest scope bbox,
  at zoom 14.
- **Tap missed thin geometry.** A single-pixel `queryRenderedFeatures(Point,…)` almost
  never lands on a 2px line or small circle. Switched to
  `queryRenderedFeaturesInRect(Rect, layerIds, filter)` with a 16px tolerance box (note:
  its `filter` is a JSON **string**, unlike `queryRenderedFeatures`' `List<Object>?`).

**Build-verified 2026-08-23 (partial):** first `flutter run` on the SM-T395 tablet
surfaced a cascade of toolchain issues. The device scaffold was on the January-2026
bleeding edge — Gradle 9.1.0, AGP 9.0.1, Kotlin 2.3.20 — where **AGP 9 ships built-in
Kotlin** and removes the classic `org.jetbrains.kotlin.android` plugin. Two version
fixes, then the root cause + its systemic resolution:
- `maplibre_gl ^0.20.0` pinned 0.20.x (pre-1.0 caret = `>=0.20.0 <0.21.0`), which
  still references the removed Android v1 `PluginRegistry.Registrar` and fails to
  compile on current Flutter. Constraint widened to `">=0.21.0 <1.0.0"` so pub takes a
  modern release (resolves to 0.27.0).
- The modern releases renamed `MaplibreMap`→`MapLibreMap` and
  `MaplibreMapController`→`MapLibreMapController` (capital L, matching maplibre-native);
  `pack_map_view.dart` updated. Other public API used here (queryRenderedFeatures,
  addGeoJsonSource/setGeoJsonSource, add{Fill,Line,Circle}Layer, onStyleLoadedCallback,
  onMapClick, CameraUpdate.newLatLngBounds) is unchanged across the rename.

**Root cause (systemic, not per-plugin).** On AGP 9 the only way to build maplibre_gl
0.27 is `android.builtInKotlin=true` (its module skips `kotlin-android` via an
`if (agpMajor < 9)` guard and gets Kotlin from the built-in `kotlin {}` extension, which
exists only when built-in Kotlin is on). But built-in Kotlin then requires **every**
native plugin to have migrated off the classic `kotlin-android` plugin — and most of the
ecosystem has **not**: `flutter_secure_storage` (verified 9.2.4 → 10.3.1 → 11.0.0, all
fail), `jni`, etc. Chasing them one at a time is whack-a-mole; each unmigrated plugin
fails identically ("the 'org.jetbrains.kotlin.android' plugin is no longer required for
Kotlin support since AGP 9.0").

**Resolution — downgrade the toolchain to the stable AGP 8 line** (where built-in Kotlin
doesn't exist, so the app + every plugin apply classic `kotlin-android` the normal way,
and maplibre_gl's `agpMajor < 9` guard fires and builds it):
- `android/settings.gradle.kts`: `com.android.application` **9.0.1 → 8.13.2** (the exact
  AGP maplibre_gl 0.27 pins/tests against — supports its compileSdk 36 / Java 21 / NDK
  28.2), `org.jetbrains.kotlin.android` **2.3.20 → 2.1.20 → back to 2.3.20** (2.1.x can't
  read the Kotlin 2.3.0 metadata in maplibre_gl's transitive play-services-location 21.4.0,
  and Flutter wants ≥ 2.2.20; Kotlin 2.3.0's supported AGP range is 8.2.2–8.13.0).
- `android/gradle/wrapper/gradle-wrapper.properties`: Gradle **9.1.0 → 8.14** (AGP 8.13
  min is 8.13; 8.14 also clears Flutter's ≥ 8.14 warning).
- `android/gradle.properties`: `android.builtInKotlin` **true → false** (Flutter's own
  default; `newDsl=false` kept).
- `android/app/build.gradle.kts`: app module now applies `org.jetbrains.kotlin.android`
  explicitly (needed for its Kotlin `MainActivity` when built-in Kotlin is off).

**flutter_secure_storage still dropped, for now.** It was removed while fighting AGP 9,
and `SettingsRepository` now persists `baseUrl` + `token` to an app-private JSON file
(`ve_settings.json` in `getApplicationDocumentsDirectory`, per-app sandboxed) via
`path_provider`. On AGP 8 the keystore plugin *would* build again, so this can be
reinstated (the `SettingsRepository` public API is unchanged — a private-method swap).
Kept as the file store for this pass to isolate the toolchain change; revisit once the
build is green and the map renders.

**Still owed:** re-run after the downgrade and confirm (a) the build completes — watch
for a JDK-21 requirement (maplibre_gl's module compiles Java 21, so Gradle must run on
JDK ≥ 21) and NDK 28.2.13676358 needing install; then (b) the map actually **draws** the
pack offline + tap-select works (the on-device MBTiles render is still the un-de-risked
integration). Slice 2b smoke test in `mobile/README.md`.

## Slice 2c — map-detail polish (code-complete 2026-08-24, verification owed)

Three refinements the user asked for right after tap-select landed.

**1 · Geometry out of the Attributes list.** There is **no "geometry" field data
type** — the server's field types are `text / numeric / integer / boolean / date /
timestamp / uuid / jsonb` (`internal/admin/types.go`); `geometry_type` is a per-*class*
concept, not a field. So a geometry appearing among attributes is a `text`/`jsonb`
field carrying WKT, WKB-hex, or GeoJSON. `FeatureDetailView` now drops such rows by
**value shape** (`_looksLikeGeometry`: a WKT regex incl. `SRID=` prefix and Z/M, a
`"coordinates"` substring for GeoJSON, a long dash-free hex run for WKB) rather than a
hardcoded field name — data-driven, so it also handles any future geometry-ish field.
The decoded **Geometry** section already summarizes the real geometry.

**2 · Peer picker (compact chooser).** `queryRenderedFeaturesInRect` returns *every*
feature under the tolerance box. `pack_map_view.dart` now resolves all **distinct**
ids (deduped, nearest-first) to pack rows and reports them via
`onFeaturesTapped(List<FeatureRecord>)` (renamed from `onFeatureTap`). The page selects
directly on a single hit; on a stack of 2+, it opens a `showModalBottomSheet` list
(name + class per row) and selects the chosen one — so a line buried under a point stays
reachable.

**3 · Connectivity + relationships (list + tap to navigate).** New
`PackDatabase.connectionsFor(id)` / `relationshipsFor(id)` — an `OR` over both endpoint
columns (all four are indexed in the pack) with a `LEFT JOIN features` for the
neighbour's name — return `NeighborLink { kind, featureClass, featureId, name, detail }`.
The page loads both on every `_select` (index-backed, cheap) and passes them into
`FeatureDetailView`, which renders tappable **Connections (n)** / **Relationships (n)**
sections. Tapping a row calls `_navigate(id)` → `featureById` → `_select`, so the map
re-highlights and the panel walks the graph hop by hop. The full-page
`FeatureDetailScreen` (browse list) passes no neighbours and no `onNavigate`, so the
sections are simply absent and rows are non-tappable there (it has no `PackDatabase`).

## Slice 3a — offline attribute edit + dirty overlay + outbox (code-complete 2026-08-24, verification owed)

The foundation of offline editing: a **writable** local store beside the read-only
pack, a type-aware attribute form, the map's "pending edit" overlay, and a push queue.
**No move, no server sync yet** — those are 3b / 3c. Two design choices the user
confirmed (AskUserQuestion, 2026-08-24): the edit store is **raw sqlite3-direct**
(not Drift — a fixed two-table schema doesn't earn build_runner), and 3a's scope is
**attr edit + dirty overlay** only.

**The writable edit DB (`edits.sqlite`, beside `data.sqlite`).** `edit/edit_database.dart`
opens a second SQLite file per pack `OpenMode.readWriteCreate` (WAL — this file is written
interactively, unlike the throwaway pack build). Two tables:
- `overlays` — one row per edited feature, the current local edit state. `attrs` is a
  **JSON delta**, not the full attribute map: only the keys whose value differs from the
  packed (server) value. Reverting a field to its packed value shrinks the delta; clearing
  every change drops the overlay entirely. `geom`/`name`/`base_version` columns exist now
  (so 3b needs no migration) but stay null for an attribute-only edit.
- `outbox` — the push queue. **One PENDING row per (feature, kind)**: a re-edit *replaces*
  the pending `update_attrs` op with a fresh one carrying the latest delta (LWW: newest
  local intent, no bloat). `payload` is already in **`crud.UpdateFeatureInput` shape**
  (`{"attributes": {...delta}}`), so 3c's `POST /sync` posts it verbatim; `op_id` is the
  idempotency key the sync endpoint will dedupe on; `base_version` = the pack `snapshot_at`,
  carried for the server's LWW resolve. Overlay upsert + outbox replace happen in one
  `BEGIN/COMMIT` so UI state and push state can't diverge.

**The reactive store (`edit/edit_store.dart`).** `EditStore extends ChangeNotifier` mirrors
the overlay rows in memory (hydrated once at open) for zero-cost reads: `dirtyIds` (the set
the map paints), `isDirty(id)`, `mergedAttributes(id, packAttrs)` (pack values with the delta
laid over). `saveAttrEdit` computes the delta vs the **packed** values (a structural
`_deepEquals` over decoded-JSON scalars/lists/maps — Dart's `5 == 5.0` handles the num case),
writes overlay+outbox, and `notifyListeners()`. Empty delta ⇒ clear the feature. op_ids are a
monotonic seq + microsecond timestamp (no `uuid` dep needed).

**The edit form (`ui/feature_edit_screen.dart`).** A `Form` over the feature's editable
fields, read from the packed field defs via new **`PackDatabase.fieldsFor(code)`** →
`FieldDef {name, dataType, nullable, isComputed, visibleOnUpdate, editableAfterPublish,
domainId}`. The pack's `metadata.fields` blob is Go's `[]metadata.Field` marshalled with
**default capitalized keys** (that struct has no json tags), so the parser reads `Name` /
`DataType` / `VisibleOnUpdate` / etc. Editability mirrors the server's own rules: only
`visible_on_update`, non-computed fields are editable; a field that isn't
`editable_after_publish` is **locked (shown read-only)** once the feature is `published`.
Controls are type-aware (`boolean`→switch, `integer`/`numeric`→number field + validator +
input filter, `jsonb`→multiline, `coded`→dropdown, `date`→**calendar picker**, else text) and
values are **coerced to match the field type** on save (an integer field sends an `int`, not
`"5"`, so the sync PATCH validates). Coded fields render as **dropdowns** (code stored, label
shown) from the domain values the pack now ships — see the *Coded pick lists* addendum below.

**Date fields — tap-to-pick calendar (no future dates).** `date`-typed fields used to fall
through to a plain text box; they now render a shared **`DateFormField`**
(`mobile/lib/src/ui/date_form_field.dart`, used by BOTH the edit and create forms). It's a
read-only `TextFormField` backed by the same controller as the text controls (so it slots
into the existing controller map + save/coerce path unchanged), holding an ISO `yyyy-MM-dd`
string — exactly what a server `date` field expects. Tapping opens `showDatePicker` with
**`lastDate` = today**, so a future date can't be selected (a survey/inspection date can't be
in the future); `firstDate` = 1900. Prefill normalizes any stored value to `yyyy-MM-dd` via
`normalizeIsoDate` (shared in `format.dart` alongside `parseIsoDate`/`formatIsoDate`); a
nullable field with a value offers a clear (✕) button, otherwise a calendar icon. Required
date fields validate empty→"Required" like the other controls. No pack/schema change — this
is a pure client-side control swap keyed off the existing `date` `data_type`.

**The dirty overlay (`map/pack_map_view.dart`).** A new `edits` GeoJSON source + amber
(`#ff9f0a`) fill/line/**hollow-ring** layers, geometry-type-filtered exactly like the red
selection overlay and added *first* so selection draws on top. `_applyDirty` repaints it from
the `dirty` feature list on style-load and whenever the dirty id-set changes. For 3a the
overlay reuses each feature's **packed** geometry (attribute edits don't move it) — it's a
"this feature has unsynced edits" marker. 3b (move) will feed *edited* geometry through the
same source, at which point the base-layer-hide half of the trick gets added.

**Page wiring (`ui/pack_map_page.dart`).** The page now owns the `EditStore` (opened in
`_open`, `snapshot_at` from the manifest as `base_version`, disposed with the page) and listens
for changes → `setState`. The panel header's **Edit** button flips a `_editing` flag that swaps
the panel body **in place** — the read-only `FeatureDetailView` becomes the inline
`FeatureEditView` in the *same slot* (tablet side panel / phone bottom sheet), so the user never
leaves the map or has to guess where the form went. The form carries its own Cancel/Save bar;
Save writes through `EditStore` and returns to the read-only panel; picking another feature,
navigating a neighbour, or Close all leave edit mode. On the phone the sheet grows to 70% and
lifts above the keyboard (`viewInsets`) while editing. In read-only mode `FeatureDetailView`
shows the **merged** values (`attributesOverride`) and an **"Edited offline — not yet synced"**
chip when `pending`. Editing is reachable from the map panel only (it has the store); the
browse-list `FeatureDetailScreen` stays read-only, as before.

Verification owed (Bash blocked here; user runs Flutter): edit a field offline → panel shows
the new value + pending chip, feature turns amber on the map, survives app restart (overlay
rehydrates), and reverting every change clears both.

### Slice 3a addendum — coded pick lists in the pack + edit-form styling (code-complete 2026-08-24, verification owed)

Two follow-ups the user asked for after seeing the form: coded fields (e.g. `condition_rating`,
`voltage_class`) showed as plain text boxes, and the form "could use a little more styling".

**Coded pick lists shipped in the pack (pack schema v1 → v2).** The pack shipped field *defs*
but not domain *values*, so the editor had no allowed list to show. The builder
(`internal/fieldpack/builder.go`) now writes a new `domain_values(domain_id, code, label,
sort_order, PK(domain_id,code))` table: as it loads each class, it collects the coded domains
its fields reference (exactly the keys of `metadata.Loaded.DomainValues`, which `metadata.Load`
populates for coded domains only — range/other domains have no list and stay free-text), then
after the class loop reads `metadata.domain_values` for that id set (under the same REPEATABLE
READ snapshot) and writes them ordered by `sort_order`. A domain shared by several classes is
collected into a set and written once. `packSchemaVersion` bumped to **2** and a
`domain_value_count` manifest entry added. On-device, `PackDatabase.domainValuesFor(domainId)`
returns the ordered `DomainValue {code, label}` list (falling back to `code` when a label is
blank); it **swallows the missing-table error** so a pre-v2 pack degrades to plain inputs
instead of crashing. `FeatureEditView` renders a `DropdownButtonFormField` for any field whose
`domainId` has a non-empty list — code is stored, label is shown, and a **stale current value**
not in the list is preserved as a synthetic "(current)" item rather than silently dropped. The
locked (read-only) section resolves the code to its label too. **Requires a pack re-download**
to take effect (existing v1 packs have no `domain_values` table). No new Go table needs
migrating — the pack is a throwaway build artifact, not a DB.

**Styling.** No new dependency — Flutter's own theming is the "styling engine" (Tailwind is a
CSS/DOM tool with no Flutter analogue). `FeatureEditView` now uses a shared filled
`InputDecoration` (rounded, dense, borderless) across every control, uppercase section labels
("Attributes" / "Locked after publish" with a lock icon), tighter vertical rhythm, and a
tinted tile for boolean switches — all driven off `ThemeData`/`ColorScheme` so it stays
consistent with the rest of the app.

### Slice 3a addendum 2 — humanized field labels + accurate per-subtype requiredness (pack schema v2 → v3, code-complete 2026-08-25, verification owed)

Three UX asks after the dropdowns landed: fields sat flush to the panel edges (and the dropdown
popup spilled off-screen), the label showed the raw column name (`voltage_class`), and the
`text · required` hint under each field was confusing.

**Field-form padding + dropdown fit.** The parent (`pack_map_page._editForm`) passed
`EdgeInsets.only(bottom:…)` which *replaced* the form's default horizontal padding, so fields —
and each `DropdownButtonFormField`'s popup, which inherits the button's width/position — lost
their inset. The form now always applies `horizontal:16` and merges any parent padding on top
(`EdgeInsets.symmetric(horizontal:16, vertical:12).add(widget.padding ?? EdgeInsets.zero)`), so
fields breathe and the menu lines up under the field instead of overflowing.

**Humanized labels.** `metadata.fields` has **no label column** — `name` *is* the physical
column name (migrations/0008 comment: "Physical column name once compiled, e.g. voltage_class").
So there is no richer label to load into the pack; the web app derives one the same way
(`formatFieldLabel`). Ported that as a shared `fieldLabel(name)` in `mobile/.../format.dart`
(replace `_`→space, capitalize first letter), used by **both** the edit form and the read-only
detail panel (`FeatureDetailView` attribute keys) — one source of truth. The `text · required`
type hint was removed. *(Real first-class field labels would be a platform-wide schema change —
`ALTER TABLE metadata.fields ADD COLUMN label` + admin + web + pack — deliberately out of scope.)*

**Accurate required marker (nullable → subtype rules).** The red `*` and the Save validator were
driven off column `nullable`, but the platform's real requiredness is per-subtype
(`metadata.subtype_field_rules.is_required`), which the web surfaces as `required_for_subtypes`.
Now shipped in the pack: `metadata.Loaded.RequiredSubtypeCodes(fieldID)` (new, the by-field
inverse of `RequiredFieldNames`) returns the subtype codes a field is required for; the builder
wraps each field in a `packField{metadata.Field; RequiredForSubtypes []int}` (embedding keeps
every existing JSON key identical, adds one), so `metadata.fields` now carries
`RequiredForSubtypes` per field. `packSchemaVersion` bumped to **3**. On-device, `FieldDef`
gains `requiredForSubtypes` and `FeatureEditView._isRequired(f)` = `feature.subtype ∈
f.requiredForSubtypes` — mirroring the web's `isFieldRequired` exactly (a class with no subtype,
or a field with no rule for this subtype, is optional). Drives both the `*` and the validators.
**Requires a v3 pack re-download**; an older pack ships no codes, so nothing is marked required
offline (graceful degradation, like the pre-v2 domain fallback). Verification expectation on the
seed: for an `LV_CONDUCTOR` feature, **Phase / Conductor spec / Lv role** show the `*`
(required for all its subtypes per `seed/lv_conductors_example.sql`), Condiction / Voltage class
do not.

## Slice 3d — offline feature CREATE: draw a point → form → outbox (code-complete 2026-08-25, verification owed)

**The ask.** Create new features in the field: pick a class → draw the geometry on the
map → fill the create form → save; saves pile into the outbox for a later sync. Locked
decisions (with user, via AskUserQuestion): **client-minted UUID** for the id (idempotent,
no server round-trip), **points first** (lines/polygons next), **create → outbox first,
sync next**. This slice (call it 3d, the create-to-outbox half) is therefore **mobile-only**
— nothing touches the server; the client UUID just rides in the outbox payload until the
sync slice (3e) honours it.

**Data layer (`edits.sqlite`, no schema-version bump — it's the writable edit DB, not the
pack).** New `creates` table beside `overlays`/`outbox`: one row per offline-created
feature (`feature_id` = client UUID PK, `feature_class`, `subtype`, `geom_json` = GeoJSON
geometry, `attrs` = FULL attribute map [not a delta — there's no packed truth to diff],
`name`, `base_version`, `created_at`). `EditDatabase.putCreate` upserts the row AND replaces
the single pending `'create'` outbox op in one tx (same LWW "newest intent" rule the update
path uses); `allCreates` hydrates; `clearFeature` now also drops the create. The outbox op
`kind` generalizes to `'update_attrs' | 'create'`. Payload is in `crud.CreateFeatureInput`
shape — `{id, subtype?, name?, geometry(GeoJSON), attributes}` — posted verbatim on sync;
`id` is included now (the server ignores unknown keys until 3e adds `ID` + an idempotent
insert). `EditStore` gains `_creates`, `createFeature(...)` (mints/reuses UUID via new
`util/uuid.dart` hand-rolled v4 — no new dep), `discardCreate`, `createdIds`, `isCreated`,
`isPending` (edit OR create), `createFor`, `creates`.

**Client UUID.** `mobile/lib/src/util/uuid.dart` — RFC-4122 v4 from `Random.secure()`,
hand-rolled to avoid the `uuid` package for one call site; layout matches Postgres `uuid`.

**Render + tap.** A created feature isn't baked into the pack's vector tiles, so the map
paints it from a synthetic `FeatureRecord` as a GREEN overlay (`#34c759`, FILLED — it's the
only thing drawing it) and INCLUDES those layers in the tap query, so a create is tappable
like a packed feature. `FeatureRecord` gained an optional `geometryOverride` (a pre-decoded
`WkbGeometry`) that wins over `geomBytes`; `WkbGeometry.fromGeoJson` builds it from the
create's GeoJSON (coordinates are already GeoJSON-style). `PackMapView` gains `created`
(green overlay + queryable + resolve-by-id), `drawing` (next tap PLACES a point via
`onPointPlaced` instead of selecting), and `draftPoint` (blue `#0a84ff` marker on top).

**Draw + form (no draw plugin — custom).** `PackMapPage` owns the flow: a **Create** FAB →
point-class picker (`_pickCreateClass`, points selectable, line/polygon classes listed
disabled "coming soon") → **place mode**: a top banner "tap to place / tap to move", the tap
sets a draggable-by-retap draft point, a **Place** button confirms → the create form takes
the panel slot (same slot as detail/edit, never a page push). `FeatureCreateView` is a
focused sibling of `FeatureEditView` (not a mode of it): a **subtype picker first** (drives
requiredness), a feature **name** field, and `visible_on_create` fields (new
`FieldDef.visibleOnCreate` — already in every pack's blob since the builder marshals the
whole `metadata.Field`; subtypes read from the packed `metadata.subtypes` blob via new
`PackDatabase.subtypesFor`), with the same coded-dropdowns / required-`*` / type-coercion as
the editor. Save → `EditStore.createFeature` → the green overlay + the new feature selected.
Editing an un-synced create re-opens the form pre-filled and re-saves in place (UUID reused
→ `INSERT OR REPLACE`); a **Discard** button drops it entirely.

**No pack rebuild, no Go changes** this slice: `VisibleOnCreate` and `metadata.subtypes`
are already in any current-builder pack (the v3 pack from addendum 2 suffices). Slice 3e
(sync) will add `ID *string` to `CreateFeatureInput` + `INSERT … ON CONFLICT (id) DO
NOTHING` and a `POST /sync` that replays the outbox (create + update ops).

**Files.** `mobile/lib/src/util/uuid.dart` (new), `edit/edit_database.dart` (creates table +
`LocalCreate` + `putCreate`/`allCreates`/`_toCreate`), `edit/edit_store.dart`
(`createFeature`/`discardCreate` + create getters), `geo/wkb.dart` (`fromGeoJson`),
`pack/pack_database.dart` (`FieldDef.visibleOnCreate`, `SubtypeDef` + `subtypesFor`,
`FeatureRecord.geometryOverride`), `map/pack_map_view.dart` (green overlay + draw mode +
draft), `ui/feature_create_screen.dart` (new), `ui/pack_map_page.dart` (FAB + create flow).

## Slice 3e — sync: replay the outbox to the server (code-complete 2026-08-25, verification owed)

The push half of offline edit/create: a device sends its outbox to a new
`POST /sync` on the fieldsync server, which replays each op through the **same shared
`crud.CreateFeature` / `crud.UpdateFeature`** the REST + MCP write paths use — one source of
truth for what a write validates and how a territory is assigned. No new write logic.

**Idempotent by construction.** A create carries the device's client-minted UUID as the
feature id. `crud.CreateFeatureInput` gains `ID *string` (`json:"id,omitempty"`, nil on the
REST path so the DB still mints it); `insertFeature` writes the supplied id and appends
`ON CONFLICT (id) DO NOTHING`, then — when `RETURNING` yields no row because the row already
existed — reads it back via `getFeature` and returns it as success. So re-pushing an
already-applied op is a **no-op that returns the existing row**, not a duplicate-key error.
That's what lets a device safely retry a partial push: it marks synced exactly the ops the
server acked and re-sends the rest. (An update replay is naturally idempotent — it just
re-sets the same values.)

**Per-op, never fail the batch.** `POST /sync` decodes `{ops:[{op_id, feature_class, kind,
feature_id, payload, base_version?}]}` and applies each op in **its own transaction** (a
failure rolls back just that op; a create's territory-assign stays atomic with its insert),
returning `{results:[{op_id, status:"applied"|"error", error?, feature?}]}` at HTTP 200. One
bad op can't strand the ops queued behind it. The route sits behind `bearerAuth` +
**`features:write`** (its own group — the pack lifecycle stays `features:read`), so a
pull-only device can't push.

**Mobile.** `FieldsyncClient.sync(ops)` posts the pending outbox and returns
`SyncOpResult`s. `EditDatabase.markSynced(opIds, ts)` stamps `synced_at` (the op row + its
overlay/create stay — the pack's snapshot predates the edit, so the feature must keep
rendering its local truth until a fresh pack); `pendingFeatureIds()` recomputes the store's
new **`_pending`** set. `EditStore` now separates *display* (overlay/create kept) from *sync
state* (`_pending`): `dirtyIds` (amber) = overlays ∩ pending, `pendingCount`/`isPending` read
`_pending`, and `markSynced` flips pending off (amber → plain, chip off, badge down) without
dropping the row. The map page (now a `ConsumerStatefulWidget`) shows a badged **Sync**
button in the app bar that opens a **pending-changes sheet** — one row per queued op
(green add-icon for a create, amber edit-icon for an update; name + class + changed-field
count), tap a row to jump to that feature on the map, a trailing per-row **Push this change**
(selective — `POST /sync` with just that op) plus Discard on an un-synced create, and a
**Sync N changes** button that pushes the lot. Push-all is the default (one tap, idempotent
+ per-op resilient); per-row push covers "commit just this finished feature, leave the rest
queued" without a multi-select mode. Both go through the same `_pushOps(ops)`. A snackbar reports
applied/failed counts. Two edge-guards flow from
the display/sync split: editing a **synced** create routes through the attribute editor (an
`update_attrs` op keyed by the same id — a re-create would hit `DO NOTHING` and lose the
change), and **Discard** is shown only for an un-synced create.

**Open follow-up:** on a fresh pack download the synced overlays/creates become redundant
(the new snapshot includes them) — the clean lifecycle is to clear synced rows from
`edits.sqlite` at download time. Deferred; until then a synced create keeps its green tint
until re-pack.

**Files.** `api/internal/crud/create.go` (`ID` field + thread through),
`api/internal/crud/sql.go` (`insertFeature` id param + `ON CONFLICT`/idempotent fetch),
`api/internal/fieldpack/sync.go` (new — handler + replay), `api/internal/fieldpack/routes.go`
(`/sync` route in a `features:write` group), `mobile/lib/src/api/models.dart` (`SyncOpResult`),
`mobile/lib/src/api/fieldsync_client.dart` (`sync`), `mobile/lib/src/edit/edit_database.dart`
(`markSynced`/`pendingFeatureIds`), `mobile/lib/src/edit/edit_store.dart` (`_pending` +
`markSynced`, display/sync split), `mobile/lib/src/ui/pack_map_page.dart` (Consumer + sync
button + `_sync`, edge-guards).

## Slice 4 — pack management: delete/disable packs from the web UI (code-complete 2026-08-25, verification owed)

**Problem.** Field packs accumulate. `cmd/fieldsync`'s device surface only let a tech
*build*, *poll*, and *cancel* their own packs — there was no way to remove a stale or
abandoned pack, so a field tech's tablet pack list only ever grows. Admins needed a
central place to prune them.

**Decisions (with user, via AskUserQuestion 2026-08-25):**
- **Admin manages ALL packs** (not owner-only): the web page lists every user's packs.
- **Both delete and disable:** *disable* hides a pack from its owner's device list
  (reversible, artifacts kept); *delete* reaps both RustFS artifacts + the row (permanent).

**Where it lives — the main API, not `cmd/fieldsync`.** The web app talks to `server`
with a **cookie session + CSRF + RBAC**; `cmd/fieldsync` only speaks `iam.api_tokens`
bearer and is owner-scoped by design. So admin pack management is a *second* HTTP surface
onto `ops.field_packs`, mounted by `cmd/server`. It reuses the `fieldpack.FieldPack` model
(no duplication) and the `export.ObjectStore` the API already holds (to reap artifacts):

    GET    /field-packs            every user's packs, newest first (owner email/name +
                                   bbox/classes unpacked from scope + disabled flag)
    POST   /field-packs/{id}/disable   set disabled_at = now()
    POST   /field-packs/{id}/enable    clear disabled_at
    DELETE /field-packs/{id}       reap both RustFS objects (best-effort) + delete row

One `admin:field-packs` RBAC gate covers every method (list + actions are one capability),
plus `auth.RequireCSRF` inside `AdminRoutes` (delete reaps storage — exactly what CSRF
guards). Migration **0040** adds the `disabled_at` column and the `admin:field-packs`
permission, granted to `admin` + `break-glass-admin` (following 0031's precedent).

**Disable = hide from the device.** `fieldpack.listPacks` (the device's `GET /packs`) now
filters `disabled_at IS NULL`, so a disabled pack drops off the tablet's list while its
artifacts stay in RustFS — re-enabling makes it reappear. **No mobile code change needed**:
the device already reads its list from the server, so disable/delete take effect on the
next list fetch.

**Delete robustness.** `adminDeletePackHandler` loads the row for its object keys, reaps
them (a missing object is swallowed — the retention sweep is the backstop), then hard-
deletes. A queued/running pack has no keys yet; if the worker later finishes, its
`completePack` no-ops (row gone) and the orphan artifacts fall to retention. Note: deleting
server-side does **not** remove a copy already downloaded to a device — the tablet keeps
its local `PackStore.delete` for that.

**Frontend.** `web/src/hooks/use-field-packs.ts` (`useAdminFieldPacks`/`useSetPackDisabled`/
`useDeletePack`, TanStack Query, invalidate-on-success) + `web/src/routes/admin-field-packs.tsx`
(a card list: owner, status chip, coverage/size/date summary, a Hidden badge, an eye toggle
for disable/enable, and a `ConfirmDeleteDialog` for delete). Wired into `app.tsx` behind a
`RequirePermission perm="admin:field-packs"` route and the sidebar Admin group (both mirror
the API gate; the server is the real one).

**Relation to the synced-create "green geometry" question.** This is the admin-side answer
to stale packs; the device-side auto-clear that closes the same loop is below.

**Files.** `migrations/0040_field_packs_admin.sql` (new), `api/internal/fieldpack/admin.go`
(new — `AdminRoutes` + handlers + owner-join list query + artifact reap),
`api/internal/fieldpack/pack.go` (`DisabledAt` field + `listPacks` filter),
`api/internal/auth/rbac.go` (`PermFieldPacksAdmin`), `api/cmd/server/main.go` (import +
`/field-packs` mount), `web/src/hooks/use-field-packs.ts` (new),
`web/src/routes/admin-field-packs.tsx` (new), `web/src/app.tsx` (route),
`web/src/components/layout/app-sidebar.tsx` (nav item).

### Slice 4 addendum — device auto-clear of synced edits + expandable pack card (code-complete 2026-08-25, verification owed)

Two follow-ups the user asked for after Slice 4 landed.

**(1) Auto-clear synced overlays/creates on a fresh pack download (mobile).** Closes the
Slice 3e open follow-up: a create that's been synced kept rendering as its green local
overlay because its `edits.sqlite` row was retained (the pack snapshot predated the edit).
The subtlety is that **each pack has its own `edits.sqlite`** — a re-download is a *new*
pack id with a fresh empty one — so the stale row to clean up always sits in the *prior*
pack's dir, not the freshly downloaded one. Within a single pack there is structurally
nothing to clear (its own snapshot can never contain an edit made against it). So the
reconcile is deliberately **cross-pack and membership-checked**: after `PackStore.download`
writes the new pack, `reconcileSyncedEdits(freshPack)` sweeps every *other* downloaded pack
and, for each, drops the overlay/create + synced outbox history of any feature that (a) is
fully synced — has an acked op and **nothing pending** — and (b) actually exists in the
fresh pack's `data.sqlite`. Pending (unpushed) work is never eligible; an unrelated-region
pack keeps a synced edit the new pack doesn't cover. Best-effort: a reconcile failure never
fails a good download.

New primitives: `EditDatabase.syncedFeatureIds()` (fully-synced, no-pending ids) +
`EditDatabase.dropSynced(ids)` (delete overlays/creates + synced outbox rows for those ids);
`PackDatabase.existingFeatureIds(ids)` (index-backed membership check); the coordinator
`PackStore.reconcileSyncedEdits(pack)` called at the tail of `download()`. No server change.
Files: `mobile/lib/src/edit/edit_database.dart`, `mobile/lib/src/pack/pack_database.dart`,
`mobile/lib/src/pack/pack_store.dart`.

**(2) Expandable pack card on the web (contents on click).** The admin pack card was a
summary line only; clicking it now expands an inline read-only detail panel (`PackDetails`)
showing owner name+email, status/hidden, the full feature-class list, feature count, tile-set
and data-pack sizes broken out, the geographic extent (bbox as a lon/lat range), and the
snapshot/requested/finished timeline — plus the failure reason for a failed build. A chevron
marks expand state; the header toggles on click while the disable/delete action buttons
`stopPropagation` so they don't also toggle. File: `web/src/routes/admin-field-packs.tsx`.

Verification owed (Bash blocked here): `flutter analyze` / `flutter run` for the mobile
reconcile (create → sync → re-download a pack covering the feature → green tint gone; a
pack of a different region keeps its synced create), and `pnpm build` / a browser check for
the expandable card.

## Slice 5 — map controls fix + GPS placement + settings page (code-complete 2026-08-25, verification owed)

Three map asks the user raised together. All mobile-only; no server change.

**5a — the "north button" that was always missing, and control overlap.** MapLibre's
*native* compass auto-hides at bearing 0 (due north), so it was invisible almost always,
and when it did appear it fought the attribute panel and the Create FAB for the same
corner. Fix: disable the native compass (`compassEnabled: false`), turn on
`trackCameraPosition: true`, and draw our **own always-visible control cluster** in
`PackMapView` — a `_MapControls` column (top-right, inside a `SafeArea`) with a compass
button (a `Icons.navigation` glyph rotated by `-bearing` that taps to reset north via
`CameraUpdate.bearingTo(0)`), a recenter button, and a my-location button. Bearing is read
from a controller listener (`_onCameraChanged`, `setState` only on a ≥0.5° change) added in
`_onMapCreated` and removed in `dispose`. The controls hide while drawing so they don't
overlap the draw banner. Separately, the **Create FAB moved out of the Scaffold** into the
map region's own `Stack` (`_mapArea`, bottom-right, gated by `_showCreateFab`): on a tablet
it floats over the map not the side panel; on a phone the bottom sheet draws over it.

**5b — GPS placement ("use my position or place it on the map?").** New
`geolocator: ^13.0.1` dep + `mobile/lib/src/geo/location_service.dart` (`LocationService.current()`
→ `LocationResult`, collapsing service-off / denied / deniedForever / 15s-timeout into one
message; foreground-only, single fix, never background). Native perms added:
`ACCESS_FINE/COARSE_LOCATION` (Android manifest) + `NSLocationWhenInUseUsageDescription`
(iOS Info.plist). The page owns GPS; the map takes a reactive `centerOn` param (mirrors
`draftPoint`) and animates whenever it changes. Two entry points, so the user keeps the
choice: the map's my-location button (`_locateMe` → recenter only), and the draw banner's
"use my location" button (`_placeAtMyLocation` → drop the draft point at the fix *and*
recenter), alongside the existing tap-to-place. `_fetchFix()` is the shared fetch that
flips the `_locating` spinner and snackbars any failure.

**5b live-location addendum — the blue dot ("shouldn't the user see their position, a
pulsing blue dot?").** Beyond the on-demand fix above, the map now shows MapLibre's native
**location puck** (the live pulsing blue dot + accuracy ring) that tracks the device as it
moves. `PackMapView` gained a `showUserLocation` flag driving the MapLibreMap options
`myLocationEnabled` (+ `myLocationRenderMode: normal` = the standard dot,
`myLocationTrackingMode: none` so the dot never hijacks the camera — panning stays free and
the my-location button is what recenters). The puck's location engine needs foreground
permission, so `PackMapPage` only flips it on once permission is granted: new
`LocationService.ensurePermission()` (requests once, returns granted, fetches no fix) is
called at page open (`_initUserLocation`), and `_fetchFix()` also flips it on after any
successful fix. A denial just leaves the dot off — the user can still tap my-location, which
re-requests and turns the dot on on success. Reuses the same `ACCESS_FINE/COARSE_LOCATION`
perms from 5b; the flag toggles live via the MapLibreMap widget's reactive option diff (no
restyle needed).

**5c — map settings page.** New `mobile/lib/src/ui/map_settings_screen.dart`
(`MapSettingsScreen`, a `ConsumerStatefulWidget`) pushed from a new settings-gear
`IconButton` in the map app bar (`_openSettings`). Sections: **Basemap** — a selectable
provider list (see the basemap addendum below). **Editing** — a `SwitchListTile`
**Snapping** toggle, honestly labeled cosmetic (persisted via
`SettingsRepository.snappingEnabled` / `setSnappingEnabled` in `ve_settings.json`, but no
edit path reads it until move/line editing lands). **Features on the map** — live
(`ListenableBuilder` on the `EditStore`) tallies: loaded-from-pack (sum of class counts),
New = `EditStore.createdCount`, Modified = `EditStore.modifiedCount`, Deleted = 0 with a
"not yet supported" note (delete isn't a slice yet). **Loaded by class** — the per-class
inventory from `PackDatabase.classCounts()`. New `EditStore.createdCount` / `modifiedCount`
getters back the tallies.

**5c basemap addendum — selectable online basemap (OSM / Carto), mirroring the web.** The
user asked the settings Basemap section to actually pick a provider "just like the web
system." New `mobile/lib/src/map/basemaps.dart` = a `Basemap` catalogue mirroring the web's
list (`web/src/stores/basemap-store.ts` + `web/src/lib/basemaps.ts`): **OpenStreetMap**
(`https://tile.openstreetmap.org/{z}/{x}/{y}.png`, maxzoom 19) and **Carto Voyager**
(`https://basemaps.cartocdn.com/rastertiles/voyager/{z}/{x}/{y}.png`, maxzoom 20) — both
keyless raster XYZ — plus **None** (pack-only). ids `osm` / `carto-voyager` match the web;
the web's `blank` is `none` here. **Default = `none`** (differs from web's `osm`) because
the field tool is offline-first: an online basemap is opt-in, and when offline its tiles
just don't load while the pack still renders on top. `PackStyle.build` gained a `Basemap?`
param and injects one raster source + a raster layer directly above the background /
beneath all pack layers when a provider is chosen. `SettingsRepository` persists `basemapId`
(default `none`) via `setBasemapId`. The picker is a `RadioListTile` list in the settings
Basemap section showing each provider's attribution. Live switching without losing the
camera: `PackMapView` takes a `basemapId`; on change (`didUpdateWidget`) `_applyBasemap`
rebuilds the style and calls maplibre_gl's `controller.setStyle(json)` (0.27's dedicated
API — reloads + re-fires `onStyleLoaded`, which re-adds the create/edit/selection/draft
overlays); a new `_framedOnce` guard stops that reload from re-framing to the pack extent,
so the user's pan/zoom survives. `_style` (the boot style backing the widget's stable
`styleString` + hit-test layer ids, both basemap-independent) is deliberately NOT
reassigned, so the widget never double-reloads. `PackMapPage` holds `_basemapId` (init from
settings), passes it down, and re-reads it when the settings page closes to apply a change
live. `INTERNET` permission added to the main `AndroidManifest.xml` (was only auto-added in
debug; online basemaps + the loopback tile server + API/sync all need it in release too).

Verification owed (Bash blocked here): `flutter pub get` (new geolocator dep) + `flutter
analyze` / `flutter run` — compass shows and resets north, controls clear of the panel/FAB,
my-location recenters and places (native perm prompt on first use), and the settings page
reads the right counts and persists the snapping toggle across a restart.

## Slice 6 — metadata-driven symbology on the map (code-complete 2026-08-26)

Until now the on-device map colored each class by a placeholder per-code palette hash
(`pack_style.dart`'s `_colorForCode`) — every feature of a class one flat color, ignoring
`metadata.symbology_rules` entirely. This slice brings the real symbology into the pack and
onto the map, so a feature is styled by its subtype and attributes exactly as the web map
styles it (`web/src/lib/symbology.ts` → `map-layers.ts`), just resolved through MapLibre
data-driven paint instead of a per-feature OpenLayers style function.

**How the web resolves symbology (the thing we replicate).** Martin never sees a symbology
rule; every styling decision is client-side. The schema endpoint pre-resolves each rule's
ids into codes/field-names (`api/cmd/server/main.go`'s `resolveSymbologySchema`: `subtype_id`
→ subtype code, `domain_value_id` → the field *name* to compare + the value *code*), and
`resolveSymbology` picks the **most specific matching rule** per feature — (subtype +
domain_value) > subtype alone > domain_value alone > the mandatory class-level default —
turning its `style_def` into color/width/dash/fill/stroke. A field map (OpenLayers) can call
that per feature; a tiled MapLibre map can't, so we compile the same precedence into paint
expressions.

**Pack side (`internal/fieldpack/builder.go`, pack schema v3 → v4).** New `symbology` table
(`feature_class, subtype_code, domain_field, domain_value_code, min_zoom, max_zoom,
style_def`) + a `symbology_class_idx`. New `loadClassSymbology` mirrors
`resolveSymbologySchema` **verbatim** under the same pack snapshot tx: it loads a class's
rules (`metadata.LoadSymbologyRules`), builds `subtypeCodeByID` from `loaded.Subtypes`,
resolves each `domain_value_id` to `(field name via loaded.Fields' DomainID, value code)`,
and writes one row per rule — **filtered to `render_context = "map"`** (the field app draws
no diagram, so diagram rules never ship). It returns the set of domain field *names* any
rule keys off; `writeFeature` now folds **only those** attribute values into the tile
properties (kept minimal so tiles stay small), because MapLibre's data-driven paint can only
match on `['get', field]` if that field is actually in the tile. `subtype` was already in the
tile props. Manifest gains `symbology_count`; the build log line reports it.

**Device side.** `PackDatabase.symbologyByClass()` reads the whole `symbology` table grouped
by class into ported `SymbologyRule` / `StyleDef` models (a pre-v4 pack with no table
degrades to the placeholder colors — the missing-table `SqliteException` is swallowed exactly
as `domainValuesFor` does for pre-v2). `PackStyle.build` takes that map and, per class,
compiles each paint property into a MapLibre value via `_paintValue`: rules sorted
most-specific-first (`_sortedRules`, an explicit index tiebreaker since Dart's `List.sort`
isn't stable), each rule that sets the property contributing a `['case', <predicate>,
<value>, …, <default>]` branch, the class-level default rule supplying the fallback. A
predicate (`_predicate`) is `['==', ['get','subtype'], code]` and/or `['==', ['to-string',
['get', field]], valueCode]` (`to-string` coerces since domain codes are strings but a raw
attribute may be numeric); `case` picking the first matching branch reproduces
most-specific-wins. Applied to `line-color`/`line-width`, `fill-color`/outline
`line-color`/`line-width`, and point `circle-color`/`circle-radius` (radius honoring
`style_def.scale`). The `_colorForCode` palette is kept purely as the per-class **fallback**
so unseeded classes stay distinct. Both `PackStyle.build` call sites (`pack_map_view.dart`
boot + `_applyBasemap`) pass `symbology: widget.db.symbologyByClass()`; the amber-dirty and
green-create overlays stay flat (they signal edit state, not symbology).

**Deferred (documented v1 limitations, not bugs).** (a) **Icons** — `style_def.icon_ref`
isn't rendered; the pack ships no sprite, so a point with an icon rule falls back to a
colored circle. A sprite pipeline is a follow-on — **now done in Slice 7 below.** (b)
**Dash** — `line-dasharray` is
**not** data-driven in MapLibre GL (can't vary per feature via `['get']`), so dash patterns
(overhead vs underground) aren't applied on-device; correct support needs splitting a class's
line layer into per-dash sub-layers with `filter`s, deferred. **— now done in Slice 18 below.**
(c) **Zoom bounds** —
`min_zoom`/`max_zoom` are shipped in the table but not enforced: MapLibre can't gate a
data-driven branch on `['zoom']` in the same expression, so a zoom-restricted rule applies at
every zoom. Most seed rules omit zoom, and field packs span a narrow zoom range, so this is
low-impact; revisit if a seeded rule visibly misbehaves. **— now done in Slice 18 below**
(via per-rule sub-layers with layer-level `minzoom`/`maxzoom`).

**REQUIRES a v4 pack re-download** to take effect (an old v3 pack has no `symbology` table and
renders with the placeholder colors, harmlessly). Verification owed (Bash blocked here): `go
build ./...` for the builder; rebuild a pack and confirm `symbology_count > 0` in the log +
the `symbology` table populated; `flutter analyze` / `flutter run` and confirm features draw
in their metadata colors (e.g. an MV conductor styled by voltage/subtype) rather than one flat
per-class color, and that a pre-v4 pack still renders.

## Slice 7 — point icons on the map (code-complete 2026-08-26)

Slice 6 rendered symbology **colors** but every point still drew as a circle: `style_def
.icon_ref` (square transformer, hollow open switch, diamond service point) was documented as
deferred. This slice renders points as their real shapes, closing the biggest remaining visual
gap vs the web — where a point is an SVG from the `symbology-icons.ts` fallback registry.

**Pure frontend — no builder, no schema, no re-download.** `icon_ref` already ships inside
`symbology.style_def` (Slice 6's v4 table), so nothing on the Go/pack side changes. The whole
slice is Dart. (A v4 pack is still the prerequisite, same as Slice 6.)

**Why a raster atlas, not a sprite/SDF.** MapLibre GL renders tiles on the GPU, so it can't run
a per-feature Dart draw callback the way OpenLayers does on the web. Icons must be **registered
images** the style names. The web's icon vocabulary is a small fixed shape set
(circle/square/triangle/triangle-outline/diamond), so rather than ship SVG + a rasterizer dep
(`flutter_svg`) or generate SDFs (needed only for runtime tinting), each **(shape, color)** pair
is drawn once with `dart:ui` Canvas — color baked into the pixels — and registered via
`controller.addImage(name, bytes)`. No new dependency.

**`mobile/lib/src/map/icon_atlas.dart` (new).** Draws the five shapes from the web fallback
registry's exact 24-unit viewBox coordinates (so field and web read identically), each with a
thin white halo for legibility over a dark basemap (the same reason the old circle layer carried
a white stroke). `triangle-outline` is stroked, not filled — a genuinely hollow open-switch
silhouette. `rasterizeIcon(shape, color)` → PNG `Uint8List`; `iconName(shape, color)` is the
stable registration key used on both sides of the wire; `normalizeShape` clamps an unknown
`icon_ref` (a typo, or a DB-authored SVG icon the pack can't ship yet) to a circle so a bad ref
never makes a feature vanish. `IconSpec` (name + shape + color) is the record the style hands to
the view.

**`pack_style.dart`.** Point classes now emit a **symbol layer** (`type: symbol`) instead of a
circle layer. `icon-image` is a data-driven `case` over the *same* rule predicates as the colors
(`_iconImageValue`, mirroring `_paintValue`): most-specific-first, each branch resolving to an
`iconName(shape, color)` — the rule's `icon_ref` (unknown → circle) in its color, or a circle in
the class fallback color where a rule sets no color. So **every** point still draws, and a
pre-symbology / iconless class looks like the old colored dot (now a drawn circle). `icon-size`
honors `style_def.scale` off a base constant (`kIconBaseScale`, paired with the atlas's
`kIconDrawSize` — drawn large for crispness, scaled down on screen); `icon-allow-overlap` +
`icon-ignore-placement` are on, since field data is dense and an icon must never be dropped for
de-cluttering (which would also make it untappable). `PackStyle` gained an `icons` list — every
distinct `IconSpec` the expressions reference, deduped by name.

**`pack_map_view.dart`.** New `_registerIcons()` rasterizes and `addImage`s exactly the set in
`PackStyle.icons`. It runs FIRST in `_onStyleLoaded` — MapLibre draws nothing for an
`icon-image` whose bytes it hasn't been handed, and a style reload (basemap swap) clears
registered images, so registration must precede everything and re-run on every style load
(`_onStyleLoaded` fires again after `setStyle`). Best-effort per image so one bad icon can't
blank the map. The point layer id (`pack-<code>`) is unchanged, so hit-testing / the queryable
layer set is untouched (symbol layers are queryable; with overlap+ignore-placement on, all icons
place and stay tappable). The amber-dirty / green-create overlays are untouched — they signal
edit state, not symbology.

**Deferred (from Slice 6).** DB-authored custom SVG icons (`metadata.symbology_icons`, beyond
the five built-in shapes) fall back to a circle — shipping arbitrary SVG would need a rasterizer
dep this slice avoided. **Addressed in Slice 8 below.** Dash and zoom-bounds remain as documented
in Slice 6.

**Verification owed (Bash blocked here):** `flutter analyze` (new file + the symbol-layer/style
changes) and `flutter run` on a device with a **v4** pack — confirm points draw as their real
shapes (a `DIST_XFMR` square, a `DIST_SWITCH` open state as a hollow red triangle, a
`SERVICE_POINT` diamond), that taps still select, and that a basemap swap re-registers the icons
(they don't disappear after switching basemap).

## Slice 8 — DB-authored custom icons in the pack (code-complete 2026-08-26)

Slice 7 drew the five **built-in** shapes but any **admin-authored** icon in
`metadata.symbology_icons` (migration 0023 — the transformer's double-circle winding symbol, etc.)
still fell back to a circle: the field app couldn't render SVG. This slice ships those SVGs in the
pack and rasterizes them on-device, so the field map matches the web for **any** icon, present or
future — the last piece of the symbology story.

**Why this needed a schema bump + a dep (unlike Slice 7).** The custom SVG markup lives only in
`metadata.symbology_icons` server-side; the web fetches it (`use-symbology-icons.ts`) and renders
it as an SVG. To do the same offline, the field app has to (a) carry the markup in the pack and (b)
rasterize SVG. That's a pack table (schema v5) plus a rasterizer dep (`flutter_svg`) — exactly the
two costs Slice 7 avoided by hand-drawing shapes. Confirmed with the user before taking them on.

**Pack side (`internal/fieldpack/builder.go`, schema v4 → v5).** New `icons` table
(`icon_ref TEXT PRIMARY KEY, svg TEXT NOT NULL`). `loadClassSymbology` now also collects each
map-context rule's `style_def.icon_ref` into a shared `iconRefs` set (parsed from the rule's
`style_def` JSON); after the class loop, `writePackIcons` selects exactly those refs from
`metadata.symbology_icons` and writes them to the pack. Only **referenced** icons ship (an admin
icon set can grow without bloating every pack); a ref with no row is simply absent and the device
falls back to a built-in shape. Manifest gains `icon_count`; the build log reports it. The five
seeded shapes (`seed/symbology_icons.sql`) are themselves rows here, so a seeded pack now renders
its circles/squares/etc. from the DB SVG (identical to the web) rather than the Canvas fallback —
the fallback is purely for a ref the pack didn't ship.

**Device side.** `PackDatabase.iconSvgByRef()` reads the `icons` table into `{icon_ref: svg}`
(pre-v5 pack with no table → `{}` swallowed, same pattern as `symbologyByClass`). `icon_atlas.dart`
gains `rasterizeSvg(svg, color)` — substitutes the rule's color into the SVG's `{{COLOR}}`
placeholder (exactly as the web's `iconDataUri` does), renders it off-screen with `flutter_svg`
(`vg.loadPicture(SvgStringLoader(...))` → `Picture`), scaled to fit the icon box preserving aspect,
to PNG bytes. `IconSpec` now carries the raw `iconRef` + an optional `svg`; `rasterizeIconSpec`
dispatches: SVG when the pack shipped one for the ref, else the built-in Canvas shape. `iconName`
now keys on the **raw** ref (not a normalized shape) so two distinct custom icons never collide on
one image name. `pack_style.dart`'s `_iconImageValue` takes the `iconSvg` map and stamps each
`IconSpec` with `iconSvg[ref]`. Both `PackStyle.build` call sites pass
`iconSvg: widget.db.iconSvgByRef()`; `_registerIcons` calls `rasterizeIconSpec`. The map's
`icon-image` expression and layer wiring are unchanged from Slice 7 — only the bytes behind a name
differ (real SVG vs drawn shape).

**Fallback chain (so a point always draws):** pack-shipped SVG for the ref → built-in Canvas shape
(five shapes) → circle (unknown ref). A pre-v5 pack (no `icons` table) takes the middle path for
every ref, i.e. renders exactly as Slice 7 did.

**REQUIRES a v5 pack re-download** to take effect (a v4 pack has no `icons` table → custom refs
draw as a built-in circle, harmlessly). **Verification owed (Bash blocked here):** `go build ./...`
(builder); rebuild fieldsync + build a v5 pack and confirm `icon_count > 0` in the log + the
`icons` table populated; `flutter pub get` (pulls `flutter_svg`) / `flutter analyze` / `flutter run`
on a device with a **v5** pack — confirm the transformer draws as its real double-circle symbol
(not a circle) and taps still select. **One API risk to check at analyze time:** `vg` /
`SvgStringLoader` / `PictureInfo` are used via the `flutter_svg` import (its re-export of
`vector_graphics`); if `vg` doesn't resolve, add `import 'package:vector_graphics/vector_graphics.dart';`
to `icon_atlas.dart` (and `vector_graphics` to pubspec).

## Slice 9 — offline line CREATE: trace a multi-vertex line → form → outbox (code-complete 2026-08-27, verification owed)

Slice 3d shipped **point** create; packed **lines** already render/tap/edit but couldn't be
**drawn**. This slice adds drawing a new line feature offline. It's the create-first half of the
long-deferred "line create/move" pair (move — redraw-from-scratch — is the next slice); scope split
create-first for a smaller, verifiable change.

**Mobile-only — the server needs nothing.** `crud.CreateFeatureInput.Geometry` already validates a
`LineString` for a line class (`validateGeometry`), and a create rides the existing `create` outbox
op through the shared `crud.CreateFeature` on `POST /sync`. So no Go, migration, or pack-schema
change — a line create is exactly a point create with `LineString` coordinates instead of `Point`.

**The two real gaps were the draft overlay and the draft state.** Everything else was already
geometry-agnostic: the green `creates` overlay renders LineStrings (its line layer + geometry-type
filters predate this), the tap query already includes `_createsLine`, `FeatureCreateView` only
touches subtype/name/attributes, and `WkbGeometry.fromGeoJson` + `geometryOverride` already carry a
created line to the map. What was missing: (a) the draft overlay drew only a single point, and (b)
the page tracked only a single `_draftPoint`.

**Draw state (`pack_map_page.dart`).** New `List<LatLng> _draftVertices` (grows tap by tap) and a
`_createIsLine` getter (`_createClass?.geometryType == 'line'`). The class picker
(`_pickCreateClass`) now lists **point AND line** classes as selectable (polygons stay disabled
"coming soon"). `_onPointPlaced` branches: a line **appends** a vertex, a point sets/moves the single
draft. New `_undoVertex` pops the last vertex. `_placeAtMyLocation` appends the GPS fix as a vertex
for a line (drops the point otherwise). `_confirmPlacement` gates on **≥2 vertices** for a line (a
point just needs to be dropped). `_saveCreate` builds a `LineString` from the vertices (or a `Point`
as before). Re-opening an un-synced create to edit its attributes (`_startEdit`) re-seeds
`_draftVertices` from the stored geometry via new `_lineOf` (the create form edits attributes only,
so Save rebuilds the same shape). All the create-flow resets (`_startCreate`/`_cancelCreate`/save)
clear `_draftVertices`.

**Draw banner.** Line variant: "Tap to add points (N)…", an **Undo** button (line-only), Place
enabled at ≥2 vertices; "use my location" adds a vertex. The point variant is unchanged.

**Draft overlay (`pack_map_view.dart`).** New `draftLine` prop (a `List<LatLng>`). `_onStyleLoaded`
adds a `draft-line` LineLayer (blue, filtered to `LineString`) below the existing `draft-circle`,
and the circle layer is now filtered to `Point` so it marks **vertices** only (not the line's path).
`_applyDraft` renders either the single point or the traced polyline **plus a marker per vertex**;
`didUpdateWidget` repaints when `draftLine` changes (compared with a new `_sameLatLngs`). **Gotcha
handled:** the page passes a **fresh copy** (`List<LatLng>.of(_draftVertices)`) each build — handing
the same mutated instance would make `old.draftLine == widget.draftLine` and defeat the diff, so the
preview would never repaint.

**No re-download / no new dep.** A current (v5) pack already renders created lines. **Verification
owed (Bash blocked here):** `flutter analyze` / `flutter run` — create a line class, tap several
points (see the blue polyline + vertex dots grow), Undo, Place, fill the form, Create → the line
draws green and is tappable; open Pending changes → Sync → `POST /sync` applies it as a `LineString`
create; re-select and edit its attributes in place (geometry preserved).

## Slice 10 — offline geometry (vertex) editor: move / add / remove / modify vertices for points & lines (code-complete 2026-08-27, verification owed)

**What it does.** Modify the geometry of an *existing packed* feature, fully offline: drag a vertex to
move it, tap a white midpoint to insert a vertex, select a vertex and delete it, all with Undo — for
both **points** (a single draggable handle) and **lines** (handles + midpoints + a blue connector).
One slice, per the user's "Full vertex editor" choice (move + add + remove + modify, points and lines).

**Mobile-only — no server / pack / schema change.** A geometry move rides the *same*
`update_attrs` outbox op as an attribute edit: `crud.UpdateFeatureInput` already carries an optional
`geometry` (`json:"geometry,omitempty"`, applied when `len(in.Geometry) > 0`), and `sync.go`'s
`applySyncOp` unmarshals `update_attrs` into it. So `POST /sync` replays a move with zero new write
logic. A current pack (any version) works — nothing to re-download.

**Persistence (`edit_database.dart` / `edit_store.dart`).** The `overlays` table gains a `geom_json
TEXT` column (idempotent `ALTER TABLE` guarded by a new `_hasColumn` PRAGMA check, so an older
`edits.sqlite` upgrades in place). `FeatureOverlay` gains `geomJson` (the moved GeoJSON geometry) +
`hasGeomEdit`. `putAttrEdit` → renamed **`putEdit`** and now writes both the attr delta *and*
`geomJson` in one `INSERT OR REPLACE` (the caller always re-passes the full desired state, since
REPLACE overwrites the row). An overlay can now carry an attr delta, a geometry move, or **both** in
one row + one op; the payload folds `{if delta 'attributes', if geomJson 'geometry'}`. `EditStore`
adds **`saveGeometryEdit`** (preserves any pending attr delta), **`movedIds`** (features with a
`geomJson`, which must be hidden from the base tiles and redrawn), and **`geomEditFor(id)`**. The
overlay is dropped only when a save leaves *nothing* (empty delta **and** no move).

**The base-hide trick (`pack_map_view.dart`).** A moved feature's original shape is baked into the
vector tiles, so to show the new position we *hide the original from every base pack layer* via
`controller.setFilter(layerId, ['!', ['in', ['get','id'], ['literal', hiddenIds]]])` over
`PackStyle.queryableLayerIds` (empty ids → `!in(id,[])` = shows everything, no clear needed). *As of
Slice 18* the per-rule symbology sub-layers carry their own base filter, so `_applyHidden` now **composes**
`['all', base, hide]` rather than overwriting — see that slice. The moved shape is
then drawn from the amber `dirty` overlay: lines/polys via the existing `edits-line`/`edits-fill`, and
a **new `edits-moved-circle`** filled amber point layer (filter gates on a `moved` feature property)
so a moved *point* — whose hollow attribute-edit ring would have nothing under it — still shows a
marker. All `edits-*` layers are added to the tap query so a moved (hidden-from-base) feature stays
tappable. Convention: **moved features render amber until the next re-pack** regardless of sync state
(same "local truth outlives the snapshot" precedent as a synced create staying green).

**The editor surface (`pack_map_view.dart`).** New widget inputs: `movedIds`, `hiddenIds`, `editing`,
`editVertices`, `editIsLine`, `selectedVertex`, and callbacks `onVertexDragged` / `onVertexTapped` /
`onMidpointTapped`. Vertices are **draggable `Circle` annotations** (blue, red when selected);
midpoints are non-draggable white circles between consecutive vertices (lines only). Each circle
carries `data: {'kind':'v'|'m', 'i': index}`. Registered once in `_onMapCreated`:
`controller.onFeatureDrag.add(_onFeatureDrag)` (commits on `DragEventType.end` only — no live
rubber-banding) + `controller.onCircleTapped.add(_onCircleTapped)` (a `'v'` tap selects, an `'m'` tap
inserts at `circle.options.geometry`); both removed in `dispose`. `_syncHandles` rebuilds the handles
(reentrancy-guarded — a drag fires a rebuild mid-flight — via `_handlesBusy`/`_handlesDirty`, and
re-runs after any style reload since annotations are cleared). A line also gets a blue `editgeom-line`
connector (`_applyEditGeom`). A map tap is a no-op while `editing` (interaction is the handles).
maplibre_gl 0.27.0 defaults have `AnnotationType.circle` in `annotationOrder` + `dragEnabled: true`,
so `addCircle` + drag work out of the box; `DragEventType` is a public enum reachable via the barrel's
`part 'src/global.dart'`.

**The page wiring (`pack_map_page.dart`).** Geom-edit state: `_editGeomId` (non-null ⇒ editor open),
`_editGeomClass`, `_editGeomIsLine`, `_editVertices`, `_selectedVertex`, `_geomUndo` (a stack of
vertex snapshots). `_canEditGeometry` gates to a **packed** (not created) Point/LineString feature — a
new **"Edit geometry"** header button (`Icons.edit_location_alt_outlined`) enters via `_startGeomEdit`,
which seeds vertices from the current move (`geomEditFor`) else the packed geometry. Handlers:
`_onVertexDragged` (snapshot + set), `_onVertexTapped` (toggle-select), `_onMidpointInserted`
(insert + select the new vertex), `_deleteSelectedVertex` (line, keeps ≥2), `_undoGeom`,
`_cancelGeomEdit`, `_saveGeomEdit` (builds the GeoJSON, calls `saveGeometryEdit`, re-selects with the
override). A **`_geomEditBar`** floats over the map's bottom (hint text + Undo / Delete-vertex /
Cancel / Save); the detail panel + Create FAB hide while `_editingGeom`. `_withOverride` (applied in
`_select` and `_dirtyFeatures`) wraps a record so its highlight/overlay draws the moved shape via
`geometryOverride = WkbGeometry.fromGeoJson(moved)`. `_hiddenIds()` = `movedIds ∪ {_editGeomId}`.
`_map()` passes a **fresh `List.of` copy** of `_editVertices` (like `draftLine`) so the map's
old-vs-new diff isn't defeated by a mutated-in-place instance.

**No re-download / no new dep.** **Verification owed (Bash blocked here):** `flutter pub get` /
`flutter analyze` / `flutter run` (+ `go build ./...`) — select a packed point, tap Edit geometry, drag
the handle, Save → the point renders filled-amber at the new spot (original gone), reopens, stays
tappable; select a packed line, drag a vertex, tap a white midpoint to add one, select a vertex and
Delete (blocked at 2), Undo, Save; open Pending changes → Sync → `POST /sync` applies the move via
`update_attrs`; confirm an attribute edit + a move on the same feature push together in one op.

## Slice 11 — sync scale hardening: metadata cache + client batch chunking (code-complete 2026-08-27, verification owed)

**Why.** With 500+ field workers, the `POST /sync` design needed a scale review. The key correction to the
mental model: a worker carries ~1000 features in the pack (the *read/edit universe*), but sync only pushes
the **outbox** — the *dirty set*, one pending op per feature actually created or modified that session. So the
server's load is `workers × changes`, not `workers × pack size`. Two real bottlenecks remained in the code,
both fixed here; a third (async queue) is explicitly deferred as premature.

**Fix 1 — server-side metadata N+1 (`sync.go`).** `applySyncOp` called `metadata.Load` **once per op**, and
`Load` runs ~6 SQL queries ([load.go](../api/internal/metadata/load.go)). A push of N ops of a few classes =
~6N metadata queries. Added a per-request **`metaCache`** (`newMetaCache` / `get`) that memoizes `Load` by
class code across the batch, caching both hits *and* load errors (so a bad class name doesn't re-run the
failing queries per op). Metadata is read-only reference data loaded on the base `db` outside any op's
transaction, so one shared copy across ops is safe — same effective consistency as the REST path (one load
per request). `syncHandler` builds the cache once and threads it through `applySyncOp(ctx, db, cache, op)`.
Behaviour otherwise unchanged: still one transaction per op, still per-op results, still always HTTP 200.

**Fix 2 — client-side batch chunking (`pack_map_page.dart`).** The client sent its **entire** outbox in one
POST ([fieldsync_client.dart](../mobile/lib/src/api/fieldsync_client.dart) `sync`), so one request held a
server DB connection (and one big cellular upload) open over an unbounded op count — the real risk at a
shift-end thundering herd was **connection-pool exhaustion**, not raw row throughput. `_pushOps` now chunks
`ops` into batches of **`_syncBatchSize = 200`**, one `client.sync(batch)` per chunk, and **marks each
batch's acks synced BEFORE the next goes out** — so a mid-way network failure leaves everything already
pushed durably synced and only the remainder queued. The idempotent server (`ON CONFLICT (id) DO NOTHING` +
read-back) makes a later retry of the queued rest a safe no-op for anything that did land. Snackbars now
accumulate `appliedTotal`/`failedTotal` across chunks, and the failure path surfaces partial progress
("Synced 400, then sync failed: …"). Chunking lives in `_pushOps`, so it covers both **Sync-all** and the
per-row **"Push this change"** (a single-op batch). `client.sync` stays a one-request primitive — the page
orchestrates the batching. Added `dart:math` for `math.min`.

**Deliberately deferred (not premature-optimized):** an **async queue** (enqueue on `/sync`, drain via a
`SKIP LOCKED` worker like [phase-17-large-export]'s exportworker) — only worth it if change *volume*, not
concurrency, becomes the wall; Fixes 1–2 address the concurrency ceiling first. Also not added: a hard
server-side op-count cap (the client contract is to chunk; the server stays permissive but now efficient).
Grouping ops by class within a batch falls out of the cache for free. **Owed:** load test to find the actual
connection-pool ceiling before further tuning (back-of-envelope: a 50-op push ≈ sub-second; the old 1000-op
worst case was ~6000 metadata queries + 1000 serial txns in one request — now ~6 metadata queries per class
and chunked into ≤200-op requests).

**No migration / no new dep / no pack change.** **Verification owed (Bash blocked here):** `go build ./...`
(+ `go vet`) for the server; `flutter analyze` / `flutter run` for the client — make >200 edits offline, Sync,
and confirm multiple sequential `/sync` requests each ≤200 ops, progress persists if the network drops
mid-sync, and re-Sync cleanly no-ops the already-applied ops.

## Slice 12 — offline connectivity + relationships: the sync spine (code-complete 2026-08-27, verification owed)

**Scope-expansion note.** This reverses the v1 locked decision *"Connectivity + relationships = read-only
context + offline TRACE in v1 … establish/break stays online. NOT editable offline"* — a deliberate v2 move,
recorded here so the reversal is on the record. Slice 12 is the **server spine only**; the mobile capture UI
(pick two features → confirm → outbox → green edge overlay) is Slices 13 (connectivity) + 14 (relationships).

**Why the spine is cheap.** An establish is the *smallest* write in the system — two feature refs + an optional
terminal/kind, no geometry, no attributes. It rides the exact rails Slices 3e/11 already built: the device's
outbox → chunked `POST /sync` → per-op transaction. The server-side write primitives already exist and take
`bun.IDB` (so they compose inside the per-op tx): `connectivity.EstablishConnection`
([export.go](../api/internal/connectivity/export.go)) and `relationship.EstablishRelationship`
([export.go](../api/internal/relationship/export.go)), both extracted in Phase 18 for the MCP
`establish_connection` / `establish_relationship` tools. So the spine is a near-copy of the existing sync path.

**What changed — [sync.go](../api/internal/fieldpack/sync.go) only.** Two new op kinds on `syncOp.Kind`:
`establish_connection` and `establish_relationship`. `applySyncOp` was restructured to dispatch by kind
*inside* the transaction, loading class metadata via the Slice 11 `metaCache` **only** for the CRUD kinds
(`create` / `update_attrs`) — the establish kinds touch no metadata at all (the engines authorize against
`network_rules` / `relationship_classes` themselves). The op payloads are sync-local snake_case structs
(`establishConnPayload{from,to}` with `syncEndpoint{feature_class,feature_id,terminal?}`;
`establishRelPayload{origin,destination,kind?}` with `syncFeatureRef`) mapped into the exported
`EstablishConnectionInput` / `EstablishRelationshipInput` — kept sync-local because those exported types'
`From`/`To`/`Origin`/`Destination` fields carry no json tags (they'd marshal as capitalized keys), so a clean
mobile wire shape needs the local structs. On success the op result's `feature` carries the committed edge's
ids (`connection_id` + rule, or `relationship_id` + class + cardinality + any `plausibility_warning`).

**Idempotency & auth — free.** Both engines no-op on `ON CONFLICT` (re-pushing an already-established edge
returns the original), so an establish op is retry-safe on the same terms as an idempotent create — the device
marks synced exactly the acked ops and re-sends the rest. The engines stamp `established_by` from
`auth.ActorID(ctx)`; the fieldsync `bearerAuth` middleware attaches the resolved user via `auth.WithUser`
([routes.go:106](../api/internal/fieldpack/routes.go#L106)), so the actor is present on the sync ctx exactly
as it is for create/update. **Permission gate:** unchanged — `/sync` stays behind `bearerAuth` +
`requirePerm("features:write")` (single-gate, chosen with the user 2026-08-27: a field worker who can write
features can also establish edges; the engines' `network_rules` / `relationship_classes` checks still authorize
each edge on replay, and the REST/MCP surfaces keep their finer `connectivity:commit` / `relationships:commit`
gates). No route change, no import cycle (connectivity/relationship don't import fieldpack; the MCP server
already composes all three).

**Deferred to Slices 13/14 (deliberately not in the spine):** shipping `network_rules` +
`relationship_classes` into the pack for **offline validation guidance** (so the UI only offers *legal*
connections) + the Dart port of `matchRule` / `matchClass`. Recommended design: **guide offline + backstop on
sync** — the server's authoritative check already rejects an illegal edge on replay for free, so the pack rules
are purely a UX nicety; they ship in the slice whose UI consumes them (matching how symbology was done). Until
then, capture is **optimistic**: a rejected edge comes back as a per-op `error` in the sync results.

**No migration / no new dep / no pack change.** **Verification owed (Bash blocked here):** `go build ./...` (+
`go vet`) for the server; a hand-crafted `POST /sync` with an `establish_connection` op between two packed
features confirming the edge commits and a re-push no-ops.

## Slice 13 — offline connectivity capture: the mobile data layer (code-complete 2026-08-27, verification owed)

**Split, mirroring 2a→2b.** Slice 13 is the **on-device data spine** for establishing connections offline; the
map/panel UI (Connect mode, terminal pickers, green local-edge overlay, discard sheet, fresh-pack reconcile) is
**Slice 13b**. Keeps each pass statically verifiable while Bash is blocked, and the UI is the larger half.

**Optimistic capture — decided.** No offline rule check in this slice: capture whatever the field worker wires,
and let the Slice 12 server backstop authorize each edge on replay (a rejected one returns as a per-op sync
`error`). Shipping `network_rules` into the pack + the `matchRule` Dart port for *offline guidance* stays
deferred to a focused **13b** (as the Slice 12 note recommended: guide offline, backstop on sync).

**New `connections` table — NOT the outbox dedupe.** [edit_database.dart](../mobile/lib/src/edit/edit_database.dart)
gains a dedicated `connections` table + `LocalConnection` model. This is the key design point: overlays/creates
keep *one* pending op per `(feature, kind)` via a delete-then-insert (LWW "newest intent"), but a feature is the
source of *many* connections and each is a distinct fact — so connections get their own table and a
**non-replacing** insert. The row `id` **is** the `establish_connection` outbox op id, so the rendered edge and
its pending op share one idempotency key (a re-push no-ops server-side). `putConnection` writes the row + the
pending op in one tx; `discardConnection` drops the row + its *un-synced* op; `syncedConnectionIds` /
`dropConnections` are the fresh-pack reconcile primitives (call-site lands in 13b).

**Terminal accessor.** [pack_database.dart](../mobile/lib/src/pack/pack_database.dart) gains `TerminalDef` +
`terminalsFor(code, {subtypeCode})`, reading the pack's already-shipped `metadata.terminals` blob (Go-capitalized
keys). A terminal's `code` is exactly what an endpoint's `terminal` sends — the server resolves it via
`Loaded.ResolveTerminal`, which matches on `Code` scoped by subtype. `terminalsFor` returns generic terminals
(null `SubtypeID`) always and subtype-scoped ones only when they match the feature's subtype (mapping the
terminal's `SubtypeID` UUID → code via the same class's `subtypes` blob). No pack-schema bump — terminals have
shipped since v6.

**Store API.** [edit_store.dart](../mobile/lib/src/edit/edit_store.dart) gains `_connections` (hydrated at open),
`createConnection(fromClass/fromId/fromTerminal?, toClass/toId/toTerminal?)` (mints the UUID, builds the
`{from,to}` payload, `_pending.add(fromId)`), `discardConnection` (recomputes `_pending` from the DB, since the
FROM feature may keep other un-synced work), and reads `connections` / `connectionsInvolving(id)` /
`connectionCount` for the 13b overlay + panel. The outbox/client already forward `establish_connection` ops
generically (`_opToJson` passes kind+payload verbatim), so **no fieldsync_client change** was needed.

**No migration / no new dep / no server change.** **Verification owed (Bash blocked here):** `flutter analyze`
(+ a `flutter test` if one is added for the new DB methods); an on-device smoke of `createConnection` →
`pendingOps()` carrying a well-formed `establish_connection` payload → `POST /sync` accepting it.

## Slice 13b — offline connectivity capture: the map/panel UI (code-complete 2026-08-27, verification owed)

The capture UI over the Slice 13 data spine — no server, migration, or dep change.

**Map overlay.** [pack_map_view.dart](../mobile/lib/src/map/pack_map_view.dart) gains a `ConnectionEdge` type and
three props: `connections` (the local edges), `connecting` (mode flag), `onConnectTarget` (target-tap callback).
A `connections` GeoJSON source feeds a green `connections-line` (LineString) + `connections-circle` (endpoint)
layer, repainted by `_applyConnections()` and diffed in `didUpdateWidget` via `_sameEdges`. In `_onMapClick`,
`connecting` routes the tapped records to `onConnectTarget` instead of selecting.

**Connect flow.** [pack_map_page.dart](../mobile/lib/src/ui/pack_map_page.dart) owns it: the read-only panel
header gains a **Connect** action (`hub_outlined`) → `_startConnect(from)`, which — if the source class has
terminals (`terminalsFor(class, subtypeCode: subtype)`) — pops a terminal picker (`_pickTerminal` → `_TermPick`,
null = dismissed/abort, `_TermPick(null)` = no-specific-terminal), arms `_connectFrom`/`_connectFromTerminal`, and
shows a top `_connectBanner` prompting the target tap. `_onConnectTarget` picks the target (disambiguating a stack
via `_pickPeer`, refusing a self-link), resolves its terminal the same way, then calls
`EditStore.createConnection(...)` and drops back to the panel with a "Connection queued" snackbar. The green edge
overlay is built by `_connectionEdges()` → `_featurePoint(id)` (mean of a feature's WKB/GeoJSON `positions()` —
exact for a point, rough centre for a line; resolves packed **or** offline-created endpoints). The create FAB +
header edit/geometry actions hide while connecting.

**Pending sheet.** `_pendingItems()` now special-cases `establish_connection` ops (`hub_outlined`, green,
subtitle "connect to <target>" resolved via `_connectionForOp(opId)` + `_displayName(id)`) with a Discard that
calls `discardConnection(op.opId)`. The op already flows through `pendingOps()` / the sync batches generically.

**Fresh-pack reconcile.** [pack_store.dart](../mobile/lib/src/pack/pack_store.dart) `reconcileSyncedEdits` gains a
`_reconcileSyncedConnections(fresh, edits)` call, symmetric to features/attachments: it drops a synced offline
connection only once the fresh pack's connectivity graph carries the edge — `EditDatabase.syncedConnectionEdges()`
(id + endpoints) × `PackDatabase.hasConnection(a, b)` (either direction) → `dropConnections(ids)`. A synced edge
the new snapshot predates keeps rendering locally until a later pack covers it; pending edges are never eligible.

**Verification owed (Bash blocked here):** `flutter analyze`; an on-device smoke — Connect from a feature, pick
terminals, tap a target, confirm the green edge draws + the op appears in the pending sheet → `POST /sync` accepts
it → a re-download reconciles the local edge away once the fresh pack's graph carries it.

## Slice 14 — offline relationship capture: the map/panel UI (code-complete 2026-08-27, verification owed)

The relationship sibling of Slice 13/13b — mobile-only, no server/migration/pack/dep change (the `establish_relationship`
crud/engine path already exists from the Slice 12 `POST /sync` spine). Built optimistically **without a kind picker**:
the pack ships only `fields, subtypes, terminals` in its metadata, **not** `relationship_classes`, so there's no
offline "kind" to choose. The server auto-resolves the single relationship_class for the (origin, destination) pair
on replay and rejects an ambiguous/illegal one per-op. A kind picker is a deferred **14b** for when
`relationship_classes` ships into the pack — exactly parallel to how connections deferred rule-guidance.

**Data layer.** [edit_database.dart](../mobile/lib/src/edit/edit_database.dart) gains a `LocalRelationship` model
(`id, originClass/Id, destinationClass/Id, kind?, baseVersion?, createdAt` + `involves`/`otherEnd`) and a
`relationships` table in the edit DB (distinct from the read-only pack's own `relationships` table — a comment flags
it), with `origin_id`/`destination_id` indexes. `putRelationship(r, payload:)` writes the row **plus** a plain
(non-deduping) `establish_relationship` outbox op whose `op_id == r.id` (shared idempotency key; one feature is the
origin of many distinct relationships, so no (feature,kind) replace). Plus `allRelationships`, `discardRelationship`,
`syncedRelationshipEdges` (id + endpoints), `dropRelationships`. [edit_store.dart](../mobile/lib/src/edit/edit_store.dart)
hydrates `_relationships` at open and adds `relationships`/`relationshipCount`/`relationshipsInvolving` +
`createRelationship({originClass/Id, destinationClass/Id, kind?})` (mints a UUID, payload `{origin, destination, kind:?}`,
marks the origin pending) + `discardRelationship`.

**Map overlay.** [pack_map_view.dart](../mobile/lib/src/map/pack_map_view.dart) gains a `RelationshipEdge` type and
three props: `relationships`, `relating`, `onRelateTarget`. A `relationships` GeoJSON source feeds a **dashed purple**
`relationships-line` + `relationships-circle` layer (purple `#bf5af2`, `lineDasharray:[2,2]` so a relationship reads
distinctly from a solid-green connection), repainted by `_applyRelationships()` and diffed via `_sameRelEdges`. In
`_onMapClick`, `relating` routes the tapped records to `onRelateTarget` (a third branch after `connecting`).

**Relate flow.** [pack_map_page.dart](../mobile/lib/src/ui/pack_map_page.dart) panel header gains a **Relate** action
(`account_tree_outlined`) → `_startRelate(from)` (no picker — just arms `_relateFrom` and shows a purple
`_relateBanner`). `_onRelateTarget` picks the destination (disambiguating a stack via `_pickPeer`, refusing a
self-link), calls `EditStore.createRelationship(...)`, and drops back with a "Relationship queued" snackbar. The purple
edge overlay is built by `_relationshipEdges()` → the shared `_featurePoint(id)`. Create FAB + header actions hide while
relating (`!_relating` added to `_showCreateFab` and the header gate); `_startCreate` clears `_relateFrom`.

**Pending sheet.** `_pendingItems()` special-cases `establish_relationship` (`account_tree_outlined`, purple, subtitle
"relate to <destination>" resolved via `_relationshipForOp(opId)` + `_displayName(id)`) with a Discard →
`discardRelationship(op.opId)`. `_PendingItem` gains `relationship`/`relationshipTo`.

**Fresh-pack reconcile.** [pack_store.dart](../mobile/lib/src/pack/pack_store.dart) `reconcileSyncedEdits` gains
`_reconcileSyncedRelationships(fresh, edits)`, symmetric to connections: `syncedRelationshipEdges()` ×
`PackDatabase.hasRelationship(a, b)` (either direction, pack `relationships` table) → `dropRelationships(ids)`. A synced
relationship the new snapshot predates keeps rendering locally until a later pack carries it; pending ones never eligible.

**Verification owed (Bash blocked here):** `flutter analyze`; an on-device smoke — Relate from a feature, tap a
destination, confirm the dashed purple edge draws + the op appears in the pending sheet → `POST /sync` accepts it (server
auto-resolves the class) → a re-download reconciles the local edge away once the fresh pack's relationships graph carries it.

**Deferred 14b:** ship `relationship_classes` (+ `network_rules`) into the pack + a Dart port for a proper offline kind
picker and rule-guidance — the relationship twin of the connections rule-guidance deferral.

## Slice 15 — edit snapping: the engine behind the toggle (code-complete 2026-08-27, verification owed)

Turns the long-cosmetic `snappingEnabled` toggle (Slice 5c: a persisted bool + a `MapSettingsScreen` switch, no
consumer) into a real geometry-editing aid, now that move/line editing exists (Slices 9/10). **Mobile-only, no
server/migration/pack/dep change.** When on, a placed point, a dragged vertex, or an inserted midpoint snaps onto the
nearest **existing feature vertex** within a small screen-space tolerance before it's reported to the parent.

**Engine.** [pack_map_view.dart](../mobile/lib/src/map/pack_map_view.dart) gains `Future<LatLng> _snap(LatLng target)`
and a `_snapPixels = 20.0` tolerance. It reuses the tap machinery: project `target` to screen via
`toScreenLocation`, `queryRenderedFeaturesInRect` a `2·_snapPixels` box around it over the same layer set the tap
query uses (base pack draw layers + green creates + amber edits, so created/moved features are snap targets too),
resolve each hit id → `PackDatabase.featureById(id) ?? _createdById[id]` → `geometry().positions()`, project every
candidate vertex to screen, and keep the closest **within** `_snapPixels`. Distance is measured in **pixels**, so the
tolerance is zoom-independent. Returns `target` unchanged when snapping is off, nothing is in range, or the
controller/style isn't ready — a pure no-op path. The feature under geometry edit is already hidden from those layers
(`hiddenIds`) and excluded from the edits overlay (`_dirtyFeatures` skips `_editGeomId`), so **a vertex never snaps to
its own geometry** — no self-snap guard needed.

**Wiring (three coordinate producers, all in the view).** `_onMapClick`'s `drawing` branch now reports
`await _snap(latLng)` (point create + each traced line vertex). `_onFeatureDrag` (vertex drag, commits on
`DragEventType.end`) fires `_snap(current).then((s) => onVertexDragged(i, s))`. `_onCircleTapped`'s midpoint (`'m'`)
branch fires `_snap(geom).then((s) => onMidpointTapped(i, s))`. The two annotation callbacks are sync `void`, so the
snap resolves fire-and-forget before the report.

**Toggle plumbing.** New `snapEnabled` prop on `PackMapView` (default true). [pack_map_page.dart](../mobile/lib/src/ui/pack_map_page.dart)
adds `_snapEnabled` state, read from `settingsRepositoryProvider.snappingEnabled` at `initState` and **re-read when the
settings page closes** — exactly the `_basemapId` pattern — so flipping the toggle takes effect live without reopening
the map. The stale "Cosmetic for now" copy is replaced in both [settings_repository.dart](../mobile/lib/src/config/settings_repository.dart)
and the [map_settings_screen.dart](../mobile/lib/src/ui/map_settings_screen.dart) subtitle.

**Scope (v1).** Vertex/endpoint snapping only — the highest-value case for a utility network (a new pole lands exactly on
the conductor it sits under; a line end lands on a pole). **Deferred:** snapping to the nearest point *along an edge*
(segment snapping) and snapping to the draft's own prior vertices (close-a-loop). Each candidate vertex costs one
`toScreenLocation` round-trip; fine on place/drop/tap (never per-frame), but a denser segment index would want batching.

**Verification owed (Bash blocked here):** `flutter analyze`; an on-device smoke — with Snapping ON, drag a vertex near
another feature's vertex and confirm it lands exactly on it; place a new point near an existing vertex and confirm the
draft jumps onto it; toggle Snapping OFF in map settings, return, and confirm the same drag/place now keeps the raw
coordinate.

## Slice 16 — visualize the pack network graph on the map (code-complete 2026-08-27, verification owed)

Before this, the pack's authoritative **connectivity + relationship graph** (the `connectivity`/`relationships` tables
in `data.sqlite`) was only ever *listed* in the detail panel (Slice 2c `connectionsFor`/`relationshipsFor` → tappable
"Connections (n)"/"Relationships (n)"); the green/dashed-purple **map** edges drew *only* the links the user established
offline and hadn't synced. So the real network was invisible on the map. This slice draws it. **Mobile-only, no
server/migration/pack/dep change** — the data already ships in the pack, and the overlays already exist (Slices 13b/14).

**Decisions (user, 2026-08-27, via AskUserQuestion):** scope = **on selection** (select a feature → draw its direct
edges to neighbours; cheapest, contextual, reuses the per-feature query; naturally bounded vs the ~200k-edge worst case,
so nothing that scales with 100k touches the render path). Provenance = **unified** (a pack edge and a local-established
edge share one style per kind — green connectivity, dashed-purple relationship — since they merge anyway once synced).
Deferred alternatives: whole-viewport edges (needs viewport-bounded query + edge cap) and baking edges into the vector
tiles at pack-build (server change + rebuild).

**Implementation.** [pack_map_page.dart](../mobile/lib/src/ui/pack_map_page.dart) — `_connectionEdges()` and
`_relationshipEdges()` (which fed the existing overlays from `EditStore` only) now ALSO append, when a feature is
selected, that feature's authoritative edges from the pack graph via the already-present `PackDatabase.connectionsFor`/
`relationshipsFor(sel.id)`. Both provenances go into the same green/purple overlay lists (unified). A new
order-independent `_edgeKey(a,b)` + a per-build `seen` set dedupe a local edge against its pack twin so a pair never
double-draws. Endpoints resolve through the shared `_featurePoint` (packed OR offline-created; unresolvable dropped).
Pack edges are suppressed while editing geometry (`!_editingGeom`) so they don't clutter the vertex handles. Selection
already triggers a rebuild, and the view diffs the edge lists (`_sameEdges`/`_sameRelEdges`), so edges repaint as the
selection moves — including hop-by-hop as the user taps neighbour rows to navigate. No view-side change (the overlays,
layers, and `_map()` wiring are untouched).

**Scope (v1).** On-selection, 1-hop. Deferred: N-hop expansion, whole-viewport mode, and baked-in-tiles rendering.

**Show/hide toggle (code-complete 2026-08-27).** The overlay is now switchable from the map settings page ("Map
overlays → Network links", default on), no longer always-on. Plumbed exactly like the existing snapping/basemap toggles:
`SettingsRepository.showNetworkEdges` (persisted in `ve_settings.json`), read into `_PackMapPageState._showNetworkEdges`
at open and re-read when the settings page closes so a flip applies live. The flag gates **only the on-selection pack-graph
edges** in `_connectionEdges()`/`_relationshipEdges()` — your own offline-established, not-yet-synced connections and
relationships keep drawing regardless (they're unsaved work you shouldn't lose sight of), and every feature's neighbours
stay listed in its detail panel either way. One switch covers both the green (connectivity) and purple (relationship)
overlays.

**Line styling — animated marching ants (code-complete 2026-08-27).** The connectivity line was solid green, which read
like a real conductor. Reworked in [pack_map_view.dart](../mobile/lib/src/map/pack_map_view.dart) so a synthetic edge
looks like an annotation: **dashed + animated** (marching ants), **0.75 opacity**, and a **zoom-interpolated width with a
~2.5px floor** (`['interpolate',['linear'],['zoom'],10,2.5,16,5]`) so it keeps a minimum thickness at every zoom. The
animation uses the documented single-layer MapLibre technique — a `Timer.periodic(80ms)` cycles a 14-frame
`_connDashSeq` and re-applies the layer paint via `MapLibreMapController.setLayerProperties`, which shifts the dash/gap
boundaries so the line appears to flow (reads as "current through the connection"). Because `setLayerProperties` resets
any null property, each tick re-sends the full paint through one `_connLineProps(dash)` builder (also used at
`addLineLayer`). The tick is a no-op when nothing is drawn (`widget.connections.isEmpty`) or the previous update is still
in flight (`_connDashBusy`), so the timer can run for the map's lifetime cheaply; it restarts on every style (re)load
(which re-adds the layer at frame 0) and is cancelled in `dispose`. The relationship line was left as-is (already dashed
purple, already un-conductor-like).

**Verification owed (Bash blocked here):** `flutter analyze`; on-device smoke — select a feature that has pack
connectivity/relationships and confirm green solid + dashed-purple edges draw to its neighbours; tap a neighbour row to
navigate and confirm the edges follow the new selection; deselect and confirm only your own unsynced local edges remain;
confirm a local edge on a pair the pack also carries draws once, not twice.

## Slice 14b — ship the rules into the pack: offline relationship-KIND picker + connectivity guidance (pack schema v6 → v7, code-complete 2026-08-27, verification owed)

Until now the connect flow was **kind-less and check-less** and the relate flow had **no kind picker** (Slice 14's own
comment even noted "the pack doesn't ship relationship_classes"): both established optimistically and left *all* legality
to `POST /sync`. That's still the authority — but the field user got no in-the-field signal, and an ambiguous
relationship (a pair with >1 `relationship_class`) could only be resolved server-side. This slice ships the two rule
tables into the pack so the device can **pick the relationship KIND offline** and **soft-warn before an illegal/disabled
connection**, while keeping the server as the zero-trust backstop ("**guide-offline + backstop-on-sync**").

**Backend — pack schema v6 → v7 ([builder.go](../api/internal/fieldpack/builder.go)).** Two additive tables:
- `relationship_classes (origin_class, destination_class, kind, cardinality, name)` — the `metadata.relationship_classes`
  rows for the packed classes, with the origin/destination **feature-class UUIDs resolved to codes**.
- `network_rules (from_class, from_subtype, from_terminal, to_class, to_subtype, to_terminal, phase_rule, is_valid, name)`
  — the `metadata.network_rules` rows, with class UUIDs → codes, **subtype UUIDs → integer codes**, **terminal UUIDs →
  codes**. A null subtype/terminal stays a wildcard.

UUID→code resolution reuses the per-class `metadata.Load` already run in the build loop: three maps
(`classCodeByID`/`subtypeCodeByID`/`terminalCodeByID`) accumulate as each class loads, then two post-loop loaders
(`loadPackRelationshipClasses`/`loadPackNetworkRules`) query `metadata.*` filtered to `... IN (packed class ids)` on
**both** ends (a rule pointing outside the pack can never be exercised offline, so it's dropped — same reasoning as a
dangling edge). An unresolvable subtype/terminal id degrades to NULL (wildcard) rather than dropping an otherwise-valid
rule. Both loaders are gated on the same scope flags as the graph tables (`IncludeRelationships` / `IncludeConnectivity`)
so RBAC that withheld the graph withholds its rules too. Manifest gains `relationship_class_count` / `network_rule_count`;
the build log gains `relclasses=` / `netrules=`. **Additive & version-tolerant:** no schema gate rejects a version, and
every Dart reader swallows the missing-table error, so a new device reads an old pack and an old device reads a v7 pack
without either crashing.

**Dart — readers + matcher.** [pack_database.dart](../mobile/lib/src/pack/pack_database.dart) gains `RelationshipClassDef`
/ `NetworkRuleDef` models and `relationshipClassesFor(origin, dest)` / `networkRulesFor(from, to)` readers (both
`on SqliteException → const []` for a pre-v7 pack). `relationshipClassesFor` matches the server's **directional**
origin→destination (no swap); `networkRulesFor` returns **both orientations** for the matcher to weigh.
New [network_rules.dart](../mobile/lib/src/pack/network_rules.dart) is a faithful port of
`api/internal/connectivity`'s `matchRuleEitherDirection` + `matchRule`: `checkConnectivity(...)` scores every rule in
either orientation (wildcard-or-exact on the four subtype/terminal axes, most-specific-wins by `_specificity`) and
returns `ConnectivityAdvice.{allowed,disabled,noRule,unknown}` — `unknown` = pre-v7/no rules (proceed silently),
`disabled`/`noRule` = advisory warning. **The phase gate is deliberately NOT ported** (the device doesn't resolve live
phase offline; the server re-checks phase on sync regardless) — a phase-opted rule still matches on its subtype/terminal
shape here.

**Dart — UI ([pack_map_page.dart](../mobile/lib/src/ui/pack_map_page.dart)).**
- **Relate flow** (`_onRelateTarget`): after the destination is picked, resolve the kind from
  `relationshipClassesFor(origin, dest)` — **0** classes → `kind = null` (server auto-resolves or rejects), **1** → its
  kind rides along with no prompt, **>1** → a `_pickRelationshipKind` bottom sheet (mirrors `_pickTerminal`; each row is
  one candidate class with its cardinality as subtitle; dismiss aborts). The chosen kind flows into the already-present
  `EditStore.createRelationship(kind:)` param → the `establish_relationship` op payload.
- **Connect flow** (`_onConnectTarget`): after both terminals resolve, run `checkConnectivity` against
  `networkRulesFor(from, to)`. A warning (`isWarning`) shows `_confirmConnectivityWarning` — "Connection may be
  rejected … Queue anyway?" — where **Back out returns early but keeps connect mode armed** (pick a different target) and
  **Queue anyway** proceeds to `createConnection`. `unknown`/`allowed` proceed silently. The op payload and the server
  path are unchanged; this is purely an in-field heads-up.

**Deferred within 14b:** the phase-compatibility check (offline phase resolution), and surfacing the *reason* string
richer than the two canned messages.

**Prereqs the user runs locally:** `go build ./...` (api) + rebuild a pack (`POST /packs`) so it's v7, then
`flutter analyze` + reinstall. **Verification owed (Bash blocked here):** confirm a rebuilt pack's manifest shows
`schema_version=7` with non-zero `relationship_class_count`/`network_rule_count` for a scope that has them; on-device —
relate two features whose pair has >1 relationship_class and confirm the KIND sheet appears and the chosen kind syncs;
connect two features the packed rules forbid and confirm the soft-warning appears, Back out keeps connect mode, Queue
anyway still queues; connect a legal pair and confirm no warning; confirm an old (v6) pack still opens and both flows
degrade to their pre-14b behaviour (no picker / no warning).

## Slice 15 — delete a feature offline (tombstone + `delete` sync op, code-complete 2026-08-27, verification owed)

The last of the feature-CRUD write surface: a **packed** feature can now be deleted
offline. A delete is a **tombstone** — unlike an overlay (a delta) or a create (a whole
new row), it records only that the feature should be soft-deleted on the server. It
flips the settings "Deleted — not yet supported" tally into a real capability.

**Server (`api`).**
- `crud.DeleteFeature(ctx, db, loaded, id) (map[string]any, error)` — a thin **exported**
  wrapper over the existing (unexported) `softDeleteFeature`, mirroring
  `CreateFeature`/`UpdateFeature` so `POST /sync` has one entry point. Same
  `deleted_at = now()` write, same idempotency (re-deleting a deleted row returns its
  current state), same returned envelope. The web CRUD handler keeps calling
  `softDeleteFeature` directly (in-package).
- `sync.go` gains a `case "delete":` alongside `update_attrs` — loads class metadata via
  the per-batch cache, requires `feature_id`, calls `crud.DeleteFeature`. **No payload**:
  a delete is fully described by `(feature_class, feature_id)`. No new RBAC (soft delete
  rides the endpoint's existing `features:write`), no response-shape change (`syncOpResult`
  already carries the outcome), no idempotency-table change (soft delete is idempotent by
  construction). Op-kind comment updated to list `delete`.

**Client (`mobile`).**
- `edit_database.dart`: a `deletes` tombstone table (`feature_id` PK, `feature_class`,
  `base_version`, `created_at`) + `LocalDelete` model. `putDelete` writes the row and a
  pending `delete` outbox op (empty `{}` payload), and — because **a delete supersedes an
  in-flight attribute edit** — drops the overlay and its pending `update_attrs` op in the
  same tx (replaces, not appends, so re-deleting is idempotent). `discardDelete` undoes an
  un-synced tombstone. `syncedDeletedFeatureIds`/`dropDeletes` are hooks for a future
  reconcile (see deferral).
- `edit_store.dart`: `_deletes` map (hydrated at open), `deletedIds` / `deletedCount` /
  `isDeleted`, `deleteFeature` (writes tombstone, drops the in-memory overlay, marks
  pending) and `undoDelete` (drops it, recomputes pending). Only **packed** features reach
  `deleteFeature`; an offline create is still discarded (`discardCreate`), there being no
  server row to soft-delete.
- The delete op flows through the **existing** sync machinery unchanged — `fieldsync_client`
  serializes any op's `kind` + `payload` verbatim, and `markSynced` stamps it. No sync-client
  or push-flow edits.
- Map/UI (`pack_map_page.dart`): `deletedIds` feed `_hiddenIds()` so a tombstoned feature
  vanishes from the base pack layers (hidden **and never redrawn** — the point of contrast
  with a move, which is hidden and redrawn). The panel header shows a **Delete** button
  (`delete_forever_outlined`) for packed features and, when already tombstoned, swaps the
  whole action cluster for a single **Undo delete** (`restore_from_trash_outlined`). Delete
  confirms via an AlertDialog, then clears the selection. The pending-changes sheet renders
  a `delete` row (red `delete_forever` icon, "· delete" subtitle) with an Undo button.
- `feature_detail_screen.dart`: a `deleted` banner (error-container red) symmetric to the
  `pending` chip, so a re-selected tombstone reads as "marked deleted — undo to keep it".
- `map_settings_screen.dart`: the "Deleted" tally now shows `edit.deletedCount` (red), the
  "not yet supported" note gone.

**Deferred — fresh-pack tombstone auto-pruning.** A synced edit is redundant once the fresh
pack CONTAINS the row; a synced *delete* would be redundant once the pack no longer carries
it — but "absent" also describes a pack that simply doesn't cover the feature's region, and
packs exclude soft-deleted rows, so there's **no positive "confirmed deleted" signal** (unlike
a connection's presence check in `_reconcileSyncedConnections`). Rather than risk wrongly
un-hiding a feature on a scope-miss, a synced tombstone is left in place and cleaned up when
its pack dir is deleted. `syncedDeletedFeatureIds` + `dropDeletes` are the hooks a future
scope-aware reconcile would use.

**Verification owed (Bash blocked here).** `go build ./...` (api) · `flutter analyze` ·
on-device smoke: delete a packed feature → it vanishes from the map + a red "Deleted 1" tally
+ a pending `delete` row; Undo (header + sheet) restores it; sync → server row's `deleted_at`
set, tally stays (tombstone kept), feature stays hidden; re-select a tombstone → deleted banner
+ Undo only; an offline create still shows Discard (not Delete).

## Slice 17 — polygon create + move (code-complete 2026-08-27, verification owed)

Closes the last geometry-shape gap on device: **polygon** feature classes can now be
**created** (trace a ring) and **geometry-edited** (move / add / delete ring corners),
matching what point and line already had (Slices 3d/9 create, Slice 10 vertex editor).
**Mobile-only** — no server, pack-schema, or sync-wire change. The decode
(`geo/wkb.dart` already reads `Polygon`/`MultiPolygon`), persistence
(`EditStore.createFeature`/`saveGeometryEdit` are geometry-agnostic — they store whatever
GeoJSON they're handed), and packed-polygon styling (`pack_style.dart` already has a
`polygon` fill+outline branch) were all already polygon-ready; the work was confined to the
create/edit **UI state + draft/handle rendering**.

**Ring model.** A polygon is stored on device as its **distinct corners** (no closing
duplicate). The closing vertex (first repeated as last) is added **only** when building the
GeoJSON `{'type':'Polygon','coordinates':[ring]}` at commit — so the editor works with N
handles for N corners and N edges (including the ring-closer). Minimums: **3** corners to
place a create or save an edit; delete-vertex keeps **≥3** (a line stays ≥2).

**Create (`pack_map_page.dart`).**
- `_pickCreateClass` now offers polygon classes (pentagon icon) alongside point/line; the
  old "polygon — coming soon" disabled tile is gone (only genuinely-unknown geometry types
  fall to a disabled "not supported" row).
- The point-vs-line boolean grew into `_createIsPolygon` + `_createTracing`
  (`_createIsLine || _createIsPolygon`, both trace vertices) + `_createMinVertices` (2 line /
  3 polygon). Tap-to-add (`_onPointPlaced`), GPS-add (`_placeAtMyLocation`), placement
  (`_confirmPlacement`), the draw banner (message/undo/Place), and `_saveCreate` all switch
  on `_createTracing` and build a closed-ring `Polygon` for a polygon via the shared
  `_closedRing(verts)` helper. Re-opening an un-synced polygon create re-seeds its corners
  from `_ringOf`.

**Vertex editor (`pack_map_page.dart`).**
- `_canEditGeometry` now allows `Polygon`. State grew `_editGeomIsPolygon` + `_editGeomTracing`.
- `_startGeomEdit` seeds corners from the moved-overlay ring (`_ringOf`) or the packed
  geometry (`_verticesOf` gained a `Polygon` branch → outer ring's distinct corners via the
  shared `_cornersOf`, which drops the closing duplicate).
- `_onMidpointInserted` allows the ring-closing segment (segIndex up to `length-1` for a
  polygon, inserting the new corner at the end); `_deleteSelectedVertex` and `_saveGeomEdit`
  enforce the polygon-3 minimum and emit a `Polygon`. Cancel/save reset `_editGeomIsPolygon`.
  The geom-edit bar's hint/delete gate keys off `_editGeomTracing` with a per-shape minimum.

**Draft + handle rendering (`pack_map_view.dart`).**
- New `draftIsPolygon` / `editIsPolygon` params (wired from the page, added to the
  `didUpdateWidget` diffs).
- A **fill** layer added to both the draft source (`draft-fill`) and the edit source
  (`editgeom-fill`), filtered to `Polygon` (translucent blue). The `editgeom-line` layer
  gained a `LineString` filter so it renders only the outline, not the `Polygon` too.
- `_applyDraft` / `_applyEditGeom`: a polygon of 3+ corners emits a **closed-ring `Polygon`**
  (fill) **+ a closed-ring `LineString`** (outline) **+** a marker per corner; below 3 (or a
  line) it stays the open connector as before.
- `_syncHandles`: midpoints now render for polygons too, over **N** segments (vs `N-1` for a
  line) — the extra one is the ring-closing edge (`verts[(i+1) % length]`), so every edge can
  gain a corner.

**Verification owed (Bash blocked here).** `flutter analyze` · on-device smoke: create a
polygon class → trace ≥3 corners (fill + closing outline preview, Place enabled at 3) → form
→ green filled overlay, tappable; edit its geometry → drag a corner, tap a midpoint (incl. the
closing edge) to insert, select+delete a corner (blocked at 3) → Save → amber filled overlay
in the new shape, original hidden; edit a **packed** polygon the same way; re-open an
un-synced polygon create → shape preserved; sync → server upserts the ring (existing
`features:write`, unchanged wire).

## Slice 18 — symbology closeout: dash lines + zoom bounds (code-complete 2026-08-27, verification owed)

Closes the two symbology deferrals carried since Slice 6 — **dash-line rendering** and
**`min_zoom`/`max_zoom` gating**. Both were blocked on the same MapLibre limitation: a
`line-dasharray` **can't be data-driven** (no `['get']`/`case`), and a `['zoom']` expression
**can't nest inside a data-driven `case`**. The fix is one decomposition: render each
line/polygon class as **per-rule sub-layers** instead of one layer with a data-driven `case`,
so each sub-layer is a **constant paint** that can carry its own dash + layer-level
`minzoom`/`maxzoom`. Pure frontend — the pack already ships `dash` + `min_zoom`/`max_zoom`
(schema v4), so **no schema/builder change and no pack re-download** is needed.

**Resolution model — now web-aligned (whole-rule-wins).** The old `_paintValue` fell back
*per-property* across rules (a more-specific rule missing `width` borrowed a less-specific
rule's). The web's `resolveSymbology` instead picks the single most-specific matching rule and
uses its **whole** `style_def`. The sub-layer decomposition adopts that: `_buckets(rules)`
splits a class into mutually-exclusive buckets, one per predicated rule (filter = its predicate
**AND NOT** every more-specific rule's predicate) plus a default bucket (matches whatever no
predicated rule claimed; keeps the base `pack-<code>` id). A feature is drawn by exactly the
bucket of its most-specific matching rule — so color/width/dash/fill/stroke all come from that
one rule, matching the web. (This is a deliberate, subtle behavior change for partial-style
rules; it makes mobile match the source of truth.)

**`pack_style.dart`.**
- `_buckets` / `_Bucket` — the per-rule partition described above.
- Line class → one line layer per bucket; polygon class → a fill + outline pair per bucket.
  Each carries the bucket's `filter`, constant `line-color`/`line-width` (or `fill-color`), and
  a `line-dasharray` when the rule's `dash` is non-solid.
- `_dashArray(dash, width)` — mirrors `web/src/lib/line-dash.ts`'s four patterns
  (solid/dashed/dotted/dashdot) but expressed in **units of line width** (MapLibre's dasharray
  unit, vs OpenLayers' pixels), so a dash looks consistent as width varies. Dotted uses a
  zero-length dash + the layers' round `line-cap` = round dots.
- `_withZoom(rule, layer)` — hangs `minzoom`/`maxzoom` on a sub-layer from the rule's bounds.
  The pack's (and web's) bounds are **inclusive** both ends; MapLibre's `maxzoom` is exclusive,
  so `maxzoom = max_zoom + 1` keeps the feature visible AT its max zoom.
- Points keep their **single** data-driven symbol layer (icon image + size *can* be
  data-driven). Zoom for points is honored only at the **class level** (the class-default
  rule's bounds on the whole layer); per-subtype point zoom stays a documented limitation.
- `PackStyle` gains `layerFilters` (layerId → its base filter) so the map view can compose,
  not clobber, those filters.

**`pack_map_view.dart` — `_applyHidden` now composes.** It previously *overwrote* each pack
layer's filter to hide moved/edited features, which was safe only because layers carried no
filter. With per-rule sub-layers each layer has a base filter, so the hide filter is combined
`['all', base, hide]` (layers with no base filter still just take the hide filter). With an
empty hidden-id list the hide term is `true`, so composing leaves the base filter intact.

**Accepted limitation (documented in code).** MapLibre can't gate a *filter* on zoom, so a
zoom-gated rule's bucket simply **hides** its features at that zoom rather than **falling
through** to a less-specific rule the way the web does. Exact per-feature zoom fall-through is
impossible in MapLibre GL; "hide at that zoom" is the sensible, documented default.

**Verification owed (Bash blocked here).** `flutter analyze`; seed a pack with dash rules
(overhead=solid vs underground=dashed, a dotted/dashdot rule) and a zoom-bounded rule, then
on-device confirm: dashed/dotted/dashdot lines render per rule; a rule's feature disappears
outside its `min_zoom..max_zoom`; most-specific-wins still resolves colors; a moved/edited
feature still hides correctly under its overlay (the composed filter); a pre-symbology / no-rule
pack still renders one flat color per class. No pack rebuild required (data already in v4).

## Slice 19 — gate connectivity UI on `network_role` (code-complete 2026-08-27, verification owed)

**Bug.** Non-electrical features offered a terminal/connection affordance on **both** web and
mobile. A POLE (`metadata.feature_classes.network_role = 'none'` — see `seed/pole_example.sql`)
is *fully outside the network model* (migration `0007` comment: "edge/junction participate in
network_rules and get traced … none is fully outside the network model (e.g. a pole)"), yet the
web panel always rendered the **Terminals** tab and the mobile detail bar always showed the
**Connect** (hub) action. Establishing a connection from one would only ever produce an edge the
server `network_rules` reject (zero-trust: no rule = invalid), so the affordance was a dead end.

**Rule.** Show connectivity UI only when `network_role ∈ {edge, junction}` — an `edge` connects
to junctions, a `junction` carries the terminals (`metadata.terminals` rows live only on junction
classes). `container` (diagram host) and `none` (e.g. POLE) get no connect UI. **Relationships
stay available for every feature** — a pole may still *relate* to the gear it carries; only
electrical connectivity is gated.

**Web** (`web/src/components/map/feature-panel.tsx`). `selectedNetworkRole` was already derived
(it gates isolation). Added `isConnectable = role === "edge" || role === "junction"` and
`visibleTabs = TABS.filter(t => t.id !== "terminals" || isConnectable)`; the tab strip maps
`visibleTabs`, and the content pane guards `activeTab === "terminals" && isConnectable`. The
existing reset-to-Attributes effect (fires on every selection change) means a pole never lands on
a stale Terminals tab. The lone connect affordance (TerminalsTab's "New connection" button) is
hidden with the tab; the Relationships tab's own start is untouched.

**Mobile.** The pack already carries `network_role` per class in its `metadata` table
(`fieldpack/builder.go` writes it), but the device never read it. Added
`PackDatabase.networkRoleFor(code)` (`mobile/lib/src/pack/pack_database.dart`) — reads
`network_role` from `metadata`, wrapped so a pre-column pack degrades to `null` (unknown) instead
of throwing. In `pack_map_page.dart`'s selected-feature action bar, `canConnect = role != 'none'
&& role != 'container'` now guards the hub/Connect `IconButton`. Note the **negative** test
(vs. web's positive `edge||junction`): an unknown role on an **older pack** keeps the button
rather than silently stripping connectivity; for well-formed data the two are identical.

**Verification owed (Bash blocked here).** Web: `tsc`/lint; select a POLE → no Terminals tab,
Relationships tab still present; select an edge/junction → Terminals tab back. Mobile:
`flutter analyze`; on a **rebuilt** pack (network_role present) select a POLE → no Connect (hub)
action, Relate still shown; select an edge/junction → Connect back; an old pre-column pack still
shows Connect (no crash). No schema bump — `network_role` was already packed.

## Status

**Design locked (2026-08-22). Slice 1 (engine) RUNTIME-VERIFIED 2026-08-22.** A pack
built end to end via the compose `fieldsync` service (tippecanoe 2.79.0 in the image):
`POST /packs` for `[POLE, MV_CONDUCTOR, DIST_XFMR]` over a world bbox → succeeded with
18 features, 3 connectivity + 3 relationship edges, 3 metadata rows, both artifacts in
RustFS (tiles 48 KB, data 76 KB) + a presigned manifest. The downloaded SQLite pack has
all 5 tables and valid **OGC WKB** (`0101000000…` = LE Point, no SRID bit — SRID kept
per-class in `metadata`, as designed). Prereqs confirmed applied: `go get
modernc.org/sqlite`, migration 0039, tippecanoe built into the image.

**Presign-endpoint split (done 2026-08-22).** `export.ObjectStore` now signs download
URLs with a second minio client bound to `RUSTFS_PUBLIC_ENDPOINT` (falls back to
`RUSTFS_ENDPOINT` when unset), so a browser/device off the compose network gets a URL
pointing at a host it can actually reach instead of `rustfs:9000`. Wired into the `api`
+ `fieldsync` compose services (`localhost:3702` default; override with a LAN/public
host for a real device). Fixes both export and pack downloads from one choke point.

**Slice 2a (Flutter data layer) code-complete 2026-08-22** in `mobile/` (see the Slice
2a section) — download + open the SQLite pack + WKB decode + browse UI, no map;
verification owed. **Slice 2b (the map) code-complete 2026-08-23** (see the Slice 2b
section) — `maplibre_gl` render of `tiles.mbtiles` via an in-app loopback tile server,
tap-select → `featureById`, responsive tablet/phone layout. **Runtime-verified
2026-08-24** on the SM-T395 (map draws offline + tap-select) — the on-device MBTiles
render is now proven. **Slice 2c (map-detail polish) code-complete 2026-08-24** (see the
Slice 2c section) — geometry filtered out of attributes, a peer picker for stacked taps,
and tappable connectivity/relationship navigation; verification owed. **Slice 3a
(offline attribute edit + dirty overlay + outbox) code-complete 2026-08-24** (see the
Slice 3a section) — a writable `edits.sqlite` beside the pack (delta overlays + push-ready
outbox), a type-aware attribute form respecting `visible_on_update`/`editable_after_publish`,
an amber pending overlay on the map, and merged values + a "not yet synced" chip in the
panel; verification owed. **Slice 3d (offline feature CREATE — point → form → outbox)
code-complete 2026-08-25** (see the Slice 3d section) — a `creates` table in `edits.sqlite`,
a client-UUID mint, a green tappable overlay for created features, a custom place-a-point
draw flow, and a `FeatureCreateView` (subtype-picker-first, `visible_on_create` fields);
mobile-only, no server/pack changes; verification owed. **Slice 3e (`POST /sync` — replay
the outbox to the server) code-complete 2026-08-25** (see the Slice 3e section) — a shared-crud
replay behind `features:write`, idempotent create (`ID *string` + `ON CONFLICT (id) DO
NOTHING` → returns the existing row), per-op transactional results, and the mobile push
(badged Sync button, `markSynced`, display/sync split via `_pending`); verification owed.
**Slice 4 (pack management — delete/disable from the web UI) code-complete 2026-08-25**
(see the Slice 4 section) — a session/RBAC admin surface in the main API onto
`ops.field_packs` (`GET /field-packs` + disable/enable/delete behind `admin:field-packs`,
migration 0040 `disabled_at` + permission), the device `listPacks` now hides disabled packs,
and a `/admin/field-packs` web page; verification owed. **Slice 6 (metadata-driven
symbology on the map) code-complete 2026-08-26** (see the Slice 6 section) — pack schema
v4 `symbology` table replicating `resolveSymbologySchema` (map-context rules only) + the
domain-field values those rules key off carried in the tile props, and `pack_style.dart`
compiling each class's rules into MapLibre data-driven `case` paint (subtype + domain match,
most-specific-wins); dash/zoom-bounds deferred as documented; REQUIRES a v4 pack
re-download; verification owed. **Slice 7 (point icons on the map) code-complete
2026-08-26** (see the Slice 7 section) — pure frontend (no builder/schema change; `icon_ref`
already rides Slice 6's `style_def`): a `dart:ui`-drawn raster atlas (`icon_atlas.dart`) of the
five built-in shapes in baked colors, registered via `controller.addImage`, and point classes
promoted to MapLibre **symbol layers** with a data-driven `icon-image` `case` resolved per rule
(same predicates as the colors); every point still draws (iconless → circle), taps unchanged;
custom DB SVG icons still fall back to a circle; verification owed. **Slice 8 (DB-authored custom
icons in the pack) code-complete 2026-08-26** (see the Slice 8 section) — pack schema v4→v5
`icons` table shipping the referenced `metadata.symbology_icons` SVG, and the device rasterizing it
with `flutter_svg` (`{{COLOR}}` substitution → off-screen `vg.loadPicture` → `addImage`), so a
point renders as its real admin-authored shape (the transformer double-circle) not a built-in
circle; fallback chain pack-SVG → built-in shape → circle; new `flutter_svg` dep; REQUIRES a v5
pack re-download; verification owed. **Slice 9 (offline line CREATE) code-complete 2026-08-27**
(see the Slice 9 section) — trace a multi-vertex line, Undo/Place, then the same create form +
`create` outbox op as a point; mobile-only, no server/pack change; verification owed. **Slice 10
(offline geometry / vertex editor — move + add + remove + modify vertex, for points and lines)
code-complete 2026-08-27** (see the Slice 10 section) — drag/insert/delete vertices with Undo on an
existing packed Point/LineString, overlay `geom_json` column riding the same `update_attrs` op (no
server/pack/schema change), the base-hide `setFilter` trick + a filled amber `edits-moved-circle` so a
moved point still shows, and draggable `Circle` vertex handles; delivers the previously-pending Slice
3b **move**; verification owed. **Slice 11 (sync scale hardening) code-complete 2026-08-27** (see the Slice
11 section) — for 500+ workers: a per-request `metaCache` in `sync.go` kills the `metadata.Load`-per-op N+1,
and `_pushOps` chunks the outbox into ≤200-op POST /sync batches (acks marked synced per batch → durable
partial progress); async-queue drain deferred as premature; no migration/dep/pack change; verification owed.
The photos/trace/delta and polygon-create/move slices remain. Related:
[phase-17-large-export] (reused pipeline), [phase-18-mcp] (auth + shared-write
pattern), [phase-19-assistant].
