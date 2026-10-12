# Switching & Protection

**Written for:** a network modeller, control engineer or protection engineer representing switchgear and protection in VoltEdge — what each device *is*, how its state reconfigures the live network, and how the protection devices grade against each other.

This guide is the companion to the interactive **Switching & Protection** guide (Interactive guides — `switching-and-protection-guide-interactive.html`, which runs ports of the real trace, grading and restoration rules in your browser). It leans on two other manuals rather than repeating them: **Feeders, Sections & Subnetworks** for the sectioning backbone, and **Engineering Studies** for the fault, coordination and arc-flash *math*. It is written in two layers:

- **▶ Operator** — what the device is, what to enter, what happens when you throw it.
- **∑ Engineer** — the model: the classes, state axes, barrier rules and the grading engine, with their source in the seed and solver code.

**Who this is for.** Network modellers capturing switchgear and protection; control engineers who operate the model (open/close, isolate, restore); and protection engineers who set relays and check coordination.

**The cast.** **Dana** (editor/field supervisor) captures the switches, breakers and RMUs and wires the relays; **Priya** (dispatcher) opens and closes devices, isolates for work and watches what goes dark; **Tom** (analyst) runs coordination and fault studies; **Ada** (admin) authors the classes, domains and curves behind it all. These are *people*, not roles: the roles that actually exist are `viewer`, `analyst`, `editor`, `admin` and `break-glass-admin` (plus the three `field-*` roles) — **there is no `dispatcher` role**, so Priya holds `editor` (`migrations/0025_create_rbac.sql`, `0088_seed_oms_permissions.sql`). What each persona may do:

| Action | Permission(s) | viewer | analyst (Tom) | editor (Dana, Priya) | admin (Ada) |
|---|---|---|---|---|---|
| Look at devices, states, relays | `features:read` | ✓ | ✓ | ✓ | ✓ |
| Run a trace, isolate, section, supply path | `trace:run` | — | ✓ | ✓ | ✓ |
| Run fault / coordination / arc-flash / contingency | `analysis:run` | — | ✓ | ✓ | ✓ |
| Change `device_state`, edit a relay's settings | `features:write` | — | — | ✓ | ✓ |
| Wire a device (connection) | `connectivity:commit` | — | — | ✓ | ✓ |
| Link a relay to a breaker / CT / VT (relationship) | `relationships:commit` | — | — | ✓ | ✓ |
| Place a tie, coupler | `features:write` + `connectivity:commit` | — | — | ✓ | ✓ |
| Place an RMU | `features:write` + `connectivity:commit` + `relationships:commit` | — | — | ✓ | ✓ |
| Read the outage board | `oms:read` | ✓ | ✓ | ✓ | ✓ |
| Generate / execute a switching order | `oms:write` | — | — | ✓ | ✓ |
| Edit classes, domains, field rules | `metadata:write` | — | — | — | ✓ |
| Load a new TCC curve | *none — SQL only* (see §6.3) | — | — | — | DB access |

(`auth.RequirePermission(read, write)` picks the first permission for `GET/HEAD/OPTIONS` and the second for everything else — `api/internal/auth/rbac.go:75`. `/trace` and `/analysis` use one permission for both: `trace:run`, `analysis:run` — `api/cmd/server/main.go:376-377`.)

**Legend.** 🔒 an action that needs a permission (named inline). 💡 a tip. ⚠️ a caution or a known limitation stated honestly. **▶** the build/operate lane; **∑** the model/engine lane. A boxed **How to** is a click-by-click procedure; a **Worked example** carries real numbers you can reproduce (the interactive edition recomputes every one of them).

**Running example — Feeder 7.** An 11 kV distribution feeder off a zone substation. We build it up through the guide: a feeder breaker with its relay and instrument transformers, a mid-feeder recloser, a fused tee-off to a distribution transformer, a ring main unit, a sectionalizer on the ring tail, and a normally-open tie to the next feeder.

```
  Zone S/S 11 kV bus ═══════════════════ B7
         │
      [CB-7]  MV_BREAKER · FEEDER           relay R-7  : 50/51, IEC_SI, pickup 400 A, TD 0.5
         │                                  sensing    : CT-7 (400/5), VT-7 (11 kV/110)
      MV_CONDUCTOR  (Feeder 7, 11 kV)
         │
      [REC-7] MV_RECLOSER                   fast_curve_ref / slow_curve_ref = ampere-quantity curve codes
         │                                  (admin-loaded; no time dial); reclose_attempts 4
         ├──[FSW-12] MV_SWITCH is_fused (100 T) ──► DIST_XFMR TX-220 (11 kV/415 V) ──► LV
         │
      [RMU-5] RMU 2W_1T ──(fused way)──► DIST_XFMR TX-230
         │   (ring ways continue…)
         ├──[SEC-4] MV_SECTIONALIZER  count_to_lockout 3
         ┆
      [T-79] MV_SWITCH  — normal-open tie  ╌╌╌► Feeder 9     (normal_state NORMALLY_OPEN, device_state OPEN)
```

> **Grounding note.** Everything here is drawn from the shipped seed and engine, not an idealised description. Switching/protection classes and domains live in `seed/*.sql` (`mv_lv_switch_example.sql`, `mv_switching_example.sql`, `mv_recloser_example.sql`, `mv_protection_example.sql`, `protective_relay_example.sql`, `mv_relay_example.sql`, `mv_measuring_example.sql`, `protection_curves_example.sql`, `switch_normal_state_fields.sql`, `bus_coupler_fields.sql`); the behaviour lives in `api/internal/trace` (barriers, isolate, energization, sectioning), `api/internal/powerflow` (coordination, fault, arc-flash, contingency), `api/internal/oms` (live switching) and the fan-outs `api/internal/tie`, `coupler`, `rmu`. Where the design intent and the shipped behaviour differ, this guide follows the code and says so; every such place is listed with file and line in **Appendix D — Corrections**.

> **How to read the numbers.** Feeder 7 is *illustrative*: the impedances (0.32 + j0.30 Ω/km, source 0.048 + j0.48 Ω — the same source the shipped protection seed uses), the recloser curves and the load figures are invented for teaching and labelled as such. The curve constants, fuse points, domain codes, defaults and engine rules are the real ones.

**What's inside**

- **Part A — Switching:** §1 the devices (+ *how to place a switch*, *split a line*, field and wiring reference, data-entry mistakes) · §2 the state axes (+ *how to open/close*, normal vs current state, OMS switching orders) · §3 what opening a device does — barriers, the de-energised cone, isolate (+ *how to isolate*), sections vs. zones, and what the Live view does *not* tell you · §4 ties, couplers, RMUs (+ *how-tos*, wiring directions, rejection reasons).
- **Part B — Protection:** §5 the devices (+ *how to wire a relay to a breaker*, CT/VT sanity) · §6 TCC curves (+ constants, interpolation, *how to set a curve*, *how to load a curve*) · §7 coordination (+ *how to run a study*, a full grading study for Feeder 7, mis-coordination case studies, reclose sequences, sectionalizer counting, fuse-saving vs fuse-blowing) · §8 fault & arc-flash (cross-reference with numbers).
- **Part C — Together:** §9 the trip → switch → reconfigure loop (+ *how to see what's de-energised*, a worked restoration with the three views of "dead") · §10 what the model does **not** do · §11 troubleshooting · Appendix — domains, formulas, quick reference, recipes, **Corrections**.

---

# Part A — Switching

## 1. The switching devices

Every switching device in VoltEdge is a **point feature** on the MV or LV network with `network_role = 'junction'` — it sits *inline* on a conductor and can interrupt it. They share one shape:

- Two terminals: **T1 = primary** (line side) and **T2 = secondary** (load side).
- Three connectivity rules wire them in: a conductor lands on T1, a conductor lands on T2, and T2 feeds a conductor onward — so a downstream trace walks *through* a closed device and *stops* at an open one. (More on which rules exist — and which do not — in §1.2.)
- Two fields are always required: **`device_state`** (the live open/closed position) and **`condition_rating`** (asset health: `GOOD`/`FAIR`/`POOR`). Protection-grading fields are optional.
- The MV and LV switching classes are `geometry_optional` and `is_diagram_placeable` (`seed/mv_lv_switch_example.sql:80,130`, `seed/mv_switching_example.sql:84,134,181`): a switch may be on the map, on a one-line diagram, or both. (The legacy `DIST_SWITCH` is neither — `seed/switching_device_example.sql:20`.) The *tie* assembly is the exception — it needs map geometry (§4).

The classes:

| Class | What it is | Subtypes | Notable fields |
|---|---|---|---|
| **`MV_SWITCH`** | Generic MV switch / load-break switch. Also the **RMU way** and the default **feeder-tie** switch. | `standard` | `is_fused`, `fuse_rating_a`, `curve_ref`, `normal_state` |
| **`LV_SWITCH`** | LV sibling of `MV_SWITCH`. | `standard` | `is_fused`, `fuse_rating_a`, `curve_ref` (no `normal_state`) |
| **`MV_BREAKER`** | Substation / feeder **circuit breaker** — interrupts fault current. Also the **bus-coupler** device. | `vacuum_breaker`, `sf6_breaker` | `switch_function`, `normal_state` |
| **`MV_ISOLATOR`** | Off-load **disconnector / isolator** — a visible isolating gap, switched dead. | `single_break`, `double_break` | `normal_state` |
| **`MV_RECLOSER`** | Self-contained multi-shot **protective** recloser (see Part B). | `standard` | `reclose_attempts`, `fast_curve_ref`, `slow_curve_ref`, `reclose_interval_s`, `reset_time_s`, `normal_state` |
| **`MV_SECTIONALIZER`** | Counts fault passages, opens on a dead line. | `hydraulic`, `electronic` | `count_to_lockout`, `normal_state` |
| **`MV_FUSE`** | Inline MV **fuse** — a switching *and* protective device (Part B). | `expulsion_fuse`, `current_limiting_fuse` | `fuse_rating_a`, `fuse_speed`, `curve_ref` (no `normal_state`) |
| **`RMU`** | **Ring main unit** — a `container`, not an inline switch. Holds an internal busbar and its switched ways. | `standard` | `rmu_config` (`2W_1T`, `2W_2T`) — required |
| **`DIST_SWITCH`** | The original, now-**legacy** switch class. | `manual_switch`, `automatic_recloser` | superseded by `MV_SWITCH` / `MV_RECLOSER` |

> **∑ Three things are *fields or assemblies*, not their own classes** — a common modelling trap:
> - A **bus coupler** is an `MV_BREAKER` with `switch_function = BUS_COUPLER`, wired bus-to-bus.
> - A **feeder tie / normal-open point** is an `MV_SWITCH` with `normal_state = NORMALLY_OPEN`, wired conductor-to-conductor.
> - An **RMU way** is a plain `MV_SWITCH` contained by the `RMU`; a ring vs. transformer way is distinguished by `is_fused`. The RMU's internal bus is a plain `MV_BUSBAR`. (The old `RMU_WAY`/`RMU_BUSBAR` classes were collapsed into these — `seed/collapse_rmu_way_into_mv_switch.sql`.)
>
> There is also no literal `CIRCUIT_BREAKER` (it's `MV_BREAKER`), `DISCONNECTOR` (it's `MV_ISOLATOR`), or `LOAD_BREAK` class — a load-break switch is modelled as a generic `MV_SWITCH`. **`DIST_SWITCH`** is the original, now-legacy switch class, superseded by `MV_SWITCH`.

**Where each sits in the hierarchy (∑).** Three orthogonal taxonomies tag these classes; they drive the trace and sectioning behaviour in §3:

- **`trace_tier`** (the grid layer, for the cross-layer walk guard — `seed/trace_tiers.sql`): `GENERATOR` is **0**; `POWER_XFMR` **200**; the MV layer is **300** — `MV_BUSBAR`, `MV_SWITCHGEAR_PANEL`, `MV_CONDUCTOR`, `MV_SWITCH`, `MV_ISOLATOR`, `MV_RECLOSER`, `MV_BREAKER`, `MV_FUSE`, `MV_SECTIONALIZER`, `MV_EARTH_SWITCH`, `MV_LV_SWITCH`, `MV_CT`, `MV_VT`, `MV_METER`, `MV_METERING_POINT`; `DIST_XFMR` and `AUX_TRANSFORMER` **400**; the LV layer is **500** — `LV_BUSBAR`, `LV_PILLAR`, `LV_CONDUCTOR`, `LV_FUSE`, `LV_SWITCH`, `SERVICE_CUTOUT`; `SERVICE_POINT` and `METER` **600**. Legacy `DIST_SWITCH`, the `RMU` container, relays and poles are **untiered** (transparent to the guard).
- **`functional_role`** (`seed/functional_role_backfill.sql`): every state-bearing switch is a **`controller`** — "the only role whose state can stop a trace" (`LV_SWITCH`, `MV_SWITCH`, `MV_ISOLATOR`, `MV_RECLOSER`, `MV_BREAKER`, `MV_FUSE`, `MV_SECTIONALIZER`, `MV_EARTH_SWITCH`, `SERVICE_CUTOUT`). Transformers are `transformer`, busbars `node`, conductors `passthrough`, loads `consumer`, and `MV_CT`, `MV_VT`, `PROTECTIVE_RELAY` are `sensor`.
- **`boundary_role`** (what bounds a section — `seed/boundary_role_backfill.sql`): `protective` = `MV_BREAKER`, `MV_RECLOSER`, `MV_FUSE`, `SERVICE_CUTOUT`, `LV_FUSE`; `switching` = `MV_SWITCH`, `MV_ISOLATOR`, `MV_SECTIONALIZER`, `LV_SWITCH`, `LV_LINK_BOX`; `transformer` = `DIST_XFMR`, `POWER_XFMR`; `source` = `GENERATOR`. (`MV_EARTH_SWITCH` is deliberately **not** a boundary — it grounds for safety, it doesn't section.)

> **The sectionalizer is a full switching boundary.** It is a trace barrier when its `device_state` is `OPEN` (every class with a `device_state` field gets the barrier — `seed/trace_barrier_resync.sql`), **and** — as of the taxonomy backfill above — it is tier 300, a `controller`, and a `switching` boundary, so a *switching-section* walk and the *isolate* boundary walk stop at it like any other switch. It is *not* a `protective` boundary: it interrupts no fault current, so it never bounds a *protection zone*. (Earlier editions noted it as an un-classified gap; the taxonomy backfill now closes it — re-apply those seeds and recompile for it to take effect.)

### 1.1 Field reference — what each switching class carries

Required (`R`) fields are enforced on create (a missing one is a `400` naming the field) and must stay complete once a row is published. Everything else is optional.

| Class | Required | Optional (protection / state) | Optional (other) |
|---|---|---|---|
| `MV_SWITCH`, `LV_SWITCH` | `device_state`, `condition_rating` | `is_fused`, `fuse_rating_a`, `curve_ref`; `normal_state` (MV only) | `circuit_id`, `operational_state` |
| `MV_BREAKER` | `device_state`, `condition_rating` | `switch_function`, `normal_state` | `circuit_id`, `operational_state` |
| `MV_ISOLATOR` | `device_state`, `condition_rating` | `normal_state` | same |
| `MV_RECLOSER` | `device_state`, `condition_rating` | `reclose_attempts`, `fast_curve_ref`, `slow_curve_ref`, `reclose_interval_s`, `reset_time_s`, `normal_state` | same |
| `MV_SECTIONALIZER` | `device_state`, `condition_rating` | `count_to_lockout`, `normal_state` | same |
| `MV_FUSE` | `device_state`, `condition_rating` | `fuse_rating_a`, `fuse_speed`, `curve_ref` | same |
| `RMU` | `rmu_config` | — | `circuit_id`, `operational_state` |
| `PROTECTIVE_RELAY` | `device_number` | `curve_type`, `pickup_current_a`, `time_dial`, `inst_pickup_a`, `definite_time_s`, `manufacturer`, `model`, `condition_rating` | `operational_state` |
| `MV_CT` | `ct_ratio`, `condition_rating` | — | — |
| `MV_VT` | `vt_ratio`, `condition_rating` | — | — |

Sources: `seed/mv_lv_switch_example.sql:94-111,144-160`, `seed/mv_switching_example.sql:97-110`, `seed/mv_recloser_example.sql:90-110`, `seed/protection_curves_example.sql:96-140`, `seed/switch_normal_state_fields.sql:96-103`, `seed/bus_coupler_fields.sql:92-97`, `seed/rmu_example.sql:92-125`, `seed/protective_relay_example.sql:80-100`, `seed/mv_measuring_example.sql:36-90`. The protection fields are nullable by design — the additive-compile rule (a NOT NULL column cannot be added to a class that already has rows).

### 1.2 Wiring — which connections exist (and which do not)

| Device | Conductor → T1 | Conductor → T2 | T2 → conductor | Bus (MV_BUSBAR) |
|---|---|---|---|---|
| `MV_SWITCH` / `LV_SWITCH` | ✓ | ✓ | ✓ | `MV_SWITCH` T2 → bus (RMU way) only |
| `MV_BREAKER`, `MV_ISOLATOR`, `MV_SECTIONALIZER` | ✓ | ✓ | ✓ | bus T2 → T1 (feeds line side) and T2 → bus T1 (incomer / coupler) |
| `MV_RECLOSER`, `MV_FUSE` | ✓ | ✓ | ✓ | — |

Sources: `seed/mv_lv_switch_example.sql:183-215`, `seed/mv_switching_example.sql:222-260`, `seed/mv_busbar_example.sql:154-185`, `seed/collapse_rmu_way_into_mv_switch.sql:183-199`, `seed/mv_recloser_example.sql:122-160`.

⚠️ **There is no device-to-device rule.** Two switching devices cannot be wired directly to each other: a conductor (it may be a short geometry-less one) must sit between them. That is why a coupler is "breaker only" and why a tie is a single switch — `api/internal/coupler/fanout.go:19-22`, `api/internal/tie/fanout.go:28-31`.

### 1.3 Common data-entry mistakes

| Mistake | What happens | Fix |
|---|---|---|
| Model a load-break switch as a `LOAD_BREAK` class | no such class | `MV_SWITCH` |
| Put a bus coupler in as an `MV_SWITCH` | not a protective device; no `switch_function` | `MV_BREAKER` + `switch_function = BUS_COUPLER` (or use **Assemblies → Bus coupler**) |
| Set `normal_state = NORMALLY_OPEN` and leave `device_state = CLOSED` | the "tie" carries load; the diagram shows a NOP marker on a closed switch | both: `NORMALLY_OPEN` **and** `OPEN` (the tie assembly sets both) |
| Set `is_fused = true` but no `curve_ref` | the device is skipped in coordination with the note *"fuse has no curve_ref set"* | set `fuse_rating_a` and an ampere-quantity `curve_ref` |
| Type a relay curve (`IEC_VI`) into a recloser's `slow_curve_ref` | the recloser drops out of grading — *"no total_clear points for curve IEC_VI"* | an ampere-quantity curve code (§6.3) |
| Wire switch → switch directly | the connection is refused (no network rule) | add a conductor between them |
| Leave `operational_state` `PLANNED` on a device that is in the field | the lifecycle axis says "not built"; `PLANNED → ISOLATED` is an illegal jump | `PLANNED → IN_SERVICE` first |
| Model a sectionalizer and expect it to bound a *protection zone* | it does not — it's a `switching` boundary, not `protective` (it clears no fault) | it *does* bound a switching section and an isolation boundary (§1); use a breaker/recloser/fuse if you need a protection-zone edge |
| Draw the relay on the map | `PROTECTIVE_RELAY` has no geometry (`geometry_type = 'none'`) | place it on the substation single-line diagram |

> **How to — place a switch on the map** 🔒 `features:write` + `connectivity:commit`
> 1. Map toolbar (top-right) → **➕ New feature** → pick the class (type "MV switch" in the search) → click **MV Switch**.
> 2. The banner reads *"Click the map to place the new feature."* Click where the switch goes — on or beside the conductor it interrupts.
> 3. The create form opens. Set the required fields — **`device_state`** (`CLOSED` to start), **`condition_rating`** — then any protection fields (leave `is_fused` off for a plain switch). **Save.**
> 4. Wire it in: select the switch → **Terminals** tab → **➕ New connection** → click the line-side conductor, pick terminal **T1**; repeat for the load-side conductor on **T2**. (The three switch rules authorise exactly these joins.)
>
> 💡 If the point lands within about a metre of an existing conductor (the default tolerance is 1.0 m — `api/internal/crud/split_suggest.go`), the in-app assistant offers **"Split … here"** as a suggestion chip beside the connect chips. Accepting it opens the manual-split form pre-filled from the source line; it never happens automatically (`docs/phase-29-point-on-line-split.md`).
>
> 💡 Prefer to drop a device **mid-span** on an existing conductor yourself? Split the line first:
>
> > **How to — split a conductor to insert a device**
> > 1. Select the conductor → **Edit** (pencil, panel header) → the floating geometry toolbar appears → click **Split** (scissors).
> > 2. Helper text: *"Click the line where you want to cut it into a new feature."* Click the point. The far half becomes a new conductor feature (`POST /features/{code}/{id}/split`; `features:write`, line classes only; a cut that lands on an endpoint is refused).
> > 3. Now place your switch at the cut and connect T1 to one half, T2 to the other.
> >
> > ⚠️ Split only cuts the line into two conductors — it does **not** drop a device for you. Placing and wiring the switch is the step above.

---

## 2. The two state axes (plus normal state)

A switching device carries **two independent state columns**. Confusing them is the number-one modelling mistake:

1. **`device_state`** — the **live, operational position** right now. Coded values: **`CLOSED`** and **`OPEN`**. *This is the only field that reconfigures the live network* — an `OPEN` device is a barrier to every trace, de-energises what's below it, and shrinks in OMS as crew close it back.
2. **`operational_state`** — the **asset lifecycle**, an entirely different axis. Coded values: **`PLANNED` → `IN_SERVICE` → `ISOLATED` → `RETIRED`**. `ISOLATED` and `RETIRED` *also* block a trace (the asset isn't really there), but they describe the *asset's status*, not a switch position.

> **▶** `device_state = OPEN` means "the switch is open, flip it and power flows." `operational_state = ISOLATED` means "this asset is out of commission." A closed switch that is `ISOLATED` still blocks flow — because the *asset* is unavailable, not because it's switched open.

**`operational_state` is a state machine (∑).** Illegal jumps are refused with a `400` at write time (`crud.UpdateFeature`, enforced via `metadata.domain_transitions` — `api/internal/crud/validate.go:344`). The legal edges (`seed/operational_state_transitions.sql:40-45`):

```
PLANNED ──▶ IN_SERVICE ──▶ ISOLATED ──▶ IN_SERVICE   (restore)
   │             │             │
   └──▶ RETIRED  └──▶ RETIRED  └──▶ RETIRED
```

`NULL → any` and `any → NULL` are always allowed. Nothing leaves `RETIRED`. Every other jump — including `PLANNED → ISOLATED` and `RETIRED → IN_SERVICE` — is refused with *"field "operational_state" cannot change from "X" to "Y" -- not a permitted OPERATIONAL_STATE transition"*. Existing rows are back-filled on install: published features become `IN_SERVICE`, drafts `PLANNED` (`seed/operational_state_backfill.sql`). The field exists on the conductors, busbars, MV/LV switching classes, `DIST_XFMR`, `SERVICE_POINT` and others (`seed/operational_state_example.sql:21-27`, `mv_lv_switch_parity.sql`, `mv_recloser_example.sql`) — **not** on `POWER_XFMR`, so a power transformer cannot be tagged out this way.

**`normal_state` and `switch_function` (∑).** Two more descriptive fields:

- **`normal_state`** — the **intended** position in normal running: `NORMALLY_CLOSED` or `NORMALLY_OPEN`. It exists on `MV_BREAKER`, `MV_ISOLATOR`, `MV_SWITCH`, `MV_SECTIONALIZER` and `MV_RECLOSER` — *not* on `MV_FUSE` or `LV_SWITCH` (`seed/bus_coupler_fields.sql`, `seed/switch_normal_state_fields.sql:100`). It is a *label* — it does **not** itself block flow; only `device_state = OPEN` does. Three things read it: the diagram's amber **NOP** marker (drawn only for `NORMALLY_OPEN`), the tie assembly (which sets it), and the **contingency study**, which ranks `NORMALLY_OPEN` candidate ties first (`api/internal/powerflow/contingency.go:142`). It is nullable with no subtype rule — an unset value simply reads "no declared open point".
- **`switch_function`** (on `MV_BREAKER`) — the bay role: `FEEDER`, `INCOMER`, `BUS_COUPLER`, `BUS_SECTION`, `TRANSFORMER`.

**Normal state vs. live state — reading the pair.**

| `normal_state` | `device_state` | Reading |
|---|---|---|
| `NORMALLY_CLOSED` | `CLOSED` | normal running |
| `NORMALLY_OPEN` | `OPEN` | a healthy standby tie (T-79 today) |
| `NORMALLY_CLOSED` | `OPEN` | **abnormal** — a switch is open that should be closed (a fault isolated, work in progress, or a forgotten step) |
| `NORMALLY_OPEN` | `CLOSED` | **abnormal** — a tie is closed: the feeders are paralleled or one is carrying the other's load |
| unset | either | no declared design posture; nothing to compare |

The model does not raise an alarm on the abnormal rows — it simply stores both columns. Put them side by side in a query or report to find them.

**Who sets state.** Both state columns are ordinary attributes edited through the generic write path (REST `PATCH /features/...`, the map's attribute editor, or the `update_feature` MCP tool). The **OMS module** flips `device_state` programmatically as crew execute a switching order: executing an **open** step sets `OPEN`, executing a **close** step sets `CLOSED`, and un-ticking a step restores the opposite — each inside the same transaction that ticks the step, written through `crud.UpdateFeature` so it is validated and history-logged (`api/internal/oms/switching.go:317-378`).

A generated switching order (`POST /oms/incidents/{id}/switching-orders`, `oms:write`) always has **five steps**: isolate — *open* the blamed device, *verify* the downstream supplies are de-energised, *earth* at the nearest upstream isolation point (`MV_SWITCH`, `MV_ISOLATOR`, `MV_RECLOSER`, `MV_BREAKER` — `switching.go:41`); restore — *note* "remove earths", *close* the device. Only the `open`/`close` steps move a device; `verify`, `earth` and `note` change nothing. Order status follows the ticks: `draft` → `issued` → `in_progress` (first tick) → `completed` (last tick); `cancelled` keeps the steps for the record.

> **How to — open or close a device** 🔒 `features:write`
> 1. Select the switch/breaker on the map → **Edit** (pencil, panel header) → **Attributes** tab.
> 2. Find **`device_state`** — it's a dropdown — choose **`OPEN`** (or `CLOSED`) → **Save.**
> 3. Re-run any trace, or open the one-line's **Live** shading, to see the effect immediately.
>
> ⚠️ **There is no one-click "operate" toggle.** Changing a switch position is the normal Edit → Attributes → `device_state` → Save path (or, during a managed outage, an OMS switching-step tick). Don't look for a right-click "open switch" — it isn't there.
>
> 💡 To take an asset *out of service* (not just switch it open), edit **`operational_state`** to `ISOLATED` instead — it blocks traces the same way but records that the asset itself is unavailable, and it's governed by the state machine above.

---

## 3. What opening a device does to the network

A switch isn't "wired into" a frozen graph — its state is read **live at every hop** of every trace. One device opening instantly changes four things.

### 3.1 It becomes a trace barrier

The engine resolves a set of **barrier rules** for each trace (`trace_barrier_rules`: "on class X, if field *f* equals value *v*, halt here"). For switching that means: **`device_state = OPEN`** on any class that has the field (every switch, breaker, isolator, recloser, sectionalizer, fuse — `seed/trace_barrier_resync.sql`), **or** `operational_state IN (ISOLATED, RETIRED)` (attached to `DOWNSTREAM_TRACE` and `UPSTREAM_TRACE` only — `seed/operational_state_barriers.sql:49`; this reaches conductors, busbars and transformers too, so a tagged-out *cable* is a barrier). A class with several rules halts on **any** of them. At each step the walk reads the device's *current* attributes. The barrier node itself **is visited and reported** (it appears in the result as a barrier hit); nothing beyond it is reached. The check runs on the start node too — which is why isolate and the cone start from a device's *neighbours*, not from the device.

Barriers are only consulted when a trace configuration has `stop_at_open_devices = true`. The shipped configurations (`seed/trace_configurations_example.sql`, `trace_target_class_example.sql`, `loop_check_example.sql`):

| Trace configuration | Direction | Stops at open devices? |
|---|---|---|
| `CONNECTED_TRACE` | connected | no (sees the whole galvanic island) |
| `DOWNSTREAM_TRACE` / `UPSTREAM_TRACE` | downstream / upstream | **yes** (and at `ISOLATED`/`RETIRED`) |
| `SHORTEST_PATH_TRACE` | shortest path | no |
| `LOOP_CHECK_TRACE` | shortest path | **yes** |
| `MV_CIRCUIT_TRACE` | connected, `scope_to_circuit` | no — the circuit boundary is *membership*, not switch state |
| `TRACE_TO_TRANSFORMER` | upstream, target `DIST_XFMR` | yes |
| `LV_NETWORK_TRACE` | downstream, target `DIST_XFMR` | yes |

### 3.2 It de-energises what's below it — the cone

The **de-energised cone** of an open device is everything a live, barrier-respecting **downstream** walk reaches from that device's load-side neighbours. The network-wide dead set is the **union** of the cones of *every* currently-open (or isolated/retired) device (`trace/energization.go:32`). Close the device and its cone collapses; partially restore and the cone shrinks to whatever the next open device still cuts off.

```
  source ──[CB closed]── busbar ──[REC closed]──┬── load A   (live)
                                                │
                                   [SW OPEN]────┴── load B   (DEAD — in the cone)
```

> **∑ Conservative on meshed networks — and on every normally-open tie.** The cone is a *radial* answer: it starts from the open device and goes down, and it never asks whether something else is feeding that region. Two consequences, both stated in the code (`trace/energization.go:27-31`): a region back-fed through a **closed** tie is *still* reported dead, **and** the far side of a *normally-open* tie is in the cone of that open tie — so in the Live view the conductor, transformer and loads just beyond T-79 are shaded as dead even though Feeder 9's own breaker is feeding them. Read "dead" in the Live view as "downstream of an open device", not as "unsupplied". The §9 worked example shows the three views side by side.

### 3.3 Transformers, busbars and the layer guard

Three rules shape how a trace moves through switchgear:

- **Transformer crossing.** A `transformer`-role feature (`DIST_XFMR`, `POWER_XFMR`) is a **downstream boundary** for the **interactive map trace only**: it stops at the transformer unless the result table's *Continue past transformers* toggle is ticked (`trace/graph.go:658-668`). Every other consumer — energisation, isolate, feeder, service and section traces — passes **through** transformers into the LV side, because stopping there "would wrongly strand a transformer's whole LV network as de-energised". So an isolate on FSW-12 lists TX-220 *and* its LV loads.
- **Junction pass-through.** Any `network_role = 'junction'` feature is expanded *undirected* on directional traces, and everything discovered from it is "unlocked" and expands undirected too (`trace/graph.go:806`). That includes busbars and RMU internal buses — **and every switching device, transformer and service point**, because they are all `junction` in the seeds (only conductors are `edge`). In a plain radial feeder this changes nothing (the way back is already visited). It matters at a bus whose ways are all authored "switch → bus", and at a closed tie — where the walk crosses into the other feeder and, until the layer guard stops it, back up toward that feeder's source. Starting a downstream trace *at* a bus-attached breaker therefore also reaches the sibling feeders on the same bus.
- **Cross-layer guard** (`trace_tier`). A downstream step may not jump to a strictly-lower tier, nor an upstream step to a strictly-higher one (`trace/graph.go:861-866`) — this stops the undirected junction expansion from leaking back toward the source (the step from an 11 kV conductor to the `POWER_XFMR` at tier 200 is refused). Untiered classes (e.g. legacy `DIST_SWITCH`) always pass. Blocked neighbours are *not* marked visited, so a legitimate deeper edge can still reach them.

### 3.4 "What if I open this?" — the isolate trace

`POST /trace/isolate` answers the switching question **without mutating live state** (`trace/isolate.go`). It needs a `DOWNSTREAM`-direction trace configuration and a `device`. Given a device, it returns:

1. **The de-energised set** — a downstream walk from the device's *downstream neighbours* (i.e. "if this device were open now"). Other open devices in that walk are barriers: they appear in the list but cut the walk.
2. **The upstream isolation boundary** — an upstream walk to the nearest **controller** (`boundary_role` `protective` + `switching`): the devices you'd actually open to work on it safely. ⚠️ **Read it, don't trust it blindly.** The walk starts at the device, and a switching device is a `junction`, so the walk begins *undirected*: devices at the same depth on the *load* side (for FSW-12: the RMU ring-in way beside it) can be hits alongside the true upstream device. The API then returns only the first of the hits (`isolate.go:293`), taken from an unordered set. Confirm the boundary against the one-line before you tag anything.
3. **Critical facilities** inside the dead set — service points (and primary meters) whose `load_priority` rank is **P1 or P2** (`isolate.go:19-22`; the old `is_critical` boolean is only a fallback), and **possible back-feed paths** — any live connected neighbour of the dead set that isn't already in the result, flagging a closed tie or mesh that could re-energise your work area. A normally-open tie's own switch is in the dead list (it is a visited barrier); the back-feed entry is the first node *beyond* it.
4. A **service rollup** — how many customers / kW drop, split by restoration tier.

> **How to — isolate a device for work** 🔒 `trace:run`
> 1. Select the device (it must be a network **junction** — a switch/breaker/recloser/busbar; the menu item is hidden otherwise — `web/src/components/map/feature-panel.tsx:2751`).
> 2. Panel header → **Trace** dropdown → **Isolate from here** (⚡).
> 3. Read the result table: the **de-energised** list, the **upstream boundary** device to open, the **critical facilities** affected, and any **possible back-feed paths** to verify before you work.
>
> **Worked example — isolate FSW-12 to work on TX-220.** Select **FSW-12** → *Isolate from here*:
> - **De-energised set:** `C-3`, `TX-220` and its LV load `SP-220` (85 customers, 310 kW) — the only things below FSW-12. The walk passes through the transformer.
> - **Upstream boundary:** *not* FSW-12 itself — the start is never its own boundary. The walk reaches `REC-7` upstream (via C-2) **and** the RMU ring-in way `Way A` (a sibling load-side switch on C-2); the API returns one of them. Open FSW-12 *and* confirm REC-7 or Way A against the one-line.
> - **Back-feed paths:** none — TX-220's LV is radial.
>
> Now contrast **isolate REC-7** (open the mid-feeder recloser):
> - **De-energised set:** everything below REC-7 — `C-2`, FSW-12/TX-220/SP-220, the RMU (ways, bus, tee, TX-230/SP-230), the ring tail, `SEC-4`, `C-4b` **and the open tie T-79 itself** (a visited barrier).
> - **Critical facilities:** `SP-230` (the clinic, P2). Service rollup: 570 kW, 155 customers.
> - **Possible back-feed path:** ⚠️ the node beyond T-79 — **C-9, Feeder 9's trunk**. T-79 is open today, but the entry says there is a live connection past the dead set: *close T-79 and the "dead" tail can be re-energised from Feeder 9.* Earth before you trust it dead.

### 3.5 Sections vs. zones

`POST /trace/section` bounds a region by walking out until it hits — but does **not** cross — a boundary device. The **kind** of boundary distinguishes two ideas (`trace/class_walk.go:94-96`):

- **Switching section** — bounded by `protective` + `switching` + `transformer` + `source` roles: *"the smallest piece I can isolate to work on it."* A plain `MV_SWITCH` **does** bound it.
- **Protection zone** — bounded by `protective` + `transformer` + `source` roles only: *"what de-energises when a fault here trips the protection."* A plain switch does **not** bound a protection zone — only a protective device or transformer does.
- **Isolation boundary** (the isolate trace's upstream leg) uses `protective` + `switching` only — a transformer isn't openable.

The boundary devices themselves are part of the result (they are visited, then not crossed). If the request names a `boundary_class` it is honoured as exactly that one class; with no `boundary_class` and no `boundary_role` stamped the engine falls back to the `controller` functional role, then to `MV_SWITCH`.

See **Feeders, Sections & Subnetworks** for the full sectioning model; the point here is that a *switch* and a *protective device* bound *different* regions. On Feeder 7: the switching section around C-2 runs from REC-7 to FSW-12 and the ring-in way; the protection zone around C-2 runs from REC-7 all the way down to the two distribution transformers — FSW-12, Way A and the other switches do not bound it.

### 3.6 What the Live view tells you — and what it does not

| View | Question it answers | Basis |
|---|---|---|
| Live shading on a one-line (`Live`, ⚡) | "which of these features lie below *any* open device?" | `NetworkDeEnergized`: union of every open device's cone, limited to the features on the diagram (`trace/energization.go:32`) |
| OMS incident energisation | "what is cut off below *this incident's blamed device* right now?" | one device's cone; empty when that device is closed (`oms/energization.go`) |
| Contingency study | "if I close this tie, what would the other feeder pick up, and does it hold?" | a sweep from the donor source (`powerflow/contingency.go`) — the only view that is source-rooted |

So: closing T-79 does **not** make the Live shading of the tail go away (the open REC-7's cone still contains it), and opening a *sectionalising* device below the blamed one *does* shrink the OMS incident view even though nothing has yet re-fed the section. Use the contingency study to decide whether a transfer is feasible, and use the shading to see what is *downstream of an open device*.

---

## 4. Ties, bus couplers and RMUs

These three aren't built by hand — each is a guided composite on the map's **Assemblies** menu (top-right, next to *New feature*) that creates the device **and** wires it in one transaction, so you never place the switch and its two connections separately. A failure anywhere leaves nothing half-wired.

Permissions: **tie** and **coupler** 🔒 `features:write` + `connectivity:commit` (`api/internal/tie/http.go:33`, `coupler/http.go:34`); **RMU** 🔒 `features:write` + `connectivity:commit` + **`relationships:commit`** (`rmu/http.go:36-37` — it also writes the containment relationships).

> **How to — add a normal-open tie between two feeders** (`POST /ties`)
> 1. Map toolbar → **Assemblies** → **Feeder tie (NOP)** → pick the switch class (default **MV Switch**; the list offers every active class that has a `normal_state` with a `NORMALLY_OPEN` value *and* both conductor rules — `tie/options.go`).
> 2. Place the switch point between the two feeders. **Map geometry is required** (`tie/fanout.go:140`).
> 3. The create form shows **two feeder pickers** — search and pick the conductor on each side (`feeder_a`, `feeder_b`) → **Save.** They must be different features.
> 4. The tie is born `normal_state = NORMALLY_OPEN` **and** `device_state = OPEN` — a declared, live-open standby path. Only the `OPEN` state blocks flow; `normal_state` is the label telling an operator it *should* be open. An explicit value you supply wins over both defaults. *(This is T-79 in our example.)*
>
> ∑ **Direction matters.** The wiring is `feeder A → switch.T1` and `switch.T2 → feeder B`: feeder A is the supply side, B the load side. Both conductor ends are wildcard terminals.

> **How to — add a bus coupler** (`POST /couplers`)
> 1. **Assemblies** → **Bus coupler** → pick the breaker class (**MV Breaker**).
> 2. Place the breaker point between the two busbars.
> 3. The form shows **two bus pickers** (`bus_a`, `bus_b`; different features) → **Save.** It's wired `bus A.T2 → breaker.T1` and `breaker.T2 → bus B.T1` and stamped `switch_function = BUS_COUPLER` (you may pass `BUS_SECTION` instead).
> 4. Downstream there's no coupler-specific code — a coupler is just a breaker, so trace/isolate/coordination treat it as one.

> **How to — build a ring main unit** (`POST /rmus`)
> 1. **Assemblies** → **Ring Main Unit** → pick the config: `2W_1T` (*2 ring + 1 transformer*) or `2W_2T` (*2 ring + 2 transformer*). `rmu_config` is required.
> 2. Place the RMU point → confirm name/subtype → **Save.**
> 3. The fan-out creates the `RMU` container, its internal `MV_BUSBAR`, and its `MV_SWITCH` ways (3 for `2W_1T`, 4 for `2W_2T`: two ring ways at the ends, then the tee ways below the bus — `seed/rmu_templates.sql:96-104`), wiring each way's load side (T2) to the shared internal bus. Each way also gets a geometry-less `MV_CONDUCTOR` drop on its cable side. Opening one tee way isolates only its transformer while the ring keeps feeding through the others. Fused (transformer) ways are stamped `is_fused = true`. *(RMU-5 in our example.)*

> **∑** Under the hood these POST to `api/internal/tie`, `api/internal/coupler` and `api/internal/rmu`; each composes `crud.CreateFeature` + `connectivity.EstablishConnection` calls in one transaction against the same switch/busbar rules you'd use by hand.

**What the fan-outs reject (all `400`).** Tie: no geometry; either feeder blank; both feeders the same feature; a feeder pick that no network rule authorises. Coupler: either bus blank; both buses the same; no rule. RMU: no `rmu_config`, or a config with no template, or no geometry. Rule misses surface as the connectivity error, with the wiring step named (*"wiring feeder_a … → MV_SWITCH.T1"*).

**An RMU ring left closed is a mesh.** The two ring ways both land on the RMU bus, so a ring that is closed at both ends is a loop: traces cope (they carry on past visited nodes), but the power-flow engines refuse a non-radial network (*"loop/mesh detected … run LOOP_CHECK_TRACE first"*). Keep a normally-open point in every ring.

---

# Part B — Protection

Switching devices *carry* current; protection devices *decide when to interrupt it*. In VoltEdge the protective intelligence (the relay and its curves) is modelled separately from the thing it opens (the breaker), and a grading engine checks that they trip in the right order.

## 5. The protection devices

### `PROTECTIVE_RELAY` — the intelligence

A **diagram-only** feature (`geometry_type = 'none'`, `network_role = 'none'`) — a relay lives on a single-line diagram, never on the map, and never appears in a trace or load flow. One subtype (`standard`); the functional variant is captured by its ANSI device number.

| Field | Meaning |
|---|---|
| `device_number` *(required)* | ANSI function code — see below |
| `curve_type` | the TCC curve **code** it runs (a `protection_curves.code`; the `TCC_CURVE` domain) |
| `pickup_current_a` | overcurrent pickup, **primary** amps |
| `time_dial` | time multiplier (TMS) applied to the curve; unset or ≤ 0 is graded as **1** |
| `inst_pickup_a` | instantaneous-element pickup, amps; when the through-current reaches it the relay operates in **0 s** |
| `definite_time_s` | fixed operate time (for a `DEFINITE_TIME` curve; not scaled by `time_dial`) |
| `manufacturer`, `model`, `condition_rating` | asset detail |

`device_number` draws from the **`ANSI_DEVICE_NUMBER`** domain — the standard function catalogue: `27` undervoltage, `59` overvoltage, `50_51` phase overcurrent, `50_51N` ground overcurrent, `86` lockout, `87` differential, `25` synch-check, `32` directional power, `37` under-current/power, `46` negative-sequence, `49` thermal overload, `64` earth-fault/REF, `67` / `67N` directional phase/ground OC, `79` auto-reclose, `81` over/under-frequency. **Only the overcurrent settings are graded** — the engine reads `curve_type`, `pickup_current_a`, `time_dial`, `inst_pickup_a`, `definite_time_s` and **never reads `device_number` itself**: a relay linked by `protection_trip` is graded from whatever overcurrent settings it carries, whatever its ANSI code (a `27` or `87` relay with a curve filled in would be graded as an overcurrent relay; one with no settings is stored, never simulated).

**How a relay connects to the network (∑).** A relay isn't wired with connectivity rules — it uses two **relationship classes**:

- **`protection_trip`** — "this relay operates that switching device." Targets `MV_BREAKER` and `MV_RECLOSER` (`seed/mv_relay_example.sql:105`, `mv_recloser_example.sql`) and the legacy `DIST_SWITCH` — and, after `seed/migrate_dist_switch_to_mv_switch.sql` has run, `MV_SWITCH` too (it clones the legacy relationship classes onto the successors). This is the link the coordination engine follows to find a breaker's grading curve. ⚠️ Only a **breaker's** relay is ever read; a relay linked to a recloser or a switch is documentation. If several relays trip one breaker, the engine grades the **first** (oldest link) and says so in the device note.
- **`protection_sensing`** — "this relay is fed by that instrument transformer." Targets `MV_CT` and `MV_VT`. This is documentation of the measurement chain; **nothing in the Go code reads it** (a search for `protection_sensing` finds only the relationship-kind whitelist).

> **How to — wire a relay to the breaker it trips** 🔒 `features:write` + `relationships:commit`
> 1. **Create the relay.** A relay has no map geometry, so add `PROTECTIVE_RELAY` on the substation **single-line diagram** (place its stencil, or promote one), then set its fields — `device_number` (e.g. `50_51`), `curve_type` (e.g. `IEC_SI`), `pickup_current_a`, `time_dial`.
> 2. **Link it to the breaker.** Select the relay → **Relationships** tab → **➕ New relationship** → click the **breaker** (CB-7) → in the confirm panel, pick **Kind = `protection_trip`** → **Confirm.**
> 3. **Link its sensing** (optional but recommended). New relationship again → click the **CT** (CT-7) → **Kind = `protection_sensing`** → Confirm; repeat for the **VT**.
>
> 💡 The **Kind** dropdown only offers the relationship classes that actually link the two classes you picked. If it offers exactly one, it's chosen for you; if it offers none, no relationship class connects them (check you clicked the right pair). The `protection_trip` link is the one coordination follows — without it, the engine can't find the breaker's curve (*"no PROTECTIVE_RELAY linked via a protection_trip relationship"*).

### `MV_CT` / `MV_VT` — the sensing

Point `junction` features that tap the line to feed a relay (they are **not** trace barriers — no `device_state`, though they are on the connectivity graph as leaves):

- **`MV_CT`** (current transformer) — subtypes `metering_ct`, `protection_ct`; field `ct_ratio` from the **`CT_RATIO`** domain (`100_5`, `200_5`, `400_5`, `800_5`, `1600_5`, e.g. "100/5 A").
- **`MV_VT`** (voltage transformer) — subtypes `inductive_vt`, `capacitive_vt` (CVT); a shunt line-to-ground tap; field `vt_ratio` from **`VT_RATIO`** (`11KV_110`, `33KV_110`).

⚠️ **The CT/VT ratio is a recorded fact, not a computation input** — no engine reads it, there is no saturation or burden model, and the relay's `pickup_current_a` is in *primary* amps (the engine compares it straight to the through-fault current). So the sanity checks are yours: the pickup should sit comfortably above the maximum load current and the CT primary rating should exceed it; a fault current more than ~20× the CT primary is a saturation risk for a protection-class core (a rule of thumb — not a VoltEdge check); and a 400/5 CT with a 400 A pickup is a 5 A secondary setting (400 ÷ 80). The interactive edition has a calculator for these.

### `MV_FUSE` — switching *and* protection

An inline point `junction` (T1 line / T2 load). `device_state = OPEN` makes it a trace barrier like any switch, but it also grades as a protective device:

- Subtypes `expulsion_fuse`, `current_limiting_fuse`.
- `fuse_rating_a` (amps), `fuse_speed` from **`FUSE_SPEED`** (`K` fast, `T` slow, plus `SLOW`/`FAST`), and `curve_ref` — the `protection_curves.code` of an **ampere-quantity** melt/clear curve (free text, not domain-constrained, so any loaded manufacturer curve resolves; an unknown or blank code leaves the operate time unresolved). The rating and speed are *labels*; only `curve_ref` drives the grading. The only fuse curve shipped is `FUSE_100T_EXAMPLE` — "NOT a real manufacturer sheet" (`seed/protection_curves_example.sql:177`).

### `MV_RECLOSER` — protection as a device

The recloser (from Part A) *is* its own protection: it runs a **two-curve** scheme — a `fast_curve_ref` (instantaneous, to clear transient faults before a fuse blows) and a `slow_curve_ref` (delayed, to coordinate with downstream fuses on a persistent fault) — and after `reclose_attempts` (the seed's example: 4 operations) goes to **lockout**, modelled as `device_state = OPEN`. In coordination it grades off its **slow** curve (falling back to the fast one if only that is set).

⚠️ **Both curve fields name an ampere-quantity curve — not a relay curve, and there is no time dial.** A recloser carries its own absolute amps-vs-seconds characteristic (graded on the `total_clear` band, like a fuse). Typing a relay code such as `IEC_VI` there finds no `total_clear` points and the recloser silently drops out of the grading. **No recloser curve is shipped** — you must load one (§6.3). And the *sequence* — shot count, dead-time (`reclose_interval_s`), reset (`reset_time_s`), lockout — is documentation: no engine counts shots (`coordination.go:588-591`). §7.7 shows how to reason about a sequence by hand.

### Fused generic switches

An `MV_SWITCH` or `LV_SWITCH` with **`is_fused = true`** becomes a protective device graded exactly like an `MV_FUSE`, via the same `fuse_rating_a` and `curve_ref` fields. This is how RMU transformer ways protect their transformer. An *unfused* switch on the path is a plain topological hop — it contributes no operate time (*"switch is not fused -- no protection element to grade"*).

### `MV_SECTIONALIZER` — counts, doesn't grade

Hydraulic or electronic, it counts the fault passages that a recloser's trips cause and opens on the dead line after `count_to_lockout` (integer, nullable). It has no time-current curve, so it is **never time-graded**, and the OMS prediction engine does not treat it as protective either. §7.8 shows the counting rule.

---

## 6. Time-current curves (TCC)

A TCC curve is the promise *"at this multiple of pickup, I operate in this many seconds."* VoltEdge stores curves as **tabulated points**, not live formulas (`metadata.protection_curves` + `protection_curve_points`, migration `0076`):

- **`protection_curves`** — `code` (unique), `name`, `standard`, and **`quantity`**: `multiple` (relay curves, x-axis = multiple of pickup, time at `time_dial = 1`) or `ampere` (fuse and recloser curves, x-axis = absolute amps).
- **`protection_curve_points`** — `(band, current, time_s)`, where **`band`** is `operate` (relay), or `minimum_melt` / `total_clear` (fuse). Constraints: `current > 0`, `time_s >= 0`, `UNIQUE (curve_id, band, current)`.

### 6.1 The seeded catalogue (∑)

The **`TCC_CURVE`** domain ships the standard inverse families — `IEC_SI` (standard inverse), `IEC_VI` (very), `IEC_EI` (extremely), `IEC_LTI` (long-time), `IEEE_MI` / `IEEE_VI` / `IEEE_EI`, and `DEFINITE_TIME` — **seven tabulated families plus `DEFINITE_TIME`, which has no points** (it means "operate after `definite_time_s`"). Their points are generated **once at seed time** from the unified IEC 60255 / IEEE C37.112 form

```
t = coeff / ( M^exp − 1 ) + b        (at time_dial = 1, M = I / pickup)
```

with these constants (`seed/protection_curves_example.sql:164-171`), sampled at the eleven multiples **1.5, 2, 2.5, 3, 4, 5, 6, 8, 10, 15, 20** and rounded to 4 decimals:

| Curve | coeff | exp | b | t at M = 2 | M = 5 | M = 10 | M = 20 |
|---|---|---|---|---|---|---|---|
| `IEC_SI` | 0.14 | 0.02 | 0 | 10.029 s | 4.280 s | 2.971 s | 2.267 s |
| `IEC_VI` | 13.5 | 1.0 | 0 | 13.500 s | 3.375 s | 1.500 s | 0.711 s |
| `IEC_EI` | 80 | 2.0 | 0 | 26.667 s | 3.333 s | 0.808 s | 0.201 s |
| `IEC_LTI` | 120 | 1.0 | 0 | 120.000 s | 30.000 s | 13.333 s | 6.316 s |
| `IEEE_MI` | 0.0515 | 0.02 | 0.114 | 3.803 s | 1.688 s | 1.207 s | 0.948 s |
| `IEEE_VI` | 19.61 | 2.0 | 0.491 | 7.028 s | 1.308 s | 0.689 s | 0.540 s |
| `IEEE_EI` | 28.2 | 2.0 | 0.1217 | 9.522 s | 1.297 s | 0.407 s | 0.192 s |

(Values are the formula at those multiples; the stored rows are the same numbers rounded to 4 decimals. The interactive edition plots every family and reads exact values.) The seed also ships **one illustrative fuse curve**, `FUSE_100T_EXAMPLE` (quantity `ampere`):

| Current (A) | 200 | 300 | 500 | 1,000 | 2,000 | 4,000 |
|---|---|---|---|---|---|---|
| minimum melt (s) | 300 | 30 | 3.5 | 0.30 | 0.045 | 0.011 |
| total clear (s) | 600 | 60 | 7 | 0.60 | 0.090 | 0.022 |

### 6.2 How the engine reads a curve

**Runtime evaluation.** The engine never re-evaluates the formula; it **log-log interpolates** between the stored points (`powerflow/curves.go:102`), then scales:

```
t_operate = time_dial × interp( I_through / pickup_current_a )        (relay)
t_operate = interp_total_clear( I_through )                            (fuse, recloser, fused switch)
```

The edges are clamped, not extrapolated, and the result is flagged `extrapolated`:

| Where the current falls | What the engine returns | Consequence |
|---|---|---|
| multiple ≤ 1 (at or below pickup) | **no operate time** — *"through-current at or below pickup — relay does not operate"* | the device drops out of the grading |
| 1 < M < 1.5 (below the first stored point) | the time at **M = 1.5**, flagged | **shorter** than the real curve (`IEC_SI` at M = 1.2: engine 17.2 s, formula 38.3 s) |
| 1.5 ≤ M ≤ 20 | interpolated | within a fraction of a percent of the formula |
| M > 20 (above the last point) | the time at **M = 20**, flagged | **longer** than the real curve (`IEC_SI` at M = 30: engine 2.27 s, formula 1.99 s; at M = 83: 2.27 vs 1.52 s) |
| fuse/recloser current outside its tabulated range | the end point's time, flagged | a recloser curve whose first point is 800 A reports its 800 A time for a 376 A fault |

So a high-fault-current study makes the backup device look slower than it is (flattering the margin and over-stating arc-flash energy), and a low-current one makes it look faster. Check the `extrapolated` flag on every device row.

**Relay specifics.** The instantaneous element is checked first: if `inst_pickup_a` is set and the through-current reaches it, the relay operates in **0 s** — and that is what the grader sees. A `DEFINITE_TIME` relay needs `definite_time_s`, ignores `time_dial`, and operates only above pickup.

> **How to — set a relay's curve and settings** 🔒 `features:write`
> 1. Select the relay → **Edit** → **Attributes.**
> 2. Set **`curve_type`** to a `TCC_CURVE` code (e.g. `IEC_SI`), **`pickup_current_a`** (the overcurrent threshold in primary amps, e.g. 400), and **`time_dial`** (the TMS, e.g. 0.5). Optionally **`inst_pickup_a`** for an instantaneous element. **Save.**
> 3. Grade it against downstream devices with a coordination study (§7).

> **How to — protect a tee with a fuse** 🔒 `features:write`
> - **A dedicated fuse:** place an **MV Fuse**, set `fuse_rating_a`, `fuse_speed` (e.g. `T`), and `curve_ref` to an ampere-quantity curve code. `device_state = OPEN` blows/isolates it.
> - **A fused switch (one device):** on an existing **MV Switch**, set **`is_fused = true`** and fill `fuse_rating_a` + `curve_ref`. It now grades exactly like a fuse — this is how an RMU transformer way protects its transformer.

> 💡 **Adding a brand-new curve** (not in the seeded catalogue) is a database job — see §6.3. The seven seeded IEC/IEEE families plus `DEFINITE_TIME` cover most relays out of the box; fuses and reclosers need curves you load.

### 6.3 Loading a curve (SQL only)

There is **no API or screen** for curves — a search of `web/src` and `api/internal` finds only the read path (`powerflow/curves.go`). A curve is two inserts, run with `psql` (the Taskfile's `seed-protection-curves` shows the pattern). A recloser slow curve, ampere-quantity:

```sql
INSERT INTO metadata.protection_curves (code, name, standard, quantity)
VALUES ('REC_SLOW_B', 'Recloser slow B (vendor sheet 12)', 'VENDOR', 'ampere');

INSERT INTO metadata.protection_curve_points (curve_id, band, current, time_s)
SELECT c.id, 'total_clear', v.i, v.t
FROM metadata.protection_curves c
JOIN (VALUES (800,8.0),(1000,3.5),(2000,1.0),(4000,0.45),(8000,0.30)) AS v(i,t) ON true
WHERE c.code = 'REC_SLOW_B';
```

Then type `REC_SLOW_B` into the recloser's `slow_curve_ref`. For a fuse add a `minimum_melt` band as well (it is drawn, not graded). A relay curve uses `quantity = 'multiple'` and band `operate`, and a matching `TCC_CURVE` domain value (`metadata:write`) so it can be chosen from the relay's dropdown.

---

## 7. Coordination

**`POST /analysis/coordination`** (`powerflow/coordination.go`) answers: *"for a fault here, do the devices on the path trip in the right order, with enough margin?"* It needs `analysis:run`.

**How it works (∑):**

1. It reuses the fault substrate — builds the network tree from the **start** feature (stopping at open devices), resolves impedances, and computes the **prospective three-phase fault current** at the fault node: `I = (V_base / √3) / |Z|` on a line-to-line nominal (the request's `voltage_basis` may declare `line_to_neutral`, which drops the √3). The **start's own** `source_r_ohm` / `source_x_ohm` (or, at a boundary transformer, its secondary and `%Z`) seed the impedance — so a study started at a *breaker* has no source impedance and over-states the fault level; start at the substation transformer (§7.3).
2. It refers that current to **each device's own voltage base** across any transformer on the path: `I_device = I_fault × V_fault / V_device` (power is conserved across the transformer).
3. It walks the path **source → fault**, and for each time-graded device reads its operate time off its curve at that through-current: a **breaker** via its `PROTECTIVE_RELAY` (`protection_trip`) `curve_type` (instantaneous element first, then the curve scaled by `time_dial`); a **fuse** via its `curve_ref` `total_clear` band; a **recloser** via its `slow_curve_ref` (else `fast_curve_ref`); a **fused switch** as a fuse.
4. The first device **with a resolved operate time**, nearest the fault, is the **primary**; every device upstream of it with a time is a **backup**. For each adjacent pair of operating devices it computes **margin = t_backup − t_primary**, and marks the pair **coordinated** when `margin ≥ CTI`. The default **CTI** (coordination time interval) is **0.3 s** — the common relay-relay margin — overridable per request via `cti_seconds` (the map panel pre-fills 0.3).

The covered classes are `MV_BREAKER`, `MV_FUSE`, `MV_SWITCH`, `LV_SWITCH`, `MV_RECLOSER`. **`MV_SECTIONALIZER` is explicitly not time-graded** — a sectionalizer counts fault passages and opens on a dead line, so it has no TCC to grade. A device with no resolved time (no relay link, missing pickup/curve, at or below pickup, unfused switch, no curve points) **stays in the list with a note and is skipped**, so a non-operating device between two graders doesn't break the chain.

**What it returns (▶).** The fault current and common base; a `Devices[]` list (each with its role `primary`/`backup`, through-current, resolved operate time, curve plot and an `extrapolated` flag); a `Pairs[]` list (each with `margin_s`, `cti_seconds`, `coordinated`); and an **`overall`** boolean — coordinated only if *every* graded pair clears its margin, and **absent** when fewer than two devices could be graded. The fault feature must be reached by a live path *downstream* of the start — a fault behind an open device is refused (*"fault feature is not downstream of start …"*).

> **How to — run a coordination study** 🔒 `analysis:run`
> 1. Select the **source** — the substation transformer **T-ZS** (not the breaker; see §7.3) — and note that the devices you want graded must lie on the path below it.
> 2. Panel header → **Trace** dropdown → **Fault & protection** → **Protection coordination…**
> 3. The panel opens armed on the source. Now click a **downstream feature** on the map (the fault location) → **Set fault.**
> 4. Set **Nominal (V)** — `11000` for our 11 kV feeder (leave blank to derive a transformer secondary) — and **CTI (s)** — `0.3`.
> 5. **Run.** Read the **log-log TCC chart** (one trace per device), the **operate time** per device, the **pairwise margins**, and the **Coordinated / Miscoordination** verdict.

### 7.1 Worked example — grade the Feeder 7 path

**The fault level first (so the number is not magic).** Fault just below **FSW-12** — at the TX-220 primary. Source `T-ZS`: 0.048 + j0.48 Ω. Conductors at 0.32 + j0.30 Ω/km: C-0 0.2 km, C-1 1.0 km, C-2 1.5 km, C-3 0.1 km (2.8 km):

```
R = 0.048 + 0.32 × 2.8 = 0.944 Ω        X = 0.48 + 0.30 × 2.8 = 1.320 Ω
|Z| = √(0.944² + 1.320²) = 1.623 Ω
I = (11 000 / √3) / 1.623 = 6 351 / 1.623 = 3 913 A        (κ = 1.02 + 0.98·e^(−3·0.944/1.32) = 1.135 → i_p = 6.28 kA)
```

Nothing between source and fault is a transformer, so every device sees the same 3,913 A (if a transformer *were* in the path, the engine would refer the current across it: `I_device = I_fault × V_fault / V_device`).

The path, fault-first: **FSW-12 (fuse)** → **REC-7 (recloser)** → **CB-7 (breaker + relay R-7)**.

- **FSW-12**, 100 T fuse (`curve_ref = FUSE_100T_EXAMPLE`) — off its **total-clear** curve between the 2,000 A (0.090 s) and 4,000 A (0.022 s) points, log-log: **0.023 s** *(primary, nearest the fault)*. *(The first edition of this guide said 0.05 s; the shipped curve gives 0.022 s at 4,000 A.)*
- **REC-7**, slow curve — with a recloser slow curve loaded as in §6.3 (the **illustrative** points 800 A/8 s, 1 kA/3.5 s, 2 kA/1.0 s, 4 kA/0.45 s, 8 kA/0.30 s — not shipped): **0.46 s** at 3,913 A.
- **CB-7 / R-7**, `IEC_SI`, pickup 400 A, TD 0.5 — computed exactly from the seeded curve form:

  ```
  M = I / pickup      = 3913 / 400                 = 9.78
  t = TD · interp(M)  = 0.5 × 3.001 s (between the stored M=8 and M=10 points)  ≈ 1.50 s   (backup)
  at exactly 4000 A:  M = 10 → 0.5 · 0.14 / (10^0.02 − 1) = 0.5 · 2.971 = 1.485 s
  ```

**The margins:**

| Pair (primary → backup) | margin | ≥ CTI 0.3 s? |
|---|---|---|
| FSW-12 (0.023) → REC-7 (0.462) | 0.438 s | ✓ |
| REC-7 (0.462) → CB-7 (1.501) | 1.039 s | ✓ |

**Overall: coordinated ✓** — the fuse clears first, the recloser backs it up, the feeder breaker is the last resort. Had the relay's TD been 0.15 (t ≈ 0.45 s), the REC-7 → CB-7 margin would collapse to **−0.01 s** and the study would flag a **miscoordination**.

```
   source ──[CB-7: R-7, IEC_SI, TD 0.5]── … ──[REC-7: slow, ampere curve]── … ──[FSW-12: 100 T]──× fault
             t ≈ 1.50 s  (backup)                t ≈ 0.46 s                        t ≈ 0.023 s  (primary)
                      └── margin 1.04 s ✓ ──┘          └── margin 0.44 s ✓ ──┘
```

### 7.2 The complete grading table for Feeder 7 (CTI 0.3 s)

| Fault at | Fault current | Graded devices (fault-first) | Operate times | Margins | Verdict |
|---|---|---|---|---|---|
| C-3 / TX-220 primary (below FSW-12) | 3,913 A | FSW-12 → REC-7 → CB-7 | 0.023 / 0.462 / 1.501 s | 0.438, 1.039 | coordinated |
| C-4 ring tail (no fuse below) | 2,796 A | REC-7 → CB-7 (ring ways unfused: skipped) | 0.680 / 1.770 s | 1.090 | coordinated |
| LV side of TX-220 (415 V) | 9,969 A at 415 V = **376 A at 11 kV** | FSW-12 → REC-7; **CB-7 does not operate (376 A < 400 A pickup)** | 23.18 s / 8.0 s (**extrapolated**) | −15.18 | *miscoordination* — a **false alarm**, see below |

The third row is the instructive one. The LV fault current referred to 11 kV is 9,969 × 415 / 11,000 = 376 A: below the relay's pickup, so the breaker never sees it; the 100 T fuse takes 23 s; and the recloser's curve starts at 800 A, so the engine clamps to the 800 A point (8 s) and flags `extrapolated`. The negative margin is an artefact of the clamp: a real recloser would not pick up at all. Treat any pair involving an `extrapolated` device as "unknown", not "failed" — and remember that LV faults are normally the fuse's job alone.

### 7.3 Where you start matters

| Study started at | Source impedance used | Fault level at TX-220 primary | FSW-12 row |
|---|---|---|---|
| T-ZS (the substation transformer) | its `source_r_ohm` / `source_x_ohm` (0.048 + j0.48 Ω) | 3,913 A | 0.023 s |
| CB-7 (the breaker) | none — the breaker has no source fields | **5,569 A** (+42 %) | 0.022 s, `extrapolated` (past the 4,000 A point) |

Start a study at the transformer (or any feature that carries `source_r_ohm`/`source_x_ohm`), not at a breaker, unless you mean to ignore the upstream network.

### 7.4 Mis-coordination case studies

| # | What was wrong | What the engine says | Fix |
|---|---|---|---|
| 1 | R-7 time dial 0.15 (was 0.5) | CB-7 0.450 s vs REC-7 0.462 s → margin **−0.011 s**, *miscoordinated* | raise TD, or lower the recloser's slow curve |
| 2 | R-7 `inst_pickup_a` 3,000 A (below the 3,913 A fault) | CB-7 operates in **0 s** → REC-7 → CB-7 margin **−0.461 s** | instantaneous pickup must sit above the largest fault *downstream of the next device* — the shipped seed uses 15,000 A for exactly this reason (`protection_network_example.sql:512`) |
| 3 | R-7 on `IEC_EI`, TD 0.2 | CB-7 0.169 s → margin −0.293 s | extremely-inverse is steep: it is fast at high current; pick the family the study needs |
| 4 | R-7 `DEFINITE_TIME` 0.8 s | CB-7 0.800 s → margin 0.339 s ✓ | works — but `time_dial` is ignored |
| 5 | REC-7 `slow_curve_ref` = `IEC_VI` (a relay curve) | REC-7 *"no total_clear points for curve IEC_VI"* → skipped; FSW-12 → CB-7 graded directly (1.478 s) | an ampere-quantity curve (§6.3) |
| 6 | FSW-12 `is_fused` left false | *"switch is not fused — no protection element to grade"*; REC-7 becomes the primary | set `is_fused`, `curve_ref` |
| 7 | no `protection_trip` link on CB-7 | CB-7 *"no PROTECTIVE_RELAY linked…"*; only FSW-12 → REC-7 graded | wire the relationship (§5) |
| 8 | CTI changed to 0.5 s | FSW-12 → REC-7 (0.438 s) now fails | CTI is a policy number — set it to your utility's value |
| 9 | REC-7 left open (locked out) before the study | *"fault feature is not downstream of start …"* | close it for the study; the engine cannot grade across an open device |

### 7.5 Reading a TCC plot

Each device is one trace on a shared log-log chart (current on the common voltage base on x, seconds on y); a vertical line marks the fault current; each curve crosses it at that device's operate time. A relay draws one trace (its operate curve, scaled by TD and truncated at any instantaneous pickup); a fuse draws two (minimum melt and total clear). Coordination is a *vertical gap* at the fault line: the upstream curve must sit at least CTI above the downstream one — and, for a fuse–recloser pair, across the **whole** current range where both can see the fault, not only at the one fault current you studied (§7.9).

### 7.6 What coordination does *not* check

The grading compares one operate time per device at **one** fault current. It does not model: pre-load or cold-load pickup; fuse pre-damage (cumulative heating) from earlier recloser shots; relay reset times and overshoot beyond the CTI; breaker opening time (it is part of what the CTI is for); ground-fault (`50_51N`) settings; directional or differential elements; CT saturation; the recloser's fast curve; sequence coordination; or the fault current's decay. §10 lists the rest.

### 7.7 Reclose sequences and lockout

A recloser on Feeder 7 (`reclose_attempts = 4`) with the fast curve first: **fast, fast, slow, slow** is the classic fuse-saving program. For a *transient* fault the first fast trip clears it, the recloser closes after the dead time (`reclose_interval_s`, 2 s here) and the feeder is back. For a *persistent* fault the recloser steps through its operations and then **locks out** — `device_state = OPEN` — until a crew closes it. In the model the only thing that records the outcome is `device_state`: nothing counts the shots. So after a lockout, set `REC-7.device_state = OPEN` (or let a switching-order step do it) and the §3 machinery takes over.

### 7.8 Sectionalizer counting

A sectionalizer (`SEC-4`, `count_to_lockout = 3`) has no fault-interrupting ability. It **counts** the recloser's trips for faults beyond it and, once the count is reached, **opens during the recloser's dead time**. Rule: `count_to_lockout` must be **less than** the recloser's operations to lockout (`reclose_attempts`), or the recloser locks out before the sectionalizer can clear the fault. With 4 operations and a count of 3:

| Fault is… | Operations | Result |
|---|---|---|
| beyond SEC-4 (persistent) | trip 1, 2, 3 (SEC counts 1, 2, 3 and opens in the 3rd dead time) | recloser's 4th close finds the fault isolated: **SEC-4 `OPEN`, REC-7 stays closed** — only the tail beyond SEC-4 stays out |
| upstream of SEC-4 (persistent) | 4 trips, SEC-4 sees no through-current and counts nothing | **REC-7 locks out `OPEN`** |
| transient | 1 trip | nothing changes |

In the model: after the beyond-SEC-4 case record `SEC-4.device_state = OPEN`. The sectionalizer is tier 300 with `boundary_role = switching`, so the section and isolate tools stop at it like any switch (§1); it still has no TCC, so coordination does not time-grade it.

### 7.9 Fuse-saving vs. fuse-blowing

- **Fuse-saving:** the recloser's *fast* curve operates **before** the fuse can melt, so a transient fault on the tee is cleared without a fuse blowing. Requirement: fast total-clear × K < fuse minimum-melt at that current (K ≈ 1.25 is a typical allowance for the fuse's earlier-shot heating — a rule of thumb, not a VoltEdge setting).
- **Fuse-blowing:** for a persistent fault the *slow* curve must be slower than the fuse's **total-clear**, so the fuse blows and isolates the tee while the recloser (after its fast shots) stays in service.

With the shipped `FUSE_100T_EXAMPLE` and the **illustrative** recloser curves (fast: 800 A/0.50 s, 1 kA/0.20 s, 2 kA/0.075 s, 4 kA/0.040 s, 8 kA/0.030 s), fuse-saving works only up to about **1.1 kA**: at 1 kA the fast shot takes 0.20 × 1.25 = 0.25 s against a minimum melt of 0.30 s (saved), but at 1.2 kA it is 0.19 s against 0.18 s (the fuse is damaged), at 2 kA it is 0.094 s against 0.045 s, and at the 3.9 kA fault level it is 0.051 s against **0.012 s** — there is no fuse-saving at all; the fuse will blow on the first fast shot. Fuse-blowing holds across the whole range (slow time above the fuse total-clear at every current from 500 A to 8 kA: 3.9 kA gives 0.46 s vs 0.023 s). The engine grades **only** the slow-vs-total-clear pair at one current; the fast curve and the fuse-saving window are checked by hand (the interactive edition's sequence simulator does it). The practical reading: on a 11 kV feeder with a 4 kA fault level and a 100 T tee fuse, run the recloser **fuse-blowing** (fast shots disabled, or a "slow only" program) unless the fuse is much bigger or the fault current at the tee is below ~1 kA.

---

## 8. Fault duty & arc-flash (cross-reference)

Protection isn't only about *timing* — the gear must **withstand and interrupt** the fault it sees. Two studies cover that; this guide points at them rather than repeating **Engineering Studies**:

- **Fault current — `POST /analysis/fault-current`** (`powerflow/fault.go`). The IEC 60909 three-phase (and SLG) fault level a device must make and break, including the **peak asymmetrical** current for breaker making-duty: `κ = 1.02 + 0.98·e^(−3R/X)`, `i_p = κ·√2·I_sym` (`fault.go:243-250`). Validation tier: **Validated**. At the Feeder 7 tee (R = 0.944, X = 1.320 Ω): κ = 1.135, I_sym = 3.91 kA, **i_p = 6.28 kA**.
- **Arc-flash — `POST /analysis/arc-flash`** (`powerflow/arcflash.go`, IEEE 1584-2002). Incident energy and PPE category — which **derive from the clearing time** the coordination evaluators above produce: the first resolving protective device from the fault toward the source, evaluated at the *arcing* current and at 85 % of it, the worse energy governing; the clearing time is capped at **2.0 s**; a slower-tripping device means more incident energy (`E ∝ t`). Tier: **Experimental** — *indicative only, never a basis for PPE selection or energized-work permits*.

On Feeder 7 (11 kV switchgear defaults: gap 153 mm, enclosed, working distance 910 mm, ungrounded; arcing current from the ≥1 kV equation = 3.86 kA):

| Clearing device (time read at the arcing current) | Time | Incident energy | PPE |
|---|---|---|---|
| FSW-12 fuse | 0.024 s | 0.14 cal/cm² | minimal |
| REC-7 (if the fuse were absent) | 0.47 s | 2.81 cal/cm² | Category 1 |
| CB-7 alone | 1.51 s | 9.04 cal/cm² | Category 3 |

⚠️ Two limits: the clearing time is the *relay operate time only* — no breaker opening time is added — and an instantaneous trip returns 0 s, i.e. **0 cal/cm²**. Both under-state real energy.

**The cross-check.** Whatever the studies say, VoltEdge stores **no kA interrupting or making rating** on the switchgear classes (a search of the switching seeds finds none), so it cannot flag "fault level exceeds breaker rating" — you compare `I_sym` and `i_p` with the nameplate yourself.

| Validation tier (Engineering Studies) | Study |
|---|---|
| Validated | Fault current |
| Derived | Coordination (TCC timing/grading not independently verified), Contingency |
| Experimental | Arc-flash |

---

# Part C — Switching and protection together

## 9. The trip → switch → reconfigure loop

The two parts meet in one cause-and-effect chain:

```
  fault ─▶ relay (51) senses over-current via its CT
        ─▶ relay operates the breaker   (protection_trip relationship)
        ─▶ breaker device_state ⟶ OPEN
        ─▶ trace/energisation re-read the live state
        ─▶ de-energised cone appears below the breaker; OMS shows the dead region
```

- **Protection chooses the device; switching is the result.** The relay's `protection_trip` link names the breaker it opens; coordination makes sure the *nearest* protective device opens first. When it operates, the only thing that changes in the data is the breaker's `device_state`.
- **A recloser does both in one feature** — it senses, trips (fast then slow), recloses, and finally locks out as `device_state = OPEN`.
- **OMS closes the loop for operators.** As a switching order runs, each open/close step flips a `device_state`; the live energisation model recomputes the incident's dead cone step by step, so the console shows exactly what is out and what each operation restores. Its prediction engine blames the *nearest protective device upstream of the calling customers* — only `MV_FUSE`, `MV_RECLOSER` and `MV_BREAKER` count (`oms/predict.go:38`); an `is_fused` switch (FSW-12, the RMU tee ways) and a sectionalizer are skipped, and the class walk is strictly directional with **no** junction pass-through, so a customer fed through an RMU's one-way bus can come back with *no* device (an unattributed cluster for a human to assign). The same directed walk produces the incident's *affected customers* and the switching order's *verify* count, so supplies behind that bus are **not counted there** — while the energisation view (the cone) does include them (on Feeder 7 the clinic SP-230 is missing from REC-7's affected count of 1 service point / 85 customers). Confidence = distinct calling service points ÷ service points below the device.

> **How to — see what's de-energised** 🔒 `trace:run`
> 1. On the map toolbar, click **Schematic** to open the temporary one-line of your selection (or current view).
> 2. Toggle **Live** (⚡) in the one-line toolbar — it shades every de-energised feature (anything below an open device) in grey and renders open vs. closed switches distinctly, with a *De-energised* swatch in the legend.
> 3. On the main map, de-energised regions appear automatically when an **OMS incident** is open (drawn red/amber downstream of the fault) — that view is driven by OMS, not a manual toggle.

> **Worked example — a fault and its restoration on Feeder 7.** Follow the loop end to end:
> 1. **Fault** below FSW-12. Relay **R-7** (50/51) picks up; by the coordination above the **fuse FSW-12** clears first (~0.023 s) — only **TX-220** goes dark. The relay/CB are the backup that never had to operate.
> 2. **If instead the fault is on C-2 (the trunk between REC-7 and the tees) and REC-7 locks out:** `REC-7.device_state → OPEN`. The one-line's **Live** shading greys the whole tail — FSW-12/TX-220, RMU-5/TX-230 and beyond — *and* the conductor/loads beyond the open tie T-79 (§3.2).
> 3. **Isolate.** *Isolate from here* on REC-7 shows the dead tail, SP-230 (the clinic, P2) as critical, and flags the node beyond T-79 (C-9) as a back-feed path. To keep the tail on supply while C-2 is repaired you must **open the two switches on the load side of the fault**: the RMU ring-in `Way A` (so C-2 is cut off from the RMU) and `FSW-12` (so C-2 is cut off from TX-220).
> 4. **Restore through the tie.** With REC-7, Way A and FSW-12 open, run **Contingency & backfeed…** from Feeder 9's source (T-ZS9). The study sees T-79 as the open boundary the base sweep halted at and re-sweeps with it closed: it **restores `C-4b … TX-230/SP-230` — 260 kW, the clinic — feasible**, lowest voltage ≈ 0.977 pu, no overloads (figures from the interactive edition's simplified constant-power sweep over the illustrative impedances; the server's sweep will differ in the last digits, the structure of the answer will not). TX-220/SP-220 stay dark (they hang off C-2, which is still isolated). Then close **T-79** (`device_state OPEN → CLOSED`, which also executes the "close" in a switching order). Re-close REC-7/Way A/FSW-12 once C-2 is repaired and reopen T-79 to put the feeders back to normal.
> 5. **What the shading shows after step 4.** The Live view still greys the tail, because the open REC-7 (and Way A) still have it in their cones — a closed tie does *not* subtract (§3.2, §3.6). The authority on "who is supplied" is the contingency sweep, not the shading.

**The three views of "dead" in that scenario:**

| State | Live shading (union of cones) | OMS incident cone (blamed REC-7) | Actually supplied (source-rooted) |
|---|---|---|---|
| REC-7 open, rest closed, T-79 open | C-2 … tail, T-79, **and C-9/TX-910/SP-910** (beyond the NOP) | C-2 … tail | F9 live; tail dead |
| + Way A and FSW-12 open | same set (Way A's and FSW-12's own cones cover the rest) | C-2, FSW-12, Way A only (**shrunk**: the walk stops at the open ways) | F9 live; tail dead |
| + T-79 closed | tail **still grey**; the cones now also reach F9's breaker and bus (`CB-9`, `B9`, `C-90`) through the closed tie | unchanged | F9 + C-4 … RMU bus + TX-230/SP-230 supplied; C-2 and TX-220 dead |

(The interactive edition computes all three columns for any switching state you set.)

**Two regions, don't conflate them (recap).** A **switching section** (bounded by switches *and* protective devices) is *what you can isolate to work on*. A **protection zone** (bounded by protective devices and transformers only) is *what goes dark when protection trips here*. A plain switch bounds the first, never the second — because a switch doesn't clear a fault.

**Reconfiguration checklist (the contingency study's rules).** Candidates are the open boundary switches the donor's base sweep halts at, normally-open ones ranked first; closing one that forms a loop is reported *"forms a loop/mesh … run LOOP_CHECK_TRACE"* (a parallel path, not a radial transfer); one that energises nothing says so; an option is **feasible** only if it restores load **and** causes no voltage violation (band 0.95–1.05 pu by default — `powerflow/profile.go:16-17`) and no overload (conductor/transformer loading above 100 %). The study is **read-only** — it overrides one barrier per candidate and mutates nothing. Validation tier: **Derived**.

---

## 10. What the model does **not** do

| Expectation | Reality |
|---|---|
| "Opening a device re-routes power automatically" | No. Only `device_state` changes; nothing re-feeds a region. A tie must be closed by a person or a switching step. |
| "Closing the tie clears the Live shading" | No — a tie-fed region stays in the open device's cone (§3.2). Use the contingency study. |
| "The Live view shows what is unsupplied" | It shows what is downstream of an open device, including the far side of every normally-open tie. |
| "The upstream boundary of an isolate is always an upstream device" | Not guaranteed; the walk starts undirected and returns the first hit of an unordered set (§3.4). |
| "A recloser counts its shots" | No. `reclose_attempts`, `reclose_interval_s` and `reset_time_s` are stored, never simulated; lockout is whatever you set `device_state` to. |
| "A sectionalizer counts automatically" | No: it has `count_to_lockout` but **no counting engine** — you record the outcome in `device_state`. It *does* carry `boundary_role = switching`, `functional_role = controller` and tier 300, so it bounds sections and isolation like any switch. |
| "Coordination checks the recloser's fast curve" | No — slow curve only, at one current. |
| "The fuse is chosen/sized for me" | No. `fuse_rating_a` and `fuse_speed` are labels; only `curve_ref` matters, and only one example fuse curve ships. |
| "CT ratio / saturation / burden is modelled" | No. `ct_ratio` and `vt_ratio` are recorded; pickup is in primary amps. |
| "The relay's other functions run" | Only overcurrent (`curve_type`, pickup, TD, instantaneous, definite time) — and `device_number` is never consulted, so the ANSI code neither enables nor restricts anything. 27/59/81/87/67 … have no simulation; `protection_sensing` is never read. |
| "A fault is simulated and the breaker trips" | No. There is no time-domain simulation: a fault location is a *question* to the grader, not an event; `device_state` changes only by an edit or a switching step. |
| "Switchgear duty is compared with its rating" | No kA rating is stored on the classes. |
| "Arc-flash includes breaker opening time" | No; and an instantaneous trip yields 0 cal/cm² (§8). |
| "Earthing is tracked" | The `earth` switching step is a tick with no device; `MV_EARTH_SWITCH` is not on the connectivity graph (`network_role = 'none'`). |
| "OMS blames a fused switch / counts every customer" | An `is_fused` `MV_SWITCH` is never blamed — only `MV_FUSE`, `MV_RECLOSER`, `MV_BREAKER` are. The class walk behind prediction, *affected customers* and the order's *verify* count now passes through junction buses (`trace/class_walk.go`, `walkWithJunctions`), so a customer behind an RMU bus is found; it is still barrier-free, so it can count supplies that a closed tie or an open device further down has already re-fed or cut off, which the live energisation cone accounts for. |
| "Interlocks / permit-to-work" | Not modelled. |
| "Several relays on one breaker are combined" | The first (oldest link) is graded, with a note. |
| "A curve can be added in the UI" | No — SQL only (§6.3). |
| "Power-flow engines honour every barrier rule" | They keep **one** barrier rule per class (`powerflow/graph.go:156`, a map overwrite) and `LoadTraceBarrierRules` has no `ORDER BY` (`metadata/load.go:740`). A class carrying both a `device_state` and an `operational_state` rule may therefore be judged on only one of them in load flow / fault / coordination. **Not verified at runtime** — test it on your data: open a device, run a study across it, and expect the *"not downstream of start"* refusal. |

---

## 11. Troubleshooting

| Symptom | Likely cause | Where to look |
|---|---|---|
| Coordination says *"fault feature is not downstream of start"* | the fault is behind an open device, or not below the start | open devices on the path (§3); start/fault picked the wrong way round |
| A device shows no operate time | no relay link / pickup / curve; at or below pickup; unfused switch; missing `curve_ref`; no `total_clear` points | the device row's note |
| A recloser disappears from the grading | `slow_curve_ref`/`fast_curve_ref` empty or a relay curve code | §5, §6.3 |
| Pair margin is hugely negative at a small fault | a clamped (`extrapolated`) curve | §6.2, §7.2 |
| Fault level looks too high | study started at a breaker (no source impedance) | §7.3 |
| Relay "operates" in 0 s | `inst_pickup_a` at or below the through-current | relay settings |
| Isolate button missing | the selected class is not a `junction`, or no `DOWNSTREAM` trace configuration exists | `feature-panel.tsx:2751` |
| Isolate returns an odd "upstream boundary" | start is a junction, boundary is the first of an unordered hit set | §3.4 |
| Live view greys a feeder that is energised | it is downstream of a normally-open tie | §3.2 |
| After closing the tie the region stays grey | cones do not subtract back-feed | §3.6 |
| A sectionalizer doesn't stop the section | the taxonomy seeds weren't re-applied after the fix | re-run `trace_tiers` / `boundary_role_backfill` / `functional_role_backfill` + recompile (§1) |
| Tie creation fails | no geometry; same feeder twice; no network rule between the classes | §4 |
| RMU creation fails for an editor | missing `relationships:commit` | §4 |
| `PLANNED → ISOLATED` refused | not a legal edge; commission to `IN_SERVICE` first | §2 |
| Can't change a switch position | needs `features:write` (viewer/analyst cannot) | persona table |
| Switching order won't generate | the incident has no blamed device, or already has a live order | §2 |
| Load flow runs straight through an open switch | the one-rule-per-class barrier map (§10) | verify on your data |

---

## Appendix — reference

### A. State domains

| Domain | Values |
|---|---|
| `DEVICE_STATE` | `CLOSED`, `OPEN` |
| `OPERATIONAL_STATE` | `PLANNED`, `IN_SERVICE`, `ISOLATED`, `RETIRED` (state machine) |
| `NORMAL_STATE` | `NORMALLY_CLOSED`, `NORMALLY_OPEN` |
| `SWITCH_FUNCTION` | `FEEDER`, `INCOMER`, `BUS_COUPLER`, `BUS_SECTION`, `TRANSFORMER` |
| `CONDITION_RATING` | `GOOD`, `FAIR`, `POOR` |
| `RMU_CONFIG` | `2W_1T` (2 ring + 1 transformer), `2W_2T` (2 ring + 2 transformer) |
| `LOAD_PRIORITY` | `P1` life support, `P2` essential, `P3`, `P4` (critical = P1–P2) |

### B. Protection domains

| Domain | Values |
|---|---|
| `ANSI_DEVICE_NUMBER` | `27`, `59`, `50_51`, `50_51N`, `86`, `87`, `25`, `32`, `37`, `46`, `49`, `64`, `67`, `67N`, `79`, `81` |
| `TCC_CURVE` | `IEC_SI`, `IEC_VI`, `IEC_EI`, `IEC_LTI`, `IEEE_MI`, `IEEE_VI`, `IEEE_EI`, `DEFINITE_TIME` |
| `CT_RATIO` | `100_5`, `200_5`, `400_5`, `800_5`, `1600_5` |
| `VT_RATIO` | `11KV_110`, `33KV_110` |
| `FUSE_SPEED` | `K`, `T`, `SLOW`, `FAST` |

### C. Formulas

```
Relay operate time:   t = time_dial × interp( I_through / pickup_current_a )     (log-log on stored points)
                      t = 0 if inst_pickup_a is set and I_through ≥ inst_pickup_a
                      t = definite_time_s (unscaled) for DEFINITE_TIME above pickup;  none if M ≤ 1
Seeded curve points:  t = coeff / ( M^exp − 1 ) + b          (M = I/pickup, at time_dial = 1)
Fuse / recloser:      t = interp_total_clear( I_through )    (ampere curve; minimum_melt is drawn only)
Fault current:        I = (V_base / √3) / |Z|   (|Z| = √(R²+X²) of source + path; line_to_neutral drops the √3)
Peak:                 κ = 1.02 + 0.98·e^(−3R/X) ;  i_p = κ·√2·I_sym
Through-fault refer:  I_device = I_fault × V_fault / V_device
Coordination margin:  margin = t_backup − t_primary ;  coordinated ⟺ margin ≥ CTI  (default 0.3 s)
Transformer Z (2-wd): Z = (%Z/100)·V_sec²/(kVA·1000);  R = Z/√(1+xr²), X = R·xr   (xr default 2.0)
Arc-flash (≥1 kV):    lg Ia = 0.00402 + 0.983·lg Ibf ;  lg En = k1 + k2 + 1.081·lg Ia + 0.0011·gap
                      E = Cf·En·(t/0.2)·(610/D)^x      (Cf = 1.0 above 1 kV; k1 −0.555 enclosed/−0.792 open; k2 −0.113 grounded)
```

### D. Corrections made to the first edition (with evidence)

| # | The guide said | The code/seed says | Evidence |
|---|---|---|---|
| 1 | FSW-12 clears in ≈ 0.05 s at 4 kA | 4,000 A total-clear point is **0.022 s** (3,913 A → 0.023 s) | `seed/protection_curves_example.sql:200`; `powerflow/curves.go:102` |
| 2 | REC-7 has `fast IEC_SI (TD 0.1) · slow IEC_VI (TD 0.3)`, graded 0.45 s | recloser curve fields name **ampere-quantity** codes, no TD; a relay code there leaves the recloser unresolved (*no total_clear points*) | `seed/mv_recloser_example.sql:8-18,93-97`; `powerflow/coordination.go:578-623` |
| 3 | "eight seeded IEC/IEEE families" | seven tabulated families + `DEFINITE_TIME` (no points) + one example fuse curve | `seed/protection_curves_example.sql:68-72,146-173` |
| 4 | curve constants listed for 3 curves | full set: `IEC_VI` 13.5/1.0, `IEC_LTI` 120/1.0, `IEEE_VI` 19.61/2.0/0.491, `IEEE_EI` 28.2/2.0/0.1217 | `seed/protection_curves_example.sql:164-171` |
| 5 | `normal_state` on `MV_SWITCH`/`MV_BREAKER`/`MV_ISOLATOR`; nothing consumes it | also on `MV_SECTIONALIZER`, `MV_RECLOSER`; the contingency study ranks `NORMALLY_OPEN` first | `seed/switch_normal_state_fields.sql:100`; `powerflow/contingency.go:12-18,142` |
| 6 | `MV_SECTIONALIZER` has no notable fields | `count_to_lockout`, `normal_state` | `seed/protection_curves_example.sql:121,138`; `seed/switch_normal_state_fields.sql:100` |
| 7 | transformers: "downstream boundary … energisation, isolate, feeder, section traces never cross" | only the interactive map trace stops at transformers; every other consumer **passes through** | `trace/graph.go:658-668` |
| 8 | "A `junction` (busbar, RMU internal bus) is expanded undirected" | **every** `network_role='junction'` class (all switches, transformers, service points) is; walks after a junction are "unlocked" | `trace/graph.go:806,886`; `seed/mv_lv_switch_example.sql:80` |
| 9 | trace configurations: four, `SHORTEST_PATH` doesn't stop | eight; `LOOP_CHECK_TRACE` (shortest path) **does** stop at open devices | `seed/loop_check_example.sql:31`; `trace_target_class_example.sql:37`; `trace_configurations_example.sql:92` |
| 10 | barrier = `device_state OPEN` or `operational_state ISOLATED/RETIRED` on any config with `stop_at_open_devices` | `operational_state` rules attach to `DOWNSTREAM_TRACE`/`UPSTREAM_TRACE` only | `seed/operational_state_barriers.sql:49` |
| 11 | section/zone role sets omit `source`; isolation set not stated | switching section = protective+switching+transformer+source; zone = protective+transformer+source; isolation = protective+switching | `trace/class_walk.go:94-96` |
| 12 | isolate FSW-12: "upstream boundary is FSW-12 itself" | the start is never its own boundary; hits are REC-7 and the sibling Way A (first of an unordered set returned) | `trace/graph.go:783`; `trace/isolate.go:293` |
| 13 | isolate REC-7 "flags **T-79** as a back-feed path" | T-79 is a visited barrier in the dead list; the back-feed entry is the node **beyond** it (C-9) | `trace/isolate.go:194-209`; engine port |
| 14 | critical facilities "by `load_priority`" | rank ≤ 2 (P1–P2); `is_critical` only a fallback | `trace/isolate.go:19-22`; `trace/service.go:71` |
| 15 | §9 step 3: closing T-79 shrinks the grey cone | it does not — cones never subtract a back-feed; the far side of a NOP is in the open tie's cone | `trace/energization.go:27-31` |
| 16 | RMU 🔒 `features:write` + `connectivity:commit` | also `relationships:commit` | `api/internal/rmu/http.go:36-37` |
| 17 | tie/coupler "pick the switch class" with no geometry note | tie requires map geometry; wiring direction A→T1, T2→B; feeders must differ | `tie/fanout.go:140,149`; `coupler/fanout.go:134` |
| 18 | coupler/breaker has the three conductor rules | breakers/isolators/sectionalizers additionally have the bus rules (bus T2→T1, T2→bus T1); there is no device↔device rule | `seed/mv_busbar_example.sql:154-185`; `coupler/fanout.go:19-22` |
| 19 | `protection_trip` targets `MV_BREAKER`, `DIST_SWITCH`, `MV_RECLOSER` | plus `MV_SWITCH` after the DIST_SWITCH migration; only a breaker's relay is ever read | `seed/migrate_dist_switch_to_mv_switch.sql:302-322`; `coordination.go:437-445` |
| 20 | "(Flagged experimental / safety — see Engineering Studies)" for arc-flash | confirmed (Experimental tier); fault = Validated, coordination/contingency = Derived | `docs/engineering-studies.md:61-73` |
| 21 | "Adding a brand-new curve is an admin job" | SQL only — no API or UI; not `metadata:write` | search of `api/internal`, `web/src` |
| 22 | split helper text "Click the line where you want to cut it." | "…cut it into a new feature" | `web/src/components/map/geometry-edit-toolbar.tsx:100` |
| 23 | the dispatcher is a role | there is no `dispatcher` role; Priya is an `editor` | `migrations/0025_create_rbac.sql:90-95` |
| 24 | trace-tier list was MV switchgear / LV_SWITCH / transformers only | full list incl. `MV_EARTH_SWITCH`, `MV_LV_SWITCH`, `MV_CT/VT`, `MV_BUSBAR`, `MV_CONDUCTOR`, `AUX_TRANSFORMER`, `GENERATOR` | `seed/trace_tiers.sql:44-72` |
| 25 | a relay whose `device_number` is `27`/`87` is "stored but not simulated" | `device_number` is never read by any engine; a linked relay is graded from its overcurrent settings whatever its ANSI code | `powerflow/coordination.go:437-516` (no read of `device_number`) |
| 26 | OMS "affected customers" and the switching order's *verify* count the whole dead cone | **Fixed:** they used a strictly directed class walk (no junction pass-through), missing supplies behind an RMU's one-way bus; the class walks now share `bfs`'s junction pass-through and tier guard, so they agree with `IncidentEnergization` (the cone) on bus-fed supplies | `trace/class_walk.go` (`walkWithJunctions`); `trace/service.go:432-445`; `oms/predict.go:240` |
| 27 | Isolate menu item is "greyed out" for a non-junction | it is **hidden** (`canIsolate` gates the render) | `web/src/components/map/feature-panel.tsx:2751,2985` |

*Not verified at runtime* (code reading only; the build environment here cannot run the stack): the isolate boundary ordering (#12), the Live-view false positive at a NOP (§3.2), the single-barrier-rule-per-class overwrite (§10), and the OMS no-device case behind an RMU bus (§9).

### E. Quick reference — recipes that exist

```
task seed-mv-switching            # MV_BREAKER, MV_ISOLATOR, MV_SECTIONALIZER (+ rules)
task seed-mv-lv-switch            # MV_SWITCH, LV_SWITCH   (then compile CODE=MV_SWITCH / LV_SWITCH)
task seed-mv-recloser             # MV_RECLOSER
task seed-mv-protection           # MV_FUSE
task seed-mv-measuring            # MV_CT, MV_VT
task seed-mv-relay                # PROTECTIVE_RELAY + protection_trip / protection_sensing
task seed-protection-curves       # settings fields + the TCC catalogue + FUSE_100T_EXAMPLE
task seed-switch-normal-state     # normal_state on isolator/switch/sectionalizer/recloser
task seed-trace-tiers             # trace_tier stamps
task seed-boundary-roles          # boundary_role stamps
task seed-protection-network      # a gradeable chain: SUB-SOURCE → BRK-1 (+RLY-1) → FUSE-1 → TX-1
```

| Question | Call |
|---|---|
| What dies if I open it? | `POST /trace/isolate` (`trace:run`) |
| Section / zone around a feature | `POST /trace/section` (`protection_zone: true` for the zone) |
| Are the devices graded in order? | `POST /analysis/coordination` (`analysis:run`) |
| Fault level / making duty | `POST /analysis/fault-current` |
| Incident energy | `POST /analysis/arc-flash` (experimental) |
| What can the other feeder pick up? | `POST /analysis/contingency` |
| Place a tie / coupler / RMU | `POST /ties`, `/couplers`, `/rmus` |
| Generate / tick a switching order | `POST /oms/incidents/{id}/switching-orders`, `POST /oms/switching-steps/{id}/execute` (`oms:write`) |

### What is *not* a class (modelling notes)

- **Bus coupler** = `MV_BREAKER` + `switch_function = BUS_COUPLER`. **Feeder tie / NOP** = `MV_SWITCH` + `normal_state = NORMALLY_OPEN` + `device_state = OPEN`. **RMU way** = `MV_SWITCH` contained by `RMU`.
- No `CIRCUIT_BREAKER` (→ `MV_BREAKER`), `DISCONNECTOR` (→ `MV_ISOLATOR`), `LOAD_BREAK` (→ `MV_SWITCH`), `RMU_WAY`/`RMU_BUSBAR` (→ `MV_SWITCH`/`MV_BUSBAR`).
- `DIST_SWITCH` is legacy, superseded by `MV_SWITCH`.
- `MV_SECTIONALIZER` carries `device_state` (so it bars a trace when open) **and** is now tier 300, `functional_role = controller`, `boundary_role = switching` — so it bounds switching sections and isolation like any switch. It is deliberately *not* `protective` (no fault interruption → never bounds a protection zone), and its `count_to_lockout` is stored but not simulated.
- `MV_CT` / `MV_VT` have **no** `device_state` and are never trace barriers.

### Related guides

- **Feeders, Sections & Subnetworks** — the sectioning backbone (`boundary_role`, switching sections vs protection zones, section highlighting).
- **Engineering Studies** — the fault-current, coordination and arc-flash math in depth, with worked numbers.
- **Substation Guide** — where this switchgear lives: bays, panels and the live one-line.
