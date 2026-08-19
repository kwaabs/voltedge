# voltedge

A metadata-driven utility GIS — hybrid of Esri's Utility Network and
Hexagon's G/Technology, built on an open-source stack: Supabase/Postgres,
Martin, RustFS, Go (chi + bun), OpenLayers.

Every feature class, field, domain, rule, and style stems from metadata
tables that compile into physical schema. See
[`docs/metadata-core.md`](docs/metadata-core.md) for the full metadata
core, symbology resolution, connectivity integrity model, snapping rules,
tracing, and MCP data-plane design — check it before writing a
feature-specific code path anywhere else. For what's deliberately left
undone in each phase, see [`docs/open-items.md`](docs/open-items.md) —
a single, current backlog kept up to date as items close, instead of
having to reread every phase doc's own "What's still open" section.

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
  done, attachments open, diagrams moved to Phase 5): metadata-validated create/read/
  update, a draft ⇄ published lifecycle with a real completeness gate, a
  soft-delete/restore/hard-delete lifecycle, a zero-trust connectivity
  engine that only commits an electrical connection when a matching,
  active `network_rules` row authorizes it — absence of a rule, or a
  matching but disabled rule, both reject the write — and a parallel
  structural-relationship engine gated on `relationship_classes` plus
  cardinality enforcement (`one_to_one`/`one_to_many`/`many_to_many`).
  Working end to end against any compiled feature class — see write-ups
  for full curl+SQL walkthroughs. Still open: attachments.
  Write-ups: [`docs/phase-2-crud-engine.md`](docs/phase-2-crud-engine.md),
  [`docs/phase-2-connectivity.md`](docs/phase-2-connectivity.md),
  [`docs/phase-2-relationships.md`](docs/phase-2-relationships.md).
- **Phase 3 — Map loop** (slices 1-2 of several: shell + map done):
  React + Vite + TypeScript frontend in `web/` — Tailwind v4 +
  shadcn-style components, a light/dark/system theme with a distinct
  palette and typography, header + collapsible icon sidebar, and
  Zustand/TanStack Query wired end to end against the live API. The map
  is the app's home route (`/`) — a real OpenLayers canvas rendering
  every compiled feature class as a live vector tile layer straight from
  Martin's auto-discovered catalog, verified against a real Martin
  instance, not just its documented API. Click-to-select (with a peer
  picker for stacked features) opens a floating, draggable attribute
  panel (drag its header anywhere, position persists across selections)
  with tabs for a feature's attributes, terminal connections, and
  structural relationships; a "New feature" tool draws a new point,
  line, or polygon feature and saves it through a metadata-driven form.
  A "+ New connection"/"+ New relationship" button in the Terminals/
  Relationships tab drives the same floating, draggable confirm panel,
  branching on which was clicked: connectivity establishes a real
  electrical link between two features' terminals through the
  zero-trust `POST /connectivity` engine; relationships establish a
  structural/spatial link (no terminals, an optional `kind` to
  disambiguate) through the equally zero-trust `POST /relationships`
  engine. The selected feature (or, mid-connect-workflow, its source and
  target) gets a real halo traced around its actual geometry on the map;
  every connection/relationship row is clickable and pans/highlights the
  feature on the other end; every error surfaces as a top-right toast
  instead of inline panel text. A pencil "Edit" button in the panel
  header opens a pre-filled attribute form (locking any field the
  backend would reject once a feature is published) and `PATCH`es only
  the fields that actually changed; a trash-can "Delete" button opens a
  real modal confirmation (built on Radix's AlertDialog, immune to an
  accidental outside click by design) whose Delete action soft-deletes
  the feature and makes it disappear from the map immediately. The same
  Edit session also drives a geometry-editing toolbar, geometry-type
  aware (a point gets only "Move," a line/polygon also gets "Edit
  vertices" — drag a vertex, drag a segment midpoint to add one,
  Alt+click to delete one) — Save commits whichever changed, attributes
  and/or geometry, in one `PATCH`. A shared "Snap" toggle (magnet icon,
  on by default) sits in both that toolbar and the "New feature" draw
  banner — while placing a brand new feature or reshaping an existing
  one, the cursor snaps to a nearby feature's vertex or edge, a real OL
  `Snap` interaction reading directly from whichever vector tiles are
  already loaded (metadata-driven `snapping_rules` still deferred) —
  completing all four of the CRUD slices originally planned (attribute
  edit, delete confirmation, geometry editing, snapping). Two more small
  items closed after that: relationships joined connectivity as
  something the connect panel can establish, not just display, and the
  attribute panel itself undocked into the floating window described
  above. The map's last big cosmetic gap closed too: every feature of
  a given geometry type used to render identically regardless of
  feature class — `metadata.symbology_rules` (schema-only since Phase
  1) is now resolved entirely client-side, most-specific-wins over
  subtype + domain-value, into real per-class SVG icon shapes for
  points (a hand-authored registry standing in for a real sprite
  pipeline) and color/width for lines and polygons; the Layers dropdown
  doubles as the map's legend, showing each class's resolved icon next
  to its name; `DIST_SWITCH`'s `device_state` is a genuine shape swap
  (hollow vs. solid triangle) that updates live the moment it's edited,
  not just a color change. The full, current list of what's still open
  across every phase — Azure AD SSO, a real symbology sprite pipeline,
  the real Martin soft-delete fix, and more — now
  lives in [`docs/open-items.md`](docs/open-items.md), kept
  current going forward instead of scattered across each slice's own
  doc.
  Write-ups: [`docs/phase-3-ui-shell.md`](docs/phase-3-ui-shell.md),
  [`docs/phase-3-map-loop.md`](docs/phase-3-map-loop.md),
  [`docs/phase-3-create-feature.md`](docs/phase-3-create-feature.md),
  [`docs/phase-3-connectivity-ui.md`](docs/phase-3-connectivity-ui.md),
  [`docs/phase-3-map-aesthetics.md`](docs/phase-3-map-aesthetics.md),
  [`docs/phase-3-edit-attributes.md`](docs/phase-3-edit-attributes.md),
  [`docs/phase-3-delete-confirmation.md`](docs/phase-3-delete-confirmation.md),
  [`docs/phase-3-edit-geometry.md`](docs/phase-3-edit-geometry.md),
  [`docs/phase-3-snapping.md`](docs/phase-3-snapping.md),
  [`docs/phase-3-relationships-ui.md`](docs/phase-3-relationships-ui.md),
  [`docs/phase-3-floating-panel.md`](docs/phase-3-floating-panel.md),
  [`docs/phase-3-symbology.md`](docs/phase-3-symbology.md).
- **Phase 4 — Network model** (closed out except `respect_phase`
  enforcement, voltage-boundary tracing, and phase-aware tracing — all
  deliberately deferred pending their own data-modeling passes): a real
  trace engine (`api/internal/trace`) walking `runtime.feature_connectivity`
  edge by edge, per a named `metadata.trace_configurations` profile —
  the four raw directions (`connected`/`downstream`/`upstream`/
  `shortest_path`), with real barrier-stopping against a live feature
  attribute (a seeded `DIST_SWITCH`'s `device_state`, checked fresh at
  each hop, never baked into the graph), plus a generalized
  `target_class_id` that stops a walk at (while still recording) the
  first node of a given class — one mechanism powering
  trace-to-transformer, transformer-to-source, and LV-network tracing,
  with `cross_transformers` finally enforced against it. `POST
  /trace/isolate` composes a downstream trace from a device's
  neighbors (simulating it open without mutating data) with an
  upstream target-class search for the nearest protective boundary,
  plus a critical-facility cross-reference and a backfeed check (every
  live connection an isolation didn't already account for, surfaced
  for a human to verify). `POST /trace/feeder` finds a feature's whole
  feeder regardless of where you start; `POST /trace/section` finds
  the section between the nearest bounding devices in each direction.
  Loop/mesh detection needed no new engine code — a second
  `SHORTEST_PATH` config that, unlike the original, respects live
  barrier state, plus a "Check for loop" trigger in the connect panel.
  A new `SERVICE_POINT` feature class and an extended seeded topology
  (a substation power transformer, two switches) make all of it real
  to run. The floating attribute panel's header has a "Run trace"
  dropdown listing every config by name plus "Isolate from here" for
  junction features, each producing its own floating result table
  (feature class/id/name per row, target/barrier/boundary/backfeed
  rows visually distinct) and map highlight. Every feature, regardless
  of class, can also carry an optional human-readable name (a
  universal system column the compiler adds to every physical table,
  same precedent as `deleted_at`) — set from the create/edit forms,
  shown everywhere a feature is referenced, and searchable via a
  "Search by name…" box in the map toolbar across every feature class.
  Write-ups: [`docs/phase-4-trace-engine.md`](docs/phase-4-trace-engine.md),
  [`docs/phase-4-feature-names.md`](docs/phase-4-feature-names.md),
  [`docs/phase-4-trace-discovery-and-search.md`](docs/phase-4-trace-discovery-and-search.md),
  [`docs/phase-4-utility-traces.md`](docs/phase-4-utility-traces.md),
  [`docs/phase-4-safety-traces.md`](docs/phase-4-safety-traces.md).
- **Phase 5 — Diagrams, attribute groups, MCP** (2 slices done: diagram
  containers + frontend canvas): `runtime.diagram_instances`/
  `diagram_placements` and a new `api/internal/diagram` package let any
  feature be placed at an `x`/`y`/`rotation` inside a diagram belonging
  to a container feature (`SUBSTATION_YARD` is the first real one),
  entirely independent of its real map geometry — gated on
  `metadata.feature_classes.is_diagram_container`, a column that had
  existed since Phase 1 with nothing using it yet. A hand-rolled SVG
  canvas (no new dependency, reuses the map's own symbology/icon
  resolution) renders a diagram in a side-by-side split view next to
  the map — placements as resolved icons, real connectivity as edges
  between placed features, first real consumer of `render_context =
  'diagram'` symbology. An existing feature can be added to an
  already-created diagram straight from the canvas (cross-class name
  search, reusing the same fan-out the map's own search box uses).
  Placements drag to a new position or rotate via a selection handle
  (both write back through the same placement upsert, independently of
  each other) and can be removed from a diagram with a hover-revealed
  control behind a confirm dialog. A `geometry_type = 'line'` feature
  (e.g. `MV_CONDUCTOR`) renders as its own routed wire between its real
  connectivity endpoints instead of a point icon, and the canvas no
  longer auto-draws a generic connector for connectivity that isn't
  backed by an actual placed conductor — a wire only appears when it's
  really there. Diagrams have a visible, editable name (`PATCH
  /diagrams/{id}`, new). A brand-new feature that only ever exists on a
  diagram (`geometry_type = 'none'`, e.g. the seeded `PROTECTIVE_RELAY`)
  can now be digitized straight from the canvas — a picker filtered to
  geometry-less classes, click to place, fill in attributes via the
  same floating `FeaturePanel`/create form the map's own draw flow
  uses, no map presence ever created or expected. The canvas also picks
  connect/relate targets directly (mirroring the map's own click
  handler), highlights the selected or connect-role placement/conductor
  with the map's own highlight colors, has its own independent pan/zoom
  camera with a Select/Pan tool toggle, snap-aligns placements to each
  other while dragging, and no longer paints a stray circle on an
  unconnected conductor's loose end. Curl- and browser-verified end to
  end. Still ahead: multi-segment bus routing, a real
  electrical-schematic symbol library, grouping/annotation boxes, dual
  line semantics for power vs. control/trip circuits, an admin UI to
  create a diagram instance itself, metadata-driven forms, MCP
  data-plane tools.
  Write-up: [`docs/phase-5-diagrams.md`](docs/phase-5-diagrams.md).
- **Phase 6 — Domain expansion + hardening** (slice 1 done: admin
  metadata UI): a new `api/internal/admin` package, mounted at
  `/admin`, is the first write path into `metadata.*` — feature
  classes, subtypes, fields, terminals, domains, field groups — that
  goes through the API rather than a hand-written seed file or
  migration. A wizard (`/admin/feature-classes/new`) builds a
  brand-new feature class end to end in one transaction — subtypes,
  fields (with optional domain/field-group references and per-subtype
  required flags), terminals for edge/junction classes, and one
  default symbology rule — then compiles it with the same
  `compiler.Compile` the CLI has always used. Separate pages
  (`/admin/domains`, `/admin/field-groups`) author the shared reference
  data those fields point at. Deliberately scoped to purely-additive
  operations on a brand-new class only: adding a field to an
  already-compiled class already worked (the compiler's existing-table
  path does a real `ALTER TABLE ADD COLUMN`), but adding a new subtype
  or domain value to a class *already* compiled would silently break
  against a `CHECK` constraint that's only ever written once, at
  initial `CREATE TABLE` — a real compiler gap this slice's scope was
  drawn around rather than papered over. User accounts, authentication,
  and authorization stay a distinct, later effort within this phase —
  genuinely greenfield work, not something to design under the
  time pressure of shipping the metadata UI. Curl- and
  browser-verified end to end. Editing a class's own basic info
  (`PATCH /admin/feature-classes/{code}`) and deleting a draft class or
  an unreferenced domain/field group shipped later the same way —
  scoped to what's provably safe rather than the full editing/deletion
  story.
  Write-up: [`docs/phase-6-admin-metadata.md`](docs/phase-6-admin-metadata.md).
  Since then: editing/deleting an already-compiled class's subtypes/
  domain values, range domains, adding and editing a field on an
  existing class, editing a domain's own values/bounds, and a
  "Rules" tab on the feature panel surfacing every network/
  relationship/snapping rule for a selected feature's class (read-only
  -- write access to those three tables is still hand-authored SQL).
  Write-up for the last one:
  [`docs/phase-6-rules-visibility.md`](docs/phase-6-rules-visibility.md).
  Still ahead: renaming/retyping an existing field, access control,
  asset numbering, units/locale, voltage-boundary and phase-aware
  tracing, load testing.
- **Phase 7 — Field collection**: mobile/offline data capture for field
  crews. Design deferred — noted here as a placeholder only.
- **Phase 8 — Materials + electrical analysis** (materials catalog and
  the load-flow engine done): a new `metadata.material_specs` table lets a coded domain
  value (e.g. a `CONDUCTOR_SPEC` domain's `ACSR_50`) carry real physical/
  electrical properties — resistance, reactance, ampacity, diameter,
  weight — instead of being just a display label. Attaches to the
  *domain value* a field already selects, not to the feature class
  directly, so no compiler or CRUD-engine change was needed: any field
  already using a material-linked domain inherits real numbers for
  free. New Materials admin page, plus a material-spec picker on the
  Domains page's own value editor. The map's own feature panel gained a
  matching read-only Materials tab (replacing the old Rules tab)
  showing a selected feature's resolved engineering numbers, with an
  "Edit in Attributes" jump — a feature's material is already just an
  ordinary attribute, not a separate thing to author. A new
  `api/internal/powerflow` package (`POST /analysis/load-flow`) now
  computes real per-node voltage and per-branch loading across a radial
  feeder — a forward-backward sweep (Kersting's method) whose branch
  impedance comes directly from this catalog (a conductor's own real
  geometry length × its resolved material spec), stopping at an open
  device and rejecting a detected loop/mesh outright rather than
  computing something wrong. Frontend done too: an unconditional "Run
  load flow…" trigger on the map's existing trace/isolate dropdown,
  a prompt-then-results panel in the shared bottom-center slot, and
  three new map highlight colors (normal/overloaded/stopped-at-barrier)
  — mutually exclusive with trace/isolate, same as those two already
  are with each other. `DIST_XFMR` also gained a `rated_kva` field, so
  a transformer's own loading % (apparent power vs. rating) falls out
  of the same sweep a conductor's ampacity loading already does —
  reusing the same loading %/overload fields rather than adding
  parallel ones, and needing zero frontend changes since the results
  table's Loading column already read that field generically. A new
  `POST /analysis/fault-current` endpoint computes available
  symmetrical (3-phase bolted) fault current across the same network —
  one exact forward pass reusing the load-flow engine's own tree-walk
  and impedance resolution directly, with no sweep needed since fault
  current has no demand-voltage circular dependency to iterate over.
  No frontend for it yet.
  Write-ups: [`docs/phase-8-materials.md`](docs/phase-8-materials.md),
  [`docs/phase-8-load-flow.md`](docs/phase-8-load-flow.md),
  [`docs/phase-8-fault-current.md`](docs/phase-8-fault-current.md).

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

With the API and Martin already running (`task api-dev` and `task
infra-up`, above), from a second terminal:

```
cd web
npm install
npm run dev
```
Open `http://localhost:5173` — it lands on the map. Vite proxies
`/api/*` to the Go API at `localhost:3704` and `/tiles/*` to Martin at
`localhost:3701`, so the frontend never needs CORS configured on
either. See [`docs/phase-3-ui-shell.md`](docs/phase-3-ui-shell.md) and
[`docs/phase-3-map-loop.md`](docs/phase-3-map-loop.md) for what to
expect and how each was validated.

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
