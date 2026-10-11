# Switching & Protection

**Written for:** a network modeller, control engineer or protection engineer representing switchgear and protection in VoltEdge — what each device *is*, how its state reconfigures the live network, and how the protection devices grade against each other.

This guide is the companion to the interactive **Switching & Protection** guide (Interactive guides). It leans on two other manuals rather than repeating them: **Feeders, Sections & Subnetworks** for the sectioning backbone, and **Engineering Studies** for the fault, coordination and arc-flash *math*. It is written in two layers:

- **▶ Operator** — what the device is, what to enter, what happens when you throw it.
- **∑ Engineer** — the model: the classes, state axes, barrier rules and the grading engine, with their source in the seed and solver code.

**Who this is for.** Network modellers capturing switchgear and protection; control engineers who operate the model (open/close, isolate, restore); and protection engineers who set relays and check coordination.

**The cast.** **Dana** (editor/field supervisor) captures the switches, breakers and RMUs and wires the relays; **Priya** (dispatcher) opens and closes devices, isolates for work and watches what goes dark; **Tom** (analyst) runs coordination and fault studies; **Ada** (admin) authors the classes, domains and curves behind it all.

**Legend.** 🔒 an action that needs a permission (named inline). 💡 a tip. ⚠️ a caution or a known limitation stated honestly. **▶** the build/operate lane; **∑** the model/engine lane. A boxed **How to** is a click-by-click procedure; a **Worked example** carries real numbers you can reproduce.

**Running example — Feeder 7.** An 11 kV distribution feeder off a zone substation. We build it up through the guide: a feeder breaker with its relay and instrument transformers, a mid-feeder recloser, a fused tee-off to a distribution transformer, a ring main unit, and a normally-open tie to the next feeder.

```
  Zone S/S 11 kV bus ═══════════════════ B7
         │
      [CB-7]  MV_BREAKER · FEEDER           relay R-7  : 50/51, IEC_SI, pickup 400 A, TD 0.5
         │                                  sensing    : CT-7 (400/5), VT-7 (11 kV/110)
      MV_CONDUCTOR  (Feeder 7, 11 kV)
         │
      [REC-7] MV_RECLOSER                   fast IEC_SI (TD 0.1) · slow IEC_VI (TD 0.3) · 3 shots
         ├──[FSW-12] MV_SWITCH is_fused (100 T) ──► DIST_XFMR TX-220 (11 kV/415 V) ──► LV
         │
      [RMU-5] RMU 2W_1T ──(fused way)──► DIST_XFMR TX-230
         │   (ring ways continue…)
         ┆
      [T-79] MV_SWITCH  — normal-open tie  ╌╌╌► Feeder 9     (normal_state NORMALLY_OPEN, device_state OPEN)
```

> **Grounding note.** Everything here is drawn from the shipped seed and engine, not an idealised description. Switching/protection classes and domains live in `seed/*.sql` (`mv_lv_switch_example.sql`, `mv_switching_example.sql`, `mv_recloser_example.sql`, `mv_protection_example.sql`, `protective_relay_example.sql`, `mv_measuring_example.sql`, `protection_curves_example.sql`); the behaviour lives in `api/internal/trace` (barriers, isolate, energization, sectioning), `api/internal/powerflow` (coordination, fault, arc-flash) and `api/internal/oms` (live switching). Where the design intent and the shipped behaviour differ, this guide follows the code and says so.

**What's inside**

- **Part A — Switching:** §1 the devices (+ *how to place a switch*, *split a line*) · §2 the state axes (+ *how to open/close*) · §3 what opening a device does — barriers, the de-energised cone, isolate (+ *how to isolate*), sections vs. zones · §4 ties, couplers, RMUs (+ *how-tos*).
- **Part B — Protection:** §5 the devices (+ *how to wire a relay to a breaker*) · §6 TCC curves (+ *how to set a curve*) · §7 coordination (+ *how to run a study*, worked margins) · §8 fault & arc-flash (cross-reference).
- **Part C — Together:** §9 the trip → switch → reconfigure loop (+ *how to see what's de-energised*, a worked restoration) · Appendix — domains, formulas, modelling notes.

---

# Part A — Switching

## 1. The switching devices

Every switching device in VoltEdge is a **point feature** on the MV or LV network with `network_role = 'junction'` — it sits *inline* on a conductor and can interrupt it. They share one shape:

- Two terminals: **T1 = primary** (line side) and **T2 = secondary** (load side).
- Three connectivity rules wire them in: a conductor lands on T1, a conductor lands on T2, and T2 feeds a conductor onward — so a downstream trace walks *through* a closed device and *stops* at an open one.
- Two fields are always required: **`device_state`** (the live open/closed position) and **`condition_rating`** (asset health). Protection-grading fields are optional.

The classes:

| Class | What it is | Subtypes | Notable fields |
|---|---|---|---|
| **`MV_SWITCH`** | Generic MV switch / load-break switch. Also the **RMU way** and the default **feeder-tie** switch. | `standard` | `is_fused`, `fuse_rating_a`, `curve_ref`, `normal_state` |
| **`LV_SWITCH`** | LV sibling of `MV_SWITCH`. | `standard` | `is_fused`, `fuse_rating_a`, `curve_ref` |
| **`MV_BREAKER`** | Substation / feeder **circuit breaker** — interrupts fault current. Also the **bus-coupler** device. | `vacuum_breaker`, `sf6_breaker` | `switch_function`, `normal_state` |
| **`MV_ISOLATOR`** | Off-load **disconnector / isolator** — a visible isolating gap, switched dead. | `single_break`, `double_break` | `normal_state` |
| **`MV_RECLOSER`** | Self-contained multi-shot **protective** recloser (see Part B). | `standard` | `reclose_attempts`, `fast_curve_ref`, `slow_curve_ref`, `reclose_interval_s`, `reset_time_s` |
| **`MV_SECTIONALIZER`** | Counts fault passages, opens on a dead line. | `hydraulic`, `electronic` | — |
| **`MV_FUSE`** | Inline MV **fuse** — a switching *and* protective device (Part B). | `expulsion_fuse`, `current_limiting_fuse` | `fuse_rating_a`, `fuse_speed`, `curve_ref` |
| **`RMU`** | **Ring main unit** — a `container`, not an inline switch. Holds an internal busbar and its switched ways. | `standard` | `rmu_config` (`2W_1T`, `2W_2T`) |

> **∑ Three things are *fields or assemblies*, not their own classes** — a common modelling trap:
> - A **bus coupler** is an `MV_BREAKER` with `switch_function = BUS_COUPLER`, wired bus-to-bus.
> - A **feeder tie / normal-open point** is an `MV_SWITCH` with `normal_state = NORMALLY_OPEN`, wired conductor-to-conductor.
> - An **RMU way** is a plain `MV_SWITCH` contained by the `RMU`; a ring vs. transformer way is distinguished by `is_fused`. The RMU's internal bus is a plain `MV_BUSBAR`. (The old `RMU_WAY`/`RMU_BUSBAR` classes were collapsed into these.)
>
> There is also no literal `CIRCUIT_BREAKER` (it's `MV_BREAKER`), `DISCONNECTOR` (it's `MV_ISOLATOR`), or `LOAD_BREAK` class — a load-break switch is modelled as a generic `MV_SWITCH`. **`DIST_SWITCH`** is the original, now-legacy switch class, superseded by `MV_SWITCH`.

**Where each sits in the hierarchy (∑).** Three orthogonal taxonomies tag these classes; they drive the trace and sectioning behaviour in §3:

- **`trace_tier`** (the grid layer, for the cross-layer walk guard): the MV switchgear family — `MV_SWITCH`, `MV_ISOLATOR`, `MV_RECLOSER`, `MV_BREAKER`, `MV_FUSE` — is tier **300**; `LV_SWITCH` is **500**; transformers sit between (`POWER_XFMR` 200, `DIST_XFMR` 400). `MV_SECTIONALIZER` and legacy `DIST_SWITCH` are **untiered** (transparent to the guard).
- **`functional_role`**: every state-bearing switch is a **`controller`** — "the only role whose state can stop a trace" (`LV_SWITCH`, `MV_SWITCH`, `MV_ISOLATOR`, `MV_RECLOSER`, `MV_BREAKER`, `MV_FUSE`, `MV_EARTH_SWITCH`, `SERVICE_CUTOUT`). `MV_SECTIONALIZER` is **not** listed — a known gap.
- **`boundary_role`** (what bounds a section): `protective` = `MV_BREAKER`, `MV_RECLOSER`, `MV_FUSE`, `SERVICE_CUTOUT`, `LV_FUSE`; `switching` = `MV_SWITCH`, `MV_ISOLATOR`, `LV_SWITCH`, `LV_LINK_BOX`; `transformer` = `DIST_XFMR`, `POWER_XFMR`. (`MV_EARTH_SWITCH` is deliberately **not** a boundary.)

> **How to — place a switch on the map** 🔒 `features:write` + `connectivity:commit`
> 1. Map toolbar (top-right) → **➕ New feature** → pick the class (type "MV switch" in the search) → click **MV Switch**.
> 2. The banner reads *"Click the map to place the new feature."* Click where the switch goes — on or beside the conductor it interrupts.
> 3. The create form opens. Set the required fields — **`device_state`** (`CLOSED` to start), **`condition_rating`** — then any protection fields (leave `is_fused` off for a plain switch). **Save.**
> 4. Wire it in: select the switch → **Terminals** tab → **➕ New connection** → click the line-side conductor, pick terminal **T1**; repeat for the load-side conductor on **T2**. (The three switch rules authorise exactly these joins.)
>
> 💡 Prefer to drop a device **mid-span** on an existing conductor? Don't try to place it exactly on the line — split the line first:
>
> > **How to — split a conductor to insert a device**
> > 1. Select the conductor → **Edit** (pencil, panel header) → the floating geometry toolbar appears → click **Split** (scissors).
> > 2. Helper text: *"Click the line where you want to cut it."* Click the point. The far half becomes a new conductor feature (`POST /features/{code}/{id}/split`).
> > 3. Now place your switch at the cut and connect T1 to one half, T2 to the other.
> >
> > ⚠️ Split only cuts the line into two conductors — it does **not** drop a device for you. Placing and wiring the switch is the step above.

---

## 2. The two state axes (plus normal state)

A switching device carries **two independent state columns**. Confusing them is the number-one modelling mistake:

1. **`device_state`** — the **live, operational position** right now. Coded values: **`CLOSED`** and **`OPEN`**. *This is the only field that reconfigures the live network* — an `OPEN` device is a barrier to every trace, de-energises what's below it, and shrinks in OMS as crew close it back.
2. **`operational_state`** — the **asset lifecycle**, an entirely different axis. Coded values: **`PLANNED` → `IN_SERVICE` → `ISOLATED` → `RETIRED`**. `ISOLATED` and `RETIRED` *also* block a trace (the asset isn't really there), but they describe the *asset's status*, not a switch position.

> **▶** `device_state = OPEN` means "the switch is open, flip it and power flows." `operational_state = ISOLATED` means "this asset is out of commission." A closed switch that is `ISOLATED` still blocks flow — because the *asset* is unavailable, not because it's switched open.

**`operational_state` is a state machine (∑).** Illegal jumps are refused with a `400` at write time (`crud.UpdateFeature`, enforced via `metadata.domain_transitions`). The legal edges:

```
PLANNED ──▶ IN_SERVICE ──▶ ISOLATED ──▶ IN_SERVICE   (restore)
   │             │             │
   └──▶ RETIRED  └──▶ RETIRED  └──▶ RETIRED
```

`NULL → any` and `any → NULL` are always allowed.

**`normal_state` and `switch_function` (∑).** Two more descriptive fields, carried by the MV switching classes (not LV):

- **`normal_state`** — the **intended** position in normal running: `NORMALLY_CLOSED` or `NORMALLY_OPEN`. This is a *label* — it does **not** itself block flow; only `device_state = OPEN` does. A normal-open tie is both `normal_state = NORMALLY_OPEN` *and* born `device_state = OPEN`.
- **`switch_function`** (on `MV_BREAKER`) — the bay role: `FEEDER`, `INCOMER`, `BUS_COUPLER`, `BUS_SECTION`, `TRANSFORMER`.

**Who sets state.** Both state columns are ordinary attributes edited through the generic write path (REST `PATCH /features/...`, the map's attribute editor, or the `update_feature` MCP tool). The **OMS module** flips `device_state` programmatically as crew execute a switching plan — each step opens or closes a device inside the same transaction that ticks the step, and the change is history-logged (`oms/switching.go`).

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

The engine resolves a set of **barrier rules** for each trace (`trace_barrier_rules`: "on class X, if field *f* equals value *v*, halt here"). For switching that means: **`device_state = OPEN`** on any controller class, **or** `operational_state IN (ISOLATED, RETIRED)`. At each step the walk reads the device's *current* attributes and stops **at** the barrier — the device is reported, but nothing beyond it is reached.

Barriers are only consulted when a trace configuration has `stop_at_open_devices = true`. The shipped configs:

| Trace | Stops at open devices? |
|---|---|
| `CONNECTED` | no (sees the whole galvanic island) |
| `DOWNSTREAM` / `UPSTREAM` | **yes** |
| `SHORTEST_PATH` | no |

### 3.2 It de-energises what's below it — the cone

The **de-energised cone** of an open device is everything a live, barrier-respecting **downstream** walk reaches from that device's load-side neighbours. The network-wide dead set is the **union** of the cones of *every* currently-open (or isolated/retired) device (`trace/energization.go`). Close the device and its cone collapses; partially restore and the cone shrinks to whatever the next open device still cuts off.

```
  source ──[CB closed]── busbar ──[REC closed]──┬── load A   (live)
                                                │
                                   [SW OPEN]────┴── load B   (DEAD — in the cone)
```

> **∑ Conservative on meshed networks.** The cone is a *radial* answer: a region back-fed through a **closed** normal-open tie is still reported dead, because the walk starts from the open device and goes down. That is correct for a radial feeder and deliberately conservative for a mesh — the isolate trace (§3.4) flags the back-feed path separately.

### 3.3 Transformers, busbars and the layer guard

Three rules shape how a trace moves through switchgear:

- **Transformer crossing.** A `transformer`-role feature (`DIST_XFMR`, `POWER_XFMR`) is a **downstream boundary** — a downstream trace stops at the transformer unless "continue past transformers" is set. (Only the interactive map trace exposes that toggle; energisation, isolate, feeder and section traces never cross.)
- **Junction pass-through.** A `junction` (busbar, RMU internal bus) is expanded *undirected* on directional traces, so a bus whose ways are all authored "switch → bus" doesn't dead-end the walk.
- **Cross-layer guard** (`trace_tier`). A downstream step may not jump to a strictly-lower tier, nor an upstream step to a strictly-higher one — this stops the undirected junction expansion from leaking back toward the source. Untiered classes (e.g. `MV_SECTIONALIZER`) always pass.

### 3.4 "What if I open this?" — the isolate trace

`POST /trace/isolate` answers the switching question **without mutating live state** (`trace/isolate.go`). Given a device, it returns:

1. **The de-energised set** — a downstream walk from the device's *downstream neighbours* (i.e. "if this device were open now").
2. **The upstream isolation boundary** — an upstream walk to the nearest **controller** (`protective` + `switching` roles): the devices you'd actually open to work on it safely.
3. **Critical facilities** inside the dead set (by `load_priority`), and **possible back-feed paths** — any live connected neighbour of the dead set, flagging a closed tie or mesh that could re-energise your work area.
4. A **service rollup** — how many customers / kW drop.

> **How to — isolate a device for work** 🔒 `trace:run`
> 1. Select the device (it must be a network **junction** — a switch/breaker/recloser; the item is greyed out otherwise).
> 2. Panel header → **Trace** dropdown (Route icon) → **Isolate from here** (⚡).
> 3. Read the result table: the **de-energised** list, the **upstream boundary** devices to open, the **critical facilities** affected, and any **possible back-feed paths** to verify before you work.
>
> **Worked example — isolate FSW-12 to work on TX-220.** Select **FSW-12** → *Isolate from here*:
> - **De-energised set:** `TX-220` and everything on its LV side — the only load below FSW-12.
> - **Upstream boundary:** `FSW-12` itself is the nearest controller; opening it isolates the tee. (If FSW-12 didn't exist, the boundary would walk up to `REC-7`.)
> - **Back-feed paths:** none — TX-220's LV is radial.
>
> Now contrast **isolate REC-7** (open the mid-feeder recloser):
> - **De-energised set:** everything below REC-7 — FSW-12/TX-220, RMU-5/TX-230, and the feeder tail.
> - **Possible back-feed path:** ⚠️ **T-79** — the tie to Feeder 9. It's open today, but the isolate result flags it: *close it and the "dead" tail is re-energised from Feeder 9.* Earth before you trust it dead.

### 3.5 Sections vs. zones

`POST /trace/section` bounds a region by walking out until it hits — but does **not** cross — a boundary device. The **kind** of boundary distinguishes two ideas (`trace/class_walk.go`):

- **Switching section** — bounded by `protective` + `switching` + `transformer` roles: *"the smallest piece I can isolate to work on it."* A plain `MV_SWITCH` **does** bound it.
- **Protection zone** — bounded by `protective` + `transformer` roles only: *"what de-energises when a fault here trips the protection."* A plain switch does **not** bound a protection zone — only a protective device or transformer does.

See **Feeders, Sections & Subnetworks** for the full sectioning model; the point here is that a *switch* and a *protective device* bound *different* regions.

---

## 4. Ties, bus couplers and RMUs

These three aren't built by hand — each is a guided composite on the map's **Assemblies** menu (top-right, next to *New feature*) that creates the device **and** wires it in one transaction, so you never place the switch and its two connections separately. All three are 🔒 `features:write` + `connectivity:commit`.

> **How to — add a normal-open tie between two feeders** (`POST /ties`)
> 1. Map toolbar → **Assemblies** → **Feeder tie (NOP)** → pick the switch class (default **MV Switch**).
> 2. Place the switch point between the two feeders.
> 3. The create form shows **two feeder pickers** — search and pick the conductor on each side (`feeder_a`, `feeder_b`) → **Save.**
> 4. The tie is born `normal_state = NORMALLY_OPEN` **and** `device_state = OPEN` — a declared, live-open standby path. Only the `OPEN` state blocks flow; `normal_state` is the label telling an operator it *should* be open. *(This is T-79 in our example.)*

> **How to — add a bus coupler** (`POST /couplers`)
> 1. **Assemblies** → **Bus coupler** → pick the breaker class (**MV Breaker**).
> 2. Place the breaker point between the two busbars.
> 3. The form shows **two bus pickers** (`bus_a`, `bus_b`) → **Save.** It's wired bus-to-bus and stamped `switch_function = BUS_COUPLER`.
> 4. Downstream there's no coupler-specific code — a coupler is just a breaker, so trace/isolate/coordination treat it as one.

> **How to — build a ring main unit** (`POST /rmus`)
> 1. **Assemblies** → **Ring Main Unit** → pick the config (e.g. *2 ring + 1 transformer*, `2W_1T`).
> 2. Place the RMU point → confirm name/subtype → **Save.**
> 3. The fan-out creates the `RMU` container, its internal `MV_BUSBAR`, and its `MV_SWITCH` ways, wiring each way's load side (T2) to the shared internal bus. Opening one tee way isolates only its transformer while the ring keeps feeding through the others. Fused (transformer) ways are stamped `is_fused = true`. *(RMU-5 in our example.)*

> **∑** Under the hood these POST to `api/internal/tie`, `api/internal/coupler` and `api/internal/rmu`; each composes `crud.CreateFeature` + `connectivity.EstablishConnection` calls in one transaction against the same switch/busbar rules you'd use by hand.

---

# Part B — Protection

Switching devices *carry* current; protection devices *decide when to interrupt it*. In VoltEdge the protective intelligence (the relay and its curves) is modelled separately from the thing it opens (the breaker), and a grading engine checks that they trip in the right order.

## 5. The protection devices

### `PROTECTIVE_RELAY` — the intelligence

A **diagram-only** feature (`geometry_type = 'none'`, `network_role = 'none'`) — a relay lives on a single-line diagram, never on the map, and never appears in a trace or load flow. One subtype (`standard`); the functional variant is captured by its ANSI device number.

| Field | Meaning |
|---|---|
| `device_number` *(required)* | ANSI function code — see below |
| `curve_type` | the TCC curve **code** it runs (a `protection_curves.code`) |
| `pickup_current_a` | overcurrent pickup, amps |
| `time_dial` | time multiplier (TMS) applied to the curve |
| `inst_pickup_a` | instantaneous-element pickup, amps |
| `definite_time_s` | fixed operate time (for a `DEFINITE_TIME` curve) |
| `manufacturer`, `model`, `condition_rating` | asset detail |

`device_number` draws from the **`ANSI_DEVICE_NUMBER`** domain — the standard function catalogue: `27` undervoltage, `59` overvoltage, `50_51` phase overcurrent, `50_51N` ground overcurrent, `86` lockout, `87` differential, `25` synch-check, `32` directional power, `37` under-current/power, `46` negative-sequence, `49` thermal overload, `64` earth-fault/REF, `67` / `67N` directional phase/ground OC, `79` auto-reclose, `81` over/under-frequency.

**How a relay connects to the network (∑).** A relay isn't wired with connectivity rules — it uses two **relationship classes**:

- **`protection_trip`** — "this relay operates that switching device." Targets `MV_BREAKER`, `DIST_SWITCH` and `MV_RECLOSER`. This is the link the coordination engine follows to find a breaker's grading curve.
- **`protection_sensing`** — "this relay is fed by that instrument transformer." Targets `MV_CT` and `MV_VT`. This is documentation of the measurement chain; the grading math doesn't consume it.

> **How to — wire a relay to the breaker it trips** 🔒 `features:write` + `connectivity:commit`
> 1. **Create the relay.** A relay has no map geometry, so add `PROTECTIVE_RELAY` on the substation **single-line diagram** (place its stencil, or promote one), then set its fields — `device_number` (e.g. `50_51`), `curve_type` (e.g. `IEC_SI`), `pickup_current_a`, `time_dial`.
> 2. **Link it to the breaker.** Select the relay → **Relationships** tab → **➕ New relationship** → click the **breaker** (CB-7) → in the confirm panel, pick **Kind = `protection_trip`** → **Confirm.**
> 3. **Link its sensing** (optional but recommended). New relationship again → click the **CT** (CT-7) → **Kind = `protection_sensing`** → Confirm; repeat for the **VT**.
>
> 💡 The **Kind** dropdown only offers the relationship classes that actually link the two classes you picked. If it offers exactly one, it's chosen for you; if it offers none, no relationship class connects them (check you clicked the right pair). The `protection_trip` link is the one coordination follows — without it, the engine can't find the breaker's curve.

### `MV_CT` / `MV_VT` — the sensing

Point `junction` features that tap the line to feed a relay (they are **not** trace barriers — no `device_state`):

- **`MV_CT`** (current transformer) — subtypes `metering_ct`, `protection_ct`; field `ct_ratio` from the **`CT_RATIO`** domain (`100_5`, `200_5`, `400_5`, `800_5`, `1600_5`, e.g. "100/5 A").
- **`MV_VT`** (voltage transformer) — subtypes `inductive_vt`, `capacitive_vt` (CVT); a shunt line-to-ground tap; field `vt_ratio` from **`VT_RATIO`** (`11KV_110`, `33KV_110`).

### `MV_FUSE` — switching *and* protection

An inline point `junction` (T1 line / T2 load). `device_state = OPEN` makes it a trace barrier like any switch, but it also grades as a protective device:

- Subtypes `expulsion_fuse`, `current_limiting_fuse`.
- `fuse_rating_a` (amps), `fuse_speed` from **`FUSE_SPEED`** (`K` fast, `T` slow, plus `SLOW`/`FAST`), and `curve_ref` — the `protection_curves.code` of an **ampere-quantity** melt/clear curve.

### `MV_RECLOSER` — protection as a device

The recloser (from Part A) *is* its own protection: it runs a **two-curve** scheme — a `fast_curve_ref` (instantaneous, to clear transient faults before a fuse blows) and a `slow_curve_ref` (delayed, to coordinate with downstream fuses on a persistent fault) — and after `reclose_attempts` goes to **lockout**, modelled as `device_state = OPEN`. In coordination it grades off its **slow** curve.

### Fused generic switches

An `MV_SWITCH` or `LV_SWITCH` with **`is_fused = true`** becomes a protective device graded exactly like an `MV_FUSE`, via the same `fuse_rating_a` and `curve_ref` fields. This is how RMU transformer ways protect their transformer.

---

## 6. Time-current curves (TCC)

A TCC curve is the promise *"at this multiple of pickup, I operate in this many seconds."* VoltEdge stores curves as **tabulated points**, not live formulas (`metadata.protection_curves` + `protection_curve_points`, migration `0076`):

- **`protection_curves`** — `code`, `name`, `standard`, and **`quantity`**: `multiple` (relay curves, x-axis = multiple of pickup, time at `time_dial = 1`) or `ampere` (fuse curves, x-axis = absolute amps).
- **`protection_curve_points`** — `(band, current, time_s)`, where **`band`** is `operate` (relay), or `minimum_melt` / `total_clear` (fuse).

**The seeded catalogue (∑).** The **`TCC_CURVE`** domain ships the standard inverse families — `IEC_SI` (standard inverse), `IEC_VI` (very), `IEC_EI` (extremely), `IEC_LTI` (long-time), `IEEE_MI` / `IEEE_VI` / `IEEE_EI`, and `DEFINITE_TIME`. Their points are generated **once at seed time** from the unified IEC 60255 / IEEE C37.112 form

```
t = coeff / ( M^exp − 1 ) + b        (at time_dial = 1, M = I / pickup)
```

with per-curve constants — e.g. `IEC_SI`: coeff 0.14, exp 0.02, b 0; `IEC_EI`: 80, 2, 0; `IEEE_MI`: 0.0515, 0.02, 0.114 — sampled at multiples 1.5 … 20.

**Runtime evaluation.** The engine never re-evaluates the formula; it **log-log interpolates** between the stored points (`powerflow/curves.go`), clamping at the ends and flagging extrapolation. The operate time at a given current is then scaled by the setting:

```
t_operate = time_dial × interp( I_through / pickup_current_a )
```

> **How to — set a relay's curve and settings** 🔒 `features:write`
> 1. Select the relay → **Edit** → **Attributes.**
> 2. Set **`curve_type`** to a `TCC_CURVE` code (e.g. `IEC_SI`), **`pickup_current_a`** (the overcurrent threshold in primary amps, e.g. 400), and **`time_dial`** (the TMS, e.g. 0.5). Optionally **`inst_pickup_a`** for an instantaneous element. **Save.**
> 3. Grade it against downstream devices with a coordination study (§7).

> **How to — protect a tee with a fuse** 🔒 `features:write`
> - **A dedicated fuse:** place an **MV Fuse**, set `fuse_rating_a`, `fuse_speed` (e.g. `T`), and `curve_ref` to an ampere-quantity curve code. `device_state = OPEN` blows/isolates it.
> - **A fused switch (one device):** on an existing **MV Switch**, set **`is_fused = true`** and fill `fuse_rating_a` + `curve_ref`. It now grades exactly like a fuse — this is how an RMU transformer way protects its transformer.

> 💡 **Adding a brand-new curve** (not in the seeded catalogue) is an admin job — a `protection_curves` row plus its `protection_curve_points` (the tabulated `(band, current, time_s)`). The eight seeded IEC/IEEE families cover most relays out of the box.

---

## 7. Coordination

**`POST /analysis/coordination`** (`powerflow/coordination.go`) answers: *"for a fault here, do the devices on the path trip in the right order, with enough margin?"*

**How it works (∑):**

1. It reuses the fault substrate — builds the network tree, resolves impedances, and computes the **prospective three-phase fault current** at the fault node.
2. It refers that current to **each device's own voltage base** across any transformer on the path: `I_device = I_fault × V_fault / V_device` (power is conserved across the transformer).
3. It walks the path **source → fault**, and for each time-graded device reads its operate time off its curve at that through-current: a **breaker** via its `PROTECTIVE_RELAY` (`protection_trip`) `curve_type`; a **fuse** via its `curve_ref` `total_clear` band; a **recloser** via its `slow_curve_ref`; a **fused switch** as a fuse.
4. For each adjacent operating pair it computes **margin = t_backup − t_primary**, and marks the pair **coordinated** when `margin ≥ CTI`. The default **CTI** (coordination time interval) is **0.3 s** — the common relay-relay margin — overridable per request via `cti_seconds`.

The covered classes are `MV_BREAKER`, `MV_FUSE`, `MV_SWITCH`, `LV_SWITCH`, `MV_RECLOSER`. **`MV_SECTIONALIZER` is explicitly not time-graded** — a sectionalizer counts fault passages and opens on a dead line, so it has no TCC to grade.

**What it returns (▶).** The fault current and common base; a `Devices[]` list (each with its role `primary`/`backup`, through-current, resolved operate time, curve plot and an `extrapolated` flag); a `Pairs[]` list (each with `margin_s`, `cti_seconds`, `coordinated`); and an **`overall`** boolean — coordinated only if *every* graded pair clears its margin.

> **How to — run a coordination study** 🔒 `analysis:run`
> 1. Select the **source** device (the upstream end of the path — **CB-7**).
> 2. Panel header → **Trace** dropdown → **Fault & protection** → **Protection coordination…**
> 3. The panel opens armed on CB-7. Now click a **downstream feature** on the map (the fault location) → **Set fault.**
> 4. Set **Nominal (V)** — `11000` for our 11 kV feeder (leave blank to derive a transformer secondary) — and **CTI (s)** — `0.3`.
> 5. **Run.** Read the **log-log TCC chart** (one trace per device), the **operate time** per device, the **pairwise margins**, and the **Coordinated / Miscoordination** verdict.

> **Worked example — grade the Feeder 7 path.** A 3-phase fault just below **FSW-12** on the 11 kV tee, prospective **I = 4,000 A**. Nothing between source and fault is a transformer, so every device sees the same 4,000 A (if a transformer *were* in the path, the engine would refer the current across it: `I_device = I_fault × V_fault / V_device`).
>
> The path, fault-first: **FSW-12 (fuse)** → **REC-7 (recloser)** → **CB-7 (breaker + relay R-7)**.
>
> - **FSW-12**, 100 T fuse — off its **total-clear** curve at 4 kA ≈ **0.05 s** *(primary, nearest the fault)*.
> - **REC-7**, slow curve `IEC_VI` (TD 0.3) — off its slow curve at 4 kA ≈ **0.45 s**.
> - **CB-7 / R-7**, `IEC_SI`, pickup 400 A, TD 0.5 — computed exactly from the seeded curve form:
>
>   ```
>   M = I / pickup      = 4000 / 400                 = 10
>   t = TD · 0.14 / (M^0.02 − 1)
>     = 0.5 · 0.14 / (10^0.02 − 1)
>     = 0.5 · 0.14 / 0.0471        = 0.5 · 2.97       ≈ 1.49 s   (backup)
>   ```
>
> **The margins:**
>
> | Pair (primary → backup) | margin | ≥ CTI 0.3 s? |
> |---|---|---|
> | FSW-12 (0.05) → REC-7 (0.45) | 0.40 s | ✓ |
> | REC-7 (0.45) → CB-7 (1.49) | 1.04 s | ✓ |
>
> **Overall: coordinated ✓** — the fuse clears first, the recloser backs it up, the feeder breaker is the last resort. Had the relay's TD been, say, 0.15 (t ≈ 0.45 s), the REC-7 → CB-7 margin would collapse to ~0.00 s and the study would flag a **miscoordination**.

```
   source ──[CB-7: R-7, IEC_SI, TD 0.5]── … ──[REC-7: slow IEC_VI]── … ──[FSW-12: 100 T]──× fault
             t ≈ 1.49 s  (backup)                t ≈ 0.45 s               t ≈ 0.05 s  (primary)
                      └── margin 1.04 s ✓ ──┘          └── margin 0.40 s ✓ ──┘
```

---

## 8. Fault duty & arc-flash (cross-reference)

Protection isn't only about *timing* — the gear must **withstand and interrupt** the fault it sees. Two studies cover that; this guide points at them rather than repeating **Engineering Studies**:

- **Fault current — `POST /analysis/fault-current`** (`powerflow/fault.go`). The IEC 60909 three-phase (and SLG) fault level a device must make and break, including the **peak asymmetrical** current for breaker making-duty: `κ = 1.02 + 0.98·e^(−3R/X)`, `i_p = κ·√2·I_sym`.
- **Arc-flash — `POST /analysis/arc-flash`** (`powerflow/arcflash.go`, IEEE 1584). Incident energy and PPE category — which **derive from the clearing time** the coordination evaluators above produce: a slower-tripping protective device means more incident energy. (Flagged *experimental / safety* — see Engineering Studies.)

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
- **OMS closes the loop for operators.** As a switching plan runs, each step flips a `device_state`; the live energisation model recomputes the dead cone step by step, so the console shows exactly what is out and what each operation restores.

> **How to — see what's de-energised** 🔒 `trace:run`
> 1. On the map toolbar, click **Schematic** to open the temporary one-line of your selection (or current view).
> 2. Toggle **Live** (⚡) in the one-line toolbar — it shades every de-energised feature (anything below an open device) in grey and renders open vs. closed switches distinctly, with a *De-energised* swatch in the legend.
> 3. On the main map, de-energised regions appear automatically when an **OMS incident** is open (drawn red/amber downstream of the fault) — that view is driven by OMS, not a manual toggle.

> **Worked example — a fault and its restoration on Feeder 7.** Follow the loop end to end:
> 1. **Fault** below FSW-12. Relay **R-7** (50/51) picks up; by the coordination above the **fuse FSW-12** clears first (~0.05 s) — only **TX-220** goes dark. The relay/CB are the backup that never had to operate.
> 2. **If instead REC-7 locks out** (persistent fault it couldn't clear): `REC-7.device_state → OPEN`. The one-line's **Live** shading greys the whole tail — FSW-12/TX-220, RMU-5/TX-230 and beyond.
> 3. **Isolate & restore.** *Isolate from here* on the faulted section shows the upstream boundary to open and flags **T-79** as a back-feed path. Close **T-79** (`device_state → OPEN → CLOSED`) and the healthy tail re-energises from **Feeder 9**; the grey cone shrinks to just the faulted bit still isolated. Re-close REC-7 once the fault's cleared and the cone collapses to nothing.

**Two regions, don't conflate them (recap).** A **switching section** (bounded by switches *and* protective devices) is *what you can isolate to work on*. A **protection zone** (bounded by protective devices and transformers only) is *what goes dark when protection trips here*. A plain switch bounds the first, never the second — because a switch doesn't clear a fault.

---

## Appendix — reference

### State domains

| Domain | Values |
|---|---|
| `DEVICE_STATE` | `CLOSED`, `OPEN` |
| `OPERATIONAL_STATE` | `PLANNED`, `IN_SERVICE`, `ISOLATED`, `RETIRED` (state machine) |
| `NORMAL_STATE` | `NORMALLY_CLOSED`, `NORMALLY_OPEN` |
| `SWITCH_FUNCTION` | `FEEDER`, `INCOMER`, `BUS_COUPLER`, `BUS_SECTION`, `TRANSFORMER` |

### Protection domains

| Domain | Values |
|---|---|
| `ANSI_DEVICE_NUMBER` | `27`, `59`, `50_51`, `50_51N`, `86`, `87`, `25`, `32`, `37`, `46`, `49`, `64`, `67`, `67N`, `79`, `81` |
| `TCC_CURVE` | `IEC_SI`, `IEC_VI`, `IEC_EI`, `IEC_LTI`, `IEEE_MI`, `IEEE_VI`, `IEEE_EI`, `DEFINITE_TIME` |
| `CT_RATIO` | `100_5`, `200_5`, `400_5`, `800_5`, `1600_5` |
| `VT_RATIO` | `11KV_110`, `33KV_110` |
| `FUSE_SPEED` | `K`, `T`, `SLOW`, `FAST` |

### Formulas

```
Relay operate time:   t = time_dial × interp( I_through / pickup_current_a )     (log-log on stored points)
Seeded curve points:  t = coeff / ( M^exp − 1 ) + b          (M = I/pickup, at time_dial = 1)
Through-fault refer:   I_device = I_fault × V_fault / V_device
Coordination margin:   margin = t_backup − t_primary ;  coordinated ⟺ margin ≥ CTI  (default 0.3 s)
```

### What is *not* a class (modelling notes)

- **Bus coupler** = `MV_BREAKER` + `switch_function = BUS_COUPLER`. **Feeder tie / NOP** = `MV_SWITCH` + `normal_state = NORMALLY_OPEN` + `device_state = OPEN`. **RMU way** = `MV_SWITCH` contained by `RMU`.
- No `CIRCUIT_BREAKER` (→ `MV_BREAKER`), `DISCONNECTOR` (→ `MV_ISOLATOR`), `LOAD_BREAK` (→ `MV_SWITCH`), `RMU_WAY`/`RMU_BUSBAR` (→ `MV_SWITCH`/`MV_BUSBAR`).
- `DIST_SWITCH` is legacy, superseded by `MV_SWITCH`.
- `MV_SECTIONALIZER` carries `device_state` (so it *does* bar a trace when open) but is absent from the `functional_role`, `trace_tier` and `boundary_role` taxonomies — it is untiered and role-less in those (a known gap).
- `MV_CT` / `MV_VT` have **no** `device_state` and are never trace barriers.

### Related guides

- **Feeders, Sections & Subnetworks** — the sectioning backbone (`boundary_role`, switching sections vs protection zones, section highlighting).
- **Engineering Studies** — the fault-current, coordination and arc-flash math in depth, with worked numbers.
- **Substation Guide** — where this switchgear lives: bays, panels and the live one-line.


