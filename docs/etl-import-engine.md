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

**v2 (later):** live DB connectors (**PostGIS, MSSQL**) reusing
`metadata.external_systems` + `api/internal/secretbox` for sealed credentials,
then **FGDB / GeoPackage**. The v1 schema is pre-shaped for these (the
`source_format` enum is pre-widened; the job keys off an `object_key`) so they
add no migration to a populated table.

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
