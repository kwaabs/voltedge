# Phase 1 — Metadata core tables and schema compiler

**Goal (per the phase plan):** the metadata tables themselves, and the
generator that turns them into physical schema.
**Status:** both halves done and verified end to end — metadata tables,
and a working compiler that turns a real feature class (`POLE`) into a
real physical table, including the incremental add-a-field path.

## What was built

### The metadata tables

- 11 migrations (`0004`-`0014`), 15 tables, all in the `metadata` schema
  Phase 0 reserved empty.
- An exact match to [`docs/metadata-core.md`](./metadata-core.md)'s ERD:
  `domains`/`domain_values`, `field_groups`, `metadata_versions`,
  `feature_classes`, `subtypes`, `fields`, `subtype_field_rules`,
  `terminals`, `relationship_classes`, `network_rules`,
  `symbology_rules`, `snapping_rules`, `trace_configurations`,
  `trace_barrier_rules`.
- Every table and every column carries a `COMMENT ON` — the standing rule
  from Phase 0, kept.
- `feature_connectivity`, `feature_relationships`, `diagram_instances`,
  `diagram_placements` were deliberately **not** built here — those are
  data tables (established facts, generic infrastructure), not metadata,
  and belong to Phase 2's "generic data-plane engine" per the original
  phase split.

### The schema compiler

- `migrations/0015_create_gis_schema.sql` — reserves the `gis` schema
  (physical, compiled feature-class tables live here, separate from
  `metadata` and `ops`) and enables the PostGIS extension, which the
  `supabase/postgres` image bundles but doesn't turn on by default.
- `api/internal/compiler` — the actual generator:
  - `models.go` — bun models mirroring the metadata tables it reads.
  - `ddl.go` — DDL generation: Postgres/PostGIS type mapping, `CREATE
    TABLE` and column-definition rendering, domain values compiled into
    `CHECK (col IN (...))` constraints, `information_schema` queries for
    diffing against what already exists physically.
  - `compiler.go` — `Compile(ctx, db, code)`: loads a feature class and
    everything it needs, creates the table if it doesn't exist (with a
    `GIST` index on `geom` when there's geometry) or adds only the
    missing columns if it does, records a new `metadata_versions` row,
    and links `feature_classes.metadata_version_id`/`physical_table`/
    `status` to it — all inside one transaction.
- `api/cmd/compile` — CLI entrypoint: `go run ./cmd/compile POLE`. There's
  no HTTP API yet (that's Phase 2), so this is the only interface to the
  compiler until then.
- `seed/pole_example.sql` — demo metadata for `POLE`, matching
  `docs/metadata-core.md`'s sample exactly, for exercising the compiler
  without hand-writing `INSERT`s. Deliberately outside `/migrations` so
  `apply.sh` never picks it up — it's demo data, not schema history.
- `make compile CODE=POLE` and `make seed-pole`, plus cheatsheet
  equivalents, for running both without needing `make`.

## How this was validated

Same approach as the tables: no Docker daemon reachable in this sandbox,
but a real local Postgres cluster was, so this got a genuine end-to-end
run rather than a syntax check.

1. Fresh database, all 15 migrations applied in order, then
   `seed/pole_example.sql` loaded.
2. **First compile** (`go run ./cmd/compile POLE`) — created `gis.pole`
   with all 6 fields as real columns, `subtype` and `status` system
   columns, a real `geometry(Point,4326)` column with a `GIST` index, and
   `CHECK` constraints for every domain-backed field, generated straight
   from what was actually in `metadata.domain_values` — inspected with
   `\d gis.pole` and it matched the metadata exactly.
3. **Second compile**, no metadata changes — reported "already up to
   date," no DDL executed, confirming idempotency.
4. **Added a new field** (`gps_capture_method`) directly to
   `metadata.fields`, then a **third compile** — added exactly that one
   column via `ALTER TABLE`, left every existing column, constraint, and
   index untouched. This is the field-lifecycle "add a field" path from
   `docs/metadata-core.md` working for real.
5. Confirmed `feature_classes.status` flipped to `active`,
   `physical_table` was set to `gis.pole`, and `metadata_version_id`
   pointed at a real, newly created `metadata_versions` row.

**A real bug turned up and got fixed during this**: the first attempt at
inserting a `metadata_versions` row failed with `invalid input syntax for
type uuid: ""` — bun was inserting the Go zero-value empty string for the
`id` column literally, instead of omitting it and letting the table's
`gen_random_uuid()` default apply. Fixed by adding `nullzero` to that
field's struct tag. Worth knowing about if any other model here ever
needs a real `INSERT` (`Fields`/`FeatureClass`/etc. are only ever read or
`UPDATE`d today, so they haven't hit this).

**Caveat, carried over from the tables:** all of this ran on Postgres 16,
not the project's actual `supabase/postgres:15.8.1.049`. Nothing here is
version-specific, but run it on your actual stack and confirm.

## How to run it

**On Windows PowerShell/cmd, skip `make`** — same failure as everywhere
else in this repo: `make` resolves its shell through a Git-for-Windows
`sh.exe` whose launch path goes through `git.exe`, which locked-down
environments block (`error launching git: Access is denied`, on every
target, not just these). Run the direct commands instead:

```
docker compose exec -T -e "DATABASE_URL=postgres://postgres:voltedge_dev@postgres:5432/postgres?sslmode=disable" gitbash bash /workspace/migrations/apply.sh
docker compose exec -T gitbash psql "postgres://postgres:voltedge_dev@postgres:5432/postgres?sslmode=disable" -f /workspace/seed/pole_example.sql
cd api
$env:DATABASE_URL = "postgres://postgres:voltedge_dev@localhost:3700/postgres?sslmode=disable"
go run ./cmd/compile POLE
```

**On Linux, Mac, or a working WSL2 shell**, `make` works as shorthand:

```
make migrate                    # applies 0001-0015, including the gis schema + PostGIS
make seed-pole                  # loads seed/pole_example.sql
make compile CODE=POLE          # compiles it into gis.pole
```

Full reference either way:
[`docs/commands-cheatsheet.md`](./commands-cheatsheet.md). Check the
result with `docker compose exec gitbash psql "..." -f -` and
`\d gis.pole`, or `make psql` then `\d gis.pole` interactively.

## Decisions made

- **11 migration files, grouped by table family** rather than one giant
  file or one-per-table — each is a coherent, independently reviewable
  unit.
- **`created_at timestamptz` added to every table**, beyond what the
  ERD's attribute sketch showed — minimal, standard auditability.
- **`feature_classes.metadata_version_id`** realizes the one relationship
  the ERD draws between feature classes and versions (`"published in"`).
  No other metadata table carries a version reference yet.
- **`CHECK` constraints enforce the small controlled vocabularies** at the
  database level, same pattern as `ops.migration_log.status`.
- **Physical tables live in their own `gis` schema**, not `metadata` and
  not `public` — same separate-schema-per-concern principle as `ops` and
  `metadata`.
- **Coded domains compile to a literal `CHECK (col IN (...))`**, snapshotting
  `domain_values` at compile time, rather than a live FK to a metadata
  table. Consequence: changing `domain_values` for a domain already in use
  requires recompiling every feature class that references it — consistent
  with "metadata publish" being the moment schema actually changes, not a
  live join at query time. **Range domains compile to nothing yet** —
  `domains`/`domain_values` has no min/max storage to snapshot from.
- **One `Compile` call = one `metadata_versions` row**, scoped to a single
  feature class. True atomic multi-class publishing (compiling several
  feature classes together as one version) is a real simplification, not
  yet built — see below.

## What's still open

- **Multi-class atomic publish** — today, compiling two related feature
  classes is two separate `metadata_versions` rows, two separate
  transactions. A real publish workflow (validate everything, compile
  everything, apply as one unit, one version) is still ahead.
- **Publish-time validation beyond individual `CHECK` failures** — e.g.
  "every feature class needs a default `symbology_rules` row before
  publish" isn't enforced anywhere yet.
- **Computed fields** (`is_computed = true`, e.g. `length_m` via
  `ST_Length(geom)`) compile to a plain nullable column today, not a
  generated/computed one — there's nowhere in the metadata yet to store
  the expression that would drive that.
- **No HTTP API** — the compiler is CLI-only. The generic CRUD engine and
  its publish endpoint are Phase 2.
- `metadata_versions`: "only one `active` row at a time" is still just
  documented, not a database constraint.
