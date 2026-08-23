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
tap-select → `featureById`, responsive tablet/phone layout; verification owed (needs
`flutter pub get`/`run` locally — the on-device MBTiles render is the un-de-risked
integration). Slices 3–6 (edit/sync/photos/trace/delta) pending. Related:
[phase-17-large-export] (reused pipeline), [phase-18-mcp] (auth + shared-write
pattern), [phase-19-assistant].
