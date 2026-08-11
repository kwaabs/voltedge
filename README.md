# voltedge

A metadata-driven utility GIS — hybrid of Esri's Utility Network and
Hexagon's G/Technology, built on an open-source stack: Supabase/Postgres,
Martin, RustFS, Go (chi + bun), OpenLayers.

Every feature class, field, domain, rule, and style stems from metadata
tables that compile into physical schema. See the architecture reference
doc for the full metadata core, symbology resolution, connectivity
integrity model, snapping rules, tracing, and MCP data-plane design.

## Build phases

- **Phase 0 — Skeleton** (this phase): repo layout, local infra, nothing
  functional yet.
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

Auth and permissions are deliberately not modeled yet; every table below
carries an `applied_by`/`established_by`-style placeholder column so a
real changeset log is possible once that design lands.

## Phase 0 — what's here

```
voltedge/
├── api/                  Go (chi + bun) skeleton — proves DB connectivity
│   └── cmd/server/main.go
├── infra/
│   └── gitbash/           Alpine sidecar: git + bash + psql
├── migrations/            Hand-written, numbered SQL + manual apply.sh
├── docker-compose.yml
├── .env.example
└── .gitignore
```

**Services:** `postgres` (`supabase/postgres:15.8.1.049`), `martin`,
`rustfs`, `api`, `gitbash`.

**Schemas:** each concern gets its own Postgres schema, never a shared
`public` dumping ground.
- `ops` — VoltEdge's own operational bookkeeping (migration history today).
- `metadata` — reserved for Phase 1, empty until then.

**Migrations:** plain numbered `.sql` files under `/migrations`, no ORM
auto-migration tool. `ops.migration_log` records what's been applied, when,
by which role, and a checksum of the file at apply time. Every table and
column in every migration carries a `COMMENT ON` — that's a standing rule
for this project, not just this phase.

## Running it

If you don't have Git Bash / a POSIX shell locally, use the `gitbash`
sidecar for every command below — it has git, bash, and psql, and the repo
is mounted at `/workspace`:

```
docker compose exec gitbash bash
```

1. Copy the env file and adjust if needed:
   ```
   cp .env.example .env
   ```
2. Bring the stack up:
   ```
   docker compose up -d
   ```
3. Run migrations (from inside the gitbash sidecar, or any shell with psql):
   ```
   DATABASE_URL=postgres://postgres:voltedge_dev@localhost:5432/postgres \
     ./migrations/apply.sh
   ```
   (Use `@postgres:5432` instead of `@localhost:5432` if running this
   from *inside* the `gitbash` container rather than the host.)
4. Confirm the API can see the database:
   ```
   curl http://localhost:8080/healthz
   ```

Martin (`:3000`) and RustFS (`:9000` API / `:9001` console) are wired in
but have nothing to serve yet — that starts in Phase 1 and Phase 2
respectively.

**Not verified in this environment:** the actual `docker compose up` boot
and migration run haven't been executed here — no Docker daemon was
reachable in this sandbox, only the CLI. `docker compose config` validated
the compose file syntactically, and `go build ./...` passed against a real
`go.mod`/`go.sum`, but please run the steps above yourself once and report
back anything that doesn't come up clean.
