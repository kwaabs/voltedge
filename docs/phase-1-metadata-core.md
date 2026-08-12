# Phase 1 — Metadata core tables

**Goal (per the phase plan):** the metadata tables themselves, and the
generator that turns them into physical schema.
**Status:** the tables are built and verified against a real Postgres
instance. The schema compiler — the generator itself — is **not** built
yet; that's the next piece of work, scoped separately below.

## What was built

- 11 new migrations (`0004`-`0014`), 15 tables, all in the `metadata`
  schema Phase 0 reserved empty.
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

## How this was validated

No Docker daemon was reachable in this sandbox, same as Phase 0 — but
this time a real local Postgres 16 cluster was available directly, so
validation went further than "syntax looks right":

1. Ran all 14 migration files (`0001`-`0014`) against a real database, in
   order, exactly as `apply.sh` would.
2. Confirmed all 15 `metadata.*` tables exist with the FK graph intact.
3. Inserted a full, real chain of rows — the `POLE` feature class from
   the reference doc's sample configuration: feature class → subtype →
   domain → domain value → field → subtype_field_rule — and read it back
   with a join across all five tables. It matched the documented sample
   exactly.
4. Deliberately inserted an invalid `feature_classes` row
   (`geometry_type = 'point'` with `srid` null) and confirmed the `CHECK`
   constraint correctly rejected it.

**Caveat:** that was Postgres 16, not the `supabase/postgres:15.8.1.049`
image this project actually runs. Nothing here uses version-specific
features — `gen_random_uuid()` is native since PG13,
`GENERATED ALWAYS AS IDENTITY` since PG10 — so it should carry over
cleanly, but run `make migrate` (or the cheatsheet equivalent) against
your actual stack and confirm before trusting this fully.

## How to run it

Nothing new to invoke — these are just more files in `/migrations`:

```
docker compose exec -T -e "DATABASE_URL=postgres://postgres:voltedge_dev@postgres:5432/postgres?sslmode=disable" gitbash bash /workspace/migrations/apply.sh
```

or `make migrate`. `make migrate-status` (or the cheatsheet's `psql`
query against `ops.migration_log`) should then list all 14 files.

## Decisions made

- **11 migration files, grouped by table family** (domains,
  feature_classes, subtypes+fields, terminals, relationship_classes,
  network_rules, symbology_rules, snapping_rules,
  trace_configurations+trace_barrier_rules) rather than one giant file or
  one-per-table — each is a coherent, independently reviewable unit.
- **`created_at timestamptz` added to every table**, beyond what the
  ERD's attribute sketch showed — minimal, standard auditability, not
  itself worth treating as a real design decision.
- **`feature_classes.metadata_version_id`** (nullable FK to
  `metadata_versions`) added to realize the one relationship the ERD
  actually draws between the two tables (`"published in"`). No other
  metadata table carries a version reference — matching the ERD exactly
  rather than inventing more versioning than was specified.
- **`CHECK` constraints enforce the small controlled vocabularies**
  (`geometry_type`, `network_role`, `status`, `domain_type`, etc.) at the
  database level — same pattern Phase 0 established with
  `ops.migration_log.status`.

## What's still open

- **The schema compiler itself** — the Go service that reads published
  metadata and generates/applies real DDL for a feature class (e.g.
  compiling `POLE` into an actual `gis.pole` table with a real geometry
  column). This is the rest of Phase 1, and the next piece of work.
- Publish-time validation (domain references resolve, every feature class
  has a default `symbology_rules` row before publish, etc.) lives in that
  compiler — the database schema alone doesn't enforce it.
- `metadata_versions`: "only one `active` row at a time" is documented as
  the compiler's responsibility, not yet a database constraint.
