# Phase 54 — Electric-vehicle charging stations

Status: **Proposed (design).** New asset domain, requested directly ("thinking
about it, can we bring in electric vehicle charging stations?"). Scope was fixed
by three explicit decisions:

1. **Modelling — managed / smart charging.** A station is a controllable load: a
   static connected load *plus* a demand cap (the service-connection / EVSE
   management limit) that clamps its coincident draw. Not a dumb fixed load, not
   V2G (bidirectional export is out of this phase — see "Later").
2. **A dedicated EV headroom study.** A new mutually-exclusive analysis that
   mirrors the DER hosting engine (`hosting.go`) but binds on **under**-voltage
   (0.95 pu floor) and thermal overload — "how much more EV load can this feeder
   / this connection point absorb before a limit binds?"
3. **Data model — Station → charger → connector.** A three-level containment
   hierarchy (site → EVSE unit → physical port), the same shape the substation
   already uses (`SUBSTATION_PANEL` contains its devices), so per-charger and
   per-connector inventory is first-class.

Like arc-flash (Phase 53) and DER hosting (Phase 50 S3), this is mostly a
*derived* addition: the electrical substrate — a load node participates in
load-flow/fault the moment it carries `demand_kw` (`resolve.go` `resolveDemand`),
and the hosting bisection is already written — so the genuinely new work is (a)
the three feature classes + managed-load fields as **metadata seed** (no
migration), (b) a small demand resolver that honours the managed cap, and (c) the
under-voltage-binding mirror of `hosting.go` plus its panel.

---

## The wall / why it's cheap

An EV charging station is, electrically, **a load** — the network engine needs no
new concept to flow power to it. Two facts from static recon make this nearly
free:

| EV charging needs | Already have | Source |
| --- | --- | --- |
| A load node draws `demand_kw` (+ pf) in load-flow / fault | any node with `demand_kw`/`demand_pf` participates, no engine change | `resolveDemand` (`resolve.go:205`) |
| Bisect added load at a node until a limit binds | DER hosting bisection (exponential ramp → bisect → name binding limit) | `runHosting` / `hostingStudy` (`hosting.go:125`, `:244`, `:343`) |
| Contain sub-assets in a site (site → unit → port) | `'containment'` relationship kind, `one_to_many` | `seed/pnc_control_devices_example.sql:195` (SUBSTATION_PANEL contains its devices) |
| Per-panel dynamic "how this was calculated" strip + Formula | shared component, wired on all 10 analyses | `analysis-explanation.tsx` ([[analysis-explanation-strip]]) |

The DER hosting engine already does the exact search we need, only with the sign
and the binding limit flipped:

| DER hosting (`hosting.go`) | EV headroom (this phase) |
| --- | --- |
| injects **negative** demand (a generator) | adds **positive** demand (charging load) |
| binds on **over**-voltage (`worstHighPu > maxPu`) | binds on **under**-voltage (`worstLowPu < minPu`) |
| thermal binds identically (`loading > 100 %`) | thermal binds identically |
| `evaluate` sets `st.ownKW = saved − injKW` | `st.ownKW = saved + addKW` |

So EV headroom = `hosting.go` with `evaluate` adding instead of subtracting and
the feasibility test reading the low-voltage worst instead of the high. Same
resolve-once + in-memory re-sweep, same exponential-ramp-then-bisect, same
binding-constraint naming (now `undervoltage` / `thermal` / `pre_existing`).

---

## Data model — Station → charger → connector (S1)

Three new feature classes, all added as **metadata seed** (nullable fields, the
additive-compile rule [[compiler-additive-nullable-required]]) — no migration,
mirroring how the P&C control layer and the substation panels were added:

- **`EV_CHARGING_STATION`** — the site and the **point of electrical connection**.
  `geometry_type = 'point'`, `network_role = 'consumption'` (a load node — this is
  the node that appears in load-flow / fault). Fields (all nullable at DB level):
  - `connection_voltage_v` (numeric) — the LV/MV service voltage it connects at.
  - `connected_load_kw` (numeric) — Σ of contained charger nameplate ratings (the
    un-diversified installed capacity; maintained as chargers are added).
  - `diversity_factor` (numeric, 0–1) — coincidence factor over the chargers
    (default 1.0 = no diversity when null).
  - `managed_demand_cap_kw` (numeric) — the smart-charging / service limit that
    clamps coincident draw. The controllable setpoint.
  - `demand_kw` + `power_factor` (numeric) — the plain load fields `resolveDemand`
    already reads (`resolve.go:193`), so the station flows as an ordinary load the
    moment it is created, before S2's managed resolver exists. `power_factor` is
    the charging pf (typ. 0.98–1.0 for modern EVSE).
  - `operator` (text) — network operator / CPO.
  - `access` (coded `EV_ACCESS`: `public` / `depot` / `residential` / `workplace`).
- **`EV_CHARGER`** (EVSE unit) — a charging unit contained by a station.
  `geometry_type = 'none'` (inventory; its map/diagram presence is the station),
  `network_role = 'none'` (it is not a separate connectivity node — its load rolls
  up to the station). Fields:
  - `charger_type` (coded `EV_CHARGER_TYPE`, required: `ac_l2` / `dc_fast` /
    `dc_ultra`).
  - `rated_power_kw` (numeric) — the unit nameplate.
  - `num_connectors` (integer).
  - `managed` (boolean) — participates in smart-charging curtailment.
  - `manufacturer` (text).
- **`EV_CONNECTOR`** — a physical port contained by a charger. `geometry_type =
  'none'`, `network_role = 'none'`. Fields:
  - `connector_standard` (coded `EV_CONNECTOR_STANDARD`, required: `type2` /
    `ccs2` / `chademo` / `nacs` / `gbt`).
  - `rated_power_kw` (numeric) — the port rating (≤ the charger's, and shared when
    a charger powers two ports).

**Containment** (kind `'containment'`, `one_to_many`, the kind the pnc seed
already uses — so *no migration*):
`EV_CHARGING_STATION` contains `EV_CHARGER`; `EV_CHARGER` contains `EV_CONNECTOR`.

**Requiredness** at the app layer via `subtype_field_rules` (one `standard`
subtype each): the identifying coded field is required (`charger_type`,
`connector_standard`), everything else optional — same idiom as the pnc seed.

**Symbology**: `EV_CHARGING_STATION` gets a map rule (it is a point); the two
`none`-geometry classes get a `diagram` rule so they render on an inventory/
schematic view (a geometry-`none` class has no map rule to fall back to).

Compile only `EV_CHARGING_STATION` for a gis table (the two `none` classes need no
gis table — geometry-`none`, network-`none`, inventory-only, exactly like
`STATION_BATTERY` et al.). Instances of chargers/connectors live in
`runtime.feature_relationships` under the station via the containment edges.

---

## Managed-charging demand (S2)

At analysis time the station's effective load is the smart-charging outcome, not
the raw installed capacity:

```
effective_demand_kw = min( managed_demand_cap_kw,
                           connected_load_kw × diversity_factor )
```

- If `managed_demand_cap_kw` is null → unmanaged: `connected_load_kw ×
  diversity_factor` (or just `connected_load_kw` if diversity is also null).
- If `connected_load_kw` is null but a plain `demand_kw` is set → fall straight
  through to today's `resolveDemand` (an EV station modelled as an ordinary load).

This is a **small, self-contained resolver addition** (**BUILT**): the real-power
magnitude in `resolveDemand` (`resolve.go`) now comes from a new `resolveDemandKW`
helper that, when the node carries the EV managed fields, computes the clamped
value above; an ordinary load (no EV fields) returns exactly `demand_kw`, so
`SERVICE_POINT` and every other load are unchanged. All three inputs are read off
the **station's own attributes** — no containment walk in the hot sweep (the
roll-up `connected_load_kw` is maintained when chargers are attached, denormalised
on purpose so the per-trial re-sweep stays query-free, the same discipline
`hostingStudy` keeps). Because every engine (`sweep.go`, `nodal.go`, `hosting.go`)
reads demand through `resolveDemand`, load-flow, fault, voltage-profile,
contingency, and the DER/EV hosting searches all see the station as a
correctly-sized load with **zero caller changes**.

The precedence when a feature carries several fields: `connected_load_kw` (× its
`diversity_factor`, default 1.0) is the EV base and wins over any plain
`demand_kw`; the `managed_demand_cap_kw` then clamps whichever base was chosen.
Absent EV fields → plain `demand_kw`, exactly as before.

---

## EV headroom study (S3)

`POST /analysis/ev-hosting` — `ev_hosting.go`, the under-voltage mirror of
`hosting.go`:

- Request mirrors `hostingRequest`: `source` + `nominal_voltage_v` (the feeder
  slack, as every sweep takes), optional `node` (one connection point to size, or
  scan every apparatus node), `power_factor` (the added EV load's pf, default
  ~0.98), `min_voltage_pu` (default 0.95, the binding floor), `max_loading_pct`
  (default 100).
- Per candidate node: exponential-ramp then bisect the **added charging load**
  until the feeder's **worst voltage drops below `min_voltage_pu`** or a branch
  exceeds thermal — report the largest hostable EV kW, the binding constraint
  (`undervoltage` / `thermal` / `pre_existing` when the base case already
  violates), the binding feature, and the worst voltage/loading at that capacity.
- Reuses `resolveNode` / `buildTree` / `resolveNodeStates` / `backwardSweep` /
  `forwardSweep` / `formatResults` unchanged; the only new code is the flipped
  `evaluate` (add load, test the low-voltage worst) and the request/response
  structs.

**Managed framing note** (documented in the response `note`, not a second engine):
the study sizes *uncapped* added load — the physical headroom. Smart charging is
then what lets an operator install more nameplate than the headroom and stay
within it by capping coincident draw; the study reports the headroom, the managed
cap is the operator's lever against it.

**Frontend** (mirrors the hosting panel + all the Phase-50/52 analyses): a new
`ev-hosting-store`, `ev-hosting-panel.tsx`, an "EV headroom…" dropdown entry
(mutually exclusive with the other analyses — arming it clears the others), a
graded highlight by remaining headroom, and — per [[analysis-explanation-strip]]
— the collapsible **"How this was calculated"** strip + a `<Formula>` block:

```
headroom = added EV load until a limit binds (bisection)
         = <kW> kW  (binding: <undervoltage|thermal> at <feature>, worst V = <pu> pu)
```

---

## Slice plan

- **S1 — data model.** `seed/ev_charging_example.sql` (+ Taskfile task
  `seed-ev-charging`): the three classes, `EV_*` domains, managed-load fields,
  the two containment edges, symbology. Compile `EV_CHARGING_STATION`. Pure
  metadata seed, no migration. *Then a small instance seed proving a station with
  two chargers and their connectors, on a feeder, so S2/S3 have something to run.*
- **S2 — managed demand (BUILT).** `resolveDemandKW` helper computing
  `min(cap, connected × diversity)`, called from `resolveDemand`; an EV station
  now flows as a correctly-sized load in every existing analysis with no caller
  change. (Surfacing "the cap bound here" as an explicit node-result flag is
  deferred to S3's EV panel, which shows the managed figures directly.)
- **S3 — EV headroom analysis (BUILT).** `ev_hosting.go`
  (`POST /analysis/ev-hosting`, the under-voltage mirror of `hosting.go`: add
  positive load in `evaluate`, feasibility on the lowest-pu worst, binding
  `undervoltage`/`thermal`/`pre_existing`/`none`; reuses the search constants and
  `binding*` labels from `hosting.go`, adds only `bindingUndervoltage`) + route
  registered in `handler.go` + frontend `ev-hosting-store` / `ev-hosting-panel.tsx`
  / "EV feeder headroom…" dropdown entry (13th mutually-exclusive analysis,
  `BatteryCharging` icon) mounted in `map.tsx` + the [[analysis-explanation-strip]]
  strip & `<Formula>` (`headroom = <kW> (binding: <constraint> at <feature>)`).
  *Owed to actually exercise it: a small instance seed of a station + two
  chargers + connectors on a feeder (the second bullet of S1).*
- **Later (out of this phase):** V2G bidirectional export (reuses the
  `resolveGeneration` negative-demand injection path + generator fault infeed,
  [[gap-embedded-generation]]); time-of-use / charging profiles; per-connector
  session modelling.

## Owed verification ([[bash-tool-blocked]])

Everything static-reviewed here; the user runs: apply the seed
(`task seed-ev-charging`) → `task compile CODE=EV_CHARGING_STATION` → spot-check a
create → `cd api && go build ./...` (S2/S3) → `cd web && npm run build` (S3 panel).
