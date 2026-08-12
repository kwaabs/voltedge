# voltedge

A metadata-driven utility GIS — hybrid of Esri's Utility Network and
Hexagon's G/Technology, built on an open-source stack: Supabase/Postgres,
Martin, RustFS, Go (chi + bun), OpenLayers.

Every feature class, field, domain, rule, and style stems from metadata
tables that compile into physical schema. See
[`docs/metadata-core.md`](docs/metadata-core.md) for the full metadata
core, symbology resolution, connectivity integrity model, snapping rules,
tracing, and MCP data-plane design — check it before writing a
feature-specific code path anywhere else.

## Build phases

- **Phase 0 — Skeleton** (done): repo layout, local infra, nothing
  functional yet. Write-up: [`docs/phase-0-skeleton.md`](docs/phase-0-skeleton.md).
- **Phase 1 — Metadata core + schema compiler**: the metadata tables
  themselves, and the generator that turns them into physical schema.
- **Phase 2 — Generic data-plane engine**: metadata-validated CRUD,
  draft/published state, connectivity/relationship establish flow,
  attachments, diagrams.
- **Phase 3 — Map loop**: Martin tile config generation, symbology
  resolution, OpenLayers viewer, snapping.
- **Phase 4 — Network model**: terminals, network_rules, connectivity
  establish flow, the trace engine.
- **Phase 5 — Diagrams, attribute groups, MCP**: one-line diagram canvas,
  metadata-driven forms, MCP data-plane tools.
- **Phase 6 — Domain expansion + hardening**: full feature class catalog,
  access control, asset numbering, units/locale, isolation/loop traces,
  load testing.
- **Phase 7 — Field collection**: mobile/offline data capture for field
  crews. Design deferred — noted here as a placeholder only.

Auth and permissions are deliberately not modeled yet; several tables
already carry an `applied_by`/`established_by`-style placeholder column so
a real changeset log is possible once that design lands.

Each phase gets its own write-up under [`docs/`](docs/) once it's built:
what was done, how to run/verify it, decisions made, what's deferred.

## Running it locally

```
cp .env.example .env
make infra-up      # postgres, martin, rustfs, gitbash
make migrate        # apply pending SQL migrations
make api-dev          # run the api on your host against dockerized postgres
curl http://localhost:3704/healthz
```

All published ports live in 3700-3720, not the defaults, so nothing here
collides with other local services. See `.env.example` for the full
assignment.

Run `make help` for the full command list (infra up/down/clean/logs,
api-dev/build/test, tidy/vet/fmt, frontend-dev, migrate/migrate-status/psql).

If `make` or shell scripts aren't runnable on your machine (locked-down
corporate policy, no script execution allowed), see
[`docs/commands-cheatsheet.md`](docs/commands-cheatsheet.md) — the same
operations as literal one-line commands you type directly into whatever
terminal is available, no `.ps1`/`.sh`/`make` involved.

If you don't have Git Bash or another POSIX shell locally, the `gitbash`
service is an Alpine sidecar with git + bash + psql, mounted at
`/workspace`:

```
docker compose exec gitbash bash
```

`make migrate`, `make migrate-status`, and `make psql` already route
through it, so you don't need bash or psql on the host at all — only
`make`, `docker`, and `go` (for the `api-*` targets).

## Repo layout

```
voltedge/
├── api/                   Go (chi + bun) service
├── infra/gitbash/          Alpine dev shell: git + bash + psql
├── migrations/             Hand-written, numbered SQL + manual apply.sh
├── docs/                   Architecture reference + one write-up per phase
├── Makefile
├── docker-compose.yml
├── .env.example
└── .gitignore
```

**Schemas:** each concern gets its own Postgres schema, never a shared
`public` dumping ground. `ops` for VoltEdge's own operational bookkeeping
(migration history today), `metadata` reserved for the schema compiler
(Phase 1). Every table and column carries a `COMMENT ON` — a standing rule
for this project.
