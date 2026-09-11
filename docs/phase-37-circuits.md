# Phase 37 — Circuits

A **circuit** is a named, network-wide electrical identity — the MV feeder
"Old Age 2", say — that many features across many classes belong to: its
fuses, conductors, isolators, transformers, and so on. Before this phase the
only trace of a circuit was `circuit_ref` (Phase 35): a free-text label on
MV/LV conductors, useful for disambiguating double circuits on the map but not
a first-class thing you could filter a trace by, stop a trace at, or summarise
results per. This phase makes the circuit a real entity.

## What the user wants (all four)

1. **Show it in results** — a trace/analysis result says which circuit each
   feature is on.
2. **Stop at circuit boundary** — a trace can halt when it would leave the
   circuit it started in.
3. **Filter to one circuit** — scope a query/trace to a single circuit.
4. **Analysis by circuit** — group and summarise results per circuit.

## Shape (decided)

- **Circuits are a lookup table** (`gis.circuits`), not a bare domain — a
  circuit has properties (network, voltage, a source feature) a domain value
  can't carry, and it's curated in-app like territories.
- **MV and LV are separate namespaces.** An MV "Old Age 2" and an LV circuit
  of the same name are unrelated; the name is only unique within a network.
  Voltage is a property of the (MV) circuit — "could be 11 or 33" — nullable
  until known.
- **Membership is derived by a downstream trace** from the circuit's source
  feature, **with manual override** — not hand-maintained per feature.
- Circuits are **flat** (no hierarchy) and carry **no geometry** of their own;
  a circuit's extent is the union of the features stamped onto it.

## Slice plan

1. **Catalog** (this slice): `gis.circuits` table + CRUD API + admin UI.
2. **Membership field**: a `circuit_id` metadata field on the electrical
   apparatus classes (MV classes → MV circuits, LV → LV) + a create/edit
   circuit picker scoped to the class's network.
3. **Assign-by-trace**: `POST /circuits/{id}/assign` → downstream/feeder trace
   from the source → bulk-stamp `circuit_id` on every feature reached. Manual
   override via the normal feature edit. (The first bulk feature write.)
4. **Trace integration**: "filter to one circuit" + "stop at circuit boundary"
   (a new *relative* barrier — halt when the circuit differs from the start's,
   not a fixed value).
5. **Analysis & display**: enrich trace/analysis output with the circuit,
   group/summarise per circuit, and reconcile the map label (denormalise the
   circuit name at stamp time so a tile carries the name, not just the uuid).

---

## Slice 1 — the circuit catalog

Modelled directly on `api/internal/territory` (a `gis`-schema lookup entity
curated in-app through a CRUD API + admin UI), but **flat**: no level catalog,
no parent tree, no geometry, no spatial resolver.

### Storage — `gis.circuits` (migration 0062)

| column                 | type          | notes                                              |
| ---------------------- | ------------- | -------------------------------------------------- |
| `id`                   | uuid pk       | `gen_random_uuid()`                                |
| `name`                 | text not null |                                                    |
| `network`              | text not null | `CHECK IN ('MV','LV')`                             |
| `voltage_kv`           | numeric       | nullable, `CHECK NULL OR > 0`                      |
| `source_feature_class` | text          | nullable — the class the assign-by-trace starts at |
| `source_feature_id`    | uuid          | nullable — not a DB FK (row lives in a per-class table) |
| `created_at`           | timestamptz   | `now()`                                            |
| `updated_at`           | timestamptz   | `now()`                                            |

Indexes: a **unique** index on `(network, lower(name))` (one circuit name per
network, case-insensitive) and a plain index on `network` (every picker
filters by it). The source reference is deliberately **not** a foreign key —
the referenced feature lives in a per-class physical table the catalog can't
FK to, the same no-enforced-join convention `territory_id` follows.

### Permission — `circuits:write` (migration 0063)

Mirrors `territories:write`: the **read** side rides `features:read` (every
viewer reads the catalog to filter a trace and to resolve a circuit's name for
a label); the **mutating** verbs need `circuits:write`, granted to `admin` +
`break-glass-admin` only (curating the catalog is administrative — a later
slice's assign-by-trace, which writes feature data, will gate on
`features:write` instead). `auth.PermCircuitsWrite` is the Go constant.

### API — `api/internal/circuit`

- **models.go** — the `Circuit` bun model + the `NetworkMV`/`NetworkLV`
  constants.
- **circuit.go** — `InvalidError`/`invalidf`/`IsInvalid` (client-fixable → 400,
  the same split territory/crud draw) and the CRUD functions:
  `ListCircuits(network)`, `GetCircuit`, `CreateCircuit`, `UpdateCircuit`,
  `DeleteCircuit`. Validation: name required; network ∈ {MV, LV}; voltage
  positive-or-null; the source reference all-or-nothing; and a
  case-insensitive duplicate-name check per network (`ensureNameFree`) that
  turns the raw unique-index violation into a clear 400. **Network is
  immutable** on update (it's the identity namespace) — change it by deleting
  and recreating.
- **http.go** — `Routes(db)`: `GET /` (optional `?network=` filter), `POST /`,
  `PATCH /{id}`, `DELETE /{id}`, with `writeJSON`/`writeError`.

Mounted in `api/cmd/server/main.go` beside territories:
`r.With(auth.RequirePermission(auth.PermFeaturesRead, auth.PermCircuitsWrite)).Mount("/circuits", circuit.Routes(db))`.

### Web admin UI

- **web/src/hooks/use-circuits.ts** — `useCircuits()` (long staleTime, like
  `useTerritories`), `circuitNameMap()` (id → name lookup, for a later slice's
  label resolution), and the `useCreateCircuit`/`useUpdateCircuit`/
  `useDeleteCircuit` mutations (each invalidates `["circuits"]`).
- **web/src/routes/admin-circuits.tsx** — `AdminCircuitsPage`: circuits grouped
  into an **MV** card and an **LV** card, each with its own "Add" button
  (create dialog pre-scoped to that network). Create dialog = network (fixed
  after) + name + optional voltage; edit dialog = name + voltage (network
  shown read-only, blank voltage clears it); delete via `ConfirmDeleteDialog`.
- **web/src/app.tsx** — route `admin/circuits` behind
  `RequirePermission perm="circuits:write"`.
- **web/src/components/layout/app-sidebar.tsx** — a "Circuits" nav item (`Zap`
  icon) gated on `circuits:write`.

## Slice 1 — verification owed (Bash blocked here)

- api: `go build ./...`, `go vet ./...`, `gofmt -l api/internal/circuit`.
- web: `tsc --noEmit`, lint, `vite build`.
- migrate: run 0062 + 0063.
- manual: as an admin, create an MV "Old Age 2" (11 kV) and an LV "Old Age 2"
  (both allowed — separate namespaces); a second MV "old age 2" is rejected
  (case-insensitive, per network); edit the voltage and clear it; delete a
  circuit; confirm a non-admin sees no Circuits nav item and gets 403 on the
  write endpoints but can still `GET /circuits`.

---

## Slice 2 — circuit membership field + network-scoped picker

Gives a feature a real, network-scoped `circuit_id` (a `gis.circuits` uuid),
picked from the catalog by name. Two orthogonal facts drive it: **which
network** a class sits on, and the **membership field** itself.

### Network as a class attribute (migration 0064)

Feature classes had no MV/LV attribute — `network_role` is the connectivity
role (edge / junction / container / none), unrelated to voltage, and the code
prefix (`MV_`/`LV_`) is unreliable (`DIST_XFMR`, `SERVICE_POINT`, `METER`,
`STREET_LIGHT`, `PROTECTIVE_RELAY` carry none). So a nullable
`network text CHECK (NULL OR IN ('MV','LV'))` column is added to
`metadata.feature_classes`, mirroring the `network` on `gis.circuits`. It's
**pure metadata** the compiler's DDL never reads — like `label_def` it just
rides along on `metadata.Load`'s struct-model select (`FeatureClass.Network
*string`), and is surfaced on the schema endpoint
(`featureClassSchemaResponse.Network`, `omitempty`). No admin editor yet —
the seed sets it; an admin toggle is a deferred follow-up.

### The `circuit_id` field + network stamp (seed)

`seed/circuit_membership_example.sql` (task `seed-circuit-membership`), applied
like the `circuit_ref` seed, then **recompile each stamped class**:

1. Stamps `network` MV/LV on every electrical apparatus class. **MV:**
   `MV_CONDUCTOR, MV_BUSBAR, MV_BREAKER, MV_ISOLATOR, MV_SECTIONALIZER, MV_FUSE,
   MV_ARRESTER, MV_CT, MV_VT, DIST_XFMR, DIST_SWITCH, PROTECTIVE_RELAY`. **LV:**
   `LV_CONDUCTOR, LV_BUSBAR, LV_LINK_BOX, SERVICE_POINT, SERVICE_CUTOUT, METER,
   STREET_LIGHT`. The `DIST_XFMR`/`DIST_SWITCH`/`PROTECTIVE_RELAY` straddlers
   are **MV** (the transformer's primary identity is its MV feeder — the very
   boundary Slice 4 stops at; substation switching/relaying is MV-side).
   Structural classes (`POLE`, `BAY`, `SUBSTATION_YARD`, `METER_CHAMBER`) stay
   NULL — no membership.
2. Adds a nullable `circuit_id` field (`data_type uuid`, no domain, existing
   `electrical` group) to every class stamped with a network (`WHERE network IS
   NOT NULL`). Compiles to a bare `uuid` column (`compiler/ddl.go` postgresType)
   flowed by generic CRUD exactly like `circuit_ref` — **no API/compiler
   change**. A soft reference like `territory_id`, not a DB FK.

### Frontend — the circuit picker

- `FeatureClassSchema.network?: "MV" | "LV"` added (use-feature-class-schema).
- `web/src/components/map/circuit-picker.tsx` — `CircuitPicker`: a `<select>`
  over `useCircuits()`, value = circuit uuid, option text = name (+voltage).
  Scoped to the `network` prop when set (MV class → MV circuits); when absent,
  all circuits grouped by MV/LV `<optgroup>`. A referenced circuit missing from
  the filtered list is kept as a "(current circuit)" option so an edit
  round-trips existing membership rather than silently dropping it.
- `feature-field-input.tsx` — `FieldInput` special-cases `field.name ===
  "circuit_id"` → `<CircuitPicker>` (the same by-name hook `circuit_ref` used
  for its label, not a data_type branch), with a new `network` prop.
- `feature-create-form.tsx` / `feature-edit-form.tsx` — both pass
  `network={schema.network}` into `FieldInput`.

### Slice 2 — verification owed

- api: `go build ./...`, `go vet ./...`.
- web: `tsc --noEmit`, lint, `vite build`.
- migrate: run 0064; `task seed-circuit-membership`; recompile each stamped
  class (`task compile CODE=…`) so the physical tables gain `circuit_id`.
- manual: open an MV apparatus create form — the `circuit_id` field shows a
  picker listing only MV circuits; an LV apparatus lists only LV; a class with
  no network (if any electrical one were left unstamped) groups both. Save,
  reopen the edit form — the picked circuit round-trips. A structural class
  (POLE) shows no `circuit_id` field at all.

### Deferred follow-ups

- Admin editor for a class's `network` (seed-only for now).

---

## Slice 3 — assign-by-trace (the first bulk feature write)

`POST /circuits/{id}/assign` traces downstream from the circuit's source
feature and bulk-stamps `circuit_id` (and, where the column exists,
`circuit_ref = the circuit name`) onto every feature reached. Every other
feature write in the codebase is single-row; this is the first bulk one.

### Membership model — full sync (decided)

A run is a **full sync**: it first **clears** this circuit off every in-scope
class, then **stamps** the freshly-traced set. So a feature that no longer
traces from the source (a switch was opened, the topology changed) is
**dropped** — membership always equals "what traces from the source right
now." A hand override on a dropped feature is therefore also cleared on the
next run; manual edits are one-off corrections, not a durable pin. (The
alternatives — additive-only, or a provenance flag that protects hand edits —
were considered and rejected for this slice; full sync is the simplest model
that keeps membership honest.)

The whole clear+stamp runs in **one transaction** (`db.RunInTx`): a mid-run
failure leaves membership exactly as it was, never half-synced. The trace
itself is read-only and runs just before the transaction.

### Engine — `api/internal/circuit/assign.go`

- `AssignCircuit(ctx, db, circuitID)` — loads the circuit (400 if it has no
  source), runs `trace.RunTrace` with the stable `DOWNSTREAM_TRACE` config from
  the source, then full-syncs in a transaction.
- **In-scope classes** (`loadScopeClasses`): the compiled classes whose
  `network` matches the circuit's *and* which carry an active `circuit_id`
  field — one metadata query, also reporting per class whether it has
  `circuit_ref`. An empty result (Slice 2 seed/recompile not run) is a
  client-fixable 400.
- **Network-scoped safety (load-bearing):** reached features are filtered to
  in-scope classes, so an MV circuit can never stamp an LV feature. This is the
  *only* guard, not belt-and-braces: `DOWNSTREAM_TRACE` carries
  `cross_transformers = false`, but the trace engine does **not** enforce that
  flag yet (`seed/trace_configurations_example.sql` notes it's "accepted but not
  read"; see docs/phase-4-trace-engine.md), so a downstream walk from an MV
  source *can* cross a `DIST_XFMR` into the LV network. The network filter is
  what keeps the stamp on the right side. (If a later slice teaches the engine
  to honor `cross_transformers`, this filter still holds — it just becomes
  redundant.)
- **Writes** are raw per-class `UPDATE gis.<table> SET circuit_id = ?[,
  circuit_ref = ?], updated_at = now(), updated_by = ? WHERE …` with
  `bun.In(ids)` for the stamp and `WHERE circuit_id = ?` for the clear, both
  `AND deleted_at IS NULL`. `updated_by` follows crud's actor rule (NULL when
  no session). `circuit_ref` is written only on the two conductor classes that
  have the column — so the `{circuit_ref}` map label now self-populates from
  the circuit name, closing the Phase 35 loop ("id is identity, ref
  auto-fills").
- Returns `AssignResult{reached, stamped, cleared, per_class[]}`.

### Endpoint

`POST /circuits/{id}/assign`, registered inside `circuit.Routes` but wrapped
with an extra `features:write` gate. The `/circuits` mount already gates
mutations on `circuits:write`, so assign effectively requires **both**
`circuits:write` and `features:write` — you are curating a circuit *and*
bulk-writing features; admin / break-glass-admin hold both. This is the only
reason the `circuit` package imports `auth`.

### Deferred to Slice 3b (not built)

- **UI trigger + source picker.** The admin dialogs (Slice 1) set only
  name/voltage/network. A circuit's `source_feature_class`/`source_feature_id`
  is settable today only through the API (`POST`/`PATCH /circuits`); picking a
  source feature on the map, and an "Assign" button that calls this endpoint
  and shows the returned counts, are a follow-up. The endpoint is fully usable
  now via the API.
- MCP `assign_circuit` tool (Phase 18 pattern), if agents should trigger it.

### Slice 3 — verification owed

- api: `go build ./...`, `go vet ./...` (new import: `circuit` → `trace`,
  `auth`; confirmed no cycle — `trace` does not import `circuit`).
- Depends on Slice 2 seed applied + classes recompiled (the `circuit_id`
  columns must physically exist) and `DOWNSTREAM_TRACE` seeded
  (`seed/trace_configurations_example.sql`).
- manual: set a MV circuit's source to a feeder-head feature (PATCH), `POST
  /circuits/{id}/assign` → response counts non-zero; spot-check that reached
  conductors carry both `circuit_id` and `circuit_ref = name` and the map label
  shows the name; open a switch and re-assign → the now-unreachable features are
  cleared (full sync); a circuit with no source → 400; a caller with
  `features:write` but not `circuits:write` (or vice versa) → 403.
