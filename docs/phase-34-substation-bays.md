# Phase 34 — Substation bays (control-building panels)

A **bay** is one circuit of a substation seen from its control building: an
incomer, an outgoing feeder, a bus coupler, a bus section, or a transformer
bay. In a control building each circuit gets a **panel** — its protection
relays, metering, control switches, and a mimic of that circuit. We model the
**circuit/bay**, not the literal panel face (that's too detailed, and the user
was explicit: a generic representation will do). The panel is then just how a
bay is drawn on a diagram.

The bay is the **intermediate grouping** between the substation and its plant.
Today containment is flat — `SUBSTATION_YARD` contains its breakers/CTs/VTs
directly. A bay slots in between: `SUBSTATION_YARD → BAY → {breaker, isolator,
CT, VT, fuse, relay}`. This is the first **two-level containment** in the repo.

## Why this shape (decided with the user)

- **One class, `BAY`, is the circuit.** The panel is not a separate entity —
  it's the bay drawn on a control-scheme/one-line diagram. (Revisit only if
  panels ever need to be inventoried independently of the circuit, or the
  panel↔circuit mapping stops being 1:1.)
- **`geometry_type = 'none'`.** A bay has no map footprint; it exists only as a
  logical grouping placed on a diagram, exactly like `PROTECTIVE_RELAY`. The
  compiler skips the `geom` + `territory_id` columns for `none`
  ([ddl.go](../api/internal/compiler/ddl.go) `buildCreateTable`) and never
  reads `network_role`, so `none` + `container` compiles cleanly (verified by
  reading the compiler — it branches only on `geometry_type`).
- **`network_role = 'container'`.** The honest label — a bay exists to contain
  other features, the same role `METER_CHAMBER` carries. Not a traced node, no
  terminals, no `network_rules`. (Note `SUBSTATION_YARD` is a containment
  origin while being `network_role='none'`, so containment does **not** require
  the `container` role — but `container` is the accurate semantic here.)
- **Role is a field (`bay_role`), not a subtype.** The functional variant
  (incomer/feeder/coupler/section/transformer) is a `BAY_ROLE` coded domain on
  a field, with a single `standard` subtype — the same call `PROTECTIVE_RELAY`
  made with `device_number`. Diagram symbology can still key on `bay_role`'s
  `domain_value_id` for role-specific glyphs (Slice 3).
- **Containment kind is `containment`, not `composite`.** The plant must be
  able to **stand on its own** — a breaker can exist unassigned to any bay, and
  deleting a bay must not delete its breaker. `composite` is VoltEdge's
  stronger, ownership-like kind (`SERVICE_POINT owns METER`, cascade-ish) and
  is deliberately **not** used. Relationships are explicitly established and
  never mandatory anyway, so an unassigned device is always valid.
- **Bay-only re-parenting (Slice 2).** The circuit's plant moves to be
  contained by the bay; the flat `yard → breaker/CT/...` rows are retired. But
  the **busbar stays contained by the yard** — the bus is shared across bays,
  not owned by any one of them.

## Slice 1 — the `BAY` feature class

[seed/bay_example.sql](../seed/bay_example.sql) (task `seed-bay`, then
`compile CODE=BAY`):

- `field_group` **`bay`**.
- domain **`BAY_ROLE`** (coded): `INCOMER`, `FEEDER` (outgoing feeder),
  `BUS_COUPLER`, `BUS_SECTION`, `TRANSFORMER`. `VOLTAGE_CLASS` (11 kV / 33 kV)
  and `CONDITION_RATING` are re-asserted defensively so the file is
  order-independent against the other seeds.
- feature class **`BAY`** — `geometry_type='none'`, `srid NULL`,
  `network_role='container'`, one `standard` subtype.
- fields: `bay_role` (BAY_ROLE, required), `voltage_class` (VOLTAGE_CLASS,
  required), `bay_ref` (asset/panel tag, nullable, like
  `METER_CHAMBER.chamber_ref`), `condition_rating` (optional).
- `subtype_field_rules`: `bay_role` + `voltage_class` required for `standard`.

Compiles to a `gis.bay` table (id, name, subtype, status, deleted_at, the four
fields, audit columns — no `geom`, no `territory_id`).

## Slice 2 — containment wiring (code-complete)

[seed/bay_containment_example.sql](../seed/bay_containment_example.sql) (task
`seed-bay-containment`) — metadata-only, no instances:

- `relationship_classes`: `SUBSTATION_YARD contains BAY` and `BAY contains
  {MV_BREAKER, MV_ISOLATOR, MV_SECTIONALIZER, MV_FUSE, MV_ARRESTER, MV_CT,
  MV_VT, PROTECTIVE_RELAY}`, all `containment` / `one_to_many`, guarded on
  `(origin, destination, kind)`. `MV_ARRESTER` is included — every per-circuit
  plant class that was flat-under-yard moves under the bay; only the shared
  busbar stays with the yard.
- Retires the flat `SUBSTATION_YARD contains {breaker/isolator/sectionalizer/
  fuse/arrester/CT/VT}` rows with a `DELETE` guarded by `NOT EXISTS` on
  `runtime.feature_relationships`, so a class with an established instance is
  left in place (the FK is `RESTRICT`, so this keeps the seed from erroring and
  never orphans a real link). [seed/mv_busbar_example.sql](../seed/mv_busbar_example.sql)
  is also narrowed at the source to seed only `SUBSTATION_YARD contains
  MV_BUSBAR` on a fresh DB.
- **No instance seeding.** Every seed in the repo is metadata-only (verified by
  grep — no `INSERT INTO gis.*` / `runtime.feature_relationships` /
  `runtime.feature_connectivity` in `seed/`). Actual bay↔device links are
  app/API-established at runtime, not seeded.

## Slice 3 — depiction (planned)

- A generic **bay block** glyph on the diagram canvas, keyed on `bay_role`, so
  each bay renders as a labeled block hanging off the bus on the one-line /
  panel-lineup — the `double_busbar_template` "Bay 1/2/3" slots made real and
  data-backed.

## BAY vs the two "panel" classes — which grouping, when

Three classes can group a substation's kit, and they are **not** duplicates —
they answer different questions and none touches the power engine (topology
rides on terminals + `feature_connectivity`, independent of the container). Read
this if `BAY`, `MV_SWITCHGEAR_PANEL`, and `SUBSTATION_PANEL` look overlapping:

- **`BAY`** — the **logical circuit** (incomer/feeder/coupler/…). The organizing
  unit of an **outdoor / AIS** substation one-line. `bay_role` on `BAY_ROLE`.
- **`MV_SWITCHGEAR_PANEL`** (Phase 42) — a **physical cubicle** in an indoor
  metal-clad `MV_SWITCHBOARD` lineup. The organizing unit of a **compact /
  indoor MV** substation. It contains the *same primary devices* a bay would
  (breaker/CT/VT…) and its `panel_role` currently **borrows `BAY_ROLE`** — which
  is exactly why it can read as a `BAY` clone.
- **`SUBSTATION_PANEL`** (Phase 45) — a **secondary/control-building cabinet**
  (protection/control/metering/RTU). Holds relays + meters, not primary plant;
  `network=none`.

**The rule of thumb:** `BAY` and `MV_SWITCHGEAR_PANEL` are an **either/or by
substation topology** — an outdoor AIS site is organized into *bays*; an indoor
metal-clad site is organized into a *board of cubicles*. A given device
**instance is contained by exactly one** parent, and a site normally uses one
idiom, so in practice they don't compete. `SUBSTATION_PANEL` is **orthogonal**
to both — a relay can sit in a control cabinet *and* serve a bay
(`SUBSTATION_PANEL serves BAY`, `panel_bay` many-to-many), because primary-plant
grouping and secondary-cabinet grouping are independent axes.

The only real wrinkle (documented, not fixed — deliberate 2026-09-11 call): the
primary cubicle **substitutes** for a bay while the secondary cabinet
**coexists** with it, and the cubicle borrows `BAY_ROLE`. If indoor panels ever
need to diverge from outdoor bays, repoint `MV_SWITCHGEAR_PANEL.panel_role` to
its own `PANEL_ROLE` domain — a one-line field change flagged in
[seed/mv_switchgear_panel_example.sql](../seed/mv_switchgear_panel_example.sql).

## Verification owed (Bash blocked here — run locally)

- Slice 1: `task seed-bay` then `compile CODE=BAY`; confirm `gis.bay` is
  created with the four fields and no `geom`/`territory_id`, the `bay_role` /
  `voltage_class` / `condition_rating` CHECK constraints match their domains,
  and `BAY` shows up as a creatable `geometry_type='none'` class (diagram
  placement only) in the app.
- Slice 2: `task seed-bay-containment`; confirm the `yard → BAY` and eight
  `BAY → plant` `relationship_classes` exist, the flat `yard → plant` rows
  (except `MV_BUSBAR`) are gone, and re-running the task is idempotent (guards
  hold, no dupes, no error).
- api: `go build ./...`, `go vet ./...`.
