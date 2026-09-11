# Phase 47 — Substation site model (overhaul)

**Status:** Slices 1–2 stand as built; **Slice 3 (`VOLTAGE_LEVEL`) retired and
Slice 4 (CIM) simplified** in the minimal-model correction below. Code-complete,
verification owed (Bash blocked here, DB apply + compile run locally).

## Minimal-model correction (2026-09-11)

After Slices 1–4 landed, the user pushed back that the model was over-built. The
decisive observation: **the container hierarchy has exactly one consumer — CIM
export.** Trace / power-flow / fault ride entirely on terminals +
`runtime.feature_connectivity` and never read the container tree, so modeling a
tier as its own *object* buys zero analytical value. Two best-practice cuts
followed (the choices are recorded in memory):

- **`VOLTAGE_LEVEL` is retired** (was Slice 3). Voltage level is **derivable** —
  every piece of equipment already carries its own nominal voltage — so a separate
  object is duplicated truth that can only drift. Voltage now rides on each
  equipment's `BaseVoltage` reference at export (unchanged since Phase 16); the
  CIM shape is `Substation → Bay → equipment`.
- **`BAY` is kept** — a bay is a genuine *named operational grouping*, not
  derivable (two breakers at the same voltage can be in different bays), and it is
  a home for real bay-level attributes. But it is now **single-parent under
  `SUBSTATION`** (the `VOLTAGE_LEVEL contains BAY` edge is gone).
- **Both "two legal parents" idioms are removed** → the model is single-parent
  throughout. `MV_BUSBAR`/`BAY` hang directly off `SUBSTATION`; `SUBSTATION_PANEL`
  hangs directly off `SUBSTATION`; `CONTROL_BUILDING` stays as an **optional map
  polygon but is no longer a containment parent**.

Applied by [migration 0086](../migrations/0086_remove_voltage_level_tier.sql)
(drops the `VOLTAGE_LEVEL` class + its containment edges + the
`CONTROL_BUILDING contains SUBSTATION_PANEL` edge, data-aware and idempotent). The
sections below are annotated where they were superseded; the original Slice 3/4
text is kept struck-through-in-prose for the record.

**Net end-state model:**

```
SUBSTATION  (polygon, site boundary)                     ← renamed from SUBSTATION_YARD (S2)
 ├─ CONTROL_BUILDING  (OPTIONAL polygon; NOT a container)  ← S1 (panel-containment edge dropped)
 ├─ SUBSTATION_PANEL  (single parent: SUBSTATION)
 ├─ MV_BUSBAR         (single parent: SUBSTATION)
 └─ BAY               (single parent: SUBSTATION) → {breaker, isolator, sectionalizer,
                                                     fuse, arrester, CT, VT, relay, POWER_XFMR}
```


Reverses the earlier "finish, don't overhaul" call (docs were closing loose ends
additively). After walking through real yard photography with the user and
auditing the blast radius, we're doing the proper overhaul — but a *contained*
one, because the audit showed the model is almost entirely metadata-driven:
**nothing in the Go API or the web frontend hardcodes `SUBSTATION_YARD`** (one
CIM-export file references it only in comments; the promotion keys on the mapped
CIM class, not the literal code). The whole surface is seeds + one export
function + a data-preserving table rename.

## Why (the three real defects — none analytical)

Trace / power-flow / fault ride on terminals + `runtime.feature_connectivity`,
which are **independent of the container hierarchy**, so this overhaul buys *zero*
analysis change. It is entirely about **model fidelity + CIM alignment**:

1. **"Yard" is semantically overloaded** — `SUBSTATION_YARD` is at once the
   site identity, the outdoor switchyard, *and* the top-level container for
   indoor control-building panels. Holding a control cabinet under a "yard" is a
   category error.
2. **One transformation is baked in** — `primary_voltage`/`secondary_voltage`
   assume exactly one voltage step; a 132/33/11 kV substation (three levels, two
   transformations) doesn't fit.
3. **No voltage-level grouping** — busbars and bays hang flat off the yard, not
   grouped by the voltage level they belong to.

The north star is the IEC **CIM** hierarchy this repo already exports to
([Phase 16](phase-16-cim-export.md)):
`Substation → VoltageLevel (BaseVoltage) → Bay → ConductingEquipment`. Today
[cim.go](../api/internal/export/cim.go) promotes each `SUBSTATION_YARD` → one
*flat* CIM `Substation` and emits `BaseVoltage` only as a reference — the
`VoltageLevel` tier is never built, because the model has no such class.

## Governing principle: everything below the boundary is optional

The user was explicit — *not all substations have a building*, and the example
they gave is "a substation with a power transformer and related devices." So no
tier below the site boundary is mandatory. A pad-mount / pole-top site is just a
boundary + connected equipment; a large yard adds a building, voltage levels, and
bays. The optional tiers are expressed as **alternate containment parents** (a
device/bus/bay has two legal parents and picks one at runtime), the same idiom
documented for bay-vs-cubicle in
[Phase 34](phase-34-substation-bays.md#bay-vs-the-two-panel-classes--which-grouping-when).

**Deliberately out of scope (user declined):** a physical steel/structure layer
(gantries, A-frames, lattice supports, equipment stands). We model the
electrically-meaningful equipment + the optional building, not the steelwork.

## Target model (as originally designed — see the minimal-model correction above for the end state)

> **Superseded.** This was the four-tier target before the correction. `VOLTAGE_LEVEL`
> was retired and both dual-parent idioms removed; the authoritative shape is the
> single-parent tree in *Minimal-model correction* above. Kept here for the record.

```
SUBSTATION  (polygon, site boundary, on the map)          ← renamed from SUBSTATION_YARD (S2)
 ├─ CONTROL_BUILDING  (OPTIONAL polygon: num_floors …)    ← NEW (S1); houses the panels
 │     └─ SUBSTATION_PANEL       (re-homed here; or under SUBSTATION directly if no building)
 ├─ [VOLTAGE_LEVEL]  (OPTIONAL: nominal voltage)          ← NEW (S3); the CIM tier
 │     ├─ MV_BUSBAR
 │     └─ BAY → {breaker, isolator, sectionalizer, fuse, arrester, CT, VT, relay}
 └─ POWER_XFMR                    (bridges levels; stays under BAY — a CIM-valid transformer bay)
```

## Slice 1 — `CONTROL_BUILDING` (code-complete)

The optional control room, and the fix for the panel category error. Built
additively *before* the rename, so it carries no rename risk and still references
`SUBSTATION_YARD` (S2 renames it).

[seed/control_building_example.sql](../seed/control_building_example.sql) (task
`seed-control-building`, then `compile CODE=CONTROL_BUILDING`):

- New class **`CONTROL_BUILDING`** — `geometry_type='polygon'`, `srid=4326`,
  **`geometry_optional=true`** (draw the footprint inside the fence *or* skip
  geometry and just record that a building exists), `network_role='container'`,
  `network` NULL (not an electrical participant, like the yard). One `standard`
  subtype.
- New domain **`BUILDING_CONSTRUCTION`** (`BRICK`/`CONCRETE`/`PREFAB`/
  `CONTAINERIZED`/`STEEL_CLAD`). Fields (group `building`): `building_ref` (tag,
  nullable), `num_floors` (`integer`, nullable), `construction_type`
  (`BUILDING_CONSTRUCTION`, nullable), `floor_area_sqm` (`numeric`, nullable),
  `condition_rating` (`CONDITION_RATING`, nullable). Nothing required — a
  building may be recorded with just a footprint.
- Containment: `SUBSTATION contains CONTROL_BUILDING` (`containment` /
  `one_to_many`). **Correction:** the original `CONTROL_BUILDING contains
  SUBSTATION_PANEL` edge was dropped (migration 0086) — a panel's sole parent is
  `SUBSTATION` (seed/substation_panel_containment_example.sql), and the building is
  an optional map polygon that is **not** a containment parent. This keeps the
  model single-parent; the building still carries its own attributes and footprint.
- Map symbology: a mandatory `map` default (subtype+domain null) polygon
  fill/stroke (slate `#cbd5e1` fill / `#475569` stroke) to clear the publish
  gate. `is_diagram_container=false`, `is_diagram_placeable=false` (a building is
  a map footprint, not a one-line element; a building schematic is a later
  nicety, out of scope).

## Slice 2 — rename `SUBSTATION_YARD → SUBSTATION` (code-complete)

A data-preserving migration (the audit confirmed the containment
`relationship_classes` reference the class by **row-id**, not code, so they need
no rewiring — only the compiled physical table, whose name is
`strings.ToLower(code)`, must move in lock-step).

[migration 0085](../migrations/0085_rename_substation_yard_to_substation.sql) —
idempotent, guarded on `to_regclass('gis.substation_yard') IS NOT NULL AND
to_regclass('gis.substation') IS NULL` so a re-run (or a DB seeded straight as
`SUBSTATION`) is a clean no-op:

- Drops the old Martin tile function `gis.substation_yard(z,x,y)` (its body
  references the old table name, so it would dangle); `ALTER TABLE
  gis.substation_yard RENAME TO substation`; renames `substation_yard_geom_idx →
  substation_geom_idx`. Recompile rebuilds the tile function as
  `gis.substation(z,x,y)` and renames the check constraints by column
  introspection.
- `UPDATE metadata.feature_classes SET code='SUBSTATION', name='Substation',
  physical_table='gis.substation' WHERE code='SUBSTATION_YARD'`.
- Normalizes the containment `relationship_classes` display labels
  (`'SUBSTATION_YARD contains …' → 'SUBSTATION contains …'`) — cosmetic, since the
  rows are row-id-wired and work regardless.

Seeds (fresh-install path): the class-def seed still **keeps its historic filename**
`substation_yard_example.sql` (renaming it is unsafe under the Bash-blocked
constraint — the old file can't be deleted, so it would linger as a live orphan
re-inserting the old code), but now inserts code `SUBSTATION`. Every dependent seed
updated to JOIN `code='SUBSTATION'`: bay_containment, mv_busbar,
mv_switchgear_panel, substation_panel_containment, control_building, diagram,
symbology, operational_state, cim_mapping (+ comment-only refs in
circuit_membership, feature_class_categories, lv_metering, substation_panel_symbology).

- `primary_voltage`/`secondary_voltage` kept as a denormalized headline through S2
  (retire in S3 once `VOLTAGE_LEVEL` makes them derivable).
- `cim.go` comments + the two web comments updated to `SUBSTATION` (behaviour
  unchanged here — the CIM promotion keys on the mapped CIM class, not the code).
- Historical phase docs (phase-16/27/34/…) keep `SUBSTATION_YARD` as a
  point-in-time record of what was true at that phase; only the live-model
  descriptions are updated.

## Slice 3 — `VOLTAGE_LEVEL` tier (RETIRED — see minimal-model correction)

> **Retired 2026-09-11.** The `VOLTAGE_LEVEL` class was removed (migration 0086):
> voltage level is derivable per equipment, so it is carried by `BaseVoltage`
> references at export rather than modeled as a container. The seed
> `voltage_level_example.sql` is now a documented no-op stub and the
> `seed-voltage-level` task was removed. The original design follows for the record.

[seed/voltage_level_example.sql](../seed/voltage_level_example.sql) (task
`seed-voltage-level`, then `compile CODE=VOLTAGE_LEVEL`):

- New class **`VOLTAGE_LEVEL`** — none-geom container (`geometry_type='none'`,
  `srid` NULL, `network_role='container'`, `network` NULL), one `standard`
  subtype. A none-geom container needs no map symbology rule (BAY has none), so no
  publish-gate row.
- Fields (group `voltage_level`): `nominal_voltage` on the `VOLTAGE_CLASS` domain
  (required via `subtype_field_rules` — it's the level's identity and the CIM
  BaseVoltage anchor) + `level_ref` (nullable tag). `VOLTAGE_CLASS` re-asserted
  defensively with the full range (`400V`/`11KV`/`33KV`/`66KV`/`132KV`).
- Containment: `SUBSTATION contains VOLTAGE_LEVEL`; `VOLTAGE_LEVEL contains
  MV_BUSBAR` and `VOLTAGE_LEVEL contains BAY` (one set-generating INSERT over both
  destinations). The `SUBSTATION contains MV_BUSBAR`/`BAY` rows in the other seeds
  are **kept** as the no-level fallback → the level tier is optional (single-
  voltage sites skip it, bus/bay picks whichever legal parent applies at runtime).
- `primary_voltage`/`secondary_voltage` on `SUBSTATION` kept as an explicit denorm
  headline (not retired) — they read cleanly for the common single-transformation
  site and the levels are the authoritative structure when present.

## Slice 4 — CIM export fidelity (code-complete, simplified by the correction)

User chose **"honor modeled instances"** — the exporter loads the actual
containment relationship graph and emits the real modeled tree, rather than
synthesizing tiers from geometry + voltage. **This engine code is unchanged by the
correction and needs no edit:** with `VOLTAGE_LEVEL` gone, no container maps to CIM
`VoltageLevel`, so `writeTiers` emits only the modeled `Bay` tier and each bay
takes the `Bay.Substation` fallback — the exporter degrades gracefully to
`Substation → Bay → equipment`. The `VoltageLevel` arm is kept as a dormant generic
path (fires only if a container is ever mapped to CIM `VoltageLevel` again). Only
the `seed/cim_mapping_example.sql` rows changed (below).

- [containment.go](../api/internal/export/containment.go) — `loadContainment`
  reads `runtime.feature_relationships` joined to `metadata.relationship_classes`
  on `kind='containment'`, returning a `child feature id → container feature id`
  map for every edge whose **both** endpoints are in the exported set (closed
  graph). One flat map expresses the whole tree (containment is `one_to_many`, so
  a child has ≤1 parent, and containers can themselves be contained).
  [runner.go](../api/internal/export/runner.go) loads it under the same CIM
  snapshot tx and threads it into `writeCIM`.
- [cim.go](../api/internal/export/cim.go): `indexContainers` records every
  container feature's CIM class by id (`Substation`/`VoltageLevel`/`Bay`/
  `EquipmentContainer` — all `cim_kind='container'`) and flags the
  `VoltageLevel`/`Bay` tiers so the equipment pass skips them. `writeTiers` emits
  each `VoltageLevel` (→ `VoltageLevel.Substation` via the containment parent, →
  `VoltageLevel.BaseVoltage` from its `nominal_voltage`) then each `Bay` (→
  `Bay.VoltageLevel` if its parent is a level, else `Bay.Substation` — the
  optional-tier fallback). Equipment container resolution now consults the modeled
  parent **first** (`cw.contain[f.ID]` whose parent has a known CIM class), with
  the pre-tier point-in-polygon substation test as the fallback and the default
  `Line` container if neither.
- [seed/cim_mapping_example.sql](../seed/cim_mapping_example.sql): kept the
  `BAY → Bay` class-default mapping. **Correction:** the `VOLTAGE_LEVEL →
  VoltageLevel` class mapping and the `VOLTAGE_LEVEL.nominal_voltage →
  BaseVoltage.nominalVoltage` attribute mapping were removed with the class
  (migration 0086). Equipment `BaseVoltage` mappings (`MV_CONDUCTOR`,
  `DIST_XFMR`, …) are unchanged, so voltage is still emitted — just per equipment.

## Verification owed (Bash blocked here — run locally)

- Slice 1: `task seed-control-building` then `compile CODE=CONTROL_BUILDING`;
  confirm `gis.control_building` is created with a nullable `geom` (optional
  geometry) + the five fields, the `construction_type`/`condition_rating` CHECKs
  match their domains, the `map` default symbology rule exists (publish gate),
  and both `SUBSTATION_YARD contains CONTROL_BUILDING` and `CONTROL_BUILDING
  contains SUBSTATION_PANEL` relationship_classes exist. Round-trip: create a
  building (with and without a drawn footprint), contain a `SUBSTATION_PANEL` in
  it, and confirm a panel can alternatively still be contained directly by the
  yard. `go build ./...`, web build.
- Slice 2 (on a DB that already carried live yard rows): apply migration 0085,
  confirm `gis.substation` now exists with the same row count and geometry as the
  old `gis.substation_yard` (which is gone), the old tile function is gone, and
  `metadata.feature_classes` shows `code='SUBSTATION'` /
  `physical_table='gis.substation'`. Then `task compile CODE=SUBSTATION` and
  confirm the check constraints are renamed `substation_*_check`, the Martin tile
  function `gis.substation(z,x,y)` exists, and the map still renders substations.
  Confirm the containment `relationship_classes` labels now read `SUBSTATION
  contains …`. Re-run the seeds and confirm idempotence (no duplicate classes,
  relationship_classes, or symbology rows). Existing trace/power-flow unaffected
  (topology rides on connectivity, not the container). `go build ./...`, web build.
- Minimal-model correction — apply [migration 0086](../migrations/0086_remove_voltage_level_tier.sql):
  on a DB that ran the old Slice 3/1 seeds, confirm the `VOLTAGE_LEVEL` class,
  `gis.voltage_level` table, and its containment `relationship_classes`
  (`SUBSTATION contains VOLTAGE_LEVEL`, `VOLTAGE_LEVEL contains MV_BUSBAR`/`BAY`)
  are all gone, while `SUBSTATION contains MV_BUSBAR`/`BAY` remain (bus/bay now
  single-parent). Confirm `CONTROL_BUILDING contains SUBSTATION_PANEL` is gone and
  `SUBSTATION contains SUBSTATION_PANEL` remains. Re-run for idempotence (clean
  no-op) and on a fresh DB (never had the rows — also a no-op). If any runtime
  instances existed the migration raises a NOTICE rather than orphaning them.
  `go build ./...` (comment-only Go edits; behaviour unchanged).
- CIM (post-correction): export a substation that models `SUBSTATION → BAY →
  breaker` and confirm the RDF/XML carries `cim:Bay` (with `Bay.Substation`), the
  breaker's `Equipment.EquipmentContainer` points at the **bay** mRID, and voltage
  is carried by per-equipment `cim:BaseVoltage` references (no `cim:VoltageLevel`
  object is emitted). Export a site with no bays and confirm equipment still lands
  in the substation by geometry (fallback unchanged).
