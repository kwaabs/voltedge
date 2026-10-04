# VoltEdge LV Network & Metering Guide

A comprehensive, example-led guide to modelling the **low-voltage (LV) network** and **metering** in VoltEdge — from the transformer's secondary terminal down to the customer's meter, and the energy-accounting meters scattered across the grid above it.

This is a modelling and reference manual: it explains *what each class is*, *how the pieces connect*, and *why the model is shaped the way it is*, with worked examples drawn from the seeded test networks so every value here is one you can reproduce. Where the physics matters (it does, for metering), a **∑** aside explains it; the **▶** lane is the plain "how you build it" story you can follow on its own.

**Who this is for.** Editors and network modellers capturing LV feeders and services; analysts who read demand and consumption off them; and anyone who needs to know the difference between a revenue meter, an MV meter, and a network energy meter — because VoltEdge models all three, differently and on purpose.

**The cast.** **Dana** (editor/field supervisor) captures LV feeders, pillars and services; **Tom** (analyst) reads demand and runs consumption rollups; **Priya** (dispatcher) cares about affected customers during an outage; **Ada** (admin) authors the metadata (classes, domains, rules) behind it all. The running example is the seeded **test network** — transformer **TX-101** feeding service points **SP-A** and **SP-B** through meters **MTR-A/MTR-B** — plus the **LV ring** network for the one closed-loop case.

**Legend.** 🔒 an action that needs a permission (named inline). 💡 a tip. ⚠️ a caution or a known limitation stated honestly. **▶** the build lane; **∑** the model/physics lane.

> **One structural fact first.** The LV and metering **classes** are not defined in a migration — they're authored as **seed** metadata (`seed/*.sql`) and then **compiled** into physical `gis.<class>` tables (`task compile CODE=…`). Migrations only create the generic machinery (the `feature_classes`/`fields`/`terminals`/`network_rules` tables, new relationship *kinds*). So "which migration adds LV_CONDUCTOR?" has no answer — it's `seed/lv_conductors_example.sql`. The migrations that *do* matter are the relationship-kind wideners (0080, 0121), called out where relevant.

---

## Chapter 1 — The LV layer in one page

VoltEdge models a network as a **graph**: **edge** features (conductors) joined at **junction** features (devices) through typed **terminals**, with every join authorised by a **network rule**. The LV layer is that same graph, starting where the medium-voltage network ends — at a distribution transformer's **secondary**.

Four ideas carry the whole chapter:

- **LV starts at the transformer's T2.** A `DIST_XFMR` has three terminals: `T1` (primary, the MV side), **`T2` (secondary — the LV bus)**, and `T3` (ground). Every LV feeder originates at a `T2`. (`seed/connectivity_example.sql:108-117`.)
- **Conductors are edges; everything else is a node.** `LV_CONDUCTOR` is an `edge` with **no terminals** — it's the wire. Cut-outs, switches, pillars, meters and service points are `junction` nodes with terminals power flows *through* or *into*. Chambers and pillars are `container`s — they *hold* things, they're not on the electrical path.
- **Phase is data, not structure.** A conductor doesn't come in "single-phase" and "three-phase" *classes*; it carries a `phase` field (`L1`, `L2`, `L3`, `N`, `L1N`, …, `L1L2L3N`). Some LV connections are **phase-gated** — the rule only fires if the phases intersect (§8).
- **The meter is optional and the service point is the leaf.** A supply can be metered or not; what always terminates an LV supply is a `SERVICE_POINT` (the point of delivery) or an unmetered leaf like a `STREET_LIGHT`.

From those, the model authorises exactly **seven valid LV supply chains** (§8) — from a bare unmetered tap to a 20-flat apartment riser. The rest of this guide is those seven chains and the classes that make them up.

```
   MV network
       │
   DIST_XFMR  (11 kV → 400 V)
       │ T2  (secondary = LV origin)
       ▼
   LV_CONDUCTOR ──► [cut-out / pillar / busbar] ──► METER ──► SERVICE_POINT
      (edge)              (junction nodes)        (optional)     (leaf)
```

---

## Chapter 2 — The LV conductor

`LV_CONDUCTOR` (`seed/lv_conductors_example.sql`) is the LV wire — the edge every supply rides. It is drawn as a **map line**, `network_role = edge`, and carries **no terminals** (an edge is joined *to* nodes, it doesn't own join points of its own).

**▶ Three required fields** define every span:

| Field | Domain | What it is |
| --- | --- | --- |
| `phase` | `PHASE` | Which phases the span carries — `L1L2L3N` for a three-phase main, `L1N` for a single-phase service, etc. |
| `conductor_spec` | `LV_CABLE_SPEC` | The cable type — which resolves to real impedance/ampacity for load flow *and* to a per-km cost for the Bill of Quantities. |
| `lv_role` | `LV_ROLE` | `main` (the backbone down a street) or `service` (the drop to one premises). |

💡 **`lv_role` is a field, not a subtype — and that was a deliberate decision.** An abandoned design split LV into separate `LV_MAIN` and `LV_SERVICE` *classes*; the model instead keeps one class and makes main-vs-service an attribute, so a main re-tasked as a service (or vice versa) is an edit, not a re-creation. The **subtypes** encode *construction* instead: `1 = overhead_lv_conductor`, `2 = underground_lv_conductor` — mirroring `MV_CONDUCTOR`.

**∑ The cable catalog.** `conductor_spec` points at an `LV_CABLE_SPEC` coded value, each backed by a `material_specs` row carrying the real numbers (`seed/lv_conductors_example.sql:99-123`):

| Spec | Description | R (Ω/km) | X (Ω/km) | Ampacity | Ø |
| --- | --- | --- | --- | --- | --- |
| `ABC_95` | ABC 95 mm² (aerial bundled) | 0.320 | 0.080 | 240 A | 14 mm |
| `WAVEFORM_185` | Waveform 185 mm² (LV underground) | 0.164 | 0.070 | 313 A | 30 mm |
| `CONCENTRIC_25` | Concentric 25 mm² (service) | 0.727 | 0.080 | 100 A | 12 mm |

This is the same material-backed pattern the MV conductor uses: picking a cable size resolves straight to its impedance (for the load-flow voltage drop) and — once you've seeded cost (see the [Bill of Quantities guide](bill-of-quantities-guide.md)) — its `cost_per_km`.

**Where a span originates.** The rule `DIST_XFMR T2 → LV_CONDUCTOR` (`seed/lv_conductors_example.sql:174-182`) is the LV source hop. ⚠️ Once an LV conductor exists on a transformer, the *direct* `DIST_XFMR T2 → SERVICE_POINT` rule is **retired** (`is_valid=false`, `:222-230`) — supply must flow through the conductor, not leap from the transformer straight to a customer.

```
 DIST_XFMR ─T2─► LV_CONDUCTOR ──(phases_intersect)──► LV_CONDUCTOR ──► …
  (400 V)          main                                  service
```

The `LV_CONDUCTOR → LV_CONDUCTOR` splice rule is **phase-gated** (`phase_rule = phases_intersect`, `:192-200`): a single-phase `L1N` service can tap a three-phase `L1L2L3N` main (they share L1 and N) but two services on different phases cannot be spliced together by mistake.

---

## Chapter 3 — LV devices: cut-outs, switches, pillars

Between the main and the customer sit the switching and distribution devices. All are `junction` nodes.

### Service cut-out — the intake fuse
`SERVICE_CUTOUT` (`seed/lv_devices_example.sql:76-118`) is the service fuse at the intake. Subtypes `1 = fuse_carrier`, `2 = switch_fuse`. Two terminals — `T1` (primary, line) and `T2` (secondary, load) — and a `device_state`; **an OPEN cut-out is a trace barrier**, so a trace stops at a pulled fuse exactly as the network does.

### LV switch — the generic switch
`LV_SWITCH` (`seed/mv_lv_switch_example.sql:129-171`) is a geometry-optional (`geometry_optional=true`) inline switch, one subtype `standard`, with `device_state` and optional protection attributes (`is_fused`, `fuse_rating_a`, `curve_ref`). `T1`/`T2`; OPEN is a barrier.

### LV link box / feeder pillar — the flat junction
`LV_LINK_BOX` (`seed/lv_devices_example.sql:127-168`) is a sectionalising cabinet: subtypes `1 = feeder_pillar`, `2 = link_box`. One incoming terminal `T1` and **three switchable outgoing ways** `T2`/`T3`/`T4`.

⚠️ **Honest limitation:** the link box's OPEN barrier is *feature-level*, not per-way. A normally-open way is modelled by the **absence** of a connection on that terminal, not by a per-way open flag. If you need true per-way isolation, use the LV pillar instead.

### LV pillar — the assembly with per-way fusing
`LV_PILLAR` (`seed/lv_pillar_example.sql`) is a **container** (`network_role=container`, `network='LV'`) — the LV sibling of the RMU. You don't wire it by hand; you place it as an **assembly** (🔒 `features:write` + `relationships:commit`, via `POST /assemblies`) and it **fans out its internals in one transaction**:

- **`WAYS_4`** ("Feeder pillar — 4-way") → `1× LV_BUSBAR` + `4× LV_FUSE`
- **`WAYS_6`** ("Feeder pillar — 6-way") → `1× LV_BUSBAR` + `6× LV_FUSE`

Each `LV_FUSE` (`seed/lv_pillar_example.sql:134-177`) is a **diagram-only** node (`geometry_type=none`) with `T1` (bus side) / `T2` (out), a `fuse_rating` (`100A`…`400A`, BS88), and a `device_state`. The fuses are **born OPEN** — a pillar is commissioned way by way. Crucially, **`LV_FUSE` OPEN is a *per-way* barrier** — that's what gives the pillar the per-outgoing-feeder isolation the flat link box can't express.

```
 LV_CONDUCTOR ─► LV_PILLAR  (container)
                   │ contains
                   ├─ LV_BUSBAR  ◄── incoming main (T1)
                   │     │ T2 (equipotential bar)
                   │     ├─ LV_FUSE (OPEN) ─T2─► LV_CONDUCTOR  → feeder 1
                   │     ├─ LV_FUSE (OPEN) ─T2─► LV_CONDUCTOR  → feeder 2
                   │     ├─ LV_FUSE (OPEN) ─T2─► …
                   │     └─ LV_FUSE (OPEN) ─T2─► feeder 4
```

---

## Chapter 4 — Service points: the point of delivery

`SERVICE_POINT` (`seed/service_point_example.sql`) is the **leaf** of an LV supply — the point of delivery to a premises. It's a `junction` with a **single** terminal `T1`.

- **Subtypes = customer class:** `1 = residential`, `2 = commercial`, `3 = industrial`.
- **Load fields** (`seed/load_flow_fields.sql`): `demand_kw`, `power_factor` (unset = unity), and `customers_served` (integer; **NULL reads as 1** — it's the SAIDI/SAIFI denominator).
- 💡 **No `phase` field.** Phase lives on the conductor and the meter, not the leaf. And the old `meter_number` scalar is **gone** — the meter is now a first-class `METER` feature linked by ownership (Chapter 5), not a string on the service point.

Drawn as a **violet diamond** on the map. Fed by any of `DIST_XFMR T2`→…→`SERVICE_POINT T1`, `SERVICE_CUTOUT T2 → T1`, or `METER T2 → T1` — the last of which is what makes it a *metered* leaf.

---

## Chapter 5 — LV metering: the revenue meter

This is the heart of the guide. At LV, the meter is **inline** — the customer's current physically flows *through* it — so `METER` (`seed/lv_metering_example.sql:127-175`) is a real `junction` node, with `T1` (line) and `T2` (load). That single physical fact drives the whole LV metering model, and it's the sharpest contrast with MV metering (Chapter 6), where current never flows through the meter at all.

**▶ The meter's fields:**

| Field | Domain | Required | Meaning |
| --- | --- | --- | --- |
| `meter_serial` | — | always | The device identity (the CIS/MDM key). |
| `account_ref` | — | optional | Billing link — a meter may be in-stock, de-energised, or unassigned. |
| `phase` | `PHASE` | always | 1-phase vs 3-phase lives here, not in the subtype. |
| `ct_ratio` | `CT_RATIO` | **`ct_metered` only** | `100_5` … `1600_5`. Required for the CT subtype, absent for whole-current. |

The **subtypes** encode the *metering arrangement*: `1 = whole_current` (the meter carries full load current) and `2 = ct_metered` (it reads a current transformer's 5 A secondary). 💡 `ct_ratio` is the model's canonical example of a **per-subtype-required field** — physically nullable on the class, but made mandatory for the `ct_metered` subtype by a single `subtype_field_rules` row (`seed/lv_unmetered_ct_example.sql:165-172`).

### 5.1 The SP-owns-METER model

A service point **owns** its meter through a `composite` (ownership) relationship, `one_to_many` (`seed/lv_metering_example.sql:394-401`):

```
 SERVICE_POINT ──owns (composite)──► METER   (0..N meters per SP; each meter → at most 1 SP)
```

This is why a service point can carry a main meter *and* a check meter, a multi-rate pair, or no meter at all, without any of it being a magic string. The `one_to_many` caps the *destination* — a meter belongs to at most one service point — enforced in `api/internal/relationship/cardinality.go`.

💡 Note the **two** relationships a meter can be in: it's *owned* by a service point (commercial/billing fact) and it's *wired inline* on the electrical path (`…→ METER.T1`, `METER.T2 → SERVICE_POINT.T1`). Ownership is who bills; connectivity is where the current flows. They usually point at the same service point, but they're modelled separately on purpose.

### 5.2 The meter chamber and the busbar fan-out

Several meters in one cabinet (an apartment block) are modelled with two more classes:

- **`METER_CHAMBER`** (`seed/lv_metering_example.sql:234-281`) — the physical enclosure. A `container` (not on the electrical path, no terminals), subtypes `cabinet`/`room`, carrying a `chamber_ref` asset tag. It **contains** its busbar and meters (`containment`), and owns a `CHAMBER_SCHEMATIC` diagram restricted to exactly `{LV_BUSBAR, METER}`.
- **`LV_BUSBAR`** (`:183-221`) — the distribution board inside it. Subtypes `lv_distribution_board`/`busbar_riser`. `T1` is the incoming supply; **`T2` is the equipotential bar** that fans out to every tenant meter.

**∑ Why one terminal feeds twenty meters.** A busbar is a *solid bar* — every tap is the same electrical node. So instead of modelling twenty switchable ways, the model uses **one rule, `LV_BUSBAR T2 → METER T1`**, and lets it be satisfied **N times** (one `runtime.feature_connectivity` row per meter on the same `T2`). Twenty flats need no twenty-terminal schema — just twenty connections on the one bar.

```
 LV_CONDUCTOR ─► SERVICE_CUTOUT ─► LV_BUSBAR
                                     │ T2 (one bar, N taps)
                                     ├─► METER ─► SERVICE_POINT   (flat 1)
                                     ├─► METER ─► SERVICE_POINT   (flat 2)
                                     ├─► METER ─► SERVICE_POINT   (… )
                                     └─► METER ─► SERVICE_POINT   (flat 20)
            └──────────── all inside one METER_CHAMBER ───────────┘
```

### 5.3 Unmetered supplies and CT metering

Not every LV leaf is metered. **`STREET_LIGHT`** (`seed/lv_unmetered_ct_example.sql:99-147`) is an unmetered leaf — subtypes `column_mounted`/`wall_mounted`, a `luminaire_type` (`LED`/`SON`/`SOX`/`MH`), fed by a **phase-gated** rule `LV_CONDUCTOR → STREET_LIGHT T1` (`phases_intersect`) so a light only hangs off a phase the conductor actually carries.

And **CT metering at LV is an *attribute*, not a class** — it's the `ct_metered` subtype plus the `ct_ratio` field on the ordinary meter, not a separate CT feature. (That changes at MV — see Chapter 6.)

---

## Chapter 6 — MV metering: a different shape, for a reason

**∑ The physics that forces a different model.** At MV, the primary current is far too large to run through a meter, so it never does. The meter instead reads the **secondaries** of instrument transformers — a current transformer (CT) steps the line current down to 5 A, a voltage transformer (VT) steps the voltage down to 110 V, and the meter multiplies those. So an MV meter is **off the primary path entirely**. Modelling it as an inline `METER` would be a lie about where the current goes.

So MV metering mirrors the LV `SERVICE_POINT` + `METER` split, but with the meter **off-path**:

- **`MV_METERING_POINT`** (`seed/mv_metering_example.sql:91-131`) — the **delivery-point leaf**, the commercial boundary where the utility hands off to the customer; a "trace to the customer" ends here. A `junction` with a single `T1`, subtypes `industrial`/`commercial_complex`/`bulk_supply`, carrying the **required** `supply_voltage` (`11KV`/`33KV`) and `contracted_demand_kva`, plus optional `tariff_ref`/`account_ref`. Fed by `MV_CONDUCTOR → MV_METERING_POINT T1` or `MV_SWITCH T2 → MV_METERING_POINT T1`.
- **`MV_METER`** (`:138-173`) — the meter **device**, `network_role = none` with **no terminals** (like a pole — it's an asset, not a path node). Subtypes `main_meter`/`check_meter`; required `meter_serial` and `metering_class` (IEC 62053: `0.2S`/`0.5S`/`1.0`). It carries **no CT/VT ratio of its own** — it reads them from the linked instrument transformers.
- **`MV_METERING_CUBICLE`** (`seed/mv_metering_cubicle_example.sql`) — the enclosure, a `container` (subtypes `indoor`/`outdoor_kiosk`) that **contains** `{MV_METER, MV_CT, MV_VT}`.

**How it's wired together** (ownership + sensing, not connectivity):

```
 MV_CONDUCTOR ─► MV_METERING_POINT  (leaf — the commercial boundary)
                      │ owns (composite)
                      ▼
                   MV_METER  (off-path; network_role=none)
                      │ measures via (metering_sensing, many-to-many)
                      ├──► MV_CT   (reads the 5 A current secondary)
                      └──► MV_VT   (reads the 110 V voltage secondary)
```

**∑ Why `metering_sensing` is many-to-many.** A meter reads several CTs (one per phase) *and* a single CT/VT can feed both a main and a check meter. Neither side is "one". This relationship kind was added by **migration 0080** (`0080_widen_relationship_kind_metering.sql`) — the metering twin of protection's `protection_sensing`.

---

## Chapter 7 — Network metering: energy accounting

The third kind of meter has no customer at all. **`NETWORK_METER`** (`seed/network_meter_example.sql`, migration **0121**) meters a point of the **network itself** — a substation incomer, a feeder head, a transformer's output, a bus section, or a tie between territories — so the utility can **account for energy**: how much a transformer delivered downstream, how much crossed a territory boundary, how much a generator exported.

- `network_role = none`, **no terminals**, **no network rules**, **no trace barrier.** It is an **accounting overlay**, not a topology node — a trace never walks *through* it. "What's downstream of this meter?" is answered by tracing the *element it meters*, not the meter.
- **Subtypes = accounting purpose:** `substation_incomer`, `feeder_head`, `transformer_metering`, `bus_section`, `boundary_exchange`, `interchange_tie`, `generation_export`.
- **Fields:** required `meter_serial` + `metering_class`; optional `ct_ratio`, `vt_ratio` (`11KV_110`/`33KV_110`), and **`external_ref`** — the join key into the external MDM/head-end.
- ⚠️ **It stores no readings.** kWh/kVArh/demand live in the external metering system; VoltEdge holds only the asset and the `external_ref` to fetch them at view time. The class is a registry of *where* energy is accounted, not a store of *how much*.

**How it attaches:** a `metering_accounting` relationship (migration 0121, `many_to_many`) onto the metered element — `DIST_XFMR`, `POWER_XFMR`, `MV_BUSBAR`, `MV_CONDUCTOR`, or `LV_CONDUCTOR`:

```
 NETWORK_METER ──meters (metering_accounting)──► DIST_XFMR | POWER_XFMR | MV_BUSBAR | MV_CONDUCTOR | LV_CONDUCTOR
   (no terminals, off-path; readings via external_ref → MDM)
```

---

## Chapter 8 — How it all connects: the rules and the seven chains

Every LV connection is authorised by a row in `metadata.network_rules` (from-class [+ terminal] → to-class [+ terminal], with an optional `phase_rule`). The LV rule set authorises exactly **seven valid supply chains** — memorise these and you know what the model will and won't let you build (`docs/phase-9-lv-service-delivery.md`):

| # | Chain | Use |
| --- | --- | --- |
| 1 | `LV_CONDUCTOR → SERVICE_POINT` | bare / unmetered tap |
| 2 | `LV_CONDUCTOR → SERVICE_CUTOUT → SERVICE_POINT` | fused, unmetered |
| 3 | `LV_CONDUCTOR → METER → SERVICE_POINT` | metered, no cut-out |
| 4 | `LV_CONDUCTOR → SERVICE_CUTOUT → METER → SERVICE_POINT` | **the standard house** |
| 5 | `LV_CONDUCTOR → LV_BUSBAR → METER×N → SERVICE_POINT×N` | board fan-out |
| 6 | `LV_CONDUCTOR → SERVICE_CUTOUT → LV_BUSBAR → METER×N → SERVICE_POINT×N` | **the apartment block** |
| 7 | `LV_CONDUCTOR → STREET_LIGHT` | unmetered leaf |

Two rules are **phase-gated** (`phase_rule = phases_intersect`): the conductor-to-conductor splice and the conductor-to-street-light hop — everywhere else a phase mismatch is allowed because the device handles it. And remember the **retired** rule: once an LV conductor exists, the direct transformer-to-service-point hop is invalid — supply flows through the wire.

💡 LV also originates *inside a substation*, not just from a pole-top transformer: `DIST_XFMR T2 → LV_BUSBAR T1` feeds a station LV board, and station auxiliaries run `AUX_TRANSFORMER T2 → LVAC_BOARD T1 → LV_CONDUCTOR`. Same edge/node grammar, different enclosure.

---

## Chapter 9 — Reading consumption off the LV network

Once the network is built, you *read* it. The **service / consumption rollup** (`api/internal/trace/service.go`) answers "how much load, how many customers, at what restoration priority, sits below here?" — not with a power-flow solve, but by summing the planner's stored nameplate figures across the **load leaves** a trace reaches.

**▶ What counts as a load leaf:** `SERVICE_POINT` always, and a `METER` **only when its `meter_role = PRIMARY`**. This is the "the network ends at the meter" contract: a `CHECK` meter or a role-less device meter contributes **nothing**, so a supply's load is counted **exactly once** — never double-counted across the meter *and* its service point.

**What it computes** (`ServiceTotals`): per set of reached leaves it sums `service_points`, `customers` (NULL `customers_served` = 1), `demand_kw`, and `demand_kva` — where **kVA = kW ÷ power_factor** per leaf (pf defaults to unity), summed, not re-derived off an averaged power factor. It also bucket the load by **restoration tier** (`LOAD_PRIORITY` P1…P4; P1 "Life support", P2 "Essential"; "critical" = P1 or P2).

**Where it surfaces:**
- Every ordinary trace (run / feeder / section / isolation / supply) attaches a consumption summary.
- `POST /trace/service` 🔒 (`trace:run`) runs a dedicated downstream rollup from a start feature, returning the totals plus a per-service-point breakdown.
- **OMS** reuses the same rollup for "affected customers" during an outage, so the dispatcher's number can never drift from a downstream trace.

💡 So when **Priya** sees "1,240 customers affected, 85 critical" on an outage, that figure came from this rollup walking the de-energised LV leaves — the same code **Tom** gets when he runs a feeder consumption trace.

---

## Chapter 10 — Worked examples

### 10.1 The standard metered feeder (the test network)

The seeded test network (`seed/test_network_example.sql`) is chain #3 built twice off one LV main. Real values:

```
 TX-101  (DIST_XFMR, pad-mounted, 11 kV → 400 V, 500 kVA, Z=4.5%)
   │ T2
   ▼
 LV-1  (LV_CONDUCTOR, overhead, WAVEFORM_185, phase L1L2L3N, lv_role=main)
   ├─► MTR-A.T1  [METER whole_current · MTR-A-0001 · ACC-A · L1L2L3N]  ─T2─► SP-A.T1  [commercial · 15 kW · pf 0.95]
   └─► MTR-B.T1  [METER whole_current · MTR-B-0001 · ACC-B · L1L2L3N]  ─T2─► SP-B.T1  [industrial · 30 kW · pf 0.90]
```

Every value above is in the seed. Price the materials with the [Bill of Quantities](bill-of-quantities-guide.md) (two transformers, one LV span, two meters, two service points), trace supply from either service point back to TX-101, and roll up consumption — SP-A + SP-B give **45 kW**, and **≈ 49.1 kVA** (15/0.95 + 30/0.90 = 15.79 + 33.33).

⚠️ **A subtlety worth seeing in a real tool.** MTR-A and MTR-B in the seed have **no `meter_role` set**, so under the Chapter 9 PRIMARY contract the *meters* are inert — the 45 kW rolls up via **SP-A/SP-B**, not the meters. Mark a meter `PRIMARY` only when you want the network to "end" at it; otherwise the service point is the load leaf. This is exactly the guard that stops double-counting.

### 10.2 A closed LV loop (the ring network)

The LV ring (`seed/lv_ring_network_example.sql`) is the one **closed-loop** LV example — two mains from one transformer meeting at a single meter:

```
 TX-R  (DIST_XFMR, 11 kV → 400 V, 500 kVA)  ─T2─►  (two parallel mains)
        │                                        │
   LVA (WAVEFORM_185, ~100 m)             LVB (WAVEFORM_185, ~200 m = 2 × LVA)
        │                                        │
        └──────────────► MTR-R.T1 ◄─────────────┘   (both feed the same meter — the loop)
                           │ T2
                           ▼
                        SP-R  [commercial · 30 kW · pf 1.0]
```

**∑ The hand-check the seed is built for:** LVB is exactly twice LVA's length, so twice its resistance. Current splits inverse to resistance, so of SP-R's 30 kW, **LVA carries 20 kW and LVB carries 10 kW** (a 2:1 split). pf is unity on purpose so kW = kVA and the arithmetic is clean. This is the fixture the meshed-LV load flow validates against.

### 10.3 The apartment block (chain #6, conceptually)

A twenty-flat block is chain #6: one `SERVICE_CUTOUT` off the street main, into one `LV_BUSBAR` inside one `METER_CHAMBER`, with twenty `METER → SERVICE_POINT` pairs hanging off the **one** `T2` bar (§5.2). You build it by placing the chamber, dropping the busbar and meters on its `CHAMBER_SCHEMATIC` diagram, and letting the single `LV_BUSBAR T2 → METER T1` rule be satisfied twenty times. Each flat's meter is *owned* by its own service point; all twenty are *contained* in the one chamber.

---

## Chapter 11 — Asset ownership: utility vs private

Not every asset on the map belongs to the utility. A common arrangement: the utility supplies and **meters an MV bulk supply** (Chapter 6 — an `MV_METERING_POINT` of subtype `bulk_supply`), and the **customer owns everything below it** — their own distribution transformer and their own LV network. The assets are on the grid, and you want them on the map, but they are *not* utility plant. The **ownership axis** captures that.

### 11.1 The model

Every transformer and LV-set class carries a coded **`ownership`** field — `utility` (the default; an unset value reads as utility) or `private`. One rule governs how you use it, and it matches the physics:

> **Declare private at the boundary; it propagates downstream.** Mark the customer's `DIST_XFMR` private, and *everything electrically downstream* — the LV conductors, boards, pillars, service points, meters — is private too.

**∑ Why propagation, not per-feature tagging.** Ownership is a **connectivity** property: an asset is the customer's if it hangs off the customer's transformer. So the propagation is a downstream walk that **ignores open switches** (a normally-open tie inside the private network doesn't change who owns the assets beyond it) and **crosses the transformer** into the LV (the whole galvanic island below the boundary is private). It is *not* an energisation trace.

### 11.2 Doing it in the app ▶ 🔒

🔒 *Needs `features:write`.* Open the customer's transformer on the map and open its **actions menu** (the ⌄ button in the panel header, alongside the trace actions). On a transformer it carries two ownership actions:

1. **Mark downstream private** — one click: it declares the transformer private and stamps `ownership = private` across its whole downstream LV network, in one transaction. A toast reports how many features were marked. (It reads **Re-mark downstream private** once the transformer is already private.)
2. **Re-sync ownership** — recomputes the entire ownership map from every currently-private transformer. Run it after a re-wire so ownership always equals "what traces from a private transformer right now" (the drift fix: marking a transformer back to utility, or moving a cable, is reconciled here).

These actions appear **only on a transformer** — the boundary where ownership is declared. A private feature elsewhere shows just a quiet **"Privately owned"** chip in its panel (nothing to set there); a plain utility feature shows no ownership UI at all, because **utility is the silent default** — you never touch ownership unless a site genuinely isn't utility-owned.

💡 You *can* also set the `ownership` field by hand on a single feature (it's a normal attribute edit), but the **Mark downstream** action is the intended workflow — it's what makes "the LV below is private" true without tagging every cable.

### 11.3 How private assets read

- **On the map:** private plant looks **exactly like every other feature** — standard colours and symbology. Ownership is a *data flag*, not a restyle; the map isn't recoloured by it.
- **In the feature panel:** a private feature shows a small **"Privately owned"** chip; a transformer (the declare boundary) carries the **Mark downstream private** / **Re-sync ownership** actions in its actions dropdown. A plain utility feature shows no ownership UI at all (utility is the silent default).
- **In a trace / load flow:** a trace does **not** stop at the ownership boundary — it crosses and includes the private section — but every private feature in the result carries a **"Private"** badge (and the load-flow table an **Owner** column), so you can always see which stretch is the customer's. This is the deliberate "include it, but flag it" behaviour.
- **From an agent:** the `mark_ownership_downstream_private` and `resync_ownership` MCP tools do the same two actions over the agent surface.

### 11.4 Worked example — a privately-owned transformer

Take the test network (§10.1). Suppose **TX-101** is actually the customer's transformer, fed from a utility MV bulk supply that the utility meters at the boundary. Open TX-101's actions menu → **Mark downstream private**:

```
 [utility MV] ──(metered at the boundary: MV_METERING_POINT bulk_supply)──►
 TX-101  (DIST_XFMR)  ← ownership = private (declared here)
   │ propagates downstream ▼
 LV-1  (LV_CONDUCTOR)   ← private   (standard symbology; flagged, not restyled)
   ├─► MTR-A ─► SP-A     ← private
   └─► MTR-B ─► SP-B     ← private
```

TX-101, LV-1, both meters and both service points are now `private`; they look the same on the map (ownership is a data flag, not a restyle), and a supply trace from SP-A back up through TX-101 shows the whole run with a **Private** badge until it reaches the utility side. The utility's revenue meter remains the **MV** meter at the boundary (what the utility bills); the private LV meters are the customer's own.

### 11.5 What's deliberately not (yet) modelled

- **Who** owns it — v1 is a plain `utility` / `private` flag, not a named organisation. A richer "owned by *Acme Steelworks*" layer (CIM `AssetOwner`) can come later.
- **The boundary** is declared on the **transformer** today. A private *MV* network above the transformer (an org that owns MV switchgear and several transformers) would want the boundary set at the MV intake / metering point — a fast follow; the propagation engine already handles any start feature.
- **Load flow** treats the private site's downstream as ordinary network for now; the trace family tags ownership, but the power-flow bus/branch tables don't yet.

---

## Appendix A — Class quick reference

| Class | Role | Geometry | Terminals | Subtypes |
| --- | --- | --- | --- | --- |
| `LV_CONDUCTOR` | edge | line | — | overhead / underground |
| `SERVICE_CUTOUT` | junction | point | T1 line · T2 load | fuse_carrier / switch_fuse |
| `LV_SWITCH` | junction | point (optional) | T1 · T2 | standard |
| `LV_LINK_BOX` | junction | point | T1 in · T2/T3/T4 out | feeder_pillar / link_box |
| `LV_PILLAR` | container | point | — (owns bus + fuses) | standard |
| `LV_BUSBAR` | junction | point | T1 in · T2 bar | lv_distribution_board / busbar_riser |
| `LV_FUSE` | junction | none (diagram) | T1 bus · T2 out | standard |
| `METER_CHAMBER` | container | point | — | cabinet / room |
| `METER` | junction | point | T1 line · T2 load | whole_current / ct_metered |
| `SERVICE_POINT` | junction (leaf) | point | T1 | residential / commercial / industrial |
| `STREET_LIGHT` | junction (leaf) | point | T1 | column_mounted / wall_mounted |
| `MV_METERING_POINT` | junction (leaf) | point | T1 | industrial / commercial_complex / bulk_supply |
| `MV_METER` | none (off-path) | point | — | main_meter / check_meter |
| `MV_METERING_CUBICLE` | container | point | — | indoor / outdoor_kiosk |
| `NETWORK_METER` | none (off-path) | point | — | incomer / feeder_head / transformer / bus_section / boundary / tie / generation_export |

## Appendix B — Metering relationship kinds

| Kind | Meaning | Used by | Migration |
| --- | --- | --- | --- |
| `composite` | ownership | `SERVICE_POINT` owns `METER`; `MV_METERING_POINT` owns `MV_METER` | 0010 |
| `containment` | spatial enclosure | `METER_CHAMBER` contains `{LV_BUSBAR, METER}`; `MV_METERING_CUBICLE` contains `{MV_METER, MV_CT, MV_VT}` | 0010 |
| `metering_sensing` | meter fed by instrument transformer | `MV_METER` measures via `MV_CT` / `MV_VT` | **0080** |
| `metering_accounting` | meter accounts for energy through an element | `NETWORK_METER` meters `{DIST_XFMR, POWER_XFMR, MV_BUSBAR, MV_CONDUCTOR, LV_CONDUCTOR}` | **0121** |

## Appendix C — Which metering class, when

| You're modelling… | Use | Why |
| --- | --- | --- |
| A customer's LV supply current | `METER` (inline) + `SERVICE_POINT` | Current flows through the meter; service point is the leaf and owner. |
| An MV customer handover | `MV_METERING_POINT` + `MV_METER` + CTs/VTs | Current never flows through the meter; it reads instrument-transformer secondaries. |
| Energy accounting on the grid (no customer) | `NETWORK_METER` | An off-path accounting overlay on a transformer/busbar/conductor; readings are external. |
| Unmetered street lighting | `STREET_LIGHT` | An unmetered LV leaf, phase-gated off the main. |

## Appendix D — Key domains

- **`PHASE`** — `L1`, `L2`, `L3`, `N`, `L1N`, `L2N`, `L3N`, `L1L2L3N`.
- **`LV_CABLE_SPEC`** — `ABC_95`, `WAVEFORM_185`, `CONCENTRIC_25` (material-backed).
- **`LV_ROLE`** — `main`, `service`.
- **`CT_RATIO`** — `100_5`, `200_5`, `400_5`, `800_5`, `1600_5`.
- **`VT_RATIO`** — `11KV_110`, `33KV_110`.
- **`METERING_CLASS`** — `0.2S`, `0.5S`, `1.0` (IEC 62053).
- **`MV_SUPPLY_VOLTAGE`** — `11KV`, `33KV`.
- **`FUSE_RATING`** — `100A`, `160A`, `200A`, `250A`, `315A`, `400A` (BS88).
- **`LUMINAIRE_TYPE`** — `LED`, `SON`, `SOX`, `MH`.
- **`LOAD_PRIORITY`** — `P1` (Life support) … `P4` (Standard).
- **`OWNERSHIP`** — `utility` (default), `private` (Chapter 11).

## Appendix E — Where this is grounded

LV: `seed/lv_conductors_example.sql`, `seed/lv_devices_example.sql`, `seed/lv_metering_example.sql`, `seed/lv_unmetered_ct_example.sql`, `seed/lv_pillar_example.sql`, `seed/mv_lv_switch_example.sql`, `seed/service_point_example.sql`. Metering: the same, plus `seed/mv_metering_example.sql`, `seed/mv_metering_cubicle_example.sql`, `seed/network_meter_example.sql`, `seed/meter_load_leaf.sql`. Examples: `seed/test_network_example.sql`, `seed/lv_ring_network_example.sql`. Engine: `api/internal/trace/service.go` (consumption rollup), `api/internal/relationship/cardinality.go` (ownership/containment caps). Design: `docs/phase-9-lv-service-delivery.md`. Per-class reference: `docs/feature-catalog.md` (Chapter 6, LV). Relationship-kind migrations: `0080` (metering_sensing), `0121` (metering_accounting).
