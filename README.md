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
- **Phase 1 — Metadata core + schema compiler** (done): all 15 metadata
  tables, plus a working compiler that turns a feature class into a real
  physical table (`gis.pole`), verified end to end including the
  incremental add-a-field path. Write-up:
  [`docs/phase-1-metadata-core.md`](docs/phase-1-metadata-core.md).
- **Phase 2 — Generic data-plane engine**: metadata-validated CRUD,
  draft/published state, connectivity/relationship establish flow,
  attachments, diagrams.
- **Phase 3 — Map loop**: Martin tile config generation, symbology
  resolution, OpenLayers viewer, snapping.
- **Phase 4 — Network model**: the trace engine and the connectivity
  establish flow, built against the terminals/network_rules/
  trace_configurations metadata Phase 1 already created.
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

**On Windows PowerShell/cmd, use the direct commands** — `make` on
Windows commonly resolves its shell through a Git-for-Windows `sh.exe`
whose launch path goes through `git.exe`, which locked-down environments
often block (`error launching git: Access is denied`, on *any* target,
even ones that never mention git — see Troubleshooting in
[`docs/commands-cheatsheet.md`](docs/commands-cheatsheet.md)). These are
verified working from a plain PowerShell prompt:

```
copy .env.example .env
docker compose up -d postgres martin rustfs gitbash
docker compose exec -T -e "DATABASE_URL=postgres://postgres:voltedge_dev@postgres:5432/postgres?sslmode=disable" gitbash bash /workspace/migrations/apply.sh
cd api
$env:DATABASE_URL = "postgres://postgres:voltedge_dev@localhost:3700/postgres?sslmode=disable"
$env:PORT = "3704"
go run ./cmd/server
```
then, from a second terminal: `curl http://localhost:3704/healthz`.
Full command reference, including build/test/tidy/vet and the database
shell: [`docs/commands-cheatsheet.md`](docs/commands-cheatsheet.md).

**On Linux, Mac, or from inside a working WSL2 shell**, `make` works as a
shorthand for the same commands:

```
cp .env.example .env
make infra-up      # postgres, martin, rustfs, gitbash
make migrate        # apply pending SQL migrations
make api-dev          # run the api on your host against dockerized postgres
curl http://localhost:3704/healthz
```

Run `make help` for the full command list (infra up/down/clean/logs,
api-dev/build/test, tidy/vet/fmt, frontend-dev, migrate/migrate-status/psql).

All published ports live in 3700-3720, not the defaults, so nothing here
collides with other local services. See `.env.example` for the full
assignment.

Either way, the `gitbash` service (Alpine + git + bash + psql, mounted at
`/workspace`) is where all bash-dependent work actually executes — your
host never runs bash directly:

```
docker compose exec gitbash bash
```

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
