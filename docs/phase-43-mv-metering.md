# Phase 43 — MV metering point (bulk / industrial supply)

**Status:** S1 + S2 + S3 code-complete (verification owed). Decisions locked 2026-09-08.

## Why

LV metering is fully modelled — `SERVICE_POINT` → `METER` → `METER_CHAMBER`
(Phase 9). The **MV** side has only the instrument transformers `MV_CT` /
`MV_VT` (Phase 27 Slice 3, `seed/mv_measuring_example.sql`) — the *measuring
hardware*, not a **metering point**. Nothing today represents an industrial /
building-complex customer that takes supply at 11 kV / 33 kV and is metered
there. This phase adds that.

## The physics that shapes the model

At MV the **primary current never flows through the meter**. An LV whole-current
`METER` is an *inline* junction (T1 line → T2 load; power flows through it). An
MV meter is fed by the **CT/VT secondaries** (5 A / 110 V) — it is a
*measurement device hanging off* the instrument transformers, the same way
`MV_VT` is a shunt tap rather than a through-node. So we **cannot reuse the
inline `METER` class** — the MV meter is `network_role='none'`.

## Shape (mirrors SERVICE_POINT + METER, adapted for MV)

```
MV feeder (MV_CONDUCTOR / RMU / MV_SWITCHGEAR_PANEL)
        │  T1
   MV_METERING_POINT        delivery-point leaf — "industrial customer here"
        │ owns (composite, one_to_many)
     MV_METER               the meter device — instrument-fed, NOT inline
        ├─ measures via ──→ MV_CT   (relationship_class)
        └─ measures via ──→ MV_VT   (relationship_class)
```

### MV_METERING_POINT — the delivery point (locked: separate class)
- `point`, SRID 4326, **`network_role='junction'`**, single terminal **T1** —
  a network leaf like `SERVICE_POINT` (the traced endpoint; a "trace to
  customer" lands here). Never inline on a run.
- **Subtypes** (customer class, like SERVICE_POINT's): `industrial`,
  `commercial_complex`, `bulk_supply`.
- **Fields:** `supply_voltage` (coded — 11 kV / 33 kV; reuse the MV voltage
  domain if one exists, else new `MV_SUPPLY_VOLTAGE`), `contracted_demand_kva`
  (numeric), `tariff_ref` (text, nullable), `account_ref` (text, nullable —
  CIS/MDM join key, same role as METER.account_ref).
- **network_rules (feed-in):** `MV_CONDUCTOR -> MV_METERING_POINT T1`, and
  from switchgear where relevant (`MV_SWITCH T2 -> MV_METERING_POINT T1` /
  RMU way). Leaf, so no onward rule.

### MV_METER — the meter device (locked: measurement device, not inline)
- `point`, SRID 4326, **`network_role='none'`**, geometry_optional=true,
  is_diagram_placeable=true. **No terminals** — it is not on the primary path.
- **Subtypes:** metering arrangement, e.g. `main_meter`, `check_meter`.
- **Fields:** `meter_serial` (text, required — device identity),
  `metering_class` (coded — accuracy: `0.2S` / `0.5S` / `1.0`),
  `demand_kva` (numeric, nullable — recorded max demand), `account_ref`
  (text, nullable). **No** `ct_ratio`/`vt_ratio` text fields — ratios are read
  from the linked MV_CT/MV_VT (locked: relationship link, not duplicated
  fields).

### Relationships (locked: relationship_classes, real features linked)
- `MV_METERING_POINT owns MV_METER` — `composite`, `one_to_many` (a point may
  have main + check meter, or none yet). Exact analogue of
  `SERVICE_POINT owns METER (composite)`.
- `MV_METER measures via MV_CT` — kind **`metering_sensing`**, `many_to_many`.
- `MV_METER measures via MV_VT` — same.
  - **Resolved:** no existing `kind` fit ("measures via" is a device-fed-by-
    instrument-transformer association, the metering twin of the protection
    relay's `protection_sensing`). Added a new kind **`metering_sensing`** via
    **migration 0080** (widens the CHECK, mirroring 0075; no data rewrite).
    Not reusing `protection_sensing` (metering ≠ protection, and a shared kind
    would conflate a meter's and a relay's CT/VT on the same instrument);
    not `composite` (an MV_CT can feed both a meter and a relay, so it isn't
    "owned" by the meter). Cardinality is **`many_to_many`**, not one_to_many:
    a meter reads several CTs (one per phase) and a CT/VT can feed a main +
    check meter, so one_to_many would wrongly cap either side.

## Container (S2 — DONE)

The metering **cubicle** (`MV_METERING_CUBICLE`) contains the meter + CT + VT,
reusing the `METER_CHAMBER` containment + diagram machinery. It is a locatable
map point (geometry 'point') you click to open its `METERING_SCHEMATIC` mimic —
the METER_CHAMBER shape, not the geometry-less `MV_SWITCHBOARD`. See the S2
slice entry below.

## Slice plan

- **S1 — catalog (CODE-COMPLETE):** `migrations/0080` (the `metering_sensing`
  kind) + `seed/mv_metering_example.sql` = `MV_METERING_POINT` + `MV_METER`
  classes, subtypes, fields, domains (`MV_SUPPLY_VOLTAGE` 11/33 kV,
  `METERING_CLASS` 0.2S/0.5S/1.0), MV_METERING_POINT T1 terminal, feed-in
  network_rules (`MV_CONDUCTOR`/`MV_SWITCH T2 -> MV_METERING_POINT T1`), and the
  three relationship_classes (owns + two measures-via). `MV_METER` is
  network_role='none' → no terminals, like POLE/TOWER. Go+web
  `validRelationshipKinds`/`RELATIONSHIP_KINDS` widened to mirror the CHECK
  (also retroactively unblocks admin-authoring the protection kinds).
  `task seed-mv-metering` then `task compile CODE=MV_METERING_POINT` +
  `CODE=MV_METER`.
- **S2 — container (CODE-COMPLETE):** `seed/mv_metering_cubicle_example.sql` =
  `MV_METERING_CUBICLE` (network_role='container', geometry 'point',
  is_diagram_container=true; subtypes indoor/outdoor_kiosk; fields
  cubicle_ref/condition_rating — the METER_CHAMBER shape byte-for-byte).
  `containment` relationship_classes to {MV_METER, MV_CT, MV_VT} (one_to_many
  each). New `METERING_SCHEMATIC` DIAGRAM_TYPE value (sort_order 4) +
  diagram_placement_rules allow-list restricting that schematic to exactly
  {MV_METER, MV_CT, MV_VT} (migration 0068). No new relationship kind
  (containment already exists), no migration. `task seed-mv-metering-cubicle`
  then `task compile CODE=MV_METERING_CUBICLE`. The cubicle is the box; the
  MV_METERING_POINT stays the network leaf (not contained), exactly as
  METER_CHAMBER does not contain SERVICE_POINT.
- **S3 — symbology (CODE-COMPLETE):** `seed/mv_metering_symbology.sql` gives the
  three new classes their mandatory `map` default rule (publish gate) plus a
  `diagram` ANSI glyph for the two that get placed on a schematic. Reuses
  existing glyphs only (`ansi-meter`, `ansi-terminal`) and the map shape
  registry — no new `symbology_icons`. Scheme:
  - `MV_METERING_POINT` → map **diamond violet** `#7c3aed` (the SERVICE_POINT
    customer-point identity) + diagram **ansi-terminal** violet.
  - `MV_METER` → map **diamond cyan** `#0891b2` (instrument cyan, distinct shape
    from `MV_CT` circle / `MV_VT` square so the three stay tellable apart) +
    diagram **ansi-meter** cyan (same glyph as LV `METER`).
  - `MV_METERING_CUBICLE` → map **square slate** `#475569`, **no** diagram glyph
    (it *owns* the schematic, it is not placed on one — the METER_CHAMBER
    treatment).
  - **`MV_CT` / `MV_VT` are NOT touched** — they already have full map+diagram
    symbology from `seed/mv_symbology_example.sql` (Phase 27 Slice 5: MV_CT
    circle/ansi-ct, MV_VT square/ansi-vt, both instrument cyan). All INSERTs are
    `NOT EXISTS`-guarded (idempotent). No compile — `symbology_rules` resolve
    client-side. `task seed-mv-metering-symbology`.

## Precedents to copy verbatim

- `seed/lv_metering_example.sql` — SERVICE_POINT owns METER composite;
  field_groups; ON CONFLICT idempotency; network_rules NOT EXISTS guards.
- `seed/mv_measuring_example.sql` — MV_CT / MV_VT shape, junction shunt taps.
- `seed/service_point_example.sql` — customer-class subtypes, single-T1 leaf.

## Verification owed (Bash EPERM here — user runs)

`task seed-mv-metering`; `task compile CODE=MV_METERING_POINT` +
`CODE=MV_METER`; confirm `gis.mv_metering_point` / `gis.mv_meter` tables; a
trace to an MV customer lands on the metering point; the owns/measures-via
relationships resolve.

S2: `task seed-mv-metering-cubicle`; `task compile CODE=MV_METERING_CUBICLE`;
confirm `gis.mv_metering_cubicle`; create a cubicle, open its
`METERING_SCHEMATIC`, and confirm only {MV_METER, MV_CT, MV_VT} can be placed on
it (the allow-list rejects others); the `contains` relationships resolve.

S3: `task seed-mv-metering-symbology`; the three new classes render with their
map glyphs (no generic fallback) and can be published (map default present);
`MV_METER` shows the ansi-meter glyph on a METERING_SCHEMATIC; `MV_CT`/`MV_VT`
still render as before (untouched). No compile.
