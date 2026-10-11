# VoltEdge Data Dictionary

A reference for anyone **entering or importing data**: what every feature class is, and what each of its fields means — in a line or two, not an essay. Use it as the key while you populate the network.

**How to read it.**
- **Part 1 — System columns**: the columns *every* compiled `gis.<class>` table carries (id, name, status, geometry, audit, and the cross-cutting axes). Described once; not repeated per class.
- **Part 2 — Shared domains**: the coded value-lists (`PHASE`, `VOLTAGE_CLASS`, …) many fields draw from. Described once; the per-class tables just name the domain.
- **Part 3 — Feature classes**: one compact block per class — what it is, its subtypes, and a field table (name · type · domain · meaning).

**Two conventions that trip people up:**
- **"Required" has two layers.** (1) A field is *mandatory for the create form / API* when a **subtype rule** marks it required (the asterisk on the form; the API answers `400 field "x" is required for subtype "y"` unless the rule carries a default). (2) Most mandatory fields are *also* declared non-nullable in the metadata, so the compiled column is `NOT NULL` and the database refuses a missing value outright. In the seeded model: 372 fields, **102 are `NOT NULL`**, 95 are required by at least one subtype, 87 are both. Eight are subtype-required but nullable (`TAP_CHANGER`'s three, `EV_CHARGER.charger_type`, `EV_CONNECTOR.connector_standard`, `SUBSTATION.primary_voltage`, `MV_CAP_BANK.device_state` for `switched`, `METER.ct_ratio` for `ct_metered`), and 15 are `NOT NULL` with **no** subtype rule — all on classes that have no (loadable) subtypes (`CABLE_TRENCH`, `CABLE_DUCT`, `DUCT_WAY`, `CABLE_CHAMBER`, `EARTHING_XFMR`, the two cable-joint classes): a class without subtypes never runs required-field validation, so only the database catches a missing value there. Fields added later to an already-populated class (arc-flash geometry, `phase`, `is_critical`, the axis fields…) are deliberately **nullable**, because `ALTER TABLE ADD COLUMN … NOT NULL` fails on a table that has rows. Below, **req** means "mandatory via a subtype rule"; where a field is also `NOT NULL` or the rule is subtype-specific the text says so.
- **Coded fields take a code, not free text.** A field with a domain (e.g. `phase`) only accepts that domain's value codes (`L1L2L3N`), never a typed label. Codes are case-sensitive and are the *code* column, not the label (`VECTOR_GROUP` is `DYN11`, label "Dyn11").

> ⚠️ This is generated from the seed metadata and the feature catalog, and was audited against `seed/*.sql` and `migrations/0001`–`0134` (see **Appendix C — Corrections** for what that audit changed). Where the live model and this doc disagree, the live model (the admin feature-class screens / `GET /feature-classes/{code}/schema`) is the truth — field packs get added over time.

**Also in this document:** Part 4 *Which class do I use?* · Part 5 *Connectivity at a glance* (terminals) · Part 6 *Worked examples & common mistakes* · Appendix A (class inventory) · Appendix B (axis coverage) · Appendix C (corrections). An interactive explorer for all of it is in [`data-dictionary-interactive.html`](data-dictionary-interactive.html).

---

## Part 1 — System columns (every `gis.<class>` table)

The compiler gives every feature class these base columns; you never author them as "fields," but they're on every row.

| Column | Type | Meaning |
|---|---|---|
| `id` | uuid | Primary key, server-generated. The stable identity used by connectivity, relationships, tags, circuits. |
| `name` | text (nullable) | Human label for the feature (e.g. "TX-101"). Optional. |
| `subtype` | integer, **NOT NULL** | Which **subtype** of the class this is (the integer code — see each class's subtypes), with a `CHECK` limited to that class's codes. Drives which fields are required. **Only present on a class that has at least one subtype**; a class with none has no `subtype` column (a `subtype` sent on create is ignored; on update it is refused). |
| `status` | text | Publish state: `draft` (being authored) or `published` (live/signed-off). Orthogonal to deletion and to operational state. |
| `geom` | geometry | The map geometry (point / line / polygon per the class), SRID 4326. `NOT NULL` unless the class is **geometry-optional** (then it may be null — a diagram-only feature). A class whose geometry type is **`none`** has **no `geom` column at all**, and the API refuses a geometry sent for it. |
| `created_at` / `created_by` | timestamptz / uuid | Audit: when and by whom the row was created. |
| `updated_at` / `updated_by` | timestamptz / uuid | Audit: last edit. (Also what the circuit "stale" freshness check reads.) |
| `published_at` / `published_by` | timestamptz / uuid | Sticky "ever made public" marker (set the first time a draft is published; see the private-drafts overlay). |
| `deleted_at` | timestamptz (nullable) | Soft-delete marker; a non-null value hides the feature until purged. Every query filters `deleted_at IS NULL`. |

**Cross-cutting axis columns** — present on the classes that opt into each axis (network-participating classes mostly), added by their migrations/seeds:

| Column | On | Meaning |
|---|---|---|
| `territory_id` | every class with a geometry type (not `none`) | The territory the feature falls in (auto-assigned from geometry on save, or forced with `territory_id` in the create body); drives spatial access control. Nullable, no foreign key. |
| `operational_state` | 29 classes — physical network assets **and** `POLE`, `BAY`, `SUBSTATION`, `METER_CHAMBER` (list in Appendix B). Added by `seed/operational_state_example.sql`; the transition graph lives in `domain_transitions` (mig 0065) | The in-service lifecycle axis: `PLANNED` → `IN_SERVICE` → `ISOLATED` (tagged out) → `RETIRED` (domain `OPERATIONAL_STATE`). Nullable. Only the legal moves are accepted on update (`PLANNED→IN_SERVICE/RETIRED`, `IN_SERVICE→ISOLATED/RETIRED`, `ISOLATED→IN_SERVICE/RETIRED`; `RETIRED` is terminal). An `ISOLATED`/`RETIRED` element is a barrier for the **downstream and upstream** trace configurations (`PLANNED` is not). Distinct from `status` (publish) and `device_state` (switch position). |
| `circuit_id` | 31 electrical classes (Appendix B). Field added by `seed/circuit_membership_example.sql`; the `gis.circuits` catalog it points at is mig 0062 | Feeder/subnetwork membership: `uuid` (soft reference, no FK) of the circuit. Normally stamped by assign-by-trace; also editable by hand as an override through a network-scoped picker (`MV` classes offer MV circuits, `LV` classes LV ones). See the [Feeders, Sections & Subnetworks](network-sectioning-guide.md) guide. |
| `circuit_ref` | **`MV_CONDUCTOR` and `LV_CONDUCTOR` only** (`seed/circuit_ref_example.sql`) | The circuit's *name*, denormalised for the map label. **Computed and hidden** — the API rejects a direct write (`field "circuit_ref" is computed and cannot be set directly`); only assign-by-trace writes it. |
| `ownership` | 13 classes: `DIST_XFMR`, `LV_BUSBAR`, `LV_CONDUCTOR`, `LV_FUSE`, `LV_LINK_BOX`, `LV_PILLAR`, `LV_SWITCH`, `METER`, `METER_CHAMBER`, `POWER_XFMR`, `SERVICE_CUTOUT`, `SERVICE_POINT`, `STREET_LIGHT` (`seed/ownership_axis.sql`; *not* a migration — mig 0129 is `boundary_role`) | `utility` (default/unset) or `private` — the asset-ownership flag; set at a transformer, propagated downstream. See the [LV & Metering](lv-and-metering-guide.md) guide, Ch 11. |

**Class-level metadata** (a property of the *class*, not a row column) — you see these on the admin feature-class screens, not in the attribute form:
- **`geometry_type`** — `point` / `line` / `polygon` / `none`; plus the **`geometry_optional`** flag (the geom column is nullable, so a feature can be diagram-only), **`is_diagram_container`** (can host a schematic of what it contains) and **`is_diagram_placeable`** (can be placed on a schematic).
- **`network_role`** — the connectivity-graph role: `edge` (a conductor), `junction` (a node with terminals), `container` (groups/holds, off the electrical path), `none` (off-graph). **`network`** (`MV` / `LV` / unset) is a separate voltage-layer tag used to scope the circuit picker.
- **`functional_role`** (mig 0091) — the electrical function: `source` / `consumer` / `passthrough` / `transformer` / `controller` / `node` / `sensor` / `ground`.
- **`boundary_role`** (mig 0129) — the network-sectioning function: `source` / `protective` / `switching` / `transformer`, or unset. See the sectioning guide.
- **`trace_tier`** (mig 0126) — vertical rank source→load (0 grid infeed · 200 power xfmr · 300 MV · 400 dist xfmr · 500 LV · 600 service), guarding directional traces.

---

## Part 2 — Shared domains (coded value-lists)

Common domains referenced across many classes. A field pointing at one of these only accepts its codes.

### Electrical / connectivity
- **`PHASE`** — `L1`, `L2`, `L3`, `N` (Neutral), `L1N`, `L2N`, `L3N` (single-phase + neutral), `L1L2L3N` (three-phase + neutral). Which phases a conductor/device carries.
- **`VOLTAGE_CLASS`** — coded nominal voltage: `400V`, `11KV`, `33KV`, `66KV`, `132KV`, `161KV`, `330KV`. The class of voltage, not an exact number.
- **`DEVICE_STATE`** — `OPEN` / `CLOSED`. Live switch position of a switching device; `OPEN` stops a trace (barrier).
- **`OPERATIONAL_STATE`** — the in-service lifecycle: `PLANNED` / `IN_SERVICE` / `ISOLATED` / `RETIRED`, with a transition graph (see Part 1); `ISOLATED` and `RETIRED` are trace barriers in the upstream/downstream traces, `PLANNED` is not.

### Asset / maintenance
- **`CONDITION_RATING`** — `GOOD` / `FAIR` / `POOR`. Asset condition, on nearly every physical class.
- **`OWNERSHIP`** — `utility` (default) / `private`. Who owns the asset.

### Conductors & materials
- **`LV_CABLE_SPEC`** — LV cable catalog codes (`ABC_95`, `WAVEFORM_185`, `CONCENTRIC_25`), each material-backed (resolves to R/X, ampacity, cost/km).
- **`MV_LINE_SPEC`** — MV conductor catalog codes (`ACSR_50/100/150`, `AAAC_100`, `MV_XLPE_95/240`), material-backed.

### Metering & measurement
- **`CT_RATIO`** — `100_5`, `200_5`, `400_5`, `800_5`, `1600_5` (primary/5 A).
- **`VT_RATIO`** — `11KV_110`, `33KV_110` (primary / 110 V).
- **`METERING_CLASS`** — IEC 62053 accuracy class, e.g. `0.2S`, `0.5S`, `1.0`.
- **`MV_SUPPLY_VOLTAGE`** — `11KV` / `33KV` (MV customer supply).

### Service & load
- **`LOAD_PRIORITY`** — `P1` (Life support) · `P2` (Essential) · `P3` (Priority) · `P4` (Standard). Restoration tier; "critical" = P1/P2.

### Protection
- **`FUSE_RATING`** — fuse sizes (e.g. `100A`, `160A`, `200A`, `250A`, `315A`, `400A` BS88).
- **`ANSI_DEVICE_NUMBER`** — relay function codes: `27` undervoltage, `59` overvoltage, `50_51` over-current, `50_51N` earth-fault, `87` differential, `79` auto-reclose, `81` frequency, `25`, `32`, `37`, `46`, `49`, `64`, `67`, `67N`, `86`.
- **`TCC_CURVE`** — time-current curve shapes: `IEC_SI/VI/EI/LTI`, `IEEE_MI/VI/EI`, `DEFINITE_TIME`.
- **`FUSE_SPEED`** — `K` (fast), `T` (slow), `SLOW`, `FAST`.

### Transformer
- **`VECTOR_GROUP`** — winding/phase config. **Codes are upper-case** — `DYN11`, `DYN1`, `YND11`, `YND1`, `YNYN0`, `DD0`, `YY0` — shown with the labels *Dyn11, Dyn1, YNd11, YNd1, YNyn0, Dd0, Yy0*; type the code (drives zero-sequence / SLG behaviour).
- **`GROUNDING_TYPE`** — earthing arrangement: `ZIGZAG` (interconnected-star) / `WYE_DELTA` on earthing transformers.
- **`TAP_CHANGER_TYPE`** — `on_load` (OLTC) / `off_circuit` (DETC). **`TAP_CONTROL_MODE`** — `auto` (AVR) / `manual` / `off` (locked).

### LV / service roles
- **`LV_ROLE`** — `main` (backbone) / `service` (customer drop), on an LV conductor.
- **`METER_ROLE`** — `PRIMARY` (carries load — counted) / `CHECK` (audit meter — not counted).
- **`RMU_CONFIG`** — `2W_1T` (2 ring ways + 1 transformer) / `2W_2T` (2 ring + 2 transformer) — drives the RMU fan-out.

### Substation grouping
- **`YARD_ENCLOSURE`** — `FENCED` / `WALLED` / `OPEN` (site boundary).
- **`BAY_ROLE`** — `INCOMER` / `FEEDER` / `BUS_COUPLER` / `BUS_SECTION` / `TRANSFORMER`.
- **`PANEL_TYPE`** — `PROTECTION` / `CONTROL` / `METERING` / `RTU_SCADA` / `AUXILIARY_DC` / `MARSHALLING`.
- **`CONTROL_MODE`** — `LOCAL` / `REMOTE` / `LOCAL_REMOTE`.
- **`BUILDING_CONSTRUCTION`** — `BRICK` / `CONCRETE` / `PREFAB` / `CONTAINERIZED` / `STEEL_CLAD`.
- **`PACKAGED_SUB_CONFIG`** — packaged-substation kit, e.g. `COMPACT`.
- **`SWITCH_FUNCTION`** — `FEEDER` / `INCOMER` / `BUS_COUPLER` / `BUS_SECTION` / `TRANSFORMER` (on an MV_BREAKER).
- **`NORMAL_STATE`** — `NORMALLY_CLOSED` / `NORMALLY_OPEN` (designed normal position; `NORMALLY_OPEN` = a tie/NOP).

### P&C / DC auxiliary
- **`IED_ROLE`** — `bay_controller` / `rtu` / `scada_gateway` / `station_hmi` / `protocol_converter`.
- **`COMMS_PROTOCOL`** — `iec61850` / `dnp3` / `iec60870_5_104` / `modbus`.
- **`BATTERY_TECHNOLOGY`** — `lead_acid_vented` / `lead_acid_vrla` / `nicd` / `li_ion`.
- **`CHARGER_TYPE`** — `smps` / `thyristor` / `ferroresonant`.

### Structural / civil
- **`POLE_MATERIAL`** — `WOOD` / `CONCRETE` / `STEEL`. **`POLE_USE`** — `DISTRIBUTION` / `TRANSMISSION` / `SERVICE`. **`POLE_HEIGHT_CLASS`** — `CLASS_1/2/3`.
- **`TOWER_MATERIAL`** — `STEEL_LATTICE` / `TUBULAR_STEEL` / `CONCRETE`.
- **`STRUCTURE_USE`** — `RESIDENCE` / `COMMERCIAL` / `INDUSTRIAL` / `KIOSK` / `HOSPITAL` / `INSTITUTIONAL` / `EDUCATIONAL` / `RELIGIOUS` / `MIXED_USE` / `OTHER`.
- **`TRENCH_SURFACE`** — `ROAD` / `FOOTWAY` / `VERGE` / `UNMADE`. **`DUCT_MATERIAL`** — `PVC` / `HDPE` / `STEEL` / `EARTHENWARE`. **`CHAMBER_COVER`** — `LIGHT` / `MEDIUM` / `HEAVY`.
- **`INSULATION_TYPE`** — `XLPE` / `EPR` / `PILC` / `HEAT_SHRINK` (cable joints).
- **`LUMINAIRE_TYPE`** — `LED` / `SON` (HP sodium) / `SOX` (LP sodium) / `MH` (metal halide).

### Arc flash / EV
- **`ARC_ELECTRODE_CONFIG`** — `switchgear` / `mcc_panelboard` / `cable` / `open_air` (IEEE 1584 electrode geometry). Used by the optional `arc_*` fields on `DIST_XFMR`, `POWER_XFMR`, `MV_BUSBAR`, `MV_SWITCHGEAR_PANEL`.
- **`EV_ACCESS`** — `public` / `depot` / `residential` / `workplace`. **`EV_CHARGER_TYPE`** — `ac_l2` / `dc_fast` / `dc_ultra`. **`EV_CONNECTOR_STANDARD`** — `type2` / `ccs2` / `chademo` / `nacs` / `gbt`.

### Other coded lists (not used by a feature field)
- **`DIAGRAM_TYPE`** — `ONE_LINE` / `CHAMBER_SCHEMATIC` / `BOARD_SCHEMATIC` / `METERING_SCHEMATIC`: which kind of schematic a container hosts and which classes may be placed on it.
- **`SNAP_TARGET_TYPE`** — `point_feature` / `endpoint` / `edge` / `vertex`: what a snapping rule snaps to.
- **`TRACE_DIRECTION`** — `CONNECTED` / `DOWNSTREAM` / `UPSTREAM` / `SHORTEST_PATH`: the direction of a trace configuration.

*All coded domains are `coded` type; there are no numeric `range` domains in the seeded model (the `range_min`/`range_max` columns of mig 0020 exist but are unused). The 53 domains hold 220 values in total; the compiled column carries a `CHECK (col IN (…))`, and the API also checks membership first (`field "phase" value "3PH" is not a valid PHASE value`).*

---

## Part 3 — Feature classes

> Each class block: **CODE — Name** *(geometry · network_role)* · subtypes · field table. System columns (Part 1) and the common `condition_rating` are not repeated in prose. *Fields marked **req** are mandatory via subtype rules.*

> **Where is the "grid supply point"?** There is **no** `GRID_SUPPLY_POINT` class. A network infeed is modelled as a **slack transformer** — a `DIST_XFMR` (or `POWER_XFMR`) carrying `source_r_ohm`/`source_x_ohm` (the Thévenin source behind it) — plus the first-class `GENERATOR` and `MV_BESS` sources. `trace_tier=0` marks the infeed rank.

### Source & DER

**GENERATOR — Generator** *(point · junction, role=source, boundary=source, tier 0)* · subtypes: `1 diesel`, `2 solar_pv`, `3 wind`, `4 hydro`, `5 battery`, `6 gas_chp`
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `rated_kw` | numeric | — | Nameplate real output. |
| `dispatch_kw` | numeric | — | Live actual output (authoritative even at 0). |
| `power_factor` | numeric | — | Operating pf (unset = unity). |
| `fault_contribution_ka` | numeric | — | Prospective 3-phase fault infeed. |
| `subtransient_reactance_pct` | numeric | — | Machine X″d %. |
| `voltage_setpoint_pu` | numeric | — | PV-bus regulated voltage (grid-forming). |
| `reactive_limit_kvar` | numeric | — | Symmetric ±Q bound on the PV bus. |

**MV_BESS — MV Battery Storage** *(point, geometry-optional · junction, network=MV)* · subtypes: `1 standalone`, `2 hybrid`
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `rated_power_kw` | numeric | — | PCS charge/discharge rating. **req** |
| `storage_dispatch_kw` | numeric | — | Signed set-point (+discharge / −charge). |
| `energy_capacity_kwh` | numeric | — | Usable energy size. |
| `state_of_charge_pct` | numeric | — | Present charge 0–100. |
| `power_factor` | numeric | — | Operating pf. |
| `condition_rating` | text | `CONDITION_RATING` | Condition. **req** |

**MV_CAP_BANK — MV Capacitor Bank** *(point, geometry-optional · junction shunt, network=MV)* · subtypes: `1 fixed`, `2 switched` · injects reactive support at its node (T1 line, T2 earth)
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `rated_kvar` | numeric | — | Three-phase leading reactive rating, kVAr. **req** (and `NOT NULL`) |
| `device_state` | text | `DEVICE_STATE` | `CLOSED` = in circuit (injecting), `OPEN` = switched out. **req for `switched` only**; a fixed bank is always connected. |
| `condition_rating` | text | `CONDITION_RATING` | Condition. **req** |

**MV_VREG — MV Voltage Regulator** *(point, geometry-optional · junction series, network=MV)* · subtypes: `1 single_phase`, `2 three_phase` · a tap-changing regulator in the line (intended terminals T1 source / T2 load — **as seeded it has none**, Appendix C item 21)
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `regulator_step_pct` | numeric | — | Voltage change per tap step, % (an ANSI regulator is 0.625 %/step). Its presence marks the node as a regulator. **req** |
| `regulator_tap_position` | numeric | — | Live tap, signed (− buck, + boost, 0 nominal); applied ratio = 1 + tap × step/100. **req** |
| `device_state` | text | `DEVICE_STATE` | `CLOSED` = in regulation, `OPEN` = bypassed. **req** |
| `condition_rating` | text | `CONDITION_RATING` | Condition. **req** |

### Transformers

**POWER_XFMR — Power Transformer** *(point, geometry-optional · junction, role=transformer, boundary=transformer, tier 200)* · subtypes: `1 two_winding` (HV/LV/N terminals), `2 three_winding` (HV/LV/TV/N)
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `primary_voltage` | text | `VOLTAGE_CLASS` | HV nominal. **req** |
| `secondary_voltage` | numeric | — | LV nominal (volts). |
| `impedance_pct` | numeric | — | Nameplate %Z HV–LV (sets LV fault level). |
| `x_r_ratio` | numeric | — | X/R of %Z. |
| `rated_mva` | numeric | — | Nameplate MVA. |
| `tertiary_voltage` | numeric | — | TV nominal (3-winding). |
| `z_hv_tv_pct` / `z_lv_tv_pct` | numeric | — | %Z HV–TV / LV–TV (3-winding). |
| `vector_group` | text | `VECTOR_GROUP` | Winding config (record-only). |
| `tap_position` | numeric | — | Live tap step (0 = nominal). |
| `tap_step_pct` | numeric | — | % voltage change per tap. |
| `tap_min` / `tap_max` | numeric | — | Tap range (validation). |
| `z0_pct` | numeric | — | Zero-sequence %Z. |
| `source_r_ohm` / `source_x_ohm` | numeric | — | Thévenin source impedance — set these to make a power transformer the network infeed (slack). |
| `source_r0_ohm` / `source_x0_ohm` | numeric | — | Thévenin zero-sequence source impedance. |
| `arc_electrode_configuration` | text | `ARC_ELECTRODE_CONFIG` | Arc-flash electrode arrangement (optional; unset = enclosed switchgear). |
| `arc_conductor_gap_mm` / `arc_working_distance_mm` | numeric | — | Arc-flash electrode gap / worker distance, mm (optional; unset = IEEE typical). |
| `arc_system_grounded` | boolean | — | Grounded system (lower-energy case); unset = ungrounded (conservative). |
| `condition_rating` | text | `CONDITION_RATING` | Condition. **req** |

**DIST_XFMR — Distribution Transformer** *(point · junction, role=transformer, boundary=transformer, tier 400, network=MV)* · subtypes: `1 pole_mounted`, `2 pad_mounted`, `3 underground_vault` · *also the network infeed when it carries `source_*` fields (slack)*
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `primary_voltage` | text | `VOLTAGE_CLASS` | MV nominal. **req** |
| `secondary_voltage` | numeric | — | LV nominal (volts). |
| `impedance_pct` | numeric | — | Nameplate %Z. |
| `x_r_ratio` | numeric | — | X/R. |
| `rated_kva` | numeric | — | Nameplate kVA. |
| `source_r_ohm` / `source_x_ohm` | numeric | — | Thévenin source impedance (makes this the slack infeed; start-only). |
| `z0_pct` | numeric | — | Zero-sequence %Z. |
| `source_r0_ohm` / `source_x0_ohm` | numeric | — | Thévenin zero-sequence source impedance. |
| `phase` | text | `PHASE` | Phases carried (optional). |
| `arc_electrode_configuration` | text | `ARC_ELECTRODE_CONFIG` | Arc-flash electrode arrangement (optional; unset = enclosed switchgear). |
| `arc_conductor_gap_mm` / `arc_working_distance_mm` | numeric | — | Arc-flash electrode gap / worker distance, mm (optional). |
| `arc_system_grounded` | boolean | — | Grounded system flag (optional). |
| `condition_rating` | text | `CONDITION_RATING` | Condition. **req** |

**AUX_TRANSFORMER — Station Auxiliary Transformer** *(point, geometry-optional · junction, tier 400)* · subtype: `1 standard` · steps MV→LV for station services
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `primary_voltage` | text | `VOLTAGE_CLASS` | MV in. **req** |
| `secondary_voltage` | numeric | — | LV out (volts). |
| `impedance_pct` | numeric | — | %Z. |
| `rated_kva` | numeric | — | Nameplate kVA. |
| `condition_rating` | text | `CONDITION_RATING` | Condition. **req** |

**EARTHING_XFMR — Earthing Transformer** *(point, geometry-optional · junction, role=ground)* · no subtypes (so its **req** fields are enforced only by the database `NOT NULL`, not by a subtype rule) · provides a neutral for SLG earthing (terminals LINE + N; **no** secondary voltage)
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `primary_voltage` | text | `VOLTAGE_CLASS` | System voltage it earths. **req** |
| `connection_type` | text | `GROUNDING_TYPE` | Zig-zag / wye-delta. **req** |
| `z0_ohm` | numeric | — | Zero-sequence impedance (drives SLG model). |
| `rated_neutral_current_a` | numeric | — | Short-time neutral current rating. |
| `rated_time_s` | numeric | — | Duration for that rating. |
| `neutral_resistance_ohm` | numeric | — | Integral NER (folded 3× into z0). *(This is the "NER" — there is no separate NER class.)* |
| `condition_rating` | text | `CONDITION_RATING` | Condition. **req** |

**TAP_CHANGER — Transformer Tap Changer** *(none · none)* · subtype: `1 standard` · the control/settings record for a transformer's OLTC (live tap step stays on the transformer)
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `tap_changer_type` | text | `TAP_CHANGER_TYPE` | OLTC / DETC. **req** |
| `control_mode` | text | `TAP_CONTROL_MODE` | Auto (AVR) / manual / off. **req** |
| `target_voltage_kv` | numeric | — | AVR set-point. |
| `bandwidth_pct` | numeric | — | Deadband. |
| `time_delay_s` | numeric | — | Delay before tapping. |
| `ldc_r_ohm` / `ldc_x_ohm` | numeric | — | Line-drop compensation R / X. |
| `operation_count` | integer | — | Lifetime tap operations. |
| `condition_rating` | text | `CONDITION_RATING` | Condition. **req** |

### MV network

**MV_CONDUCTOR — MV Conductor** *(line, geometry-optional · edge, role=passthrough, tier 300, network=MV)* · subtypes: `1 overhead_mv_conductor`, `2 underground_mv_conductor`
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `voltage_class` | text | `VOLTAGE_CLASS` | Nominal voltage. **req** |
| `conductor_spec` | text | `MV_LINE_SPEC` | Material-backed size (carries R/X/ampacity). |
| `phase` | text | `PHASE` | Phases carried. |
| `condition_rating` | text | `CONDITION_RATING` | Condition. **req** |

**MV_BUSBAR — MV Busbar** *(point, geometry-optional / diagram line · junction, role=node, tier 300, network=MV)* · no subtypes · an equipotential node (T1 incoming, T2 fan-out)
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `voltage_class` | text | `VOLTAGE_CLASS` | Voltage. |
| `arc_electrode_configuration` / `arc_conductor_gap_mm` / `arc_working_distance_mm` / `arc_system_grounded` | text / numeric / numeric / boolean | `ARC_ELECTRODE_CONFIG` | Optional arc-flash geometry (see `POWER_XFMR`). |
| `condition_rating` | text | `CONDITION_RATING` | Condition. |

**Series-switch family** — all are *(point · junction, T1 line / T2 load)* and geometry-optional **except `SERVICE_CUTOUT`** (its geometry is mandatory); all carry **`device_state`** (`DEVICE_STATE`, **req** — `OPEN` = trace barrier) and **`condition_rating`** (**req**). Extra fields below:

| Class | Name *(role · boundary)* | Subtypes | Extra fields |
|---|---|---|---|
| **MV_BREAKER** | MV Breaker *(controller · protective)* | `1 vacuum_breaker`, `2 sf6_breaker` | `normal_state` (`NORMAL_STATE`) · `switch_function` (`SWITCH_FUNCTION`, for coupler/incomer use) |
| **MV_ISOLATOR** | MV Isolator *(controller · switching)* | `1 single_break`, `2 double_break` | `normal_state` (`NORMAL_STATE`) |
| **MV_SECTIONALIZER** | MV Sectionalizer *(no functional role, no boundary role)* | `1 hydraulic`, `2 electronic` | `normal_state` (`NORMAL_STATE`) · `count_to_lockout` (int, fault counts before lockout) |
| **MV_RECLOSER** | MV Recloser *(controller · protective)* | `1 standard` | `reclose_attempts` (int) · `fast_curve_ref` / `slow_curve_ref` (curve code) · `reclose_interval_s` · `reset_time_s` · `normal_state` |
| **MV_SWITCH** | MV Switch *(controller · switching)* | `1 standard` | `is_fused` (bool) · `fuse_rating_a` (num) · `curve_ref` (code) · `normal_state` — *RMU ways are MV_SWITCH instances; `is_fused`=fused tee way* |
| **MV_FUSE** | MV Fuse *(controller · protective)* | `1 expulsion_fuse`, `2 current_limiting_fuse` | `fuse_rating_a` (num) · `curve_ref` (curve code) · `fuse_speed` (`FUSE_SPEED`) |
| **LV_SWITCH** | LV Switch *(controller · switching, tier 500)* | `1 standard` | `is_fused` · `fuse_rating_a` · `curve_ref` · `ownership` |
| **SERVICE_CUTOUT** | Service Cutout *(controller · protective, tier 500; geometry **required**)* | `1 fuse_carrier`, `2 switch_fuse` | `ownership` |

**MV_ARRESTER — MV Surge Arrester** *(point, geometry-optional · junction shunt, network=MV)* · subtypes: `1 gapless_mov`, `2 gapped_sic` · (T1 line, T2 ground; never a barrier)
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `condition_rating` | text | `CONDITION_RATING` | Condition. **req** |

**MV_EARTH_SWITCH — MV Earth Switch** *(point, geometry-optional · none, role=controller, tier 300, network=MV)* · subtypes: `1 manual`, `2 fault_making` · grounds a de-energised section; **deliberately not a sectioning boundary**
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `device_state` | text | `DEVICE_STATE` | Open/closed. **req** |
| `condition_rating` | text | `CONDITION_RATING` | Condition. **req** |

**RMU — Ring Main Unit** *(point · container, network=MV)* · subtype: `1 standard` · a container whose internal bus is an `MV_BUSBAR` and whose ways are `MV_SWITCH` instances
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `rmu_config` | text | `RMU_CONFIG` | Assembly config driving the fan-out. **req** |

**MV_SWITCHGEAR_PANEL — MV Switchgear Panel** *(none · container, tier 300, network=MV)* · subtype: `1 standard` · one cubicle of indoor switchgear
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `panel_role` | text | `BAY_ROLE` | Incomer / feeder / coupler… **req** |
| `voltage_class` | text | `VOLTAGE_CLASS` | Voltage. **req** |
| `panel_ref` | text | — | Cubicle tag. |
| `arc_electrode_configuration` / `arc_conductor_gap_mm` / `arc_working_distance_mm` / `arc_system_grounded` | text / numeric / numeric / boolean | `ARC_ELECTRODE_CONFIG` | Optional arc-flash geometry (see `POWER_XFMR`). |
| `condition_rating` | text | `CONDITION_RATING` | Condition. |

**MV_SWITCHBOARD — MV Switchboard** *(none · container, network=MV)* · subtypes: `1 metal_clad`, `2 gas_insulated` · a geometry-none container grouping panels (hosts a `BOARD_SCHEMATIC`)
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `voltage_class` | text | `VOLTAGE_CLASS` | Board voltage. **req** |
| `board_ref` | text | — | Board asset tag. |
| `room` | text | — | Switch-room name. |
| `condition_rating` | text | `CONDITION_RATING` | Condition. |

### LV network

**LV_CONDUCTOR — LV Conductor** *(line · edge, role=passthrough, tier 500, network=LV)* · subtypes: `1 overhead_lv_conductor`, `2 underground_lv_conductor`
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `phase` | text | `PHASE` | Phases carried. **req** |
| `conductor_spec` | text | `LV_CABLE_SPEC` | Material-backed cable size. **req** |
| `lv_role` | text | `LV_ROLE` | Main backbone vs service drop. **req** |
| `ownership` | text | `OWNERSHIP` | Utility / private. |

**LV_LINK_BOX — LV Link Box** *(point · junction, boundary=switching, network=LV)* · subtypes: `1 feeder_pillar`, `2 link_box` · T1 incoming, T2–T4 switchable ways
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `device_state` | text | `DEVICE_STATE` | Open/closed per way. **req** |
| `condition_rating` | text | `CONDITION_RATING` | Condition. **req** |
| `ownership` | text | `OWNERSHIP` | Utility / private. |

**LV_BUSBAR — LV Busbar** *(point, geometry-optional / diagram line · junction, role=node, tier 500, network=LV)* · subtypes: `1 lv_distribution_board`, `2 busbar_riser`
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `phase` | text | `PHASE` | Phase config. **req** |
| `ownership` | text | `OWNERSHIP` | Utility / private. |

**LVAC_BOARD — Station LVAC Board** *(point, geometry-optional · junction)* · subtype: `1 standard` · the substation's LV AC distribution board
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `phase` | text | `PHASE` | 1-ph / 3-ph. **req** |
| `board_ref` | text | — | Asset tag. |
| `incoming_rating_a` | numeric | — | Incomer rating. |
| `num_ways` | numeric | — | Outgoing way count. |
| `condition_rating` | text | `CONDITION_RATING` | Condition. **req** |

*(`LV_PILLAR` and `LV_FUSE` are detailed below under "DER & EV / misc"; `SERVICE_CUTOUT` and `LV_SWITCH` are in the series-switch table above.)*

### Delivery & metering

**SERVICE_POINT — Service Point** *(point · junction, role=consumer, tier 600, network=LV)* · subtypes: `1 residential`, `2 commercial`, `3 industrial` · the point of supply (the meter is a separate `METER`, linked by an "owns" relationship)
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `customers_served` | integer | — | End customers (NULL reads as 1; SAIDI/SAIFI denominator). |
| `demand_kw` | numeric | — | Real demand P. |
| `power_factor` | numeric | — | Power factor. |
| `load_priority` | text | `LOAD_PRIORITY` | Restoration tier. Optional; unset reads as `P4` (the create form defaults to `P4`). |
| `is_critical` | boolean | — | **Legacy** critical-facility flag, superseded by `load_priority` (critical = `P1`/`P2`). The column remains for back-compat but is **hidden from the create/edit forms and the API rejects it** (`field "is_critical" is not editable in this context`) — set `load_priority` instead. |
| `ownership` | text | `OWNERSHIP` | Utility / private. |

**METER — Meter** *(point · junction, role=consumer, tier 600, network=LV)* · subtypes: `1 whole_current`, `2 ct_metered`
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `meter_serial` | text | — | Device identity / MDM key. **req** |
| `account_ref` | text | — | Billing link. |
| `phase` | text | `PHASE` | Phase. **req** |
| `ct_ratio` | text | `CT_RATIO` | CT ratio — **required when subtype is `2 ct_metered`** (the API says `field "ct_ratio" is required for subtype "ct_metered"`), optional for `whole_current`. |
| `demand_kw` | numeric | — | Real demand. |
| `power_factor` | numeric | — | Power factor. |
| `customers_served` | integer | — | End customers. |
| `load_priority` | text | `LOAD_PRIORITY` | Restoration tier. |
| `meter_role` | text | `METER_ROLE` | Primary (counted) / check (audit). |
| `ownership` | text | `OWNERSHIP` | Utility / private. |

**METER_CHAMBER — Meter Chamber** *(point · container)* · subtypes: `1 cabinet`, `2 room` · holds meters
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `chamber_ref` | text | — | Asset tag. |
| `condition_rating` | text | `CONDITION_RATING` | Condition. **req** |

**STREET_LIGHT — Street Light** *(point · junction, tier null, network=LV)* · subtypes: `1 column_mounted`, `2 wall_mounted` · an unmetered LV leaf
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `phase` | text | `PHASE` | Phase. **req** |
| `luminaire_type` | text | `LUMINAIRE_TYPE` | LED / SON / SOX / MH. **req** |
| `condition_rating` | text | `CONDITION_RATING` | Condition. **req** |
| `ownership` | text | `OWNERSHIP` | Utility / private. |

**MV_METERING_POINT — MV Metering Point** *(point, geometry-optional · junction, role=consumer, tier 300)* · subtypes: `1 industrial`, `2 commercial_complex`, `3 bulk_supply` · an MV-connected customer supply
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `supply_voltage` | text | `MV_SUPPLY_VOLTAGE` | 11 kV / 33 kV. **req** |
| `contracted_demand_kva` | numeric | — | Contracted demand. **req** |
| `tariff_ref` | text | — | CIS/MDM tariff key. |
| `account_ref` | text | — | Billing key. |

**MV_METER — MV Meter** *(point, geometry-optional · none, role=consumer, tier 300)* · subtypes: `1 main_meter`, `2 check_meter` · off-path; reads CT/VT secondaries via relationships
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `meter_serial` | text | — | Device identity. **req** |
| `metering_class` | text | `METERING_CLASS` | Accuracy class. **req** |
| `demand_kva` | numeric | — | Read max demand. |
| `account_ref` | text | — | Billing key. |

**MV_METERING_CUBICLE — MV Metering Cubicle** *(point · container)* · subtypes: `1 indoor`, `2 outdoor_kiosk`
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `cubicle_ref` | text | — | Asset tag. |
| `condition_rating` | text | `CONDITION_RATING` | Condition. **req** |

**MV_CT / MV_VT** — the MV instrument transformers — are in the *Instrument transformers* section below.

---

### Substation & grouping

**SUBSTATION — Substation** *(polygon · none, diagram container — not a graph container)* · subtypes: `1 distribution_substation`, `2 transmission_substation`
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `enclosure_type` | text | `YARD_ENCLOSURE` | Site boundary — fenced / walled / open. **req** |
| `primary_voltage` | text | `VOLTAGE_CLASS` | Incoming (HV) voltage. **req** |
| `secondary_voltage` | text | `VOLTAGE_CLASS` | Stepped-down (LV/MV) voltage. |
| `condition_rating` | text | `CONDITION_RATING` | Site condition. **req** |

**BAY — Bay** *(none · container)* · subtypes: `1 standard`
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `bay_role` | text | `BAY_ROLE` | Which circuit the bay is (incomer/feeder/coupler…). **req** |
| `voltage_class` | text | `VOLTAGE_CLASS` | Bay voltage level. **req** |
| `bay_ref` | text | — | Asset/panel tag label. |
| `condition_rating` | text | `CONDITION_RATING` | Condition. |

**SUBSTATION_PANEL — Substation Panel** *(none · container)* · subtypes: `1 standard` · (panel↔bay is a many-to-many `panel_bay` relationship, not containment)
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `panel_type` | text | `PANEL_TYPE` | Protection / control / metering / RTU… **req** |
| `panel_ref` | text | — | Panel asset tag. |
| `control_mode` | text | `CONTROL_MODE` | Local / remote posture. |
| `manufacturer` / `model` | text | — | Nameplate maker / model. |
| `voltage_class` | text | `VOLTAGE_CLASS` | Associated voltage. |
| `condition_rating` | text | `CONDITION_RATING` | Condition. |

**CONTROL_BUILDING — Control Building** *(polygon, optional · container)* · subtypes: `1 standard`
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `building_ref` | text | — | Building asset tag. |
| `num_floors` | integer | — | Storey count. |
| `construction_type` | text | `BUILDING_CONSTRUCTION` | Brick / concrete / prefab… |
| `floor_area_sqm` | numeric | — | Floor area. |
| `condition_rating` | text | `CONDITION_RATING` | Condition. |

**PACKAGED_SUBSTATION — Packaged Substation** *(point · container, network=MV)* · subtypes: `1 permanent`, `2 mobile` · a factory-built enclosure that fans out an MV switch + transformer + LV board from its config
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `package_config` | text | `PACKAGED_SUB_CONFIG` | Kit config (e.g. COMPACT) driving the fan-out. **req** |
| `primary_voltage` / `secondary_voltage` | text | `VOLTAGE_CLASS` | HV / LV nameplate. |
| `condition_rating` | text | `CONDITION_RATING` | Condition. |

### Protection & control (off the electrical path)

**PROTECTIVE_RELAY — Protective Relay** *(none · none, sensor, network=MV)* · subtypes: `1 standard` · the IDMT/instantaneous settings a breaker trips on
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `device_number` | text | `ANSI_DEVICE_NUMBER` | Primary ANSI function (50/51, 87…). **req** |
| `manufacturer` | text | — | Relay maker. |
| `model` | text | — | Relay model. |
| `condition_rating` | text | `CONDITION_RATING` | Condition (optional). |
| `curve_type` | text | `TCC_CURVE` | IDMT curve shape. |
| `pickup_current_a` | numeric | — | IDMT pickup current. |
| `time_dial` | numeric | — | Time-dial / TMS. |
| `inst_pickup_a` | numeric | — | Instantaneous (50) pickup. |
| `definite_time_s` | numeric | — | Definite-time delay. |

**CONTROL_IED — Control IED** *(none · none, sensor)* · subtypes: `1 standard`
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `ied_role` | text | `IED_ROLE` | Bay controller / RTU / gateway / HMI… **req** |
| `protocol` | text | `COMMS_PROTOCOL` | Comms protocol. |
| `manufacturer` | text | — | Maker. |

**ANNUNCIATOR — Annunciator** *(none · none, sensor)* · subtypes: `1 standard`
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `window_count` | integer | — | Alarm-facia window count. |
| `manufacturer` | text | — | Maker. |

**STATION_BATTERY — Station Battery** *(none · none)* · subtypes: `1 standard` · the substation DC supply
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `battery_technology` | text | `BATTERY_TECHNOLOGY` | Cell chemistry. **req** |
| `nominal_voltage_v` | numeric | — | DC bus nominal voltage. |
| `capacity_ah` | numeric | — | Amp-hour capacity. |
| `string_count` | integer | — | Number of strings. |

**BATTERY_CHARGER — Battery Charger** *(none · none)* · subtypes: `1 standard`
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `charger_type` | text | `CHARGER_TYPE` | SMPS / thyristor / ferroresonant. **req** |
| `rated_current_a` | numeric | — | Rated output current. |
| `output_voltage_v` | numeric | — | DC output voltage. |

### Instrument transformers

**MV_CT — MV Current Transformer** *(point, geometry-optional · junction, role=sensor, tier 300, network=MV)* · subtypes: `1 metering_ct`, `2 protection_ct`
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `ct_ratio` | text | `CT_RATIO` | Current ratio (e.g. 200/5). **req** |
| `condition_rating` | text | `CONDITION_RATING` | Condition. **req** |

**MV_VT — MV Voltage Transformer** *(point, geometry-optional · junction, role=sensor, tier 300, network=MV)* · subtypes: `1 inductive_vt`, `2 capacitive_vt`
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `vt_ratio` | text | `VT_RATIO` | Voltage ratio (e.g. 11 kV/110 V). **req** |
| `condition_rating` | text | `CONDITION_RATING` | Condition. **req** |

### Structural & support

**POLE — Pole** *(point · none)* · subtypes: `1 wood`, `2 concrete`, `3 steel`
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `pole_material` | text | `POLE_MATERIAL` | Wood / concrete / steel. **req** |
| `pole_use` | text | `POLE_USE` | Distribution / transmission / service. **req** |
| `height_class` | text | `POLE_HEIGHT_CLASS` | Class 1/2/3. **req** |
| `install_date` | date | — | Installation date. |
| `condition_rating` | text | `CONDITION_RATING` | Condition. **req** |
| `last_inspected_at` | date | — | Last inspection date. |

**TOWER — Tower** *(point · none)* · subtypes: `1 lattice`, `2 tubular`, `3 guyed`, `4 monopole`
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `tower_material` | text | `TOWER_MATERIAL` | Steel-lattice / tubular / concrete. **req** |
| `height_class` | text | `POLE_HEIGHT_CLASS` | Class 1/2/3. **req** |
| `install_date` | date | — | Installation date. |
| `condition_rating` | text | `CONDITION_RATING` | Condition. **req** |
| `last_inspected_at` | date | — | Last inspection date. |

**STRUCTURE — Structure** *(polygon · none)* · subtypes: `1 permanent`, `2 temporary` · a premises/building hosting a service point or meter
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `use_type` | text | `STRUCTURE_USE` | Premises classification. **req** |
| `storeys` | integer | — | Storey count. |
| `has_basement` | boolean | — | Basement flag. |

### Cable management (civil)

**CABLE_TRENCH — Cable trench** *(line · none)* · no subtypes (its **req** fields are `NOT NULL` in the database; no subtype rule exists to name them in the API error)
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `surface_type` | text | `TRENCH_SURFACE` | Road / footway / verge / unmade. **req** |
| `depth_mm` / `width_mm` | integer | — | Trench depth / width. |
| `condition_rating` | text | `CONDITION_RATING` | Condition. **req** |
| `install_date` / `last_inspected_at` | date | — | Install / last inspection. |

**CABLE_DUCT — Cable duct** *(line · none)* · no subtypes · carries conductors (a `cable_routing` relationship)
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `duct_material` | text | `DUCT_MATERIAL` | PVC / HDPE / steel / earthenware. **req** |
| `way_count` | integer | — | Number of bores (capacity). **req** |
| `bore_diameter_mm` | integer | — | Bore diameter. |
| `condition_rating` | text | `CONDITION_RATING` | Condition. **req** |
| `install_date` / `last_inspected_at` | date | — | Install / last inspection. |

**DUCT_WAY — Duct way** *(none · none)* · per-bore logical child of a `CABLE_DUCT`
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `way_number` | integer | — | Bore position 1..N. **req** |

**CABLE_CHAMBER — Cable chamber** *(polygon, optional · none)* · intended subtypes: `manhole`, `footway_box`, `joint_bay`, `jointing_pit` — **but `seed/cable_management_example.sql` inserts them with text codes into the integer `subtypes.code`, so as seeded they do not load**; the class then has no `subtype` column and no subtype rules (see Appendix C, item 20)
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `access_cover_type` | text | `CHAMBER_COVER` | Light / medium / heavy duty. **req** |
| `condition_rating` | text | `CONDITION_RATING` | Condition. **req** |
| `install_date` / `last_inspected_at` | date | — | Install / last inspection. |

**MV_CABLE_JOINT / LV_CABLE_JOINT — Cable joint** *(point · junction)* · intended subtypes: `straight`, `branch` (tee), `transition`, `termination` — **same seed defect as `CABLE_CHAMBER` (text codes into an integer column): as seeded they do not load**, so the `branch` T3 terminal is not created either (Appendix C, item 20) · a zero-impedance splice node; the `branch` subtype adds a 3rd terminal
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `insulation_type` | text | `INSULATION_TYPE` | XLPE / EPR / PILC / heat-shrink. **req** |
| `condition_rating` | text | `CONDITION_RATING` | Condition. **req** |
| `install_date` / `last_inspected_at` | date | — | Install / last inspection. |

### EV charging, LV pillar & network meter

**EV_CHARGING_STATION — EV Charging Station** *(point · junction)* · subtypes: `1 standard` · an LV load node for managed charging
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `demand_kw` | numeric | — | Load demand. |
| `power_factor` | numeric | — | Power factor. |
| `connection_voltage_v` | numeric | — | Supply voltage. |
| `connected_load_kw` | numeric | — | Total connected charger load. |
| `diversity_factor` | numeric | — | Diversity factor (simultaneity). |
| `managed_demand_cap_kw` | numeric | — | Smart-charging cap on draw. |
| `operator` | text | — | Charge-point operator. |
| `access` | text | `EV_ACCESS` | Who may use it: public / depot / residential / workplace. |

**EV_CHARGER — EV Charger** *(none · none)* · subtypes: `1 standard` · a charger unit within a station
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `charger_type` | text | `EV_CHARGER_TYPE` | EVSE type. **req** |
| `rated_power_kw` | numeric | — | Rated power. |
| `num_connectors` | integer | — | Connector count. |
| `managed` | boolean | — | Smart-managed flag. |
| `manufacturer` | text | — | Maker. |

**EV_CONNECTOR — EV Connector** *(none · none)* · subtypes: `1 standard`
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `connector_standard` | text | `EV_CONNECTOR_STANDARD` | Plug standard (CCS/CHAdeMO…). **req** |
| `rated_power_kw` | numeric | — | Port power. |

**NETWORK_METER — Network Meter** *(point, geometry-optional · none; no functional role)* · subtypes: `1 substation_incomer`, `2 feeder_head`, `3 transformer_metering`, `4 bus_section`, `5 boundary_exchange`, `6 interchange_tie`, `7 generation_export` · an **off-path energy-accounting** meter (related to an element, never connected)
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `meter_serial` | text | — | Device identity. **req** |
| `metering_class` | text | `METERING_CLASS` | Accuracy class. **req** |
| `ct_ratio` | text | `CT_RATIO` | CT ratio. |
| `vt_ratio` | text | `VT_RATIO` | VT ratio. |
| `external_ref` | text | — | MDM/head-end join key (readings live externally). |

**LV_PILLAR — LV Feeder Pillar** *(point · container, tier 500, network=LV)* · subtypes: `1 standard` · holds a bus + fused ways
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `condition_rating` | text | `CONDITION_RATING` | Condition. |
| `ownership` | text | `OWNERSHIP` | Utility / private. |

**LV_FUSE — LV Fuse Way** *(none · junction, boundary=protective, tier 500)* · subtypes: `1 standard` · one fused outgoing way inside a pillar
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `device_state` | text | `DEVICE_STATE` | Open/closed (OPEN = trace barrier). **req** |
| `fuse_rating` | text | `FUSE_RATING` | Fuse size. **req** |
| `condition_rating` | text | `CONDITION_RATING` | Condition. **req** |

### Legacy

**DIST_SWITCH — Distribution Switch** *(point · junction, network=MV; legacy)* · subtypes: `1 manual_switch`, `2 automatic_recloser` · still present in the seeded model, superseded by `MV_SWITCH` / `MV_RECLOSER` (see `seed/migrate_dist_switch_to_mv_switch.sql`; do not use it for new data)
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `device_state` | text | `DEVICE_STATE` | Open/closed (OPEN = trace barrier). **req** |
| `condition_rating` | text | `CONDITION_RATING` | Condition. **req** |

---

## Part 4 — Which class do I use?

Every row below is a decision the seeded model already makes; if a thing you want to record is not here, it is almost certainly a *field* on one of these classes, not a new class.

| I am recording… | Use | Notes |
|---|---|---|
| A network infeed / grid supply point | `DIST_XFMR` or `POWER_XFMR` **with** `source_r_ohm`/`source_x_ohm` | No `GRID_SUPPLY_POINT` class. `trace_tier` 0 is the `GENERATOR` rank. |
| A diesel / PV / wind / hydro / battery / CHP source | `GENERATOR` (subtype by technology) | MV-connected battery storage with its own PCS is `MV_BESS`. |
| A bus coupler | `MV_BREAKER` with `switch_function=BUS_COUPLER` | No `BUS_COUPLER` class. |
| A ring-main unit way | `MV_SWITCH` (inside an `RMU`; `is_fused` = fused tee way) | The RMU's internal bus is an `MV_BUSBAR`. |
| A pole-top switch, recloser, sectionalizer or fuse | `MV_SWITCH` / `MV_RECLOSER` / `MV_SECTIONALIZER` / `MV_FUSE` | `DIST_SWITCH` is legacy. |
| A neutral earthing resistor | `neutral_resistance_ohm` on `EARTHING_XFMR` | No `NER` class. |
| A cable in a duct or trench | the cable is an `MV_CONDUCTOR` / `LV_CONDUCTOR`; the civil asset is `CABLE_DUCT` / `CABLE_TRENCH` | Link them with a `cable_routing` relationship; no `CABLE_ROUTE` class. |
| A cable splice, tee or termination | `MV_CABLE_JOINT` / `LV_CABLE_JOINT` | Zero-impedance node. |
| A customer's point of supply | `SERVICE_POINT` + a separate `METER` (`SERVICE_POINT owns METER`) | Whole-current vs CT-metered is the `METER` subtype. |
| An MV customer | `MV_METERING_POINT` + `MV_METER` (+ `MV_CT`/`MV_VT` in an `MV_METERING_CUBICLE`) | |
| Energy-accounting at a substation incomer, feeder head, boundary or interchange | `NETWORK_METER` (off-path; related to the element it meters) | Never connected electrically. |
| A pole / lattice tower / building | `POLE` / `TOWER` / `STRUCTURE` | Related, not connected (`spatial_support`, `containment`). |
| A substation site, bay, panel, control building | `SUBSTATION` (polygon) · `BAY` · `SUBSTATION_PANEL` · `CONTROL_BUILDING` | Panel↔bay is a many-to-many `panel_bay` relationship. |
| Indoor MV switchgear | `MV_SWITCHBOARD` → `MV_SWITCHGEAR_PANEL` → the devices | Board-level schematic is `BOARD_SCHEMATIC`. |
| A kiosk / pad-mount / pillar with fused ways | `LV_PILLAR` (container) with `LV_FUSE` ways | `LV_LINK_BOX` is a flat 4-terminal junction (T1–T4; opening it halts the whole box) — despite its `feeder_pillar` subtype it is not the pillar container; `LV_PILLAR` + `LV_FUSE` ways give per-way isolation. |
| A factory-built compact substation | `PACKAGED_SUBSTATION` (`package_config`) | Fans out an MV switch + transformer + LV board. |
| A protection relay | `PROTECTIVE_RELAY` (off-path) | Linked by `protection_trip` (to breaker / recloser / switch) and `protection_sensing` (to CT/VT). |
| A street light | `STREET_LIGHT` | Unmetered LV leaf. |
| A station battery / charger / IED / annunciator | `STATION_BATTERY` / `BATTERY_CHARGER` / `CONTROL_IED` / `ANNUNCIATOR` | Off the electrical path. |

---

## Part 5 — Connectivity at a glance

A class only connects electrically if it has **terminals**. Two features connect when a **network rule** allows `(from class, from terminal) → (to class, to terminal)`; the seeded model holds 96 rules (2 marked invalid / retired). Terminal roles are `primary`, `secondary`, `tertiary`, `ground`, `other`; "ph" marks a phase-capable terminal (current passes through).

| Class | Terminals (code · role · `ph` = phase-capable) |
|---|---|
| `AUX_TRANSFORMER` | T1 primary ph · T2 secondary ph |
| `DIST_SWITCH` | T1 primary ph · T2 secondary ph |
| `DIST_XFMR` | T1 primary ph · T2 secondary ph · T3 ground |
| `EARTHING_XFMR` | LINE primary ph · N ground |
| `EV_CHARGING_STATION` | T1 primary ph |
| `GENERATOR` | T1 primary ph |
| `LVAC_BOARD` | T1 primary ph · T2 other ph |
| `LV_BUSBAR` | T1 primary ph · T2 other ph |
| `LV_CABLE_JOINT` | T1 primary ph · T2 secondary ph |
| `LV_FUSE` | T1 primary ph · T2 secondary ph |
| `LV_LINK_BOX` | T1 primary ph · T2 other ph · T3 other ph · T4 other ph |
| `LV_SWITCH` | T1 primary ph · T2 secondary ph |
| `METER` | T1 primary ph · T2 secondary ph |
| `MV_ARRESTER` | T1 primary ph · T2 ground |
| `MV_BESS` | T1 primary ph |
| `MV_BREAKER` | T1 primary ph · T2 secondary ph |
| `MV_BUSBAR` | T1 primary ph · T2 other ph |
| `MV_CABLE_JOINT` | T1 primary ph · T2 secondary ph |
| `MV_CAP_BANK` | T1 primary ph · T2 ground |
| `MV_CT` | T1 primary ph |
| `MV_FUSE` | T1 primary ph · T2 secondary ph |
| `MV_ISOLATOR` | T1 primary ph · T2 secondary ph |
| `MV_METERING_POINT` | T1 primary ph |
| `MV_RECLOSER` | T1 primary ph · T2 secondary ph |
| `MV_SECTIONALIZER` | T1 primary ph · T2 secondary ph |
| `MV_SWITCH` | T1 primary ph · T2 secondary ph |
| `MV_VT` | T1 primary ph · T2 ground |
| `POWER_XFMR` | [subtype 1] HV primary ph · [subtype 1] LV secondary ph · [subtype 1] N ground · [subtype 2] HV primary ph · [subtype 2] LV secondary ph · [subtype 2] N ground · [subtype 2] TV tertiary ph |
| `SERVICE_CUTOUT` | T1 primary ph · T2 secondary ph |
| `SERVICE_POINT` | T1 primary ph |
| `STREET_LIGHT` | T1 primary ph |

**No terminals (never part of a trace):** `ANNUNCIATOR`, `BATTERY_CHARGER`, `BAY`, `CABLE_CHAMBER`, `CABLE_DUCT`, `CABLE_TRENCH`, `CONTROL_BUILDING`, `CONTROL_IED`, `DUCT_WAY`, `EV_CHARGER`, `EV_CONNECTOR`, `LV_CONDUCTOR`, `LV_PILLAR`, `METER_CHAMBER`, `MV_CONDUCTOR`, `MV_EARTH_SWITCH`, `MV_METER`, `MV_METERING_CUBICLE`, `MV_SWITCHBOARD`, `MV_SWITCHGEAR_PANEL`, `MV_VREG`, `NETWORK_METER`, `PACKAGED_SUBSTATION`, `POLE`, `PROTECTIVE_RELAY`, `RMU`, `STATION_BATTERY`, `STRUCTURE`, `SUBSTATION`, `SUBSTATION_PANEL`, `TAP_CHANGER`, `TOWER`. Conductors (`MV_CONDUCTOR`, `LV_CONDUCTOR`) are *edges*: they have no terminals of their own and the rules connect them to a device's terminal. **`MV_VREG` currently has no terminals or network rules in the seeded model** (Appendix C, item 21), so it cannot yet be wired into a feeder.

---

## Part 6 — Worked examples & common mistakes

**Example 1 — a distribution transformer** (the `TX-101` of `seed/test_network_example.sql`). `POST /features/DIST_XFMR`:

```json
{ "subtype": 2, "name": "TX-101",
  "geometry": { "type": "Point", "coordinates": [-0.1960, 5.6000] },
  "attributes": { "primary_voltage": "11KV", "secondary_voltage": 400, "condition_rating": "GOOD",
                 "rated_kva": 500, "impedance_pct": 4.5, "x_r_ratio": 6, "z0_pct": 4.0 } }
```
Leave out `primary_voltage` and the API answers `field "primary_voltage" is required for subtype "pad_mounted"`; send `"primary_voltage": "11 kV"` and you get `field "primary_voltage" value "11 kV" is not a valid VOLTAGE_CLASS value` — use the code `11KV`. Validation order is: subtype → geometry → required → unknown/computed/hidden fields → each value's type and domain.

**Example 2 — a CT-metered meter.** `METER` subtype `2` makes `ct_ratio` mandatory: `{"subtype": 2, "attributes": {"meter_serial": "M-100", "phase": "L1L2L3N", "ct_ratio": "200_5"}}`. With subtype `1` the same body without `ct_ratio` is fine.

**Example 3 — a lifecycle change.** Updating `operational_state` from `PLANNED` straight to `ISOLATED` is refused: `field "operational_state" cannot change from "PLANNED" to "ISOLATED" -- not a permitted OPERATIONAL_STATE transition`. Go `PLANNED → IN_SERVICE → ISOLATED`.

**Example 4 — a CSV import.** Imports are profile-driven: a modeller (`metadata:write`) authors an *import profile* mapping source columns to field names (direct / scale / constant / domain-map), a subtype and a geometry mode (`lonlat`, `wkt`, `native`, `none`); a data-entry user (`features:write`) then runs it. Blank cells become *unset*; booleans accept `true/t/1/yes/y` and `false/f/0/no/n`; numbers are parsed then checked against whole-number/domain rules by the same validator as the form. Unmapped source columns are ignored, and the importer writes **metadata fields and geometry only** — a `name` column does not set the feature's `name` label.

**Common mistakes**

| Mistake | What happens | Fix |
|---|---|---|
| Typing the label (`Dyn11`, `11 kV`, `Good`) | `… is not a valid <DOMAIN> value` | Use the code (`DYN11`, `11KV`, `GOOD`). |
| Omitting `subtype` on a class that has subtypes, or using a code it lacks | `subtype is required for feature class "POLE"` / `subtype 9 is not valid for feature class "POLE"` | Check the class's subtype list (the integer code, not the name). |
| Sending a geometry to a geometry-`none` class | `feature class "BAY" has no geometry -- do not send one` | Omit `geometry`. |
| Wrong geometry type (Point to a line class) | `geometry type … does not match feature class …` | Match the class's geometry. |
| Writing `circuit_ref` | `field "circuit_ref" is computed and cannot be set directly` | Set `circuit_id` or run assign-by-trace. |
| Treating a missing `NOT NULL` on a subtype-less class as a form error | A database not-null violation instead of a friendly 400 | Fill the **req** fields of `CABLE_TRENCH`, `CABLE_DUCT`, `DUCT_WAY`, `EARTHING_XFMR`. |

---

## Appendix — Every feature class (63)

The complete class inventory, for cross-reference. *(Field detail for every class is in Part 3; Appendix B shows which classes carry each cross-cutting axis field.)*

**Source / DER:** `GENERATOR`, `MV_BESS`, `MV_CAP_BANK`, `MV_VREG`, `EV_CHARGING_STATION`, `EV_CHARGER`, `EV_CONNECTOR` *(infeed = a slack `DIST_XFMR`/`POWER_XFMR`)*
**MV network:** `MV_CONDUCTOR`, `MV_BUSBAR`, `MV_BREAKER`, `MV_ISOLATOR`, `MV_SECTIONALIZER`, `MV_RECLOSER`, `MV_SWITCH`, `MV_FUSE`, `MV_ARRESTER`, `MV_EARTH_SWITCH`, `MV_SWITCHBOARD`, `MV_SWITCHGEAR_PANEL`, `RMU`, `DIST_SWITCH`†
**Transformers:** `POWER_XFMR`, `DIST_XFMR`, `AUX_TRANSFORMER`, `LVAC_BOARD`, `EARTHING_XFMR`, `TAP_CHANGER`
**LV network:** `LV_CONDUCTOR`, `LV_SWITCH`, `LV_LINK_BOX`, `LV_BUSBAR`, `LV_PILLAR`, `LV_FUSE`, `SERVICE_CUTOUT`, `STREET_LIGHT`
**Metering & service:** `SERVICE_POINT`, `METER`, `METER_CHAMBER`, `MV_METERING_POINT`, `MV_METER`, `MV_METERING_CUBICLE`, `MV_CT`, `MV_VT`, `NETWORK_METER`
**Substation:** `SUBSTATION`, `BAY`, `SUBSTATION_PANEL`, `CONTROL_BUILDING`, `PACKAGED_SUBSTATION`
**P&C / DC:** `PROTECTIVE_RELAY`, `CONTROL_IED`, `ANNUNCIATOR`, `STATION_BATTERY`, `BATTERY_CHARGER`
**Structural & civil:** `POLE`, `TOWER`, `STRUCTURE`, `CABLE_TRENCH`, `CABLE_DUCT`, `CABLE_CHAMBER`, `DUCT_WAY`, `MV_CABLE_JOINT`, `LV_CABLE_JOINT`

† `DIST_SWITCH` is legacy, superseded by `MV_SWITCH`/`MV_RECLOSER`.

**Not feature classes** (common confusions): there is **no** `GRID_SUPPLY_POINT` class (a network infeed is a slack `DIST_XFMR`/`POWER_XFMR` carrying `source_r_ohm`); **no** `NER` class (neutral earthing is the `neutral_resistance_ohm` field on `EARTHING_XFMR`); **no** `BUS_COUPLER` class (a bus coupler is an `MV_BREAKER` with `switch_function=BUS_COUPLER`); and **no** `CABLE_ROUTE` class (cable↔duct is a `cable_routing` relationship). `SUBSTATION_YARD` is the old name of `SUBSTATION`.

---

## Appendix B — Axis coverage (which class carries which cross-cutting field)

Generated from the seeded metadata after the full set of field seeds has been applied (re-running the axis seeds after every class exists is what gives this coverage). `37` of 63 classes carry at least one axis field.

| Class | `operational_state` | `circuit_id` | `circuit_ref` | `ownership` |
|---|:-:|:-:|:-:|:-:|
| `BAY` | ● |  |  |  |
| `DIST_SWITCH` | ● | ● |  |  |
| `DIST_XFMR` | ● | ● |  | ● |
| `LV_BUSBAR` | ● | ● |  | ● |
| `LV_CONDUCTOR` | ● | ● | ● | ● |
| `LV_FUSE` |  |  |  | ● |
| `LV_LINK_BOX` | ● | ● |  | ● |
| `LV_PILLAR` |  | ● |  | ● |
| `LV_SWITCH` | ● | ● |  | ● |
| `METER` | ● | ● |  | ● |
| `METER_CHAMBER` | ● |  |  | ● |
| `MV_ARRESTER` | ● | ● |  |  |
| `MV_BESS` | ● | ● |  |  |
| `MV_BREAKER` | ● | ● |  |  |
| `MV_BUSBAR` | ● | ● |  |  |
| `MV_CAP_BANK` | ● | ● |  |  |
| `MV_CONDUCTOR` | ● | ● | ● |  |
| `MV_CT` | ● | ● |  |  |
| `MV_EARTH_SWITCH` |  | ● |  |  |
| `MV_FUSE` | ● | ● |  |  |
| `MV_ISOLATOR` | ● | ● |  |  |
| `MV_RECLOSER` | ● | ● |  |  |
| `MV_SECTIONALIZER` | ● | ● |  |  |
| `MV_SWITCH` | ● | ● |  |  |
| `MV_SWITCHBOARD` |  | ● |  |  |
| `MV_SWITCHGEAR_PANEL` |  | ● |  |  |
| `MV_VREG` | ● | ● |  |  |
| `MV_VT` | ● | ● |  |  |
| `PACKAGED_SUBSTATION` |  | ● |  |  |
| `POLE` | ● |  |  |  |
| `POWER_XFMR` |  |  |  | ● |
| `PROTECTIVE_RELAY` | ● | ● |  |  |
| `RMU` |  | ● |  |  |
| `SERVICE_CUTOUT` | ● | ● |  | ● |
| `SERVICE_POINT` | ● | ● |  | ● |
| `STREET_LIGHT` | ● | ● |  | ● |
| `SUBSTATION` | ● |  |  |  |

---

## Appendix C — Corrections made to this document

Found by loading migrations 0001–0134 and the full `seed-all` seed set into a scratch PostgreSQL (PostGIS-dependent tables stubbed; the `metadata.*` model is unaffected) and diffing the result against this file, plus reading `api/internal/compiler/ddl.go`, `api/internal/crud/validate.go` and `api/internal/importer/mapping.go`. Each item is already fixed above unless marked *seed defect* (the document now describes the defect instead).

1. **`subtype` is `NOT NULL`, and conditional** — was "integer (nullable)". `api/internal/compiler/ddl.go:159` emits `subtype integer NOT NULL CHECK (…)` only when the class has subtypes.
2. **`geom` for geometry-`none` classes** — was "null"; there is no `geom` column at all (`ddl.go:170`, guarded by `GeometryType != "none"`); `NOT NULL` otherwise unless geometry-optional (`ddl.go:180`).
3. **"Required ≠ NOT NULL … almost always nullable" was wrong** — 102 of 372 seeded fields are `nullable=false` (`NOT NULL`); see the rewritten convention. Source: `metadata.fields.nullable`, e.g. `seed/pole_example.sql` (`pole_material … false`).
4. **`operational_state`** — attributed to mig 0065 (that migration creates `domain_transitions`); the field comes from `seed/operational_state_example.sql`, is on 29 classes (not only "network classes"), and the domain has four values including `PLANNED` (the old text listed three). Only `ISOLATED`/`RETIRED` are barriers, and only in the up/downstream traces (`seed/operational_state_barriers.sql`).
5. **`circuit_id` / `circuit_ref`** — mig 0062 only creates `gis.circuits`; `circuit_id` (uuid) is on 31 classes (`seed/circuit_membership_example.sql`), `circuit_ref` only on the two conductor classes and is computed/hidden (`seed/circuit_ref_example.sql`, `validate.go:234`).
6. **`ownership`** — attributed to mig 0129 (that is `boundary_role`); it comes from `seed/ownership_axis.sql`, 13 classes.
7. **`VOLTAGE_CLASS` was missing `400V`** (`seed/substation_yard_example.sql:54`).
8. **`VECTOR_GROUP` codes are upper-case** (`DYN11` … `YY0`, `seed/power_transformer_example.sql:86`); the old text gave the labels as if they were codes.
9. **EV domains** — `EV_ACCESS` is `public/depot/residential/workplace` (not "public/restricted"); `EV_CHARGER_TYPE` `ac_l2/dc_fast/dc_ultra`; `EV_CONNECTOR_STANDARD` `type2/ccs2/chademo/nacs/gbt` (`seed/ev_charging_example.sql`).
10. **Three domains were undocumented** — `DIAGRAM_TYPE`, `SNAP_TARGET_TYPE`, `TRACE_DIRECTION`.
11. **`SUBSTATION`** is `network_role = none` (a diagram container), not `container`.
12. **`NETWORK_METER`** has no functional role (was "consumer"); **`MV_SECTIONALIZER`** has none either (was "controller"); **`MV_EARTH_SWITCH`** *is* `controller` (was silent).
13. **Class metadata that was missing or wrong** — `boundary_role=transformer` on `DIST_XFMR`/`POWER_XFMR`, `source` on `GENERATOR`; `trace_tier` 500 on `LV_FUSE`/`LV_PILLAR` and 300 on `MV_CT`/`MV_VT`; `network=MV` on `MV_CT`/`MV_VT`/`PROTECTIVE_RELAY`; `geometry_optional` is true on 26 classes but the old text marked only `CABLE_CHAMBER` and `CONTROL_BUILDING` — the other 24 are now marked in their Part 3 headers.
14. **Undocumented fields added** — `DIST_XFMR.phase`; `arc_*` ×4 on `DIST_XFMR`, `POWER_XFMR`, `MV_BUSBAR`, `MV_SWITCHGEAR_PANEL`; `POWER_XFMR.source_r/x(0)_ohm`; `SERVICE_POINT.is_critical` (documented as a legacy field: hidden and API-rejected, superseded by `load_priority` — `seed/service_point_load_priority.sql:85-98`); `PROTECTIVE_RELAY.model` and `.condition_rating`; `MV_FUSE.fuse_rating_a/curve_ref/fuse_speed`; `MV_SECTIONALIZER.count_to_lockout`; `LV_PILLAR.ownership` etc. (Appendix B).
15. **Classes with no field table** — `MV_CAP_BANK`, `MV_VREG`, `MV_SWITCHBOARD` (its subtypes `1 metal_clad`, `2 gas_insulated`) and `DIST_SWITCH` (`1 manual_switch`, `2 automatic_recloser`) now have blocks.
16. **"All series switches are geometry-optional"** — `SERVICE_CUTOUT` is not.
17. **`METER.ct_ratio`** — is required for subtype `2 ct_metered` (a subtype-specific rule); likewise `MV_CAP_BANK.device_state` is required for `switched` only.
18. **Stale appendix note** ("field detail lands in the next revision") removed; the class count **63** was already correct.
19. **Subtype-less classes enforce "req" only through `NOT NULL`** — `validateRequired` returns immediately when the class has no subtype (`validate.go:260`); affects `CABLE_TRENCH`, `CABLE_DUCT`, `DUCT_WAY`, `EARTHING_XFMR`.
20. *Seed defect* — `seed/cable_management_example.sql:91` (`CABLE_CHAMBER`) and `seed/cable_joint_example.sql:104,154` (`MV_CABLE_JOINT`/`LV_CABLE_JOINT`) insert text subtype codes (`'manhole'`, `'branch'` …) into the integer `metadata.subtypes.code`; the statement fails (`column "code" is of type integer but expression is of type text`), so those classes get no subtypes, no required-field rules and no `branch` T3 terminal. The old manual described the intended subtypes as if they existed; it now says so.
21. *Seed defect* — `seed/mv_voltage_regulator_example.sql:119` inserts `MV_VREG` terminals with roles `source`/`load`, which violates the `terminals_role_check` constraint (mig 0070 allows `primary/secondary/tertiary/ground/other`); `MV_VREG` therefore has no terminals and no network rules.
