# Phase 5 (slice 1) — diagram containers, backend only

**Goal:** the first slice of Phase 5 (README: "Diagrams, attribute
groups, MCP") — a real data model and API for placing features on a
schematic diagram, independent of their real map geometry. Deliberately
backend-only: no canvas view yet, matching how connectivity, relationships,
and the trace engine all shipped in this project — the data model proven
solid via curl against a real Postgres/API stack before any UI is built
on top of it.
**Status:** done and verified against a real Postgres/API stack —
create/get/list/delete a diagram instance, place/move/list/remove a
feature on it, both zero-trust gates (non-container feature class,
unknown diagram type), cascade delete, and 404 handling all confirmed
with real curl calls, not assumed from the code.

## Why this slice exists, and why backend-only

The user asked "what is next? diagrams?" and, once the actual size of
the surface became clear (a new data model, a new API, and a genuinely
new frontend canvas view — the biggest jump in scope of any slice this
session), was asked how to approach it rather than treating it as
another quick item. Two decisions came back explicit:

- **Backend first**, matching how every other major feature in this
  project started (connectivity, relationships, the trace engine all
  shipped backend-first, curl-verified, before any UI) — the diagram
  placement model and `render_context = 'diagram'` symbology (unused
  since Phase 3) both deserve their own focused UI slice once the data
  model is proven, not built and verified for the first time in the
  same pass as a canvas.
- **Flip `SUBSTATION_YARD` to a diagram container** for the first real
  diagram, rather than also proving `geometry_type = 'none'` (a
  feature that only ever exists via a diagram placement, never on the
  map) in the same slice — that path is real and already supported end
  to end (see "What was already there"), just not exercised with an
  actual feature class yet.

## What was already there

Before writing anything, the actual code was checked rather than
assumed: `docs/metadata-core.md`'s "Diagram containers" section had
already designed this in Phase 1, and most of the foundation was
already built and working, just never exercised:

- `metadata.feature_classes.geometry_type` has allowed `'none'` since
  the very first migration (`0007_create_feature_classes.sql`) — a
  feature class with no `geom` column at all, whose only visual
  representation is ever a diagram placement.
- `metadata.feature_classes.is_diagram_container` has existed since
  that same migration, already commented as "true for feature classes
  that can host a one-line/schematic diagram of the features inside
  them."
- The schema compiler (`api/internal/compiler/ddl.go`) already skips
  generating a `geom` column when `geometry_type = 'none'` --
  `buildCreateTable` guards the call to `geometryType()` behind exactly
  that check.
- The CRUD engine (`api/internal/crud/validate.go`) already rejects a
  geometry on a `geometry_type = 'none'` feature class, and requires
  (and type-checks) one otherwise.

None of that needed to change. What was actually missing was the
`diagram_instances`/`diagram_placements` tables themselves and
everything above them.

## What was built

- **`migrations/0019_create_diagram_tables.sql`** — `runtime.diagram_instances`
  and `runtime.diagram_placements`, in the `runtime` schema
  `migrations/0016` already reserved and already named ahead of time
  ("`feature_connectivity`/`feature_relationships`, attachments,
  diagram tables"). `diagram_instances` ties one diagram to a
  container feature (polymorphic `feature_class_id` + `feature_id`,
  same pattern as `feature_connectivity`'s `from`/`to`) and a
  `diagram_type_value_id` — a `domain_values` row, "just another row in
  domains, not a new lookup table," same as trace direction.
  `diagram_placements` puts any feature at an `x`/`y`/`rotation` inside
  one diagram instance, unique per (diagram, feature) so placing the
  same feature twice moves it rather than duplicating it. No `is_valid`
  gate on placement itself — the only legality question this whole
  model has is whether the container's feature class has
  `is_diagram_container = true`, enforced by the API before ever
  writing a `diagram_instances` row.
- **`metadata.ResolveDomainValueByCode`** (new, `api/internal/metadata/load.go`)
  — `DomainValueCode`'s inverse: given a domain code and a value code
  (e.g. `DIAGRAM_TYPE`/`ONE_LINE`), returns the `domain_values` row.
  Generically useful, not diagram-specific — the existing
  `DomainValueCode` only ever went id → code, and this is the first
  place in the system that needed the reverse (a human-typed code from
  a request body, resolved to the id a foreign key actually needs).
- **`api/internal/diagram`** (new package) — deliberately not shaped
  like `connectivity`/`relationship`: there's no rule table to match a
  placement against and no cardinality, so it's closer to a small,
  explicit CRUD surface than a zero-trust rule engine:
  - `POST /diagrams` — create a diagram instance for a container
    feature. Resolves the container, rejects if its feature class
    isn't a diagram container, resolves `diagram_type` against the
    `DIAGRAM_TYPE` domain, writes. No upsert/idempotency here unlike
    connectivity/relationship's `establish` — a container can host more
    than one diagram (a one-line and a control-scheme diagram for the
    same substation is a real, expected case per
    `docs/metadata-core.md`), so there's no natural key to upsert
    against.
  - `GET /diagrams?container_feature_class=&container_feature_id=` —
    list every diagram instance hosted by one container feature.
  - `GET /diagrams/{id}`, `DELETE /diagrams/{id}` (cascades to its
    placements via `ON DELETE CASCADE`).
  - `POST /diagrams/{id}/placements` — place or move a feature: an
    idempotent upsert on the table's own unique constraint, same
    pattern connectivity/relationship's own upsert-on-conflict
    establish already uses.
  - `GET /diagrams/{id}/placements`, `DELETE /diagrams/{id}/placements/{placementID}`.

## How this was validated

Driven against a real Postgres + Go API, not assumed from the code:

1. Applied migration 0019 (`runtime.diagram_instances`/`diagram_placements`
   created with all FKs, indexes, and the explicitly-named unique
   constraint — its natural Postgres-derived name would have exceeded
   the 63-byte identifier limit and been silently truncated, so it's
   named explicitly instead).
2. Seeded a `DIAGRAM_TYPE` domain + `ONE_LINE` value, and flipped
   `SUBSTATION_YARD.is_diagram_container` to `true` (`seed/diagram_example.sql`
   — schema-level only, same discipline every other `runtime.*` seed
   file in this project follows: the actual instance rows always come
   from a real API call, never a raw `INSERT`, so nothing is ever
   speculative even in demo data).
3. Created a real `SUBSTATION_YARD` feature ("Main Substation Yard")
   via `POST /features/SUBSTATION_YARD`, then a real diagram instance
   for it via `POST /diagrams`, then placed the real "Substation Power
   Transformer" (`DIST_XFMR`) and "Switch S0" (`DIST_SWITCH`) features
   on it via `POST /diagrams/{id}/placements`.
4. Confirmed `GET /diagrams` (by container), `GET /diagrams/{id}`, and
   `GET /diagrams/{id}/placements` all return the expected data.
5. Confirmed placing the transformer a second time at new
   `x`/`y`/`rotation` moved it (same placement `id`, same total
   placement count) rather than creating a duplicate.
6. Confirmed both rejection gates: `POST /diagrams` against `DIST_XFMR`
   (a real feature class, just not a diagram container) rejected with
   `is_diagram_container = false`; against an unknown `diagram_type`
   rejected with a clear "not a valid DIAGRAM_TYPE value" message.
7. Confirmed `DELETE .../placements/{id}` removes one placement without
   touching others, and `DELETE /diagrams/{id}` cascades to remove
   every placement on it (tested against a disposable throwaway
   instance, not the real demo one).
8. Confirmed `GET /diagrams/{nonexistent-id}` returns a real 404 with a
   clear error message, not a generic failure.

`go build ./... && go vet ./... && gofmt -l .` all finish clean.

## Decisions made

- **Backend-only this slice.** See "Why this slice exists" above.
- **`SUBSTATION_YARD` as the first container, not a new
  `geometry_type = 'none'` feature class.** Proves the placement
  model with features that already exist; the geometry-less path is
  real and already supported by the compiler/CRUD engine (see "What
  was already there") but deliberately not exercised with an actual
  feature class in this slice.
- **No upsert on `POST /diagrams`.** Unlike connectivity/relationship's
  `establish`, a container hosting more than one diagram is expected,
  not a duplicate to collapse.
- **`ResolveDomainValueByCode` added to the shared `metadata` package,
  not kept local to `diagram`.** The reverse-of-`DomainValueCode`
  lookup (human-typed code → id) is a generically useful capability any
  future package accepting a coded value in a request body would also
  need, not something specific to diagrams.

## What's still open

- **The frontend canvas itself.** Nothing renders a diagram yet — no
  route, no component, no drag-to-place interaction. This is the next
  slice once diagrams get picked back up.
- **`render_context = 'diagram'` symbology.** `metadata.symbology_rules`
  has carried this value since Phase 1 and it's been loadable since
  Phase 3's symbology resolution work, but nothing has ever resolved or
  rendered it — a diagram canvas is what would finally consume it.
- **`geometry_type = 'none'` with a real feature class.** The compiler
  and CRUD engine already support it (see "What was already there"),
  but nothing in the seeded data actually exercises it yet (e.g. a
  `PROTECTIVE_RELAY` that only ever appears via a diagram placement).
- **No admin UI for authoring `DIAGRAM_TYPE` values or flipping
  `is_diagram_container`.** Both are real metadata changes today made
  by hand (seed SQL / a migration), same as every other metadata-level
  change in this project pre-admin-UI.

## Try it yourself

Backend only this slice — no frontend change. Against a running API
(`DATABASE_URL=... go run ./cmd/server`, migrations 0001-0019 applied,
`seed/diagram_example.sql` applied):

```bash
# Create a container feature
curl -X POST http://localhost:3704/features/SUBSTATION_YARD \
  -H "Content-Type: application/json" \
  -d '{"subtype": 1, "name": "Main Substation Yard",
       "geometry": {"type": "Polygon", "coordinates": [[[-0.1315,51.5085],[-0.1285,51.5085],[-0.1285,51.5095],[-0.1315,51.5095],[-0.1315,51.5085]]]},
       "attributes": {"condition_rating": "GOOD", "enclosure_type": "FENCED"}}'

# Create a diagram instance for it -- note the container's real id from above
curl -X POST http://localhost:3704/diagrams \
  -H "Content-Type: application/json" \
  -d '{"container": {"feature_class": "SUBSTATION_YARD", "feature_id": "<id>"},
       "diagram_type": "ONE_LINE", "name": "Main Substation Yard - One-Line"}'

# Place a feature on it -- note the diagram's real id from above
curl -X POST http://localhost:3704/diagrams/<diagram-id>/placements \
  -H "Content-Type: application/json" \
  -d '{"feature_class": "DIST_XFMR", "feature_id": "<xfmr-id>", "x": 100, "y": 200, "rotation": 0}'

# List what's on it
curl http://localhost:3704/diagrams/<diagram-id>/placements
```

`go build ./... && go vet ./... && gofmt -l .` should all finish clean.
