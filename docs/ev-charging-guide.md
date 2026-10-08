# EV Charging & Feeder Headroom

**Written for:** a network planner or engineer assessing EV charging connections in VoltEdge — how to model a charging site, how its load flows through the network, and how to size what a feeder can absorb.

This guide is the companion to the interactive **EV Charging** guide (Interactive guides) and the deep-dive in **Engineering Studies §8.3**. It is written in two layers:

- **▶ Planner** — what to enter, what the number means, what to do about it.
- **∑ Engineer** — the model, the formulas, the defaults and their source in the solver code.

**Running example:** a rapid-charging hub connecting at the Oakwood LV board (415 V), off a distribution transformer on Feeder 7.

> **Grounding note.** Everything here is drawn from the shipped code and seed — `seed/ev_charging_example.sql`, `api/internal/powerflow/resolve.go`, and `api/internal/powerflow/ev_hosting.go` — not an idealised description. Where the design prose and the shipped behaviour differ, this guide follows the code and says so.

---

## 1. The two numbers of an EV load

An EV charging site has **two very different load figures**, and confusing them is the number-one planning mistake:

1. **Installed nameplate** — add up every charger's rated power. A site with 8 × 150 kW DC fast chargers is **1,200 kW** installed.
2. **What the grid actually sees** — the *coincident* draw: chargers are rarely all at full power at once (diversity), and a **managed** site deliberately holds its total below a cap.

The grid is sized for the **second** number, not the first. A site can have 1,200 kW of chargers installed and present **250 kW** to the feeder, because its energy-management system caps coincident draw. VoltEdge models exactly this: you record the nameplate, the diversity, and the cap, and the load flow uses the **effective** demand those three produce.

> **▶** If you size a feeder against installed nameplate you will over-build; if you ignore the cap you will reject viable connections. The point of the model below is to carry the *real* coincident load into every analysis automatically.

---

## 2. The data model — Station → charger → connector

EV charging is a **metadata seed** (`seed/ev_charging_example.sql`, Phase 54), not a migration — three classes in a containment hierarchy. Only the station is an electrical node.

```
EV_CHARGING_STATION          ← the site + point of connection (the electrical node)
  └─ contains EV_CHARGER      ← one EVSE unit (inventory only)
       └─ contains EV_CONNECTOR  ← one physical port (inventory only)
```

### EV_CHARGING_STATION — the electrical node

A **point** feature, `network_role = 'junction'` — modelled exactly like a `SERVICE_POINT`: a leaf load node with one terminal `T1`, fed off a transformer secondary by the rule `DIST_XFMR T2 → EV_CHARGING_STATION T1`. It renders as a green marker on the map. **This is the only one of the three classes that appears in load flow, trace, or fault** — the charger and connector are inventory that hang beneath it in containment.

Its fields split into two groups:

| Group | Field | Meaning |
|---|---|---|
| electrical | `demand_kw` | a plain demand, if you're not modelling managed charging |
| electrical | `power_factor` | load power factor (default unity) |
| electrical | `connection_voltage_v` | the connection voltage |
| ev_charging | `connected_load_kw` | **installed charger nameplate** (the Σ of charger ratings) |
| ev_charging | `diversity_factor` | coincidence factor, 0–1 (default 1.0) |
| ev_charging | `managed_demand_cap_kw` | the smart-charging cap on coincident draw |
| ev_charging | `operator` | site operator |
| ev_charging | `access` | `public` / `depot` / `residential` / `workplace` |

### EV_CHARGER — the EVSE unit (inventory)

`geometry_type = 'none'`, `network_role = 'none'` — it is **not** a connectivity node; its load rolls up to the station. Fields: `charger_type` (`ac_l2` / `dc_fast` / `dc_ultra`, required), `rated_power_kw`, `num_connectors`, `managed` (boolean), `manufacturer`.

### EV_CONNECTOR — the physical port (inventory)

`geometry_type = 'none'`, `network_role = 'none'`. Fields: `connector_standard` (`type2` / `ccs2` / `chademo` / `nacs` / `gbt`, required), `rated_power_kw`.

> **∑ Honest caveat — the roll-up is hand-maintained.** `connected_load_kw` is a **denormalised attribute stored directly on the station**, read straight off the station's own row in the hot sweep so no containment walk is needed. There is **no trigger or aggregation code** that sums the contained chargers' `rated_power_kw` into it — the planner sets `connected_load_kw` by hand (or a future tool will). The charger/connector features are inventory you can attach for completeness, but today they do not automatically compute the station's connected load.

---

## 3. How the load is sized — managed charging

The whole model reduces to one formula, applied in `resolveDemandKW` (`api/internal/powerflow/resolve.go`):

```
effective_kw = min( managed_demand_cap_kw , connected_load_kw × diversity_factor )
```

**∑** Precisely how it resolves, for every analysis (the single demand entry point `resolveDemand`):

- **EV base:** if `connected_load_kw` is present, `base = connected_load_kw × diversity_factor`. This **takes precedence** over any `demand_kw` on the same feature.
- **Ordinary load:** no `connected_load_kw` → `base = demand_kw`. (Ordinary `SERVICE_POINT`s etc. are unaffected by any of this.)
- **diversity_factor default = 1.0** when absent, ≤ 0, or > 1.
- **The cap clamps:** if `managed_demand_cap_kw` is present and > 0 and `base` exceeds it, `base = managed_demand_cap_kw`. **No cap is applied if the field is absent or ≤ 0** (an unmanaged site draws its full diversified load).
- **Reactive power:** `Q = P · tan(acos(pf))`, with `pf` from `power_factor`, defaulting to **unity (Q = 0)** when unset or outside (0, 1].

### Worked numbers

| connected_load_kw | diversity_factor | managed_demand_cap_kw | effective_kw | why |
|---|---|---|---|---|
| 500 | 1.0 | — | **500** | no diversity, no cap |
| 500 | 0.6 | — | **300** | 500 × 0.6 |
| 500 | 0.6 | 250 | **250** | 300 diversified, clamped to the 250 cap |
| 1,200 | 0.5 | 250 | **250** | 600 diversified, clamped to the cap |
| — (uses `demand_kw` = 180) | — | — | **180** | plain load, no EV fields |

> **▶** The cap is your lever. A site with 1,200 kW installed and a 250 kW cap presents **250 kW** to the feeder — the same as a site a fifth its size — because its EMS holds coincident draw there. Record the cap and the network sees the managed reality.

---

## 4. How it loads the network

**▶** Once modelled, an `EV_CHARGING_STATION` behaves like any other load node: wire it to the transformer secondary, and every study that walks the feeder carries its effective demand back to the source.

**∑** It is a **leaf** on the connectivity tree. The backward sweep accumulates its `(P, Q)` up every branch to the slack; the forward sweep then drops voltage along the way. A charging load deepens the volt drop between the source and the site — more so at `pf < 1`, because the reactive component `Q = P·tan(acos pf)` adds to the current without adding real power. There is **no EV-specific flag in the node result**: the station's managed demand simply folds into the feeder's `CumulativeKW` / `CumulativeKVAR` like any other load.

```
  SOURCE ──┬───────────── MV feeder ───────────── DIST_XFMR ──┬── LV board
 (slack)   │   each hop drops a little voltage               │   (0.969 pu)
           │                                                  └── EV_CHARGING_STATION
           │                                                       effective_kw load
  voltage: 1.00 ───────────────────────── 0.98 ───── 0.97 ─────── 0.969 → sags as load grows
```

---

## 5. The EV feeder-headroom study

The study (`POST /analysis/ev-hosting`, `ev_hosting.go`) answers one question:

> **How much *additional* charging load can this point absorb before the first limit binds?**

It is the exact **under-voltage mirror** of DER hosting capacity: hosting *injects* generation and binds on over-voltage; EV headroom *adds* load and binds on **under-voltage** (the 0.95 pu floor) — or on **thermal** (a conductor/transformer at 100 % of its rating), which binds identically in both.

### How the search works (∑)

Armed on a feeder's source, for each connection node:

1. **Base-case gate.** Evaluate at zero added load. If the feeder *already* violates a limit, there's no headroom — report **0 kW**, `binding_constraint = pre_existing`, and name the limit.
2. **Exponential ramp.** From `rampStartKW = 50 kW`, double the added load until it becomes infeasible (or hits the search cap `defaultMaxInjectionKW = 100 MW`). `lo` tracks the largest feasible value, `hi` the smallest infeasible one.
3. **Bisect to pin.** Binary-search `[lo feasible, hi infeasible]` for up to 40 iterations or until the bracket narrows below `0.1 kW` — pinning the capacity to a fraction of a kW.
4. **Name the binding limit** by the smallest remaining margin at capacity: under-voltage margin `worst_voltage_pu − min_voltage_pu` vs thermal margin `1 − worst_loading_pct/100`. Whichever is smaller is the limit the next kW would cross.
5. Reach the cap still feasible → `unbounded`, `binding_constraint = none` (headroom is *at least* the cap).

### The inputs that matter

| Input | Default | Effect |
|---|---|---|
| `nominal_voltage_v` | — (required) | The feeder slack / sending-end voltage. **Must match the feeder's real voltage** (see the gotcha). |
| `power_factor` | 1.0 | At pf < 1 the added load draws kVAR too, deepening the volt drop and **lowering** headroom. |
| `min_voltage_pu` | 0.95 | The binding floor (ANSI C84.1 Range A). |

> **⚠ The nominal-voltage gotcha.** The headroom panel defaults `nominal_voltage_v` to **400 V**. If you arm it on an **11 kV / MV** feeder and leave 400 V, the per-unit math is against the wrong base and the headroom is **meaningless**. Always enter the feeder's actual sending-end voltage.

### Reading the result

Each connection node reports:

- **`ev_hosting_capacity_kw`** — the headroom (kW of added charging load).
- **`binding_constraint`** — `undervoltage` / `thermal` / `pre_existing` / `none`.
- **`binding_feature`** — the node (under-voltage) or branch (thermal) the limit acts on.
- **`worst_voltage_pu`** / **`worst_loading_pct`** — the feeder's worst values *at* that capacity, so you can see how close each limit sits.

**▶ What to do about it, by binding:**

| Binds on | Means | Lever |
|---|---|---|
| **Under-voltage** | the far end would fall below 0.95 pu | raise the source/tap, reconductor, or accept a lower floor |
| **Thermal** | a conductor/transformer hits 100 % | upgrade that specific element (it's named) |
| **Pre-existing** | the feeder *already* violates a limit (headroom 0) | fix the base-case problem first — it isn't the EV's fault |
| **Unbounded** | nothing binds up to the 100 MW cap | headroom is effectively unlimited here |

---

## 6. Worked example — the Oakwood LV board

Tom, a network analyst, sizes the rapid-charging hub at the Oakwood LV board (415 V node off the Feeder-7 transformer, base voltage **0.969 pu** with the feeder's existing load). He runs `/analysis/ev-hosting` naming that node, unity pf, floor 0.95.

The search adds load and re-sweeps:

| Added load | Worst voltage | Feasible? |
|---|---|---|
| 50 kW | 0.966 pu | ✅ |
| 100 kW | 0.962 pu | ✅ |
| 200 kW | 0.954 pu | ✅ |
| 400 kW | 0.943 pu ⚠️ | ❌ under-voltage |

Bisection pins the capacity between 200 (feasible) and 400 (infeasible):

```
ev_hosting_capacity_kw ≈ 250 kW
worst_voltage_pu       = 0.950   (exactly on the floor)
worst_loading_pct      = 71 %    (thermal has margin to spare)
binding_constraint     = undervoltage
```

**▶ Tom's read:** the board can take **~250 kW** of charging before it hits the 0.95 pu floor; thermal isn't the problem (71 %). This board is **voltage-limited**. (Contrast: the same board hosts ~480 kW of *PV generation* before over-voltage binds — generation and load pull voltage opposite ways.)

---

## 7. Planning with managed charging

The headroom the study reports is the **uncapped physical ceiling** — the raw added load the feeder can take. Managed charging is then the lever that lets you connect a site whose *installed* nameplate exceeds it:

> **▶** The Oakwood board has **250 kW** of headroom. The hub operator wants **500 kW** of installed chargers (`connected_load_kw = 500`). That won't fit uncapped. But set a **`managed_demand_cap_kw` of 240 kW** and the station's effective load is `min(240, 500 × 1.0) = 240 kW` — inside the headroom. The connection is viable **because** the EMS holds coincident draw below the feeder's ceiling.

This is why the study sizes the *raw* added load and reports headroom against the cap lever, not pre-limited by it: you compare the site's **managed** effective load (§3) against the **headroom** (§5), and the cap is what you tune to make them fit.

```
installed nameplate   connected_load_kw = 500 kW  ───────────────────────┐
diversified           × diversity_factor 1.0      = 500 kW               │
managed cap           min(240, 500)               = 240 kW  ◀── the load the grid sees
feeder headroom       ev_hosting_capacity_kw       = 250 kW  ◀── the ceiling
                      240 ≤ 250  →  ✅ viable
```

---

## 8. Appendix — field & formula reference

### Fields (EV_CHARGING_STATION)

| Field | Group | Type | Notes |
|---|---|---|---|
| `demand_kw` | electrical | numeric | plain demand (ignored if `connected_load_kw` is set) |
| `power_factor` | electrical | numeric | default unity |
| `connection_voltage_v` | electrical | numeric | |
| `connected_load_kw` | ev_charging | numeric | installed charger nameplate (hand-maintained roll-up) |
| `diversity_factor` | ev_charging | numeric | 0–1, default 1.0 |
| `managed_demand_cap_kw` | ev_charging | numeric | smart-charging cap; no cap if absent/≤0 |
| `operator` | ev_charging | text | |
| `access` | ev_charging | coded | `public` / `depot` / `residential` / `workplace` |

### Formulas

```
effective_kw = min( managed_demand_cap_kw , connected_load_kw × diversity_factor )
Q (kVAR)     = P · tan(acos(power_factor))          pf default = unity → Q = 0
headroom     = largest added load with  worst_voltage_pu ≥ 0.95  AND  all loadings ≤ 100%
```

### Search constants (shared with DER hosting)

| Constant | Value |
|---|---|
| ramp start | 50 kW |
| search cap | 100 MW |
| bisection iterations | 40 |
| resolution | 0.1 kW |
| default voltage floor | 0.95 pu |

### Caveats (stated plainly)

1. The station's `network_role` in the shipped seed is **`junction`** (a leaf load node like `SERVICE_POINT`), despite the design prose saying "consumption."
2. `connected_load_kw` is a **denormalised, hand-maintained** attribute — there is no automated trigger summing the contained chargers.
3. There is **no EV-specific node-result flag** — managed demand folds into the feeder's `CumulativeKW` / `CumulativeKVAR`.
4. The EV asset domain is **metadata seed** (`seed/ev_charging_example.sql`), not a migration.

---

*See also: **Engineering Studies** §8.3 (EV feeder headroom) and §8.4 (the shared search machinery), and the interactive **EV Charging** guide for the managed-charging calculator and the headroom-search visualiser.*
