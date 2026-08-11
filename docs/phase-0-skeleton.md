# Phase 0 — Skeleton

**Goal:** repo layout and local infra running, nothing functional yet.
**Status:** built, pending your local verification (see "How to run it").

## What was built

- **Docker Compose stack** — `postgres` (pinned `supabase/postgres:15.8.1.049`),
  `martin`, `rustfs`, a Go (chi + bun) `api` skeleton, and an Alpine
  `gitbash` sidecar (git + bash + psql) for machines without a POSIX
  shell available locally.
- **Two Postgres schemas**, not one shared `public`:
  - `ops` — VoltEdge's own operational bookkeeping. Holds
    `ops.migration_log` today.
  - `metadata` — reserved, empty. Phase 1 populates it with the tables
    documented in [`docs/metadata-core.md`](./metadata-core.md).
- **Manual migration flow** — plain numbered SQL files under
  `/migrations`, no ORM auto-migration tool. `migrations/apply.sh` applies
  whatever hasn't run yet and records it in `ops.migration_log`: filename,
  a one-line description pulled from the file's header, a SHA-256
  checksum, who/when it ran, and status. Every table and column in every
  migration carries a `COMMENT ON` — a standing rule for this project, not
  just this phase.
- **`api/`** — a minimal chi server with `GET /healthz` that actually
  pings Postgres via bun, rather than returning a bare 200 that would hide
  a broken `DATABASE_URL` until the first real request failed.
- **`Makefile`** — see `make help` for the full list. Notably: `migrate`,
  `migrate-status`, and `psql` all shell out through the `gitbash`
  container rather than assuming bash/psql exist on your host, since
  avoiding that exact dependency is why the sidecar exists.

## How to run it

```
cp .env.example .env
make infra-up        # postgres, martin, rustfs, gitbash
make migrate          # applies migrations/0001-0003
make api-dev           # runs the api on the host against dockerized postgres
curl http://localhost:8080/healthz
```

Or run everything in Docker, api included: `make up`.

If `make` itself isn't available on your machine either, every target is
a one- or two-line `docker compose` / `go` command — open the Makefile
and run the underlying command directly.

Martin (`:3000`) and RustFS (`:9000` API / `:9001` console) are wired into
Compose but have nothing to serve yet: Martin needs a real feature class
(Phase 1) and RustFS needs the attachments table (Phase 2).

## Decisions made

- **Schema-per-concern**, never `public`: `ops` for infra bookkeeping,
  `metadata` reserved for the compiler's own tables.
- **Hand-written SQL migrations**, not framework-generated — consistent
  with "metadata as schema compiler": even the bootstrap layer should be
  plain, reviewable SQL, not something an ORM generates opaquely.
- `ops.migration_log.applied_by` defaults to `current_user` (the Postgres
  role) as a placeholder. Auth/permissions are explicitly deferred, so
  there's no real user identity yet — this column exists now so a proper
  changeset log is possible without a schema change once auth lands.

## Explicitly deferred

- Auth and permissions (by design — discussed later).
- `web/` — the OpenLayers frontend. `make frontend-dev` exists in the
  Makefile already but no-ops with a message until Phase 3 scaffolds it.
- Field collection (mobile/offline capture) — added to the phase list as
  a placeholder only; design not started.

## Relationship to the metadata core guide

Phase 0 doesn't implement any of [`docs/metadata-core.md`](./metadata-core.md)
— it only reserves the `metadata` schema, empty, for Phase 1 to populate.
Nothing built here contradicts that guide; there was nothing yet to build
against it. The one place they touch: the schema-separation principle
(`ops` vs `metadata`, never `public`) is applied starting now, and Phase 1
will keep following it as the compiler's own tables land.
