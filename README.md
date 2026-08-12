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
- **Phase 2 — Generic data-plane engine** (CRUD + publish/unpublish +
  delete + zero-trust connectivity + explicit-confirm relationships
  done, attachments/diagrams open): metadata-validated create/read/
  update, a draft ⇄ published lifecycle with a real completeness gate, a
  soft-delete/restore/hard-delete lifecycle, a zero-trust connectivity
  engine that only commits an electrical connection when a matching,
  active `network_rules` row authorizes it — absence of a rule, or a
  matching but disabled rule, both reject the write — and a parallel
  structural-relationship engine gated on `relationship_classes` plus
  cardinality enforcement (`one_to_one`/`one_to_many`/`many_to_many`).
  Working end to end against any compiled feature class — see write-ups
  for full curl+SQL walkthroughs. Still open: attachments, diagrams.
  Write-ups: [`docs/phase-2-crud-engine.md`](docs/phase-2-crud-engine.md),
  [`docs/phase-2-connectivity.md`](docs/phase-2-connectivity.md),
  [`docs/phase-2-relationships.md`](docs/phase-2-relationships.md).
- **Phase 3 — Map loop** (slice 1 of several, app shell done): React +
  Vite + TypeScript frontend scaffolded in `web/` — Tailwind v4 +
  shadcn-style components, a light/dark/system theme with a distinct
  palette and typography (not shadcn's default blue-on-white/black),
  header + collapsible icon sidebar, routing, and Zustand/TanStack Query
  wired end to end against the live API. Still open: Azure AD SSO, the
  map itself (Martin tile config, symbology resolution, OpenLayers
  viewer, snapping), real data views. Write-up:
  [`docs/phase-3-ui-shell.md`](docs/phase-3-ui-shell.md).
- **Phase 4 — Network model**: the trace engine, built against the
  terminals/network_rules/trace_configurations metadata Phase 1 already
  created and the connectivity Phase 2 establishes.
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

**On Windows, use `task`, not `make`.** `make` on Windows commonly
resolves its shell through a Git-for-Windows `sh.exe` whose launch path
goes through `git.exe`, which locked-down environments often block
(`error launching git: Access is denied`, on *any* target, even ones that
never mention git). [`Taskfile.yml`](Taskfile.yml) mirrors every Makefile
target 1:1 for [`task`](https://taskfile.dev) — a single static binary
that execs commands directly, no shell resolution involved, so this
failure mode doesn't apply to it:

```
copy .env.example .env
task infra-up
task migrate
task api-dev
```
`task --list` shows every available task. If `task` itself isn't
installed, the verified-working direct commands (no `make`, no `task`,
just `docker`/`go` typed straight into PowerShell) are still here:

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

### Running the frontend

With the API already running (`task api-dev`, above), from a second
terminal:

```
cd web
npm install
npm run dev
```
Open `http://localhost:5173`. Vite proxies `/api/*` to the Go API at
`localhost:3704`, so the frontend never needs CORS configured on the API
side. See [`docs/phase-3-ui-shell.md`](docs/phase-3-ui-shell.md) for
what to expect and how it was validated.

## Repo layout

```
voltedge/
├── api/                   Go (chi + bun) service
├── web/                   React + Vite + TypeScript frontend
├── infra/gitbash/          Alpine dev shell: git + bash + psql
├── migrations/             Hand-written, numbered SQL + manual apply.sh
├── seed/                  Demo metadata, applied manually (never by apply.sh)
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
