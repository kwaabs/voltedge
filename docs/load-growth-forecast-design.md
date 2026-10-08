# Load-Growth Forecast — design

**Status:** design (not built). A dev design doc, like `docs/phase-*.md` and
`docs/shared-field-packs-design.md` — deliberately **not** registered in the in-app
docs viewer.

**One-line:** a new analysis study that projects the existing load-flow + hosting-
capacity engines along a **time axis** under a **growth scenario**, to answer two
planning questions in one run — *when/where does the network overload* (capex timing)
and *how does DER headroom change over time*.

---

## 1. Purpose & scope

### What it is

A **scenario / growth projection**. You give a growth assumption (a %/year, per-feeder
or per-class rates, known new connections, a DER adoption curve); the engine scales
the network's demand year by year, re-runs the **existing** load-flow at each step, and
reports the first year each asset violates a limit — plus a DER-headroom trajectory.
It reuses the analysis engine already in the product; it adds a time loop and a
scenario model, not new physics.

### What it is NOT

- **Not statistical / ML forecasting.** It does not learn future demand from historical
  interval data, weather, or calendar. VoltEdge has no interval/AMI/SCADA time-series
  (confirmed: no such tables; demand lives as metadata attributes), so a data-driven
  forecast is a separate, data-gated track (see §9).
- **Not a prediction.** It is a deterministic *what-if* under an explicit assumption —
  garbage-in-garbage-out on the growth input. It must be **labeled as such** (§7).

### The two questions it answers (one run)

1. **Time-to-overload / capex timing** — "which transformers and feeders overload
   first, and in what year, under this growth?"
2. **DER-headroom-over-time** — "how does interconnection headroom at each point change
   as load grows (and as DER itself grows)?"

Both fall out of the **same** yearly loop (§4) — the DER curve is one extra call per
step. They also interact: a DER adoption scenario offsets load growth at a node, so you
can forecast load growth *with* DER growth together.

---

## 2. Core idea — reuse the engine along a time axis

The load-flow already runs happily under altered demand — that is exactly how
`contingency.go` works (it re-accumulates `CumulativeKW/KVAR` after restoring load onto
a tie). The forecast is that idea generalized: for each year of a horizon, compute a
scenario-adjusted demand for every load, run the existing load-flow, and judge the
result with the **same** band / overload / ampacity logic the load-flow already uses.

```
   year 0        year 1        year 2                 year N
  ┌───────┐     ┌───────┐     ┌───────┐              ┌───────┐
  │ scale │     │ scale │     │ scale │     ...      │ scale │   demand(node,t)
  │ load  │     │ load  │     │ load  │              │ load  │
  └───┬───┘     └───┬───┘     └───┬───┘              └───┬───┘
      ▼             ▼             ▼                      ▼
   load-flow     load-flow     load-flow             load-flow   (runSweep / nodal)
      │             │             │                      │
      ▼             ▼             ▼                      ▼
  violations?   violations?   violations?            violations?  ──► first-crossing
  DER headroom  DER headroom  DER headroom           DER headroom ──► trajectory
```

The topology is built **once** and held constant across years; only the per-node demand
changes each year. That keeps it correct and cheap.

---

## 3. Scenario model (the inputs)

```
{
  "horizon_years": 10,                 // annual steps, 1..horizon
  "base_growth_pct": 1.5,              // blanket compound %/yr on existing load

  "per_feeder":  { "<circuit_ref>": 3.0, ... },   // optional overrides (%/yr)
  "per_class":   { "EV_CHARGER": 8.0, ... },       // optional overrides (%/yr)

  "block_loads": [                     // hand-entered known new demand
    { "attach_ref": { "class": "...", "id": "..." },  // feature to hang it on
      "kw": 50, "kvar": 15, "start_year": 3 }
  ],

  "include_planned": true,             // pull operational_state=PLANNED as future load
  "planned_default_year": 1,           // fallback in-service year for a planned asset
  "planned_year_overrides": { "<feature_id>": 2, ... },

  "der_scenario": {                    // optional — drives the DER-headroom curve
    "per_class": { "SOLAR_PV": 12.0 }, // DER capacity growth %/yr
    "block_der": [ { "attach_ref": {...}, "kw": 100, "start_year": 4 } ]
  }
}
```

**Growth resolution per load** (most-specific wins): `per_feeder` ∧ `per_class` apply
multiplicatively where both match; else the matching one; else `base_growth_pct`.
`demand(node,t) = demand₀ × (1 + g(node))^t`.

**Planned assets (the pipeline source).** When `include_planned` is on, features with
`operational_state = PLANNED` are brought in as **future** load. Their demand comes
from their own attributes; their **in-service year** is resolved best-available:

1. a **metadata date field** on the class, if present and populated — a conventional,
   configurable field name (default `planned_in_service_date` / `in_service_date`), so
   utilities that maintain in-service dates get exact years for free, metadata-driven;
2. else a **per-asset override** (`planned_year_overrides`);
3. else the scenario **default** (`planned_default_year`, default 1).

This is the same graceful-degradation precedence the engine already uses
(`dispatch_kw → rated_kw`, `power_factor` defaults): it works with zero date data and
sharpens as a utility's data improves, with no schema change.

**The year-0 base is IN_SERVICE only.** The forecast always bases year 0 on the
in-service network; PLANNED assets never count as present load at year 0. The
`include_planned` flag only governs whether they re-enter as *future* adds — so toggling
it can never double-count, regardless of how the live engine treats PLANNED today
(confirm the exact filter during build — §8 open point).

---

## 4. The engine

### The year loop

For `t` in `1..horizon` (plus the `t=0` base):

1. **Demand override map** — compute `{ feature_id → (kw, kvar) }` for this year:
   existing loads grown by `g(node)`; planned loads = their demand if `t ≥ in-service
   year` else 0; hand-entered block loads active when `t ≥ start_year`.
2. **Run the load-flow** — the existing radial sweep (`dispatch.go` / `runSweep`) or
   nodal engine, consulting the override map instead of the attribute demand.
3. **Judge violations** — reuse the load-flow's existing transformer-loading,
   voltage-band, and conductor-ampacity (`ampacity.go`) checks.
4. **DER headroom** — run the existing **hosting-capacity** study under this year's
   load (and DER scenario) → headroom at each candidate node.

### The one engine change (v1)

A **demand-override hook** in the tree/graph demand accumulation: where `buildTree` /
`buildElectricalGraph` derives a load's `kw/kvar` from its attributes, first consult an
optional `map[featureID]demand` and use it when present. Everything else — the loop,
scenario math, first-crossing extraction — is orchestration in a new `forecast.go`.

### v1 simplification — demand-only, no planned-topology impedance

In v1, planned assets and hand-entered block loads are injected as **additive demand at
their attachment node** (the in-service node they hang off), **not** by extending the
network with the planned conductor/transformer and its impedance. So v1 answers *"can
the existing upstream network carry the growth + the new load"* — it does **not** model
the voltage drop along a brand-new lateral. Modeling planned topology (planned
conductors with real impedance entering the tree, demand year-gated) is a clean later
slice; calling it out keeps v1 honest and small (one engine hook, constant topology).

---

## 5. Outputs

```
{
  "scenario": { ...echoed inputs... },
  "horizon_years": 10,

  "assets": [                          // one row per asset that is or becomes stressed
    { "class": "DIST_XFMR", "id": "...", "name": "...",
      "limit_kind": "transformer_loading",   // | voltage | ampacity
      "rating": 500.0, "unit": "kVA",
      "first_violation_year": 6,             // null if never within the horizon
      "trajectory": [ {"year":0,"value":410}, {"year":1,"value":416}, ... ] }
  ],

  "reinforcement_order": [ ... assets ranked by first_violation_year then severity ... ],

  "feeder_headroom": [                 // per feeder, spare capacity over time
    { "circuit_ref": "...", "trajectory": [ {"year":0,"headroom_kva":...}, ... ] }
  ],

  "der_headroom": [                    // per candidate node, DER headroom over time
    { "class":"...","id":"...","trajectory":[ {"year":0,"headroom_kw":...}, ... ] }
  ],

  "assumptions": [ "1.5%/yr base growth", "planned assets included, default year 1", ... ],
  "validation": "derived"              // see §7
}
```

The headline the planner reads: the **reinforcement_order** list ("what overloads
first, and when") and the two trajectory families (demand overload, DER headroom).

---

## 6. Where it plugs in

- **Endpoint:** `POST /analysis/load-growth` — a new study run from a source/feeder
  start, beside the other 17 (same request envelope as the load-flow: a start feature +
  the scenario).
- **Engine:** `api/internal/powerflow/forecast.go` (orchestration) + the demand-override
  hook in `graph.go` / the tree build.
- **Enrichment:** reuse the trace/load-flow result-enrichment (circuit + voltage) so
  each asset row carries its circuit and nominal.
- **UI:** a new entry in the cascading **Analysis** submenu; a result panel with the
  trajectory chart + the reinforcement table; the **"How calculated"** explanation
  strip stating the scenario; the **Validated/Derived/Experimental** badge = Derived.
- **Persistence:** fold into the existing analysis result session-persistence.
- **MCP:** a `forecast_load_growth` tool — per the every-FE-activity-gets-an-MCP-tool
  mandate.
- **Docs:** a row in `docs/engineering-studies.md` + a short how-to.

---

## 7. Honesty & validation

Per the existing Validated/Derived/Experimental labeling:

- The study is **Derived** at best (a projection of validated physics under an
  **assumed** scenario), and arguably should carry an **Experimental/assumption**
  caveat — it is only as good as the growth input.
- The explanation strip must say, in plain words: *"A scenario projection — the network
  as it would be under your stated growth assumption, not a statistical prediction of
  actual future demand."*
- Every run echoes its `assumptions` so a result is never read without them.

This matters: a forecast is the kind of output people over-trust. The label and the
echoed assumptions are part of the deliverable, not decoration.

---

## 8. Open points to confirm during build

1. **The exact `operational_state` filter in the current tree-build.** The forecast
   base must be **in-service only**. If the live engine already includes PLANNED in the
   tree, the forecast build must explicitly exclude them from year 0 so the
   `include_planned` flag behaves correctly (§3).
2. **The conventional planned-date field name** — pick the default (`in_service_date`
   vs `planned_in_service_date`) and make it configurable; no schema change, it's a
   metadata field a utility opts into.
3. **Growth compounding across per-feeder × per-class** — confirm multiplicative vs
   additive when both overrides match a load (spec assumes multiplicative).

---

## 9. Slice plan

- **S1 — engine core.** The demand-override hook + the year loop + **time-to-overload**
  for transformers & feeders under a **blanket %/yr** only (no planned, no DER). Returns
  `first_violation_year` + trajectory + reinforcement order. API + unit tests on the
  loop/first-crossing (pure kernels, like `sweep_test.go`).
- **S2 — scenario model.** Per-feeder / per-class growth overrides + hand-entered block
  loads.
- **S3 — planned-asset pipeline.** `include_planned` flag + the in-service-year
  precedence; inject PLANNED (and block) loads as additive demand.
- **S4 — DER-headroom-over-time.** Run hosting-capacity per step; add the DER adoption
  scenario (`der_scenario`); return `der_headroom`.
- **S5 — UI.** Analysis menu entry + result panel + trajectory chart + reinforcement
  table + explanation strip + Derived badge + session persistence.
- **S6 — MCP tool + docs.** `forecast_load_growth` + engineering-studies row + how-to.

S1–S3 are the capex-timing core; S4 adds the DER lens; S5–S6 surface it. Each slice is
independently verifiable (engine slices via unit tests; the rest static + build).

### Future (explicitly out of v1)

- **Planned-topology impedance** — model planned conductors/transformers in the tree
  with year-gated demand (voltage drop along new laterals), not just additive demand.
- **Feeder-group in-service** — "bring all PLANNED on this feeder in year N" as a middle
  ground between per-asset and global.
- **Statistical / ML forecasting** — the data-driven track, gated on a real interval
  (AMI/SCADA/billing) time-series pipeline existing first. A different project.
- **Weather / calendar / probabilistic (Monte Carlo over growth)** — later, if wanted.

---

## 10. Reuse summary

| Piece | Reused from | New |
|-------|-------------|-----|
| Load-flow per year | `dispatch.go` / `runSweep` / nodal | demand-override hook |
| Violation judging | load-flow band / overload / `ampacity.go` | — |
| DER headroom | existing hosting-capacity study | per-step invocation + DER scenario |
| Start / enrichment | load-flow request + result-enrichment | scenario envelope |
| Planned assets | `operational_state = PLANNED` (Phase 38) | in-service-year resolution |
| UI / labels / MCP | analysis menu, explanation strip, validation badges, MCP mandate | one study's worth |

The point of the whole design: **the physics already exists and is validated** — the
forecast is a disciplined time-loop + scenario model + honest labeling on top of it.
