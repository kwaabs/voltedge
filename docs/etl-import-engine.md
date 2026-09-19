# ETL / import engine

Admin-facing bulk import: bring external data into the master `gis.*` tables one
feature class at a time. The admin maps source columns onto the target class's
fields, then the engine **inserts or upserts** the rows through the same
validated CRUD path the rest of the app writes through.

This is the mirror image of the CIM export mapping (`docs/phase-16-cim-export.md`,
migration 0034): there, feature fields are mapped **out** to a fixed external
vocabulary; here, external columns are mapped **in** to our fields. The mapping
is authored, declarative config — so it lives in `metadata` beside the CIM
mapping — while a run is operational work with a lifecycle, so it lives in `ops`
beside `export_jobs`.

## Scope

**v1 (in progress):** file uploads — **CSV, GeoJSON, Shapefile, KML/KMZ, DXF**.
Match-on-chosen-field upsert. Hybrid: the browser normalizes any format and
previews it; the server runs the authoritative load.

**v2:** richer sources, each adding only a new `rowSource` reader — server-side
**GeoPackage** and **SpatiaLite** (SQLite-file family, done), then live-DB
connectors **PostGIS** (done) and **MSSQL** (later). Live-DB credentials are
sealed with `api/internal/secretbox` but stored **on the profile**, not reused
from `metadata.external_systems`. **FGDB** stays deferred (native GDAL dep). The
v1 schema is pre-shaped for these (the `source_format` CHECK is widened per
slice; the job keys off an `object_key`, empty for live-DB) so they add no
migration to a populated table. See "v2 — richer sources" below.

## Architecture

The decisive reuse: **an import row is a field-pack sync op.** Field-pack sync
(`api/internal/fieldpack/sync.go`) already applies external data into the master
tables one feature at a time, in a transaction, idempotently, via
`crud.CreateFeature` / `crud.UpdateFeature`. The importer applies the same way.
Three more existing pieces are reused wholesale:

- **Dry-run validation is free** — `crud.ValidateFeature` / `validateForCreate`
  type-checks, enforces required fields, coded-domain membership, and range
  bounds before any write. That is the per-row pre-flight report.
- **The job runner** — `ops.export_jobs` + `api/internal/export/worker.go`
  (claim with `FOR UPDATE SKIP LOCKED`, heartbeat lease, standalone worker
  binary). The import worker copies this pattern.
- **Object storage** — `api/internal/export/storage.go` (RustFS/S3, presigned
  upload) hosts the uploaded source artifact.

### The source artifact (NDJSON)

To keep the server from needing five parsers, the **browser** normalizes every
supported upload into one canonical shape — **newline-delimited GeoJSON**, one
Feature per line:

```
{"type":"Feature","properties":{...},"geometry":{...}|null}
```

`web/src/lib/etl-import.ts` produces it (reusing the Phase 15 overlay parser for
the geometry formats, plus a hand-rolled CSV parser). It is uploaded to object
storage; the load job streams it. The server parses exactly one format —
`api/internal/importer/contract.go` (`importer.Reader`). CSV rows arrive with
`geometry: null`; their geometry, if any, is assembled server-side from the
profile's lon/lat or WKT columns.

## Data model (migration 0102)

Config, in `metadata` (mirrors the CIM mapping tables):

- **`metadata.import_profiles`** — one reusable, named import for a target class
  (optionally a subtype): `source_format`, `write_mode` (`insert_only` |
  `upsert`), `match_fields[]` (the natural key for upsert), and `geometry_mode`
  (`none` | `native` | `lonlat` | `wkt`) + its columns.
- **`metadata.import_field_mappings`** — one source column → one target field,
  with a `transform` (`direct` | `constant` | `scale` | `domain_map`) and its
  parameter (`constant_value` / `scale_factor` / `value_map`). Unique per
  `(profile, field)`.

Runs, in `ops` (mirrors `export_jobs`):

- **`ops.import_jobs`** — one row per run: `object_key`, `scope` (the resolved
  run + authz snapshot the worker applies verbatim), `dry_run`, row tallies,
  and the `status` / lease / attempts bookkeeping.
- **`ops.import_job_errors`** — per-row failures (`row_number`, `message`); the
  row is skipped and the run continues (per-row skip-and-report, not
  all-or-nothing).

## The upsert (the one genuinely new piece)

There is no natural-key upsert anywhere else in the app — every insert mints a
UUID. For `write_mode = upsert`, the load, per row:

```
SELECT id FROM gis.<code>
 WHERE <match_field_1> = :val_1 AND ... AND deleted_at IS NULL
```

0 hits → `CreateFeature`; 1 hit → `UpdateFeature`; >1 → ambiguous, recorded as a
row error and skipped.

## Permissions

No new RBAC. Editing a profile is `metadata:write` config (like the CIM
mappings); running an import writes features, so it is gated on `features:write`.

## Build slices

- **S1 — schema + contract + CSV parser** *(done)*: migration 0102, the NDJSON
  contract (`importer.Reader` + `web/src/lib/etl-import.ts` with the CSV/NDJSON
  code).
- **S2 — profile + field-mapping CRUD**: `api/internal/importer` handlers under
  `/admin` + an `admin-imports.tsx` mapping page (mirrors `admin/cim.go` +
  `admin-cim-mappings.tsx`).
- **S3 — upload + dry-run report**: presigned artifact upload; a synchronous
  dry-run over a sample via `crud.ValidateFeature`.
- **S4 — load job + worker + upsert**: `ops.import_jobs` enqueue + a claiming
  worker that streams the artifact and applies each row.
- **S5 — the wizard**: pick class → upload/preview → map → match/upsert →
  dry-run → run → live progress + error report, saved as a reusable profile.

## v2 — richer sources (each adds only a new reader)

The whole v2 effort rests on one seam: `load.go` defines
`rowSource { Read() (*Feature, int, error) }`, and `openRowSource` picks the
reader by `source_format`. Every v2 slice adds a reader; the apply spine
(`BuildAttributes → AssembleGeometry → crud validate → upsert`) never changes.

- **V2-S1 — GeoPackage** *(done)*: server-side SQLite parse (a deliberate
  departure from "browser normalises everything to NDJSON" — the browser now
  uploads the raw `.gpkg`). `geopackage.go` streams a chosen layer via
  `modernc.org/sqlite` (already a dep, no cgo); the GPB blob is decoded per-row
  and converted through `ST_GeomFromWKB` (non-fatal, so a bad blob is a row
  error). Adds `source_layer` (pinned on the profile, overridable per run) and a
  `POST /imports/layers` inspect endpoint. Migration 0103 widens the
  `source_format` CHECK to add `geopackage`.
- **V2-S2 — SpatiaLite** *(done)*: reuses the whole S1 spine — the only new code
  is `spatialite.go`'s non-standard blob decoder (`decodeSpatiaLiteBlob` → WKB →
  the shared `wkbToGeoJSON`) and its own `geometry_columns` metadata query. The
  reader carries a `rawGeomKind` discriminator so `AssembleGeometry` picks the
  right decoder. Migration 0104 adds `spatialite` to the CHECK.
- **V2-S3 — PostGIS (live database)** *(done)*: the first source that is **not a
  file**. The worker opens an outbound, read-only connection to a remote
  Postgres/PostGIS server (reusing bun's bundled `pgdriver` via `database/sql` —
  no new dep) and streams rows straight out of a chosen table or a custom
  read-only SELECT. Connection config lives **on the profile**
  (host/port/db/user + a secretbox-sealed `db_password`, write-only, surfaced as
  `has_credential`, never returned); it deliberately does **not** reuse
  `metadata.external_systems`. Migration 0105 adds the `db_*` columns +
  `source_query`. Security posture (`postgis.go`): an SSRF guard resolves the
  host and dials the validated IP (allow-list via `IMPORT_DB_HOST_ALLOWLIST`,
  private/loopback/link-local/metadata ranges blocked unless allow-listed); the
  session is opened `default_transaction_read_only=on` with a
  `statement_timeout` and idle-transaction timeout; a custom query is validated
  to be a single read-only SELECT. Native-geometry table-pick emits
  `ST_AsGeoJSON(ST_Transform(geomcol::geometry, classSRID))` so geometry lands in
  the class SRID with the apply spine untouched. Admin gets `test-connection` and
  `tables` endpoints (both operate on the saved profile).
- **V2-S4 — MSSQL (live database)** *(done)*: the second live-DB connector,
  mirroring S3 and reusing every dialect-independent piece (SSRF guard, identifier
  guard, read-only query validator, `DBConnConfig`/`ResolveConnConfig`, the whole
  admin + frontend surface). Migration-free — 0102 already admits `mssql` and the
  `db_*`/`source_query` columns from 0105 are shared. Adds `mssql.go` and one new
  dependency, `github.com/microsoft/go-mssqldb` (pure Go, cgo-free). SQL Server has
  no `ST_AsGeoJSON`/in-SQL reprojection, so the reader emits the spatial column as
  WKB (`col.STAsBinary()`) + SRID (`col.STSrid`); the WKB rides on the Feature
  (`geomKindWKB`) and is converted + reprojected by the shared `wkbToGeoJSON`
  round-trip. A **geography** column emits WKB in latitude-longitude order, so it
  sets `rawGeomFlipXY` to swap axes via `ST_FlipCoordinates` during conversion.
  Read-only note: SQL Server has no session read-only-transaction toggle like
  Postgres, so the read-only posture rests on only-SELECT table reads + the
  read-only custom-query validator + the recommendation to connect with a
  least-privileged read-only login.
- **FGDB** stays deferred — it needs a native GDAL/OGR dependency, which would
  break the cgo-free build chain.
