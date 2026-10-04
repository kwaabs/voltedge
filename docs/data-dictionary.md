# VoltEdge Data Dictionary

A reference for anyone **entering or importing data**: what every feature class is, and what each of its fields means — in a line or two, not an essay. Use it as the key while you populate the network.

**How to read it.**
- **Part 1 — System columns**: the columns *every* compiled `gis.<class>` table carries (id, name, status, geometry, audit, and the cross-cutting axes). Described once; not repeated per class.
- **Part 2 — Shared domains**: the coded value-lists (`PHASE`, `VOLTAGE_CLASS`, …) many fields draw from. Described once; the per-class tables just name the domain.
- **Part 3 — Feature classes**: one compact block per class — what it is, its subtypes, and a field table (name · type · domain · meaning).

**Two conventions that trip people up:**
- **"Required" ≠ "NOT NULL".** VoltEdge columns are almost always DB-nullable (so a field can be added to a compiled class without rewriting rows). Whether a field is *mandatory* is enforced by **subtype rules** at save time, shown as the asterisk on the form. So "required" below means "the create form will block you," not "the database rejects null."
- **Coded fields take a code, not free text.** A field with a domain (e.g. `phase`) only accepts that domain's value codes (`L1L2L3N`), never a typed label.

> ⚠️ This is generated from the seed metadata and the feature catalog. Where the live model and this doc disagree, the live model (the admin feature-class screens / `GET /feature-classes/{code}/schema`) is the truth — field packs get added over time.

---

## Part 1 — System columns (every `gis.<class>` table)

The compiler gives every feature class these base columns; you never author them as "fields," but they're on every row.

| Column | Type | Meaning |
|---|---|---|
| `id` | uuid | Primary key, server-generated. The stable identity used by connectivity, relationships, tags, circuits. |
| `name` | text (nullable) | Human label for the feature (e.g. "TX-101"). Optional. |
| `subtype` | integer (nullable) | Which **subtype** of the class this is (the integer code — see each class's subtypes). Drives which fields are required. |
| `status` | text | Publish state: `draft` (being authored) or `published` (live/signed-off). Orthogonal to deletion and to operational state. |
| `geom` | geometry | The map geometry (point / line / polygon per the class), SRID 4326 — **null** for a geometry-optional or geometry-`none` class (diagram-only / container). |
| `created_at` / `created_by` | timestamptz / uuid | Audit: when and by whom the row was created. |
| `updated_at` / `updated_by` | timestamptz / uuid | Audit: last edit. (Also what the circuit "stale" freshness check reads.) |
| `published_at` / `published_by` | timestamptz / uuid | Sticky "ever made public" marker (set the first time a draft is published; see the private-drafts overlay). |
| `deleted_at` | timestamptz (nullable) | Soft-delete marker; a non-null value hides the feature until purged. Every query filters `deleted_at IS NULL`. |

**Cross-cutting axis columns** — present on the classes that opt into each axis (network-participating classes mostly), added by their migrations/seeds:

| Column | On | Meaning |
|---|---|---|
| `territory_id` | spatial classes | The territory the feature falls in (auto-assigned from geometry); drives spatial access control. |
| `operational_state` | network classes (mig 0065) | The in-service lifecycle axis — e.g. `IN_SERVICE` / `ISOLATED` / `RETIRED` (domain `OPERATIONAL_STATE`); an `ISOLATED`/`RETIRED` device is a trace barrier. Distinct from `status` (publish) and `device_state` (switch position). |
| `circuit_id` / `circuit_ref` | conductors + network devices (mig 0062, Phase 37) | Feeder/subnetwork membership: the circuit's id and its denormalised name, stamped by assign-by-trace. See the [Feeders, Sections & Subnetworks](network-sectioning-guide.md) guide. |
| `ownership` | transformers + LV set (mig 0129) | `utility` (default/unset) or `private` — the asset-ownership flag; set at a transformer, propagated downstream. See the [LV & Metering](lv-and-metering-guide.md) guide, Ch 11. |

**Class-level metadata** (a property of the *class*, not a row column) — you see these on the admin feature-class screens, not in the attribute form:
- **`network_role`** — the connectivity-graph role: `edge` (a conductor), `junction` (a node with terminals), `container` (groups/holds, off the electrical path), `none` (off-graph).
- **`functional_role`** (mig 0091) — the electrical function: `source` / `consumer` / `passthrough` / `transformer` / `controller` / `node` / `sensor` / `ground`.
- **`boundary_role`** (mig 0129) — the network-sectioning function: `source` / `protective` / `switching` / `transformer`, or unset. See the sectioning guide.
- **`trace_tier`** (mig 0126) — vertical rank source→load (0 grid infeed · 200 power xfmr · 300 MV · 400 dist xfmr · 500 LV · 600 service), guarding directional traces.

---

## Part 2 — Shared domains (coded value-lists)

Common domains referenced across many classes. A field pointing at one of these only accepts its codes.

### Electrical / connectivity
- **`PHASE`** — `L1`, `L2`, `L3`, `N` (Neutral), `L1N`, `L2N`, `L3N` (single-phase + neutral), `L1L2L3N` (three-phase + neutral). Which phases a conductor/device carries.
- **`VOLTAGE_CLASS`** — coded nominal voltage: `11KV`, `33KV`, `66KV`, `132KV`, `161KV`, `330KV`. The class of voltage, not an exact number.
- **`DEVICE_STATE`** — `OPEN` / `CLOSED`. Live switch position of a switching device; `OPEN` stops a trace (barrier).
- **`OPERATIONAL_STATE`** — the in-service lifecycle (e.g. `IN_SERVICE` / `ISOLATED` / `RETIRED`); an isolated/retired element is a trace barrier.

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
- **`VECTOR_GROUP`** — winding/phase config: `Dyn11`, `Dyn1`, `YNd11`, `YNd1`, `YNyn0`, `Dd0`, `Yy0` (drives zero-sequence / SLG behaviour).
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
- **`ARC_ELECTRODE_CONFIG`** — `switchgear` / `mcc_panelboard` / `cable` / `open_air` (IEEE 1584 electrode geometry).
- **`EV_ACCESS`**, **`EV_CHARGER_TYPE`**, **`EV_CONNECTOR_STANDARD`** — EV public/restricted, EVSE type, plug standard (CCS/CHAdeMO…).

---

## Part 3 — Feature classes

> Each class block: **CODE — Name** *(geometry · network_role)* · subtypes · field table. System columns (Part 1) and the common `condition_rating` are not repeated in prose. *Fields marked **req** are mandatory via subtype rules.*

> **Where is the "grid supply point"?** There is **no** `GRID_SUPPLY_POINT` class. A network infeed is modelled as a **slack transformer** — a `DIST_XFMR` (or `POWER_XFMR`) carrying `source_r_ohm`/`source_x_ohm` (the Thévenin source behind it) — plus the first-class `GENERATOR` and `MV_BESS` sources. `trace_tier=0` marks the infeed rank.

### Source & DER

**GENERATOR — Generator** *(point · junction, role=source, tier 0)* · subtypes: `1 diesel`, `2 solar_pv`, `3 wind`, `4 hydro`, `5 battery`, `6 gas_chp`
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `rated_kw` | numeric | — | Nameplate real output. |
| `dispatch_kw` | numeric | — | Live actual output (authoritative even at 0). |
| `power_factor` | numeric | — | Operating pf (unset = unity). |
| `fault_contribution_ka` | numeric | — | Prospective 3-phase fault infeed. |
| `subtransient_reactance_pct` | numeric | — | Machine X″d %. |
| `voltage_setpoint_pu` | numeric | — | PV-bus regulated voltage (grid-forming). |
| `reactive_limit_kvar` | numeric | — | Symmetric ±Q bound on the PV bus. |

**MV_BESS — MV Battery Storage** *(point · junction, network=MV)* · subtypes: `1 standalone`, `2 hybrid`
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `rated_power_kw` | numeric | — | PCS charge/discharge rating. **req** |
| `storage_dispatch_kw` | numeric | — | Signed set-point (+discharge / −charge). |
| `energy_capacity_kwh` | numeric | — | Usable energy size. |
| `state_of_charge_pct` | numeric | — | Present charge 0–100. |
| `power_factor` | numeric | — | Operating pf. |
| `condition_rating` | text | `CONDITION_RATING` | Condition. **req** |

*(`MV_CAP_BANK`, `MV_VREG` also exist as MV shunt/regulator DER — same pattern.)*

### Transformers

**POWER_XFMR — Power Transformer** *(point · junction, role=transformer, tier 200)* · subtypes: `1 two_winding` (HV/LV/N terminals), `2 three_winding` (HV/LV/TV/N)
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
| `condition_rating` | text | `CONDITION_RATING` | Condition. **req** |

**DIST_XFMR — Distribution Transformer** *(point · junction, role=transformer, tier 400, network=MV)* · subtypes: `1 pole_mounted`, `2 pad_mounted`, `3 underground_vault` · *also the network infeed when it carries `source_*` fields (slack)*
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
| `condition_rating` | text | `CONDITION_RATING` | Condition. **req** |

**AUX_TRANSFORMER — Station Auxiliary Transformer** *(point · junction, tier 400)* · subtype: `1 standard` · steps MV→LV for station services
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `primary_voltage` | text | `VOLTAGE_CLASS` | MV in. **req** |
| `secondary_voltage` | numeric | — | LV out (volts). |
| `impedance_pct` | numeric | — | %Z. |
| `rated_kva` | numeric | — | Nameplate kVA. |
| `condition_rating` | text | `CONDITION_RATING` | Condition. **req** |

**EARTHING_XFMR — Earthing Transformer** *(point · junction, role=ground)* · no subtypes · provides a neutral for SLG earthing (terminals LINE + N; **no** secondary voltage)
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

**MV_CONDUCTOR — MV Conductor** *(line · edge, role=passthrough, tier 300, network=MV)* · subtypes: `1 overhead_mv_conductor`, `2 underground_mv_conductor`
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `voltage_class` | text | `VOLTAGE_CLASS` | Nominal voltage. **req** |
| `conductor_spec` | text | `MV_LINE_SPEC` | Material-backed size (carries R/X/ampacity). |
| `phase` | text | `PHASE` | Phases carried. |
| `condition_rating` | text | `CONDITION_RATING` | Condition. **req** |

**MV_BUSBAR — MV Busbar** *(point / diagram line · junction, role=node, tier 300, network=MV)* · no subtypes · an equipotential node (T1 incoming, T2 fan-out)
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `voltage_class` | text | `VOLTAGE_CLASS` | Voltage. |
| `condition_rating` | text | `CONDITION_RATING` | Condition. |

**Series-switch family** — all are *(point · junction, geometry-optional, T1 line / T2 load)*; all carry **`device_state`** (`DEVICE_STATE`, **req** — `OPEN` = trace barrier) and **`condition_rating`** (**req**). Extra fields below:

| Class | Name *(role · boundary)* | Subtypes | Extra fields |
|---|---|---|---|
| **MV_BREAKER** | MV Breaker *(controller · protective)* | `1 vacuum_breaker`, `2 sf6_breaker` | `normal_state` (`NORMAL_STATE`) · `switch_function` (`SWITCH_FUNCTION`, for coupler/incomer use) |
| **MV_ISOLATOR** | MV Isolator *(controller · switching)* | `1 single_break`, `2 double_break` | `normal_state` (`NORMAL_STATE`) |
| **MV_SECTIONALIZER** | MV Sectionalizer *(controller)* | `1 hydraulic`, `2 electronic` | `normal_state` (`NORMAL_STATE`) |
| **MV_RECLOSER** | MV Recloser *(controller · protective)* | `1 standard` | `reclose_attempts` (int) · `fast_curve_ref` / `slow_curve_ref` (curve code) · `reclose_interval_s` · `reset_time_s` · `normal_state` |
| **MV_SWITCH** | MV Switch *(controller · switching)* | `1 standard` | `is_fused` (bool) · `fuse_rating_a` (num) · `curve_ref` (code) · `normal_state` — *RMU ways are MV_SWITCH instances; `is_fused`=fused tee way* |
| **MV_FUSE** | MV Fuse *(controller · protective)* | `1 expulsion_fuse`, `2 current_limiting_fuse` | — |
| **LV_SWITCH** | LV Switch *(controller · switching, tier 500)* | `1 standard` | `is_fused` · `fuse_rating_a` · `curve_ref` · `ownership` |
| **SERVICE_CUTOUT** | Service Cutout *(controller · protective, tier 500)* | `1 fuse_carrier`, `2 switch_fuse` | `ownership` |

**MV_ARRESTER — MV Surge Arrester** *(point · junction shunt, network=MV)* · subtypes: `1 gapless_mov`, `2 gapped_sic` · (T1 line, T2 ground; never a barrier)
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `condition_rating` | text | `CONDITION_RATING` | Condition. **req** |

**MV_EARTH_SWITCH — MV Earth Switch** *(point · none, tier 300, network=MV)* · subtypes: `1 manual`, `2 fault_making` · grounds a de-energised section; **deliberately not a sectioning boundary**
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
| `condition_rating` | text | `CONDITION_RATING` | Condition. |

*(Sibling `MV_SWITCHBOARD` — a geometry-none container grouping panels: `voltage_class` **req**, `board_ref`, `room`, `condition_rating`.)*

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

**LV_BUSBAR — LV Busbar** *(point / diagram line · junction, role=node, tier 500, network=LV)* · subtypes: `1 lv_distribution_board`, `2 busbar_riser`
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `phase` | text | `PHASE` | Phase config. **req** |
| `ownership` | text | `OWNERSHIP` | Utility / private. |

**LVAC_BOARD — Station LVAC Board** *(point · junction)* · subtype: `1 standard` · the substation's LV AC distribution board
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
| `load_priority` | text | `LOAD_PRIORITY` | Restoration tier. |
| `ownership` | text | `OWNERSHIP` | Utility / private. |

**METER — Meter** *(point · junction, role=consumer, tier 600, network=LV)* · subtypes: `1 whole_current`, `2 ct_metered`
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `meter_serial` | text | — | Device identity / MDM key. **req** |
| `account_ref` | text | — | Billing link. |
| `phase` | text | `PHASE` | Phase. **req** |
| `ct_ratio` | text | `CT_RATIO` | CT ratio (required for `ct_metered`). |
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

**MV_METERING_POINT — MV Metering Point** *(point · junction, role=consumer, tier 300)* · subtypes: `1 industrial`, `2 commercial_complex`, `3 bulk_supply` · an MV-connected customer supply
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `supply_voltage` | text | `MV_SUPPLY_VOLTAGE` | 11 kV / 33 kV. **req** |
| `contracted_demand_kva` | numeric | — | Contracted demand. **req** |
| `tariff_ref` | text | — | CIS/MDM tariff key. |
| `account_ref` | text | — | Billing key. |

**MV_METER — MV Meter** *(point · none, role=consumer, tier 300)* · subtypes: `1 main_meter`, `2 check_meter` · off-path; reads CT/VT secondaries via relationships
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

**SUBSTATION — Substation** *(polygon · container)* · subtypes: `1 distribution_substation`, `2 transmission_substation`
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

**PROTECTIVE_RELAY — Protective Relay** *(none · none, sensor)* · subtypes: `1 standard` · the IDMT/instantaneous settings a breaker trips on
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `device_number` | text | `ANSI_DEVICE_NUMBER` | Primary ANSI function (50/51, 87…). **req** |
| `manufacturer` | text | — | Relay maker. |
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

**MV_CT — MV Current Transformer** *(point · junction, sensor)* · subtypes: `1 metering_ct`, `2 protection_ct`
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `ct_ratio` | text | `CT_RATIO` | Current ratio (e.g. 200/5). **req** |
| `condition_rating` | text | `CONDITION_RATING` | Condition. **req** |

**MV_VT — MV Voltage Transformer** *(point · junction, sensor)* · subtypes: `1 inductive_vt`, `2 capacitive_vt`
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

**CABLE_TRENCH — Cable trench** *(line · none)* · no subtypes
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

**CABLE_CHAMBER — Cable chamber** *(polygon, optional · none)* · subtypes: `manhole`, `footway_box`, `joint_bay`, `jointing_pit`
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `access_cover_type` | text | `CHAMBER_COVER` | Light / medium / heavy duty. **req** |
| `condition_rating` | text | `CONDITION_RATING` | Condition. **req** |
| `install_date` / `last_inspected_at` | date | — | Install / last inspection. |

**MV_CABLE_JOINT / LV_CABLE_JOINT — Cable joint** *(point · junction)* · subtypes: `straight`, `branch` (tee), `transition`, `termination` · a zero-impedance splice node; the `branch` subtype adds a 3rd terminal
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
| `access` | text | `EV_ACCESS` | Public / restricted. |

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

**NETWORK_METER — Network Meter** *(point · none, consumer)* · subtypes: `1 substation_incomer`, `2 feeder_head`, `3 transformer_metering`, `4 bus_section`, `5 boundary_exchange`, `6 interchange_tie`, `7 generation_export` · an **off-path energy-accounting** meter (related to an element, never connected)
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `meter_serial` | text | — | Device identity. **req** |
| `metering_class` | text | `METERING_CLASS` | Accuracy class. **req** |
| `ct_ratio` | text | `CT_RATIO` | CT ratio. |
| `vt_ratio` | text | `VT_RATIO` | VT ratio. |
| `external_ref` | text | — | MDM/head-end join key (readings live externally). |

**LV_PILLAR — LV Feeder Pillar** *(point · container, network=LV)* · subtypes: `1 standard` · holds a bus + fused ways
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `condition_rating` | text | `CONDITION_RATING` | Condition. |

**LV_FUSE — LV Fuse Way** *(none · junction, boundary=protective)* · subtypes: `1 standard` · one fused outgoing way inside a pillar
| Field | Type | Domain | Meaning |
|---|---|---|---|
| `device_state` | text | `DEVICE_STATE` | Open/closed (OPEN = trace barrier). **req** |
| `fuse_rating` | text | `FUSE_RATING` | Fuse size. **req** |
| `condition_rating` | text | `CONDITION_RATING` | Condition. **req** |

---

## Appendix — Every feature class (63)

The complete class inventory, for cross-reference. *(Network-layer field detail lands in the next revision; names/roles are listed here.)*

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
