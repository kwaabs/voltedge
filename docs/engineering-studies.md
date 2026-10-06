# VoltEdge Engineering Studies & Network Analysis

*A companion to the User Handbook. Where Chapter 8 of the handbook teaches you **which button to press and how to read the answer**, this guide explains **what each study actually does** — the model it builds, the assumptions it makes, the mathematics it runs, and how to trust (and sanity-check) the number it hands back.*

---

## About this guide

VoltEdge ships a suite of network-analysis studies that run directly over your live, connected asset data — no separate model to build, no export to a third-party engine. You place a transformer, wire it up, and the same features power both the map and the maths. This guide documents every one of those studies.

### Who this guide is for — and how it's layered

Two very different people reach for this document, so almost every section is written in **two layers**:

- **▶ For the operator / planner** — a plain-language part: *what the study is for, when to run it, what its inputs mean, and how to read the result.* If you run studies but don't need the derivation, read only these parts. They use worked scenarios with realistic assets and numbers.
- **∑ Method & math** — a deeper part for the engineer who wants to defend the number in a design review: *the model, the equations, the per-unit conventions, the precedence rules, and the simplifications and their validity range.* Everything here is drawn from the actual solver code, not a textbook ideal, so it tells you what VoltEdge *really* computes.

Skip the **∑** parts freely; the **▶** parts stand alone. Read only the **∑** parts if you already know the workflow and just want the internals.

### A recurring cast

To keep examples concrete, the same people appear throughout — the analysts and planners from the User Handbook:

- **Tom** — a network analyst/planner. He runs most of the studies in this guide.
- **Priya** — a control-room dispatcher. She reaches for fault levels, contingency backfeed, and arc-flash before a switching job.
- **Dana** — a field-supervising editor. She surveys the transformer nameplate data these studies depend on.

And a recurring feeder: **Feeder 7**, an 11 kV distribution feeder out of Oakwood Zone, with distribution transformer **TX-Feeder7-014** (315 kVA, 11 kV/415 V, 4.5 % impedance).

### The studies at a glance

Every study is one `POST` under `/analysis`, is **strictly read-only** (it computes over committed connectivity and the materials catalog — it never writes anything), and needs the `analysis:run` permission. The frontend groups them into three menu families:

| Family | Study | Endpoint | Chapter |
|---|---|---|---|
| **Load flow** | Radial load flow | `/analysis/load-flow` | 2 |
| | Voltage profile | `/analysis/voltage-profile` | 2 |
| | Meshed load flow | `/analysis/meshed-load-flow` | 3 |
| | Nodal (Y-bus) load flow | `/analysis/nodal-load-flow` | 3 |
| | Auto load flow (dispatch router) | `/analysis/load-flow-auto` | 3 |
| **Fault & protection** | Fault current (3-phase + SLG) | `/analysis/fault-current` | 4 |
| | Protection coordination | `/analysis/coordination` | 5 |
| | Conductor ampacity (IEEE 738) | `/analysis/ampacity` | 6 |
| | Arc-flash (IEEE 1584) | `/analysis/arc-flash` | 7 |
| **Capacity & planning** | Contingency / backfeed (N-1) | `/analysis/contingency` | 8 |
| | Hosting capacity (DER) | `/analysis/hosting-capacity` | 8 |
| | EV feeder headroom | `/analysis/ev-hosting` | 8 |

### Validation status — how far to trust each number

Not every study has been checked to the same depth, and the platform is honest about that rather than presenting every number with equal confidence. Each study carries one of three **validation tiers**, shown as a coloured badge on its analysis panel (on the *"How this was calculated"* strip) and listed here:

- **Validated** — checked against independent hand calculations or canonical reference data (e.g. IEEE test-feeder line impedances). Safe to defend in a design review, within the documented modelling assumptions.
- **Derived** — built directly on a *validated* core (the load-flow sweep or the fault engine) but the study's own additional layer has not been independently tested. The underlying physics is sound; the wrapping logic is reasoned, not benchmarked.
- **Experimental** — novel or complex and **not yet verified** against a reference. Treat the output as *indicative*, a useful first look, not an authoritative figure.

| Study | Endpoint | Tier | Basis |
|---|---|---|---|
| Radial load flow | `/analysis/load-flow` | **Validated** | Drop/current kernels and backward/forward pass composition checked vs hand calculations, incl. transformer rebasing. |
| Fault current (3-ph + SLG) | `/analysis/fault-current` | **Validated** | 3-phase and SLG currents checked vs hand calculations on an explicit voltage basis; generator infeed round-trips its nameplate kA. |
| Unbalanced load flow (coupled) | `/analysis/unbalanced-load-flow` | **Validated** | Coupled (Carson) mutual coupling matches IEEE 13-node Config 601 line impedances to 4 decimals. Tier-1 decoupled path and the Carson self-impedance diagonal are **not** independently verified. |
| Voltage profile | `/analysis/voltage-profile` | Derived | Reuses the validated load-flow sweep; the per-unit band and I²R loss summary are arithmetic over it. |
| Conductor ampacity | `/analysis/ampacity` | Derived | First-principles IEEE 738 thermal balance; standards-based, not cross-checked against a reference here. |
| Protection coordination | `/analysis/coordination` | Derived | Uses the validated fault engine for through-fault current; the TCC timing/grading is not independently verified. |
| Contingency / backfeed (N-1) | `/analysis/contingency` | Derived | Re-solves a backfed section with the validated sweep; the transfer logic is not independently tested. |
| Hosting capacity (DER) | `/analysis/hosting-capacity` | Derived | Binary search over the validated sweep for the first binding constraint. |
| EV feeder headroom | `/analysis/ev-hosting` | Derived | Binary search over the validated sweep (under-voltage mirror of hosting capacity). |
| Meshed load flow | `/analysis/meshed-load-flow` | Derived | Validated radial sweep plus loop-closing compensation; the compensation is not independently verified. |
| Auto load flow (router) | `/analysis/load-flow-auto` | Derived | Routes to the radial/meshed/nodal engine by topology; inherits whichever engine runs. |
| Nodal (Y-bus) load flow | `/analysis/nodal-load-flow` | **Experimental** | Gauss-Seidel nodal solver; not yet verified against a reference power-flow. |
| Arc-flash (IEEE 1584) | `/analysis/arc-flash` | **Experimental** | Incident energy / PPE category. **Safety-relevant and not independently verified — indicative only, never a basis for PPE selection or energized-work permits.** |
| Volt-VAR control | `/analysis/volt-var` | **Experimental** | Not verified against a reference. |
| Tap optimization | `/analysis/tap-optimization` | **Experimental** | Not verified against a reference. |
| Storage dispatch | `/analysis/storage-dispatch` | **Experimental** | Not verified against a reference. |
| Volt-VAR optimization (VVO) | `/analysis/vvo` | **Experimental** | Not verified against a reference. |

> The validated tiers rest on the accuracy-validation test suite in `api/internal/powerflow` (`*_integration_test.go`): hand-calc checks of the balanced sweep and fault engine, and the IEEE 13 Config 601 check of the coupled unbalanced engine. Moving a study up a tier means adding a reference check there.

### Table of contents

1. **Foundations — the shared network model** *(this chapter)*
2. Radial load flow & the voltage profile
3. Meshed & nodal load flow
4. Fault current & embedded-generation infeed
5. Protection coordination
6. Conductor ampacity (IEEE 738)
7. Arc-flash (IEEE 1584)
8. Capacity & planning: contingency, hosting capacity, EV headroom
9. Appendix — assumptions, precedence, symbols & a formula sheet

> **Legend:** ▶ operator/planner layer · ∑ engineer method & math · 💡 tip · ⚠️ caution.

---

## Chapter 1 — Foundations: the shared network model

Almost every study in this guide is built on **one** shared idea: turn your connected features into a graph, walk it, resolve each feature's electrical inputs from its own attributes, and solve. Load flow, fault current, coordination, arc-flash, contingency, and hosting capacity are all variations on that theme. Understand this chapter once and every later chapter becomes "the same engine, asking a different question." So this is the longest foundational chapter — the investment pays off everywhere after.

### 1.1 Two ways to read this guide

You've met the two layers already. This chapter is a little more **∑**-heavy than the rest, because the shared model *is* the method. But the ideas in the **▶** parts — *what a source is, what a barrier is, what "graceful degradation" means* — matter to everyone who reads a result, so don't skip them even if you skip the equations.

### 1.2 The mental model — your features become a graph

**▶** When you run a study, VoltEdge doesn't look at a map. It looks at the **connectivity graph**: every feature is a *node*, and every committed electrical connection between two features is an *edge*. A study starts at one feature you nominate — the **start feature** — and walks *outward along power flow*, away from the source, following connections from feature to feature until it runs out of network or hits something that stops it (§1.3).

That walk builds a **tree**: the start feature is the root, and every other feature hangs off it by exactly one path. This matters more than it sounds — see §1.3.

**∑** A node is the pair `(feature_class_id, feature_id)` — the same identity the tracing engine uses, because `runtime.feature_connectivity` stores ids, not codes. Edges are read from that table; the engine only ever queries **downstream** neighbours (`from_feature_id = current`), because a study always follows power away from the source. The walk is a breadth-first traversal from the start node, capped defensively at 2000 nodes (a guard against a pathological data loop, not a design limit). Each discovered node records its **parent** (the node that reached it) and its **children** (the nodes it reaches), plus — for a transformer — *which winding terminal* each child left through, so a three-winding unit can route its LV and TV children correctly (§1.6).

### 1.3 The radial tree and its barriers

**▶** VoltEdge's fast load-flow and fault engines assume the network downstream of your start feature is **radial** — a tree, where power reaches every point by a single path. Real distribution networks are almost always operated radially (rings exist but are run open at a tie point), so this is usually exactly right. If the walk finds a genuine loop — a point reachable two different ways — the radial engines **stop and tell you**, naming the offending feature, rather than silently guessing a path and returning a wrong answer. (When you *want* to analyse a loop, that's what the meshed and nodal engines in Chapter 3 are for.)

The walk also stops at **barriers** — open switching devices. An open switch, cutout, or link box de-energises everything beyond it, and a load flow over a dead branch is meaningless, so the walk treats an open device as the edge of the energised network and records it as a *barrier hit* in the result. This is why an open point on a ring keeps the two halves as two separate radial feeders.

💡 If a study fails with *"not radial (loop/mesh detected)"*, run the `LOOP_CHECK_TRACE` first (Chapter 8 of the handbook) to find and open the loop — or switch to the meshed/nodal engine.

**∑** "Radial" is enforced in `buildTree`: the moment BFS reaches an already-visited node by a second independent path, the radial callers (which pass `breaks = nil`) abort with an `invalid` error naming the feature. The weakly-meshed engine (Chapter 3) passes a non-nil `breaks` slice instead, which flips this to *"record the loop-closing edge, keep the spanning tree radial, and compensate the loop later."*

Barriers are **not** hardcoded. The engine loads the `DOWNSTREAM_TRACE` trace-configuration's own `trace_barrier_rules` — the same rows the downstream trace uses — so the switching devices it stops at (`DIST_SWITCH`, `SERVICE_CUTOUT`, `LV_LINK_BOX`, each on `device_state = OPEN`) are data, not code. A barrier is checked **live** against the feature's current attributes on every run, never baked into a stored graph. One deliberate override exists: a node the caller has hypothetically *closed* (the contingency study closing a normally-open tie, §8) is never treated as a barrier for that run — the one place a study overrides the on-the-ground open state, and only in memory, never touching committed connectivity.

### 1.4 Sources and the starting feature

**▶** The start feature is the **source** for the study — the point power flows *from*. You also tell the study the **nominal voltage** at that source (e.g. 11 000 V for an 11 kV feeder head). By default the source is treated as *ideal*: infinitely stiff, holding its nominal voltage no matter the load. If your source feature carries a **source impedance** (a Thévenin R + jX, e.g. the upstream grid's short-circuit contribution), the study uses it — so the source voltage sags a little under load, and the fault level is finite rather than infinite.

**∑** The nominal is a required positive input (`nominal_voltage_v`); a zero or missing nominal is rejected. Source impedance is read from the start node's own `source_r_ohm` / `source_x_ohm` fields (and `source_r0_ohm` / `source_x0_ohm` for the zero-sequence used by single-line-to-ground faults). Absent fields → an ideal stiff source (R = X = 0), which is the correct default for a feeder study where the upstream system is genuinely stiff relative to the feeder. The source impedance is applied **once**, at the start node, against the whole network's accumulated demand.

### 1.5 Loads, generation, and net injection

**▶** A feature contributes **load** if it carries a demand — modelled as **kW plus a power factor** (not kVAR), because power factor is the figure a planner actually has on hand. A `SERVICE_POINT` at 100 kW, 0.95 power factor is the common case. A feature contributes **generation** (embedded PV, a diesel set, a battery) if it carries a generation nameplate; generation is modelled as **negative load** — it injects power, lifting local voltage and reducing upstream flow. A feature that carries *both* (rooftop PV behind a load meter) nets the two.

Smart **EV charging** stations are a special load: their coincident demand is the installed charger load, diversified, then clamped to a managed cap — because a well-managed site holds its draw below its connection limit.

**∑** Load resolves to `(P, Q)` from two fields: `demand_kw` and `power_factor`. `Q = P · tan(acos(pf))`; an unset or invalid pf defaults to unity (`Q = 0`) rather than discarding the load. EV managed charging computes the base as

```
effective_kw = min(managed_demand_cap_kw, connected_load_kw × diversity_factor)
```

reading `connected_load_kw`, `diversity_factor` (default 1.0), and `managed_demand_cap_kw` (no cap if absent) off the station's own denormalised roll-up — so the sweep never has to walk containment to sum chargers. An ordinary load (no EV fields) is unaffected.

Generation resolves to an injection `(P, Q)` from `dispatch_kw` (authoritative even at 0 — a curtailed unit injects nothing) falling back to the `rated_kw` nameplate, at the unit's `power_factor`. The node's **net** own power is `demand − generation`; a pure generator ends up with a *negative* own-kW, which the backward pass carries up the tree as reduced upstream flow and the forward pass reads as a local voltage rise. This is the same negative-demand model the hosting-capacity study (§8) uses for a candidate DER — generation just makes it a permanent, asset-driven fact instead of a search knob.

⚠️ Generation's *fault* contribution is a separate matter with separate fields (`fault_contribution_ka`, `subtransient_reactance_pct`) — covered in Chapter 4. A generator is a load-flow injection **and** a second fault source, and the two are modelled independently.

### 1.6 Transformers — where voltage changes base

**▶** A transformer does two things to a study: it **changes the voltage base** (an 11 kV/415 V unit means everything below it is analysed at 415 V), and it **drops some voltage** through its own impedance under load. VoltEdge reads both from the nameplate: the primary and secondary voltages give the turns ratio; the **impedance percent** (e.g. 4.5 %) plus the rating give the through-impedance. An **on-load tap changer** shifts the effective ratio to hold output voltage as load swings. Larger **power transformers** can be **three-winding** (HV/LV/TV), and VoltEdge models the tertiary as a real third output. For fault studies, the **vector group** (Dyn11, etc.) tells the engine which windings provide an earth path.

💡 This is why a node deep in the LV network reports its voltage drop *against 415 V*, not against the 11 kV feeder nominal — the base propagates down through each transformer. A 5 % drop at the service is 5 % of 415 V, not a spurious 96 % drop from 11 kV.

**∑** The two-winding model resolves from `primary_voltage` (a coded `VOLTAGE_CLASS`, parsed — "11KV" → 11000, so any *N*kV code works without a lookup table), `secondary_voltage` (numeric volts), `impedance_pct`, and `x_r_ratio` (default 2.0 when unset). The turns ratio is `secondary / primary`, multiplied by the tap factor `1 + tap_position · tap_step_pct / 100`. The series impedance referred to the secondary is

```
Z_base = secondary² / S_rated      (per-phase ohm, 3-phase base)
Z_series = (impedance_pct / 100) × Z_base
R = Z_series / √(1 + XR²),   X = R · XR
```

Both voltages are line-to-line, so the ratio is basis-independent. The rating comes from `rated_kva` (distribution) or `rated_mva` (power, ×1000).

Three-winding units (a `tertiary_voltage` is what turns the model on) are represented as their **star equivalent** — three arms H, L, T meeting at a common point, derived from the three measured pair impedances:

```
Z_H = ½(Z_HL + Z_HT − Z_LT)
Z_L = ½(Z_HL + Z_LT − Z_HT)
Z_T = ½(Z_HT + Z_LT − Z_HL)
```

Each arm is pre-referred to the base of the winding whose branch will see it, so the forward sweep composes drops in volts without re-referring. If only the HV-LV impedance is surveyed, it's placed entirely on the L arm and the tertiary is left ideal (ratio only). The vector group (`vector_group`, default **Dyn** — delta HV, wye-grounded LV) is parsed into per-winding connections (wye-grounded / wye-ungrounded / delta) read **only** by the ground-fault calc; the balanced load flow ignores it.

### 1.7 Conductors and impedance

**▶** A cable or line drops voltage in proportion to its **length** and its material's **resistance and reactance per kilometre**. VoltEdge computes a conductor's impedance from its *real geographic length* on the map and the resistance/reactance recorded on the **material spec** its conductor-type field points at. A conductor with no material assigned, or a spec that records no resistance, simply "drops nothing" — the study degrades gracefully rather than failing (§1.9).

**∑** Length is `ST_Length(geom::geography)` — true metres regardless of the class's stored SRID. The engine scans *every* field on the edge class for one that carries a domain value linked to a material spec (so `LV_CONDUCTOR` and `MV_CONDUCTOR` needn't name the field identically, and a future edge class inherits this for free). The branch impedance is `resistance_ohm_per_km × length_km` (and likewise reactance), with zero-sequence `resistance_zero_ohm_per_km` / `reactance_zero_ohm_per_km` resolved the same way for SLG faults (falling back to 3× the positive sequence when unrecorded). The spec's `ampacity_amps` rides along, so a load flow can flag an overloaded conductor in the same pass.

### 1.8 The voltage-base convention & units

**∑** VoltEdge's radial engine works in **volts and watts**, not per-unit, using a **linearized drop** rather than a full complex solve (see §1.10 and Chapter 2). Impedances are **per-phase ohms referred to a line-to-line voltage base** — the standard distribution convention — so a branch drop composes as `(P·R + Q·X)/V` directly. The nodal engine (Chapter 3) is the one that works in true **per-unit** on an admittance matrix. Throughout:

| Symbol | Quantity | Unit |
|---|---|---|
| V | node voltage (line-to-line) | volts |
| P, Q | real / reactive power | kW / kVAR (converted to W/VAR inside the drop) |
| S | apparent power, √(P²+Q²) | kVA / VA |
| R, X | branch resistance / reactance | ohm (per-phase) |
| I | branch current | amps |

Every node reports its voltage against **its own base** (the run nominal above a transformer, the secondary below it), so drop percentages are always meaningful locally.

### 1.9 Graceful degradation — the guiding principle

**▶** Real networks are never fully surveyed. VoltEdge's studies are built around one rule: **missing data degrades the answer, it doesn't fail the run.** A conductor with no assigned material drops no voltage. A transformer with no impedance percent is treated as an ideal ratio. A load with no power factor is treated as unity. A generator with no dispatch figure runs at its nameplate. Every such fallback is a *documented assumption*, and (importantly) the result tells you when a value was assumed rather than resolved — so you know which numbers to trust and which to go survey.

💡 This is why your **first** run of a study on a new feeder is often "optimistic" — no drops, no losses. That's the signal to send Dana out to survey the transformer and conductor nameplates, not a bug.

**∑** Key fallbacks, all in `resolve.go`: conductor impedance → `resolved = false` (no drop); transformer XR → 2.0; power factor → 1.0; tap → nominal; generation → nameplate; source impedance → ideal; zero-sequence → 3× positive (conductor) or Z0 = Z1 (transformer/source), each flagged *estimated*. Results carry a `material_resolved` flag per node so a client can distinguish "0 % drop because stiff" from "0 % drop because unsurveyed."

### 1.10 Anatomy of a run

**▶** Every radial study follows the same five steps:

1. **Resolve the start** — check the start feature exists and isn't deleted.
2. **Build the tree** — walk downstream, honouring barriers, rejecting loops.
3. **Resolve node states** — load each feature once and read its electrical inputs (impedance, demand, generation, transformer model, rating).
4. **Solve** — accumulate demand up the tree (backward pass), then derive voltage down the tree (forward pass).
5. **Format** — shape the answer the study asked for (voltages, loadings, fault currents, …).

Nothing in that sequence writes to the database. Two studies can even share one solve — the load flow and the voltage profile run the identical sweep and just format it differently, so a voltage profile costs no more than a load flow.

**∑** The shared core is `runSweep`. The **backward pass** walks the BFS order in reverse (guaranteeing every child before its parent) and sets each node's cumulative power to its own net power plus the sum of its children's cumulatives. The **forward pass** runs a fixed **3 iterations** (not a convergence loop — at distribution drop magnitudes three passes over-converge already; a tolerance loop would be unobservable complexity), each pass re-measuring a branch's ΔV against the previous pass's sending-end voltage. The drop is the standard linearized approximation:

```
ΔV(volts) ≈ (P·R + Q·X) / V
```

accurate to a fraction of a percent at the drops a well-run feeder actually sees (exact would need the branch current angle — unnecessary precision here). The start node absorbs the source impedance against total demand; a conductor drops across its resolved impedance; a transformer rebases to `parent_voltage × ratio` and then drops its own series impedance on the secondary base; a plain junction is a pass-through. Branch current is recovered as `I = S / V`, and a conductor whose current exceeds its ampacity — or a transformer whose kVA exceeds its rating — is flagged *overloaded* in the same pass.

Every result panel in the app also carries a collapsible **"How this was calculated"** strip that restates, from the run's own figures, the worst node, the binding constraint, and the method — so the explanation travels with the number.

### 1.11 A worked micro-example — one small feeder, end to end

**▶** Tom wants to understand the engine before trusting it, so he runs a load flow on the smallest possible slice of Feeder 7: the feeder head, one MV cable, transformer **TX-Feeder7-014**, one LV cable, and a single 100 kW service.

```
Source (11 000 V)
  └─ MV cable  (240 mm² AL, 300 m)
       └─ TX-Feeder7-014  (315 kVA, 11 kV/415 V, Z = 4.5 %, X/R = 2)
            └─ LV cable  (185 mm² AL, 60 m)
                 └─ SERVICE-Oakwood-221  (100 kW, pf 0.95)
```

**∑** Follow the five steps:

- **Tree:** root = feeder head; a single chain of five nodes, no loops, no open devices → a clean radial tree.
- **Backward pass:** the service's net power is `P = 100 kW`, `Q = 100 · tan(acos 0.95) = 32.9 kVAR`. Every node upstream accumulates the same 100 kW / 32.9 kVAR (nothing else draws load), so the transformer and both cables each carry `S = √(100² + 32.9²) = 105.3 kVA`.
- **Transformer impedance:** `Z_base = 415² / 315 000 = 0.547 Ω`; `Z_series = 4.5 % × 0.547 = 0.0246 Ω`; split at X/R = 2 → `R = 0.011 Ω`, `X = 0.022 Ω`.
- **Forward pass:** the MV cable's drop is tiny at 11 kV (`(P·R+Q·X)/11000` is a fraction of a volt on a 300 m cable), so the transformer's primary sits at ≈ 11 kV. Its ideal secondary is `11 000 × (415/11 000) = 415 V`; its own drop is `(100 000 × 0.011 + 32 900 × 0.022) / 415 ≈ (1 100 + 724) / 415 ≈ 4.4 V`, giving ≈ **410.6 V** at the transformer secondary. The 60 m LV cable then adds its own drop on the 415 V base, landing the service around **408–409 V** — roughly a **1.5 % drop**, comfortably inside the ANSI band Chapter 2 checks against.

**▶** The takeaway for Tom: the number is small and believable, the base correctly flips to 415 V below the transformer, and the transformer — not the cables — is where most of this feeder's drop happens. That's the intuition every later study builds on.

---

*Next chapter: **Radial load flow & the voltage profile** — the everyday study, its ANSI C84.1 voltage band, the I²R loss summary, and reading loadings. Tell me to proceed (or name a later chapter).*

---

## Chapter 2 — Radial load flow & the voltage profile

This is the study you run most. It answers the two questions every distribution planner asks first: *"Is the voltage still acceptable at the far end?"* and *"Is anything overloaded?"* VoltEdge answers both from one solve — the radial forward-backward sweep from Chapter 1 — and offers two ways to read it: the **load flow** (`/analysis/load-flow`), which reports each feature's voltage and loading, and the **voltage profile** (`/analysis/voltage-profile`), which adds a per-unit voltage-band check and a feeder loss summary. They run the *identical* sweep; the profile is a richer reading of the same numbers, at no extra solve cost.

### 2.1 What it is and when to run it ▶

Run a load flow whenever you want to know how a **radial feeder** behaves under a given load: after adding a new connection, before energising an extension, when a customer complains of low voltage, or to check a transformer isn't cooking. It tells you, feature by feature from the source outward:

- the **voltage** at that point (and how far it's dropped from nominal),
- how much **power** is flowing through it (cumulative kW/kVAR),
- for a cable, the **current** and how close it is to its **ampacity**,
- for a transformer, how close its throughput is to its **kVA rating**,
- and a ready-made list of anything **overloaded** and anywhere the walk stopped at an **open device**.

The **voltage profile** takes the same run and asks the planning question directly: *is every point inside the acceptable voltage band, and how much power is this feeder losing to heat?*

💡 Use the **load flow** when you're chasing loadings and per-feature detail; use the **voltage profile** when you're signing off that a feeder is *compliant* and want the band check and losses in one summary.

### 2.2 Inputs ▶ / the request ∑

**▶** You give the study two things: the **start feature** (the source — the feeder head, or a transformer if you're studying just its LV side) and the **nominal voltage** at that source (11 000 V for an 11 kV feeder; 415 V — or 240 V — if you start at an LV board). That's it. Everything else — loads, impedances, transformer ratios — is read from the features themselves.

**∑** The load-flow body is minimal:

```json
{ "start": { "feature_class": "MV_CONDUCTOR", "feature_id": "…" },
  "nominal_voltage_v": 11000 }
```

`nominal_voltage_v` is **required**, not derived. MV and LV sit at very different nominals, `LV_CONDUCTOR` carries no voltage-class field at all, and a `DIST_XFMR` only records its primary — so guessing the sending-end voltage would be fragile. You may supply line-to-line **or** phase-to-neutral as long as you're consistent; the engine treats every voltage in the request and response on whatever single basis you supplied (see the caveat in §2.7). The voltage-profile body adds two optional per-unit bounds, `min_voltage_pu` / `max_voltage_pu` (§2.4).

### 2.3 Reading the load-flow result ▶

The result is a list of **nodes** (one per feature the sweep reached), plus two convenience lists: **barriers** (where the walk stopped at an open device) and **overloaded** (features past 100 % loading, pre-scanned so you don't have to). Each node carries:

| Field | Meaning |
|---|---|
| `voltage_v` | the operating voltage at this feature |
| `nominal_base_v` | the base that voltage is measured against — the run nominal above a transformer, the secondary below it |
| `voltage_drop_pct` | drop from `nominal_base_v` to `voltage_v`, as a percentage of the **local** base |
| `cumulative_kw` / `cumulative_kvar` | total power flowing through this point (its own load + everything downstream) |
| `current_amps` | *(conductors only)* branch current, `I = S / V` |
| `ampacity_amps` / `loading_pct` | *(conductors)* the cable's rating and how close it runs |
| `rated_kva` / `loading_pct` | *(transformers)* the nameplate and how close it runs |
| `material_resolved` | whether this branch's impedance was actually resolved (see §2.6) |

⚠️ **The base flips at every transformer — read the drop percentage locally.** An LV service reports its drop against 415 V, not the 11 kV feeder nominal. If you ever see a node reporting a ~96 % "drop," you're reading an LV voltage against an MV base — the `nominal_base_v` field is there precisely so you don't.

**∑** `voltage_drop_pct = (nominal_base_v − voltage_v) / nominal_base_v × 100`. A node never reached by the forward pass (a dangling connectivity row to a purged feature) has base 0 and reports drop 0 rather than dividing by zero. `loading_pct` is shared: a node is *either* a conductor (current/ampacity) *or* a rated junction (kVA/rating), never both, so one field serves both — a caller displaying "how loaded is this?" needn't know which kind of equipment it is. A conductor over 100 % or a transformer over its kVA lands in `overloaded`.

### 2.4 The voltage band — ANSI C84.1 ▶∑

**▶** The voltage profile judges every point against an **acceptable band**. By default that's **ANSI C84.1 Range A** service voltage: **0.95 to 1.05 per-unit** (±5 %) of each node's own nominal. Anything outside is a **violation** and listed separately, and the summary names the **worst (lowest) point** on the feeder. If your utility uses a different limit, override either bound in the request.

**∑** Judging is done in **per-unit of the local base** — `voltage_pu = voltage_v / nominal_base_v` — so MV and LV points are directly comparable without the transformer step confusing the picture (an LV node reads ~1.0 pu of *its* base, not ~0.004 pu of the MV nominal). Defaults `min = 0.95`, `max = 1.05` apply only where a bound is left unset; they're validated (positive, min ≤ max) and, crucially, **change only the judging, never the solve** — the same swept voltages are simply compared against a different band. A node with base 0 is *not* flagged under-voltage (that would be a false positive from a missing solve, not a real out-of-band condition).

### 2.5 The loss summary ∑

**▶** The profile also totals how much power the feeder is **losing to heat** in its conductors — the difference between what the source pushes in and what the loads receive. It's reported as a total (kW) and as a percentage of the feeder's load. A few percent is normal for distribution; a large figure points at an undersized or overlong conductor.

**∑** Each conductor branch's loss is `I²·R` — the branch current the load flow already computed (`I = S/V` at the sending end) squared, times the branch's resolved series resistance, in kW. The feeder total sums these; `loss_pct = total_loss / total_load_kw × 100` (0 when there's no load). `total_load_kw/kvar` is simply the start node's cumulative from the backward pass — everything downstream, including itself.

⚠️ **`losses_complete` tells you whether to trust the total.** If any branch on the tree had no resolved R/X (an unsurveyed conductor), its real, non-zero loss couldn't be counted, so the reported total is a **lower bound** and `losses_complete` comes back `false`. A suspiciously clean loss figure on a partly-surveyed feeder is this flag telling you to finish the survey, not a genuinely lossless feeder.

### 2.6 Method recap — why a sweep, not Newton–Raphson ∑

The solve is exactly the shared sweep from §1.10: a **backward pass** accumulating constant-power `(P, Q)` demand up the tree, then a **forward pass** (3 fixed iterations) applying the linearized drop `ΔV ≈ (P·R + Q·X)/V` down the tree, each iteration re-measuring against the previous pass's sending-end voltage. This is Kersting's forward-backward sweep — the standard technique for a **radial** feeder — deliberately not a full Newton–Raphson AC solve, which would be unnecessary complexity for a loop-free tree (and this project has `LOOP_CHECK_TRACE` and the meshed/nodal engines of Chapter 3 for when the network *isn't* a tree). Loads are treated as constant-power, so demand doesn't change between passes; only *which* voltage the drop is measured against is refined, and three passes over-converge at distribution drop magnitudes.

The one caveat carried through every node is `material_resolved`: a branch whose conductor material wasn't resolved contributes **no** drop (and no loss), so a "clean" result on an unsurveyed feeder is optimistic. This is the graceful-degradation principle of §1.9 surfacing in the answer.

### 2.7 Worked example — signing off Feeder 7's Oakwood spur ▶∑

**▶** Dana has just built an LV spur off **TX-Feeder7-014** to serve three new houses. Tom runs a **voltage profile** starting at the transformer's LV board, nominal **415 V**, to confirm it's compliant before energising.

The spur:

```
TX-Feeder7-014  LV board (415 V)
  └─ LV cable A  (185 mm² AL, 60 m)   → SERVICE-Oakwood-221  (100 kW, pf 0.95)
       └─ LV cable B (95 mm² AL, 45 m) → SERVICE-Oakwood-222 (40 kW, pf 0.95)
            └─ LV cable C (95 mm² AL, 40 m) → SERVICE-Oakwood-223 (40 kW, pf 0.95)
```

**∑** Walking the solve:

- **Backward pass:** total load 180 kW; `Q = 180·tan(acos 0.95) = 59 kVAR`; `S = √(180² + 59²) = 189 kVA` through cable A, less through B and C as services drop off.
- **Cable A loss (worked precisely):** it carries ≈ 189 kVA at ≈ 412 V → `I = 189 000 / 412 ≈ 459 A`. With 185 mm² AL at ≈ 0.164 Ω/km over 60 m, `R = 0.164 × 0.060 = 0.0098 Ω`, so `loss = I²R = 459² × 0.0098 ≈ 2 070 W ≈ 2.07 kW` on this branch alone. The thinner, lightly-loaded B and C branches add far less.
- **Forward pass:** the compounding drops land **SERVICE-Oakwood-223** (the far end) at roughly **402 V** → `voltage_pu = 402 / 415 = 0.969`. That's inside the 0.95–1.05 band, so `within_limits = true` and there are **zero violations**.
- **Summary:** `min_voltage_pu ≈ 0.969` at worst node 223; `total_loss ≈ 3 kW`; `loss_pct ≈ 1.7 %`; `losses_complete = true` (every cable was surveyed).

**▶** Tom's read: the spur is compliant with headroom to spare, the far house sits at ~0.97 pu, and cable A — the shared trunk — carries the bulk of both the current and the loss, which is exactly where he'd reinforce first if the load grew. He signs it off.

Now suppose Dana had **not** yet assigned a material to cable C. The profile would still run, but cable C would drop nothing, service 223 would read an optimistically high voltage, and `losses_complete` would come back `false` — Tom's signal to get the last cable surveyed before trusting the compliance sign-off.

### 2.8 Tips & gotchas ▶

- **Loads live only where `demand_kw` is set.** A feeder with no demand on its services solves to a flat, no-drop profile — correct, but not useful. Populate service demands first.
- **It's radial-only.** A ring operated closed, or an accidental double-feed, will make this study refuse with a *loop detected* error naming the feature. Open the loop, or use the meshed/nodal engine (Chapter 3).
- **Keep the voltage basis consistent.** Pick line-to-line *or* phase-to-neutral for `nominal_voltage_v` and read every result on that same basis; the engine doesn't convert.
- **First run looks too good?** Check `material_resolved` per node and `losses_complete` in the summary — an optimistic result usually means an unsurveyed conductor or transformer, not a genuinely lossless feeder.
- **The profile costs nothing extra.** If you're running a load flow to check compliance anyway, run the voltage profile instead — same solve, plus the band check and losses.

---

*Next chapter: **Meshed & nodal load flow** — ring mains and closed link boxes by loop compensation, the full Y-bus solver (Gauss–Seidel and Newton–Raphson), and the auto-dispatch router that picks the right engine for you. Tell me to proceed (or name a later chapter).*

---

## Chapter 3 — Meshed & nodal load flow

Chapter 2's radial ladder is fast and exact — for a **tree**. But real LV networks aren't always trees: a ring main fed from both ends, a normally-closed link box tying two feeders, two cables laid in parallel for capacity, an urban secondary grid meshed on purpose for reliability. Hand any of those to the radial ladder and it refuses outright with a *loop detected* error. This chapter covers the two engines that *do* solve loops — **Tier 1 mesh compensation** (`/analysis/meshed-load-flow`) for a handful of weak loops, and the **Tier 2 nodal Y-bus** solver (`/analysis/nodal-load-flow`) for dense meshes, multiple infeeds, and grid-forming generation — plus the **auto-dispatch router** (`/analysis/load-flow-auto`) that looks at the network and picks the right one for you.

### 3.1 Why a tree isn't always enough ▶

The radial ladder assumes power flows one way: from the source, down through the tree, to the loads. That assumption breaks the moment a load can be reached by **two paths at once**:

- a **ring main** operated closed (both ends live, meeting in the middle),
- a **closed link box** deliberately tying two LV feeders together for resilience,
- **parallel conductors** between the same two points (a reinforced run),
- a genuinely **meshed** urban secondary grid.

In every case, current *divides* between the paths in inverse proportion to their impedance, and the two paths must arrive at the shared point at the *same voltage*. A tree walk can't represent that — there's no single "parent" for the shared node. The three engines differ precisely in how they handle it: the radial ladder **rejects** it, mesh compensation **closes** a few loops on top of a radial base, and the nodal solver **solves the whole mesh directly**.

### 3.2 The three engines at a glance ▶

| | **Radial** (`/load-flow`) | **Meshed** (`/meshed-load-flow`) | **Nodal** (`/nodal-load-flow`) |
|---|---|---|---|
| Method | forward-backward ladder | radial base **+ loop compensation** | **Y-bus**, Gauss–Seidel or Newton |
| Network | tree only (rejects loops) | tree + a **few** weak loops | any mesh, arbitrarily dense |
| Loops | hard error | closes them by circulating power | solved natively |
| Multiple infeeds | no | no | **yes** (multi-slack) |
| Grid-forming (PV) gen | no | no | **yes** |
| Cost | cheapest | cheap | pays for a matrix solve |
| Best for | ordinary feeders | ring mains, closed link boxes | dense grids, dual-fed, PV |

💡 The plain reading: **radial** for a normal feeder, **meshed** for a ring or a link box, **nodal** when the mesh is dense or there's more than one source. And if you'd rather not choose, §3.5's router chooses for you.

### 3.3 Tier 1 — meshed load flow by compensation

#### 3.3.1 What it does ▶

`/analysis/meshed-load-flow` takes the *same* start and nominal voltage as the radial load flow and returns the *same* per-node result — voltages, loadings, overloads — but with the loops **closed**. Under the hood it solves a radial spanning tree (breaking each loop open), then injects a **circulating power** around each loop to drive its two open ends back to the same voltage. It's built for **weak meshing**: a handful of independent loops — a ring main, one or two closed link boxes — not a dense grid (that's the nodal engine's job). On a network that turns out radial, it degrades cleanly to an ordinary load flow with zero compensation passes.

The request adds one optional field, `max_iterations`, capping the compensation passes for pathologically stiff loop sets; the default cap is 25, and a single independent loop closes in one pass.

#### 3.3.2 Reading the result — the loops list ▶

Alongside the familiar nodes/barriers/overloaded lists, the response carries a **`loops`** array — one entry per loop the engine found:

| Field | Meaning |
|---|---|
| `closer` / `existing` | the two features the loop-closing edge joins |
| `loop_resistance_ohm` / `loop_reactance_ohm` | the loop's own series impedance |
| `residual_mismatch_v` | the leftover voltage gap after compensation — **≈ 0 for a properly closed loop** |
| `impedance_resolved` | whether every branch on the loop had its impedance resolved |
| `compensated` | whether the loop was actually closed |
| `note` | a plain-language reason when it wasn't |

⚠️ **A loop that meets at an open device is left open on purpose.** If the two ends of a loop close only through a normally-open tie (a barrier the radial walk already stops at), the engine reports it in `loops` with `compensated: false` and the note *"loop meets at an open device — left open, not compensated."* This is deliberate: **meshed load flow never silently energises a normally-open tie.** Likewise, a loop through only zero-impedance junctions ("an ideal wire loop") is flagged `compensated: false` rather than dividing by zero — its two ends are already the same electrical point.

`compensation_passes` in the response tells you how many outer passes ran (0 = the network was radial), and `iterations` is the inner sweep count (still 3, as in Chapter 2).

#### 3.3.3 Method — closing a loop by circulating power ∑

The engine never sees a general graph. `buildTree` walks the network and, each time it finds an edge that would close a loop, records it as a **`meshBreak`** and keeps walking the spanning **tree**. So mesh.go receives a *radial tree plus a list of broken loops*, and `runSweep` solves that radial base case exactly as Chapter 2 describes.

Each broken loop is two tree paths — from a **lowest common ancestor (LCA)** down to the two endpoints of the loop-closing edge. Call them `existing` and `closer`. Closing the (ideal-connectivity) edge means those two endpoints **must be at the same voltage**, but the radial base sweep leaves a **mismatch**:

$$M = V(\text{existing}) - V(\text{closer})$$

To drive `M → 0`, a **circulating power** `(cP, cQ)` is injected around the loop — *added* to the branches toward `closer` (segment B) and *subtracted* from the branches toward `existing` (segment A). Because the drop model `ΔV ≈ (P·R + Q·X)/V` is linear in `(P, Q)` when `V` is held at the loop's own base, the effect superposes cleanly and one linear step closes the loop:

$$dP = -\,\frac{M \cdot V_{\text{ref}}}{R_{\text{loop}} + k \cdot X_{\text{loop}}}, \qquad dQ = k \cdot dP$$

where `R_loop`, `X_loop` are the loop's summed series resistance and reactance, `V_ref` is the endpoints' **own** voltage base (LV below a transformer, MV above — so an LV ring's circulation isn't scaled by an MV nominal), and `k` is the load power factor ratio (below). The correction is accumulated, the states are reset to the radial base, every loop's correction is re-applied, and a forward sweep re-runs — Gauss–Seidel style — so **loops that share branches reconcile** over the outer passes. Convergence is declared when the largest residual mismatch drops below **`1e-4` V** (a small fraction of a volt on any real base).

#### 3.3.4 Why circulate at the *load* power factor ∑

The continuity the two endpoints must satisfy is a single scalar equation — `cP·R_loop + cQ·X_loop = −V·M` — one equation in two unknowns `(cP, cQ)`. A second assumption is needed to pin them, and the choice matters:

- **VoltEdge's choice: `cQ = k·cP` with `k = Q/P` of the load crossing the break** — circulate at the *load's own power factor*. For a purely real load this reduces to purely real circulation, and — critically — it reproduces the **exact 50/50 split** that two identical parallel conductors must carry.
- The tempting alternative — circulate at the *loop-impedance angle* (`cQ/cP = X/R`) — injects spurious reactive circulation and **mis-splits even the symmetric case** (roughly 75/25 rather than 50/50 for two identical legs). It was tried first, produced exactly that wrong split, and was replaced.

This is the honest magnitude-only stand-in for the phasor-angle continuity equation a full AC solve would form. It's documented as a planning-fidelity linearization, not pretended to be an exact AC result — which is why, past a few weak loops, the router hands off to the nodal engine.

#### 3.3.5 Worked example — the Oakwood closed link box ▶∑

**▶** Oakwood's two LV feeders off **TX-Feeder7-014** are tied through a normally-closed link box, **LB-Oakwood-3**, so a fault on either feeder can be back-fed from the other. Tom wants to confirm the ring is balanced and neither leg is overloaded, so he runs a **meshed load flow** from the transformer's LV board, 415 V.

```
TX-Feeder7-014  LV board (415 V)
  ├─ Leg A  (185 mm² AL, 80 m) ─┐
  │                             ├─►  LB-Oakwood-3 (closed)  →  60 kW load
  └─ Leg B  (185 mm² AL, 80 m) ─┘
```

**∑** The two legs are **identical**. The radial base sweep breaks the loop at the link box, feeding the whole 60 kW down one leg and leaving a large mismatch `M` at the open end. The engine computes `R_loop`, `X_loop` (leg A + leg B in series), reads `k = Q/P` from the 60 kW load's power factor, and injects one circulating correction. Because the legs are symmetric and the load is real, `cP` lands exactly where each leg carries **30 kW** — the 50/50 split — and `residual_mismatch_v` falls below `1e-4` V in a **single** compensation pass. `compensated: true`, `impedance_resolved: true`.

**▶** Tom reads: each leg carries half the load (≈ 30 kW, well inside its ampacity), the link box sits at a healthy voltage, and the ring is balanced. Now suppose leg B were the thinner **95 mm²** cable: the same run would split the load in **inverse proportion to impedance** — more through the fatter, lower-impedance leg A — and Tom would see leg A running hotter, exactly the asymmetry he'd want to know before relying on the tie under contingency.

If someone had instead left the link box **open** (a normally-open tie), the loop would come back `compensated: false` with the "meets at an open device" note — the engine's way of telling Tom the ring isn't actually closed, rather than pretending to energise it.

### 3.4 Tier 2 — nodal Y-bus load flow

#### 3.4.1 What it does ▶

`/analysis/nodal-load-flow` is a **different solver entirely**. Instead of walking a tree, it assembles a **nodal admittance matrix (Y-bus)** over the whole graph and solves it directly — so a dense, multi-path mesh is *solved*, not refused or approximated loop-by-loop. It's also the **only** engine that models:

- **multiple infeeds** (several grid sources / slack buses), and
- **grid-forming (PV) generation** — a generator that holds its terminal voltage to a setpoint and lets the solver find the reactive power.

The trade-off: it pays for a matrix solve, so you wouldn't reach for it on an ordinary radial feeder. Like every study here it's read-only.

#### 3.4.2 The bus/branch model ∑

Before solving, the engine **contracts** the connectivity graph into an electrical model:

- A **bus** is a maximal set of features shorted together through zero-impedance elements — junctions, meters, busbars, containers, *and* any conductor or transformer whose impedance/ratio didn't resolve. A union-find (disjoint-set) over the edges builds these.
- A **branch** is a series element: a conductor with resolved `R + jX`, or a transformer with a resolved ratio. Only these separate two buses; everything else contracts into a bus. **Two conductors in parallel between the same pair of buses are two branches** and split by inverse impedance — exactly the meshed case the radial engine can't do.
- Everything is **per-unit** on a fixed system base (`baseVA = 1 MVA` — it cancels out of the reported volts and amps; 1 MVA just keeps the numbers near order 1). Each bus's **voltage base** is propagated by BFS from the slack across the branches: a conductor keeps the base, a transformer multiplies by its turns ratio going primary→secondary. Choosing the bases in the transformer's turns ratio makes an at-nominal transformer a **1:1 branch in per-unit**, so a conductor and a transformer stamp into Y-bus identically — the only transformer-specific work is the base change.

Two honest first-cut limits are documented in the code: **off-nominal taps aren't yet modelled** in the nodal engine (deferred with the Newton core; every seed it targets is at-nominal), and an **un-surveyed transformer** (resolved ratio, no nameplate %Z) is stamped with a negligible reactance `idealXfmrXPU = 1e-4` pu so its two windings tie at ~equal per-unit voltage without making the matrix singular. A transformer *with* a real %Z uses its real impedance and never touches that fallback.

Each branch's per-unit admittance is `y_pu = Z_base / Z_ohm` with `Z_base = V_base² / baseVA`, stamped into Y-bus the standard way (`+y` on both diagonals, `−y` on the two off-diagonals).

#### 3.4.3 The Gauss–Seidel solver ∑

The default solver is **Gauss–Seidel**. Every bus starts flat (`1∠0` per-unit); each slack is pinned at its setpoint `∠0`. Then it sweeps the buses repeatedly:

- a **PQ bus** (a load, or generation as negative demand) updates its voltage from its injected power and its neighbours: `I_i = conj(S_i / V_i)`, then `V_i = (I_i − Σ_{j≠i} Y_ij V_j) / Y_ii`;
- a **PV bus** (grid-forming generator) holds its magnitude, fixes its real injection, and solves the reactive `Q = Im(V_i · conj(Σ Y_ij V_j))` — resetting the magnitude to the setpoint each pass;
- a **slack bus** is never updated;
- an **isolated bus** (zero diagonal, an island no branch reaches) is skipped rather than divided by zero.

It repeats until the largest per-unit voltage change falls below `gsTolerancePU = 1e-9` (converged) or `gsMaxIter = 5000` passes elapse — in which case it's returned **flagged not converged**, never silently trusted. GS converges slowly but monotonically on a well-conditioned distribution network.

#### 3.4.4 Multiple infeeds & grid-forming PV ▶∑

**▶** A real substation may feed a grid from **two transformers at once**, or a large embedded generator may **hold the voltage** at its point of connection. The nodal engine models both:

- **`sources[]`** in the request adds extra **slack** infeeds beyond the start — each held at the run nominal, each with its own synthetic ideal bus behind any source impedance (so it sags under load just as the start does). A requested source that the walk never reaches is an **error**, not a silent drop.
- **`regulated[]`** designates **PV (grid-forming)** generators: each holds its terminal to a per-unit `voltage_pu` setpoint and injects its scheduled real power, and the solver finds the reactive. A generator is *already* PV if its asset carries a `voltage_setpoint_pu` field; the request can force or override one per-run, and set a symmetric reactive limit (`q_min_kvar`/`q_max_kvar`) that, when it binds, reverts the bus to PQ at the clamped value (standard PV→PQ switching).

**∑** The payoff is in the result: each source and PV bus reports **`supplied_kw`/`reactive_kvar`** — the net power that infeed is actually pushing at the solved operating point — so a multi-source run **shows how the load splits across the infeeds**. `slack_count` tells you how many infeeds were modelled.

#### 3.4.5 The Newton–Raphson solver ∑

Gauss–Seidel is proven but only **linearly** convergent — fine for a well-conditioned network, slow (or crawling) on a stiff, heavily-meshed grid. So the same assembled model can instead be solved by **Newton–Raphson** — opt in with `"solver": "newton"` (Gauss–Seidel stays the default so existing callers' numbers don't move). Both consume the same Y-bus and return the same result shape; the response **echoes which solver ran**.

Newton uses the textbook **polar** formulation. The unknowns are the voltage **angle** at every non-slack bus and the voltage **magnitude** at every PQ bus. The mismatches are the real-power residual `ΔP` (every non-slack bus) and the reactive residual `ΔQ` (every PQ bus). Each iteration solves the linear system

$$J \cdot \Delta x = \text{mismatch}, \qquad J = \begin{bmatrix} H & N \\ M & L \end{bmatrix}$$

where the four blocks are the standard partials of `(P, Q)` with respect to `(θ, |V|)`, then updates and repeats until the largest mismatch falls below `newtonTolerancePU = 1e-9` — capped at `newtonMaxIter = 40`, though a well-conditioned network reaches machine tolerance in a **handful** of iterations because the residual squares each step (quadratic convergence). The one new numerical primitive — solving the dense linear system — is a **hand-rolled Gaussian elimination with partial pivoting** (`solveDenseLinear`), deliberately with **no external linear-algebra dependency**: a distribution study is bounded to a few hundred buses, where a dense O(m³) solve is a few milliseconds. A singular Jacobian returns what it has, flagged not converged. PV reactive-limit switching latches one-way per solve (oscillation-free).

#### 3.4.6 Reading the nodal result ▶

The response leads with the solver's own honesty: **`converged`** (true/false), **`iterations`**, **`max_mismatch_pu`** (the final residual), **`solver`** (which ran), and `bus_count`/`branch_count`/`slack_count` describing the contracted model actually solved. Each node then carries a **`role`**:

- **`bus`** — a junction/meter/load: reports its solved bus voltage;
- **`slack`** — a source infeed: reports `supplied_kw`/`reactive_kvar`;
- **`pv`** — a grid-forming generator: reports its real generation and the reactive the solver found;
- **`conductor` / `transformer`** — a branch: reports `current_amps`, `through_kw`/`through_kvar`, and `loading_pct` against ampacity/rating, feeding the same `overloaded` list.

⚠️ **Always check `converged` first.** A non-converged solve (GS hit its 5000-pass cap, or Newton's Jacobian went singular) returns numbers, but flagged — read `max_mismatch_pu` to see how far off, and don't trust the voltages until it's true.

#### 3.4.7 Worked example — the two-infeed Oakwood grid ▶∑

**▶** Oakwood's secondary grid is meshed and fed from **two** distribution transformers — TX-Feeder7-014 and TX-Feeder7-019 — for reliability. Tom wants to know how the two infeeds share a 200 kW block of load, and whether an embedded solar inverter can hold the grid voltage. He runs a **nodal load flow** from TX-Feeder7-014's LV board, 415 V, adds TX-Feeder7-019 in `sources[]`, and designates the inverter in `regulated[]` at `voltage_pu = 0.983` (≈ 408 V).

**∑** The engine contracts the grid to buses and branches, propagates the 415 V base from both slacks, assembles the Y-bus, and solves. With the two infeeds sitting behind different source impedances, the 200 kW splits **in inverse proportion to impedance** — say roughly **2 : 1**, ≈ 133 kW from the lower-impedance TX-Feeder7-014 and ≈ 67 kW from TX-Feeder7-019, each reported as that bus's `supplied_kw`. The inverter's PV bus holds ≈ 408 V and reports the `reactive_kvar` it had to inject to do so.

**▶** Tom reads: the grid is balanced, neither transformer is overloaded, the load-sharing between infeeds is as expected, and the inverter is comfortably within its reactive capability holding voltage. Had he run this against the radial ladder, it would simply have refused — two sources and a mesh are beyond a tree. (He could also flip `"solver": "newton"` and confirm the same voltages in a handful of iterations rather than Gauss–Seidel's slower crawl — a useful cross-check that both solvers agree.)

### 3.5 The auto-dispatch router — `load-flow-auto` ▶∑

**▶** You don't have to know your network's topology to pick an engine. **`/analysis/load-flow-auto`** takes the union of what all three engines need, **classifies the network once**, runs the right engine unchanged, and tells you which one ran and why.

**∑** Classification is topology-first and reuses the exact machinery the three engines already disagree over: one barrier-aware downstream walk (`buildTree` with a breaks accumulator) **counts the independent loop-closing edges**. Selection then follows a fixed order:

1. an explicit **`engine`** override (`"radial"`/`"meshed"`/`"nodal"`) always wins — for a caller who already knows, or wants to compare engines on one network;
2. else any requested **`sources[]` or `regulated[]`** → **nodal** (the only engine that models multi-slack / PV), regardless of loop count;
3. else **0 loops** → **radial** ladder (fastest, exact for a tree);
4. else **1…3 loops** (`routerMaxWeakLoops = 3`) → **meshed** compensation;
5. else **> 3 loops** → **nodal** Y-bus (dense mesh).

The threshold of 3 is deliberately conservative: compensation is accurate and cheap for a few genuinely weak loops but isn't meant to stack many strongly-interacting ones, so past that the honest choice is the Y-bus solver. The response is an **envelope** — `engine`, a one-line `engine_reason`, the `loop_count` found, and exactly one of `radial`/`meshed`/`nodal` populated with that engine's **native, unchanged** response — so a client renders it with the same view it already uses for that endpoint and only has to read `engine` to know which ran.

💡 **Reach for `load-flow-auto` as your default** unless you specifically want to force or compare engines. It costs one extra cheap graph walk and removes the "which endpoint?" guesswork — and a loop that only closes through an open device is still *counted* (then reported by the meshed engine as an open, uncompensated loop), an outcome the radial ladder could never produce because it would reject the loop outright.

### 3.6 Choosing an engine — a practical guide ▶

| Your network | Engine | Why |
|---|---|---|
| Ordinary radial feeder | radial (or auto) | fastest, exact for a tree |
| Ring main / one or two closed link boxes | meshed (or auto) | closes weak loops by compensation |
| Dense/urban meshed secondary grid | nodal | solves arbitrary mesh directly |
| Two or more infeeds | nodal | only engine with multi-slack |
| Grid-forming generation (voltage-holding) | nodal | only engine with PV buses |
| Not sure | **auto** | classifies and picks for you |
| Comparing solvers on one network | explicit `engine` override | skips classification |

### 3.7 Tips & gotchas ▶

- **A loop is a hard error only on the plain `/load-flow`.** If you hit *loop detected*, switch to `/meshed-load-flow`, `/nodal-load-flow`, or just `/load-flow-auto` — don't "fix" the data by opening a legitimately-closed ring.
- **Check `compensated` and `residual_mismatch_v` on meshed runs.** A loop reported `compensated: false` (open device or ideal-wire) wasn't closed; a large residual means the loop didn't converge in the passes allowed (raise `max_iterations`).
- **Check `converged` on nodal runs.** Numbers come back either way; only trust them when it's true and `max_mismatch_pu` is tiny.
- **Multi-slack and PV are nodal-only.** Sending `sources[]`/`regulated[]` to the radial or meshed engine does nothing; send them to the nodal engine (or let the router route them there).
- **Newton is opt-in.** Gauss–Seidel is the default and proven; flip to `"solver": "newton"` for a stiff dense grid or as a cross-check, and confirm both agree.
- **Off-nominal transformer taps aren't modelled in the nodal engine yet** — it assumes at-nominal ratios. For a study that hinges on a specific tap position, the radial ladder (which does apply the tap factor) is the more faithful engine today.

---

*Next chapter: **Fault current & embedded-generation infeed** — the three-phase and single-line-to-ground short-circuit study, symmetrical-component impedances, and how an embedded generator adds a second fault source by superposition. Tell me to proceed (or name a later chapter).*

---

## Chapter 4 — Fault current & embedded-generation infeed

The load flow of Chapters 2–3 answers *"how does the network behave under normal load?"* This study answers the opposite question: *"how much current flows if a conductor is bolted to another phase, or to earth, right here?"* That **prospective fault current** is what every protective device must be rated to interrupt and set to detect — too low a rating and the switchgear explodes trying to clear a fault; too optimistic a protection setting and it never trips. VoltEdge computes it with the *same* radial-tree machinery as the load flow, but run in the mirror-image direction, and layers on two things a real distribution study needs: the **single-line-to-ground** fault (the most common fault type, and the one that needs symmetrical components) and **embedded-generation infeed** (a second fault source the grid figure alone would miss).

### 4.1 What it is and when to run it ▶

Run a fault-current study (`/analysis/fault-current`) whenever a rating or a protection setting depends on how hard the network can push current into a short circuit:

- **specifying or checking switchgear** — a breaker's *breaking capacity* must exceed the prospective fault current where it sits;
- **setting protection** — an overcurrent or earth-fault relay's pickup and grading depend on the fault current it must see (and the minimum fault it must still detect at the far end);
- **checking a new connection** — adding an embedded generator *raises* the fault level nearby, and can push existing switchgear past its rating;
- **arc-flash and coordination studies** — both (Chapters 5 and 7) are built on this one.

It reports, for every feature from the source down to the leaves, the **series impedance** back to the source and the **prospective fault current** there — and, on an SLG run, the ground-fault current and whether the point is even grounded.

⚠️ This is a **radial** study: like the plain load flow it rejects a meshed network (a series-impedance path is only well-defined on a tree). For a fault level inside a closed ring, open the loop for the study or model it as the conservative radial case.

### 4.2 The two fault types ▶

`fault_type` selects between:

- **`three_phase`** (the default) — the symmetrical **three-phase bolted fault**. All three phases short together; the network is balanced, so only the **positive-sequence** impedance matters. This is usually the *highest* fault current, so it sizes switchgear breaking capacity.
- **`slg`** — the **single-line-to-ground fault**. One phase shorts to earth. It's the *most common* real fault, and it needs the **zero-sequence** network on top of the positive-sequence one, because earth-return current behaves completely differently (§4.6).

You'd typically run **three-phase** to size equipment (worst-case duty) and **SLG** to set earth-fault protection and to check the ground-fault level where the earthing arrangement makes it high.

### 4.3 Inputs — the request ∑

```json
{ "start": { "feature_class": "MV_CONDUCTOR", "feature_id": "…" },
  "nominal_voltage_v": 11000,
  "fault_type": "three_phase",
  "fault_impedance_ohm": 0 }
```

`start` and `nominal_voltage_v` are required, exactly as the load flow requires them (and for the same reason — nominal is not guessable). `fault_type` defaults to `three_phase`. `fault_impedance_ohm` is the fault's *own* impedance `Z_f` — **0 for a bolted fault** (the worst case), or a positive value to model an arcing/resistive fault; it enters the SLG denominator as the standard `3·Z_f` term and must not be negative.

### 4.4 Reading the result ▶

Each node carries the impedance back to the source and the fault current there:

| Field | Meaning |
|---|---|
| `resistance_ohm` / `reactance_ohm` / `impedance_ohm` | accumulated series `R`, `X`, `|Z|` from the source to this point, on this node's own base |
| `nominal_base_v` | the base those ohms and the current are referred to (run nominal above a transformer, secondary below) |
| `fault_current_amps` | the **prospective bolted-fault current** here — the total duty switchgear must interrupt |
| `grid_contribution_amps` / `generator_contribution_amps` | *(only when a generator contributes)* the split between grid infeed and embedded generation |
| `circuit_id` / `circuit_ref` | which circuit this point belongs to |
| `material_resolved` | whether every impedance-bearing element on the path resolved (else the current is an optimistic **upper bound**) |

On an SLG run each node additionally reports `grounded`, `zero_seq_resistance_ohm`/`zero_seq_reactance_ohm`, `ground_fault_current_amps`, `ground_path_resolved`, and `zero_seq_estimated` (§4.6).

⚠️ **Read `material_resolved` on the worst-case nodes.** A fault current computed with a missing conductor or transformer impedance is *too high* — the missing ohms would only reduce it — so an unresolved path gives an optimistic upper bound, flagged. And a node with **exactly zero** path impedance (an ideal source with no conductor yet between it and the fault) reports **no current** rather than a bogus infinity — the honest read of *"unbounded behind an ideal source; set `source_r_ohm`/`source_x_ohm` on the start feature for a finite figure."*

### 4.5 Method — forward impedance accumulation ∑

The fault study is the load flow's mirror image. Where the load flow accumulates *demand* **backward** (leaves → source) and derives voltage forward, the fault study accumulates *series impedance* **forward** (source → leaves) and derives current at each node as

$$I = \frac{V_{\text{base}}}{|Z_{\text{total}}|}$$

Everything up to the accumulation — resolving the start, loading barriers, building the radial tree (rejecting a mesh), resolving each node's own branch impedance — is the *exact same machinery* the load flow uses, reused not reimplemented. The forward walk (`accumulateSeriesImpedance`) processes the tree root-first, so a parent's accumulated impedance is always in hand before its child's:

- the **start** node seeds the total with the **source's own Thévenin impedance** (`source_r_ohm`/`source_x_ohm`) on the run nominal base;
- a **conductor** adds its own resolved `R + jX`;
- a **junction/container** adds nothing (a bolted pass-through node);
- a **transformer** refers the upstream (primary-base) impedance onto the **secondary base** — ohms scale by `ratio²` — then adds the transformer's own series impedance (already referred to the secondary), and **rebases every node below it** to the secondary voltage.

⚠️ That transformer step is why **the transformer's own %Z dominates an LV fault level**: the huge 11 kV source stiffness, referred across the ratio², shrinks to almost nothing on the 415 V base, and the transformer impedance is what's left. An **unresolved** conductor or a transformer with **no nameplate %Z** adds zero but flips the path's `resolved` flag false — so the resulting current is marked an optimistic upper bound (§4.4), not silently trusted.

### 4.6 The single-line-to-ground fault ∑

A balanced three-phase fault sees only the positive-sequence network `Z1` — which `accumulateSeriesImpedance` already builds. An SLG fault current is the symmetrical-component result

$$I_{\text{SLG}} = \frac{3 \cdot V_{\text{base}}}{|Z_1 + Z_2 + Z_0 + 3 \cdot Z_f|}$$

with the **negative-sequence** `Z2` taken equal to `Z1` (correct for the static plant a distribution network is made of), and the **zero-sequence** `Z0` a genuinely *different* network. Zero-sequence current is in-phase in all three phases and returns through **earth**, so it only flows where a winding provides a ground path — and a transformer's winding connections decide whether it passes through, is blocked, or is grounded locally. `accumulateZeroSeqImpedance` builds `Z0` on the same radial tree:

- **Line/conductor** — extends the ground path (if grounded) by its own zero-sequence impedance: measured `r0`/`x0` when present, else its positive-sequence `R`/`X` scaled by `zeroSeqLineFactor = 3×` (the standard planning value for an overhead earth-return path — and it marks the path **estimated**).
- **Transformer, YNyn** (grounded wye both sides) — zero-sequence **passes through**: refer upstream `Z0` across `ratio²` and add the transformer's own `Z0`.
- **Transformer, Dyn-type** (delta or ungrounded-wye HV, grounded-wye LV) — the LV neutral **grounds this side** and the HV winding **blocks** the upstream network, so `Z0` **restarts** at the transformer's own `Z0`, independent of anything above. (This is the common distribution transformer.)
- **Transformer, delta/ungrounded-wye LV** — **no** ground path onto this side; everything below is `grounded = false`.

A node reported **`grounded = false`** draws essentially no SLG current — an ungrounded section — until an **earthing transformer** provides a path. Earthing transformers (`z0_ohm`, plus a neutral earthing resistor `neutral_resistance_ohm` seen at `3×` its physical value) are pre-scanned and applied as a shunt to earth at the bus they tap: at an already-grounded bus they **parallel** the existing path (a stiffer earth, lower `Z0`), and at an otherwise-ungrounded bus they **establish** the ground (flipping `grounded` false→true from that bus down — the whole point of one). A transformer's own `Z0` is its nameplate `z0_pct` when surveyed, else taken equal to `Z1` (which marks the path estimated).

💡 Two flags travel with each SLG node: **`ground_path_resolved`** (did every element have *some* impedance?) and **`zero_seq_estimated`** (is any of it a zero-sequence *guess* — a line's 3× default, a transformer's `Z0 = Z1`, or the source's `Z0 = Z1`?). They're independent — a fully-resolved path can still be estimated — and a good SLG figure has both in your favour: resolved true, estimated false.

### 4.7 Embedded-generation infeed ▶∑

**▶** A grid isn't the only thing that feeds a fault. An **embedded generator** — a diesel set, a solar inverter, a battery — pushes its own current *into* the fault through its internal impedance and the network between it and the fault. Ignore it and you understate the duty; the switchgear near a large generator may need a higher rating than the grid figure alone suggests. So on a **three-phase** run, every `GENERATOR` in the tree is treated as a **second fault source**, and `fault_current_amps` becomes the **total** — grid plus generation — with the split surfaced in `grid_contribution_amps` / `generator_contribution_amps` whenever a generator actually contributes.

**∑** Each generator's contribution is computed by **superposition**. For a generator `g` and a fault node `f`, the loop is `g`'s internal fault impedance in series with the network path `g → f`. On a radial tree that path is read off a **common-base** impedance accumulation as the standard two-node impedance `Z(g) + Z(f) − 2·Z(lca)` (via the lowest common ancestor), referred to `g`'s terminal base; the current leaving `g` is `V_g / |Z_loop|`, referred to the fault's own side by `V_g / V_f`, and all generators' contributions sum. The generator's internal fault impedance follows a **precedence** (graceful degradation, mirroring `dispatch_kw → rated_kw`):

1. **`fault_contribution_ka`** — the explicit interconnection-study figure (authoritative, and the natural fit for a current-limited inverter);
2. else **`subtransient_reactance_pct`** (`X″d`) on the machine's rated-kVA base — the classic synchronous-machine value;
3. else, for an **inverter** subtype (`solar_pv`/`battery`), a fallback of `inverterFaultMultiple = 1.2×` rated current (power electronics current-limit at ~1.1–1.5× rather than presenting a reactance);
4. else **no contribution**. (A synchronous machine gets *no* default — assuming one would badly misstate its far larger contribution — so it needs `X″d` or an explicit kA.)

Note the fault contribution is keyed off **nameplate `rated_kw`, not `dispatch_kw`**: a curtailed or part-loaded machine still connected contributes its *full* sub-transient fault current — only removing it from the network (e.g. `operational_state: ISOLATED`) zeroes the infeed.

Two honest scope notes: the superposition is **exact** when grid and generator feed the fault through *disjoint* impedance (the dominant case — a generator downstream of, or on a sibling branch to, a fault nearer the grid), and a slightly **conservative over-estimate** when they share series impedance (a fault downstream of the generator) — the safe direction to err for a rating check. And **generator infeed applies to the three-phase fault only**: SLG generator infeed depends on the machine's grounding and its interface transformer's zero-sequence connection — data the model doesn't yet collect — so **SLG figures stay grid-only**, a documented Tier A limit.

### 4.8 Worked example — sizing the LV switchboard at TX-Feeder7-014 ▶∑

**▶** Dana is specifying the LV switchboard under **TX-Feeder7-014** (315 kVA, 11 kV/415 V, 4.5 % Z) and needs the prospective fault current at its LV board. Tom runs a **three-phase** fault study from the Feeder 7 head, nominal 11 000 V.

**∑** Walking the impedance forward:

- **Source (11 kV):** a stiff grid, source impedance ≈ `0.05 + j0.48 Ω` on the 11 kV base.
- **Across the transformer:** the secondary base is `Z_base = 415² / 315 000 = 0.547 Ω`, so the transformer's `4.5 %` impedance is `0.045 × 0.547 = 0.0246 Ω` referred to 415 V. The 11 kV source impedance, referred onto the 415 V base by `ratio² = (415/11 000)² = 0.00142`, becomes `≈ 0.0007 Ω` — **negligible**.
- **At the LV board:** `|Z| ≈ 0.0246 + 0.0007 = 0.0253 Ω`, so `I = 415 / 0.0253 ≈ 16.4 kA`.

The transformer's own 4.5 % impedance supplies **~97 %** of the limiting impedance — the textbook result that an LV fault level is set by the transformer, not the grid behind it. Dana specifies a board with breaking capacity comfortably above 16.4 kA.

**▶ Now add a generator.** Suppose a 500 kVA diesel set (`X″d = 15 %`) is connected to that same LV board for standby. Its internal fault impedance is `0.15 × 415² / 500 000 = 0.0517 Ω`; feeding a fault on its own bus, it contributes `415 / 0.0517 ≈ 8.0 kA`. The study now reports:

- `grid_contribution_amps ≈ 16.4 kA`,
- `generator_contribution_amps ≈ 8.0 kA`,
- `fault_current_amps ≈ 24.4 kA` (the total).

That 24 kA — not 16 — is the duty the board must interrupt. This is exactly the check that catches a standby generator quietly pushing existing switchgear past its rating.

**▶ The earth-fault view.** Tom re-runs as **SLG**. TX-Feeder7-014 is a **Dyn** transformer, so its LV neutral grounds the board and blocks the 11 kV network: `Z0` restarts at the transformer's own zero-sequence impedance. With `Z0 ≈ Z1` (unsurveyed, so `zero_seq_estimated = true`) and `Z2 = Z1`, the denominator is `≈ 3·Z1` and the SLG current lands near the three-phase figure — but `grounded = true` confirms the board *has* an earth path, which an ungrounded delta section downstream would not. Tom notes the `estimated` flag as a prompt to get the transformer's real `z0_pct` before finalising earth-fault settings.

### 4.9 Tips & gotchas ▶

- **Bolted (`fault_impedance_ohm = 0`) is the worst case** — use it to size equipment. Add a positive `Z_f` only to model a specific arcing/resistive fault (it lowers the current).
- **The transformer %Z dominates LV faults.** If an LV fault level looks wrong, check the transformer's impedance field first — it's the biggest lever.
- **Watch `material_resolved` (and `zero_seq_estimated` on SLG).** An unresolved path over-states the fault; an estimated zero-sequence figure is a planning default, not measured data.
- **Generator infeed is three-phase only.** Don't read an SLG `fault_current_amps` as including embedded generation — it's grid-only by design.
- **`grounded = false` is information, not an error.** It correctly says an SLG fault there draws almost nothing — until an earthing transformer is added on the delta section.
- **It's radial.** A meshed fault study isn't supported; open the ring for the study.

---

*Next chapter: **Protection coordination** — time-current characteristic (TCC) curves, how VoltEdge grades upstream and downstream devices, and reading a coordination margin. Tell me to proceed (or name a later chapter).*

---

## Chapter 5 — Protection coordination

Chapter 4 told you *how much* fault current flows. This study asks the next question: *when a fault happens, do the protective devices trip in the right order?* A well-protected feeder clears a fault with the device **nearest** the fault — the fuse on the faulted lateral, not the breaker at the substation — so the rest of the network stays energised. That's **selectivity** (or discrimination), and achieving it means every upstream **backup** device must be *slower* to operate than the downstream **primary** device it backs up, by a safe margin. The coordination study (`/analysis/coordination`) walks the path from a source to a postulated fault, works out how long each protective device on that path would take to clear it, and checks every adjacent pair grades correctly.

### 5.1 What it is and when to run it ▶

Run a coordination study whenever the *timing* of protection matters:

- **commissioning a new protective device** — confirm a new fuse or relay grades with what's upstream and downstream;
- **changing a relay setting** — a new pickup or time dial can silently break coordination two devices away;
- **investigating a nuisance trip** — a breaker that tripped for a fault that a downstream fuse should have cleared is a coordination failure;
- **planning a lateral or recloser** — check the new device coordinates before it goes in.

It reports, for one source→fault scenario: the prospective fault current, every protective device on the path with **how long it would take to operate**, and a pairwise **margin** check against the coordination time interval — plus the full time-current curves for plotting.

⚠️ Like the fault study it's built on, this is a **radial**, single-scenario study: one source, one fault location, along the series path between them. Coordination is checked *for that fault* — you'd typically run it for a fault at the far end of each protected section.

### 5.2 Inputs — the request ∑

```json
{ "start": { "feature_class": "MV_CONDUCTOR", "feature_id": "…" },
  "fault": { "feature_class": "MV_CONDUCTOR", "feature_id": "…" },
  "nominal_voltage_v": 11000,
  "cti_seconds": 0.3 }
```

`start` is the source and `fault` is the feature a fault is postulated at — it **must lie downstream of start** (reached by a live path; a fault behind an open device or not below start is rejected with a clear message). `nominal_voltage_v` is the pre-fault sending-end voltage as everywhere else. `cti_seconds` is the **coordination time interval** — the minimum acceptable margin between a backup and its primary — defaulting to **0.3 s** (a common relay-to-relay value: breaker operating time + relay overshoot + a safety allowance). Override it per study (fuse-fuse coordination often uses a different rule).

### 5.3 Which devices are graded ▶

The study grades every **time-graded protective device** on the source→fault path:

| Device | How it's graded |
|---|---|
| **MV_BREAKER** | via its **PROTECTIVE_RELAY**, found through the `protection_trip` relationship (relay pickup, curve type, time dial, instantaneous element) |
| **MV_FUSE** | inline, via its `curve_ref` (a manufacturer ampere-quantity curve) |
| **fused MV_SWITCH / LV_SWITCH** | via the same `curve_ref`, but **only** when `is_fused` is set |
| **MV_RECLOSER** | inline, via its `slow_curve_ref` (the delayed curve that must coordinate downstream), falling back to `fast_curve_ref` |

An **unfused switch** appears on the path but is a plain topological hop — no protection characteristic, no operate time. **Sectionalizers** are deliberately out of scope here: they count fault passages during a recloser's dead time rather than time-grade, so they aren't part of a time-current check (a natural follow-up, noted in the code).

💡 A **recloser** carries its own characteristic and needs no separate relay; coordination grades it on its **slow** curve, because the fast (trip-and-reclose) curve is about clearing transient faults, while the slow curve is the one that must discriminate with downstream devices on a persistent fault. Its reclose attempts and dead times shape the *reclosing sequence*, not the single-shot operate time this check compares.

### 5.4 Reading the result ▶

The response has three layers: the **devices**, the pairwise **pairs**, and an **overall** verdict.

Each **device** carries:

| Field | Meaning |
|---|---|
| `role` | `primary` (nearest the fault that actually clears it) or `backup` (every operating device upstream) |
| `order` | position on the path, 0 = nearest the fault |
| `through_current_amps` | the fault current *this device* sees (referred to its own voltage base) |
| `operate_time_s` | how long it would take to clear this fault — **nil** if it has no settings or the current is at/below pickup (it never operates) |
| `curve` | the curve/curve-type used |
| `resolved` | whether an operate time was found |
| `extrapolated` | true if the current fell outside the tabulated curve range (result clamped to the nearest endpoint) |
| `note` | a plain-language reason when something's missing |
| `curves` | the full time-current polyline(s) for plotting (§5.6) |

Each **pair** is one adjacent (downstream primary, upstream backup) couple: `margin_s = t_upstream − t_downstream`, the run's `cti_seconds`, and **`coordinated`** (true when `margin_s ≥ cti`). A pair is omitted when either device's operate time is unresolved — there's nothing to grade, and reporting `false` would wrongly imply a failure. Finally, top-level **`coordinated`** is the AND of every gradeable pair (nil when fewer than two devices resolved a time).

⚠️ **A `coordinated: false` pair means the backup trips too soon.** The upstream device would clear the fault *before* — or too close behind — the primary, tripping more of the network than the fault requires. The fix is usually to slow the backup (raise its time dial) or speed the primary, then re-run.

### 5.5 Method — from fault current to operate time ∑

The study **reuses the fault-current substrate wholesale**: `resolveNode → buildTree → resolveNodeStates → accumulateSeriesImpedance` give the prospective three-phase bolted-fault current at the fault node (Chapter 4). Then:

1. **The path.** `pathToFault` walks `tr.parent` up from the fault to the start — the series path, ordered **fault-first** (order 0 = nearest the fault).
2. **Through-current per device.** Every device on that series path carries the *same* fault contribution, so its through-current is the fault current **referred to that device's own base**: `I_device · V_device = I_fault · V_fault` (power through a series path is conserved across an ideal transformer). A device on the HV side of a transformer therefore sees a proportionally smaller current than the LV fault node.
3. **Operate time.** Each device's time is read from its **tabulated** time-current curve — *not* an IDMT formula. The standard IEC/IEEE relay shapes were evaluated from their defining formula **once, at seed time**, into stored `(current, time)` points (migration 0076); a manufacturer fuse curve is loaded as measured points the same way. The engine **interpolates** those points:
   - **Inverse-time relay** — `operate_time = time_dial × curve(I / pickup)`, reading the stored *multiple-of-pickup* curve at the current multiple. If the multiple is ≤ 1, the relay doesn't operate.
   - **Instantaneous element** — if `inst_pickup_a` is set and the through-current reaches it, the relay operates with **no intentional delay** (`operate_time_s = 0`).
   - **Definite-time relay** — operates after a fixed `definite_time_s` once above pickup (time dial doesn't scale it).
   - **Fuse / recloser** — graded on the **total-clear** band (the conservative "fully cleared" time) of the ampere-quantity curve.
4. **Roles & grading.** The first device with a resolved operate time is the **primary**; every operating device above it is **backup**. `gradePairs` grades each adjacent operating pair — `margin = t_backup − t_primary ≥ cti` — skipping any non-operating device so it doesn't break the chain. A device with no settings still appears (with a `note`) but carries no role and isn't graded.

**Log-log interpolation** is the key numerical choice: a TCC is a straight line on log-log axes for an IDMT relay, so interpolating between tabulated points *in log-log space* tracks the true characteristic far more faithfully than linear interpolation would between sparse samples. Outside the tabulated range the result is **clamped** to the nearest endpoint (a relay curve flattens toward its definite-minimum time at high multiples and shoots up near pickup) and the device is flagged `extrapolated` — resting on the edge of its data, honestly surfaced.

### 5.6 The coordination chart — one shared current axis ∑▶

**∑** The scalar fields above stay on each device's **own** base, but the `curves` for plotting are all referred to a single **common base** — the fault node's (`common_base_v`) — via `I_common = I_device · V_device / V_fault`. That's what makes a coordination chart readable: every device's curve is drawn on **one shared current axis**, and the fault current is a **single vertical line** at `fault_current_amps` that each curve crosses at its own operate time. A relay contributes one trace (its operate curve, converted from multiples-of-pickup to absolute amps, scaled by time dial, truncated at any instantaneous knee); a fuse or recloser contributes two (minimum-melt and total-clear bounds).

**▶** Reading the chart, coordination is *visual*: the downstream device's curve should sit **below** (faster than) each upstream curve at the fault current, separated by at least the CTI. Where two curves cross or crowd together, that's the miscoordination the `pairs` list flags numerically.

### 5.7 Worked example — grading the Oakwood lateral fuse against the feeder relay ▶∑

**▶** Feeder 7 is protected at its head by an **MV_BREAKER** driven by a **PROTECTIVE_RELAY** (IEC standard-inverse, pickup 200 A, time dial 0.2). A spur — the Oakwood lateral — is protected by an **MV_FUSE** (a 63 A manufacturer curve). Tom wants to confirm that for a fault at the end of the lateral, the **fuse** clears it and the **relay** holds off as backup. He runs a coordination study from the feeder head to the fault at the lateral's end, 11 000 V, default CTI 0.3 s.

**∑** The engine finds the prospective fault current at the lateral's end — say **2 000 A** at 11 kV — and walks the two devices:

- **MV_FUSE** (order 0, primary). Both device and fault are at 11 kV, so it sees the full 2 000 A. Reading its **total-clear** curve at 2 000 A interpolates to `operate_time_s ≈ 0.08 s`.
- **MV_BREAKER** (order 1, backup). Also at 11 kV, so 2 000 A through-current, a multiple of `2 000 / 200 = 10×` pickup. Read off the tabulated IEC standard-inverse curve and scaled by the 0.2 time dial, `operate_time_s ≈ 0.59 s`.

The one pair: `margin_s = 0.59 − 0.08 = 0.51 s`, which is **≥ 0.3 s**, so `coordinated: true`, and the overall verdict is **coordinated**.

**▶** Tom's read: the fuse clears the lateral fault in ~0.08 s and the feeder breaker waits ~0.59 s — a comfortable 0.51 s margin — so a fault on the lateral takes out *only* the lateral, exactly as intended. On the chart, the fuse's total-clear curve sits well below the relay's operate curve where the 2 000 A fault line crosses them.

**Now the failure case.** Suppose someone lowers the relay's time dial to **0.1** to speed up tripping. The relay's operate time at 10× drops to `≈ 0.30 s`, so `margin_s = 0.30 − 0.08 = 0.22 s < 0.3 s` — the pair comes back `coordinated: false`, and the overall verdict flips to **not coordinated**. The study has caught that the "faster" relay setting now trips the whole feeder too close behind the lateral fuse: for a slightly different fault the *breaker* could beat the *fuse*, blacking out the entire feeder for a fault one lateral could have contained. The fix is to restore a higher time dial (or a slower curve) and re-run until the margin returns above CTI.

### 5.8 Tips & gotchas ▶

- **Run it for a fault at the end of each protected section.** Coordination that holds for a close-in fault can fail for a remote one (lower current, different curve slopes) — check the cases that matter.
- **A `note` explains every unresolved device.** "No PROTECTIVE_RELAY linked via protection_trip", "fuse has no curve_ref", "through-current at or below pickup — relay does not operate" — read it before assuming the device is fine.
- **`extrapolated: true` means the fault current is off the end of the curve.** The time is clamped to the nearest tabulated point; get more curve data if the operate time matters at that current.
- **Time dial is the usual coordination lever.** To fix a failing pair, slow the backup (raise its time dial) rather than speeding the primary, which can compromise *its* coordination further down.
- **Reclosers grade on the slow curve.** If a recloser looks miscoordinated, remember the check uses its slow curve by design; its fast curve is for transient faults, not discrimination.
- **The default 0.3 s CTI is relay-to-relay.** Fuse-fuse and fuse-relay coordination often use different margins — set `cti_seconds` to your standard.

---

*Next chapter: **Conductor ampacity (IEEE 738)** — the thermal current rating of a line from its material, size, and environment, and how it compares against the load-flow loading. Tell me to proceed (or name a later chapter).*

---

## Chapter 6 — Conductor ampacity (IEEE 738)

Every chapter so far has treated a conductor's current rating as a *given* — the `loading_pct` in the load-flow result (Chapter 2) divides the flowing current by a static ampacity the material catalog carries. But that catalog figure is a single number stamped for one assumed set of conditions. A bare overhead line's real, safe current depends on the weather around it: a stiff cross-wind cools it and lets it carry far more; a still, sunny afternoon heats it and cuts the rating sharply. **Conductor ampacity** (`/analysis/ampacity`) computes that weather-dependent rating from first principles using **IEEE 738's steady-state heat balance**, so a planner can ask "what can this line *actually* carry at 35 °C with a light breeze?" rather than trusting one catalog stamp.

### 6.1 What it is and when to run it ▶

Ampacity answers: **given this conductor's material and size, and these ambient conditions, what steady current heats it exactly to its maximum operating temperature?** That current is its rating for those conditions — carry more and the conductor exceeds its rated temperature (annealing, sag, clearance loss).

Run it when the *thermal envelope* matters:

- **seasonal or dynamic ratings** — the catalog's static figure is one assumption; recompute for a summer-noon worst case, or a cool windy night that unlocks headroom;
- **checking a load-flow overload** — the load flow flagged a branch at 105 % of *static* ampacity; a first-principles rating for the actual day may show it's genuinely fine, or genuinely not;
- **specifying a reconductor** — compare candidate conductors' ratings under the design-day conditions, not their nameplate;
- **understanding *why* a line is limited** — the response breaks out every heat term, so you can see whether it's convection-, radiation-, or resistance-driven.

⚠️ This is a **single-conductor, steady-state** study — one feature, one weather snapshot, thermal equilibrium. It does **not** consider transient/emergency (short-time) ratings, nor does it re-run the load flow; it's the rating a *separate* current would be judged against.

### 6.2 Inputs — the request ∑

```json
{ "conductor": { "feature_class": "MV_CONDUCTOR", "feature_id": "…" },
  "ambient_temp_c": 35,
  "wind_speed_mps": 0.6,
  "wind_angle_deg": 90,
  "solar_heat_flux_w_m2": 1000 }
```

`conductor` must be an **edge** class (`network_role == "edge"`) — ampacity only means something for a branch that carries current *along its own length*; a point device is rejected with that explanation.

The environment inputs encode two deliberate, asymmetric defaulting choices — read them carefully, because a silent default here changes the answer:

- **`ambient_temp_c` is required** and typed as a pointer (`*float64`), not a plain number. 0 °C is a *real, common* ambient (freezing point), so there's no "≤ 0 means missing" sentinel to lean on the way `nominal_voltage_v` uses; explicit presence is the only honest way to require it.
- **`wind_speed_mps` defaults to 0 when omitted — and that's conservative.** No wind leaves only **natural convection**, the weaker cooling path, so the rating comes out *lower* (safer). `wind_angle_deg` is the angle between wind and the conductor axis (90° = perpendicular, the most effective cooling).
- **`solar_heat_flux_w_m2` defaults to 0 when omitted — and that is NOT conservative.** Zero solar silently assumes a **clear night**, which *overstates* daytime ampacity (no solar heating to fight). ⚠️ **A caller doing a summer-noon rating must supply their own solar figure** (~1000 W/m² for full sun) — the default will flatter the rating.

Both must be ≥ 0 (negatives rejected).

### 6.3 The material spec — where the physics comes from ∑

The conductor feature itself carries no thermal properties; they live on its linked **material spec**. `resolveAmpacityMaterialSpec` scans the feature class's domain-linked fields for whichever currently holds a material code on *this* feature and loads that spec (the same by-field-name scan the impedance resolver uses, kept as an independent copy because this study's required fields are thermal/geometric, not R/X). If nothing resolves you get a clear *"needs a material-linked field set"*; if a spec resolves but is under-populated, `missingAmpacityFields` names **exactly which columns are unset** rather than failing vaguely. The six required fields:

| Field | Symbol | Role |
|---|---|---|
| `max_operating_temp_c` | T_c | the temperature the rating heats the conductor *to* |
| `diameter_mm` | D | sets the surface area for convection/radiation and the solar-intercept width |
| `resistance_ohm_per_km` | R₂₀ | DC/AC resistance, assumed measured at **20 °C** (this project's stated convention) |
| `resistance_temp_coefficient_per_c` | α | how resistance rises with temperature |
| `emissivity` | ε | how effectively the surface **radiates** heat away (weathered ≈ 0.5) |
| `absorptivity` | γ | how effectively the surface **absorbs** solar heat (weathered ≈ 0.5) |

### 6.4 Reading the result ▶

```json
{ "material_spec_code": "ACSR_DOG",
  "max_operating_temp_c": 75, "ambient_temp_c": 35,
  "convective_loss_w_per_m": 37.8, "radiative_loss_w_per_m": 7.2,
  "solar_gain_w_per_m": 7.1, "resistance_ohm_per_m": 0.000329,
  "ampacity_amps": 339, "static_ampacity_amps": 300 }
```

`ampacity_amps` is the headline: the steady current for these conditions. But the study deliberately reports **every heat-balance term** so you can see *what's driving the rating*:

- **`convective_loss_w_per_m`** — heat carried off by air. Usually the **dominant** cooling term outdoors, and the one most sensitive to wind.
- **`radiative_loss_w_per_m`** — heat radiated away, set by ε, diameter, and the *fourth-power* temperature difference.
- **`solar_gain_w_per_m`** — heat *added* by the sun (a debit against cooling), set by γ, diameter, and your solar flux.
- **`resistance_ohm_per_m`** — the resistance **at T_c**, already temperature-corrected; this is what the current works against.
- **`static_ampacity_amps`** — the catalog's own static figure, echoed **purely for comparison** (omitted if the spec has none). The study never reads it into the calc and never writes it back.

💡 The comparison of `ampacity_amps` to `static_ampacity_amps` is the practical payoff: it tells you whether today's conditions give you **more** headroom than the catalog assumes (windy/cool) or **less** (still/hot/sunny).

### 6.5 Method — the heat balance, solved in closed form ∑

IEEE 738's steady-state balance says a conductor is at thermal equilibrium when heat out equals heat in:

$$q_c + q_r \;=\; q_s + I^2 R(T_c)$$

Convective + radiative losses balance solar gain plus the resistive (I²R) heating. Solving for current:

$$I \;=\; \sqrt{\dfrac{q_c + q_r - q_s}{R(T_c)}}$$

**The key simplification — and why no iteration is needed.** Full IEEE 738 derives its air properties (density, viscosity, thermal conductivity) from the actual *film* temperature (T_c + T_a)/2, which makes q_c depend on the solution and forces an iterative root-find. This study instead holds air properties **constant** at typical values near a ~40 °C film temperature at sea level (ρ_f = 1.029 kg/m³, μ_f = 1.9×10⁻⁵ Pa·s, k_f = 0.028 W/m·K) — the same "real physics, simplified input" trade the load flow's linearized voltage drop already makes. With constant air properties, **q_c and q_r depend only on (T_c, T_a), not on current**, so the right-hand side is fully known and I comes out **closed-form** — evaluated once, no iteration.

The terms in detail:

- **Resistance at temperature.** `R(T_c) = (R₂₀ / 1000) · (1 + α·(T_c − 20))` — the per-km catalog figure to per-metre, corrected from the 20 °C reference up to the operating temperature. At 75 °C with α = 0.004, that's a 22 % uplift.
- **Radiation.** `q_r = π·D·ε·σ·((T_c+273.15)⁴ − (T_a+273.15)⁴)` — Stefan–Boltzmann, in **kelvin**, fourth-power. Only computed when T_c > T_a (a conductor cooler than ambient radiates *in*, not out; the guard skips it).
- **Convection.** `convectiveLossWM` takes the **greater** of forced and natural convection. Forced convection itself is the greater of two Reynolds-number forms (`qc1` dominates at low wind, `qc2` at high), scaled by a wind-angle factor k_angle (max at 90°, min when wind runs *along* the line). Natural convection `0.0205·√ρ_f·D^0.75·ΔT^1.25` is the floor — so a near-zero wind naturally **falls through to natural convection** with no explicit "no wind" branch. This max-of-both structure is why wind is the single most influential input.
- **Solar.** `q_s = γ · solar_flux · D` — absorptivity times flux times the intercepted width.

If the net cooling `q_c + q_r − q_s` comes out ≤ 0 (the sun more than offsets convection+radiation at T_c), the current is reported as **0** rather than an imaginary square root — a signal the conductor can't even hold T_c under that load-free solar load, let alone carry current.

### 6.6 Worked example — rating an Oakwood overhead line on a summer afternoon ▶∑

**▶** Feeder 7 runs overhead through open country as an **ACSR "Dog"** line (`ACSR_DOG`: D = 14.15 mm, R₂₀ = 0.27 Ω/km, α = 0.004/°C, ε = 0.5, γ = 0.5, T_c = 75 °C). Its catalog **static ampacity is 300 A**. Tom is checking a summer-afternoon rating: ambient **35 °C**, a light **0.6 m/s breeze across the line** (90°), full sun **1000 W/m²**.

**∑** The study evaluates each term at T_c = 75 °C, T_a = 35 °C (ΔT = 40 K):

- **Resistance:** R(75 °C) = 0.00027 Ω/m × (1 + 0.004·55) = 0.00027 × 1.22 ≈ **0.000329 Ω/m**.
- **Radiation:** π·0.01415·0.5·σ·(348.15⁴ − 308.15⁴) ≈ **7.2 W/m**.
- **Convection:** Reynolds ≈ 460, k_angle = 1.0 at 90°; forced convection ≈ **37.8 W/m** (dominating natural convection's ~0.1 W/m floor entirely).
- **Solar gain:** 0.5 · 1000 · 0.01415 ≈ **7.1 W/m** (a debit).

Net cooling = 37.8 + 7.2 − 7.1 ≈ **37.9 W/m**, so:

$$I = \sqrt{\dfrac{37.9}{0.000329}} \approx \sqrt{115{,}000} \approx \textbf{339 A}.$$

**▶** Tom's read: on this moderate day the line will actually carry **~339 A** before reaching 75 °C — about **13 % more** than the 300 A catalog stamp. The convective term (37.8 W/m) dwarfs radiation and solar, so the rating is **wind-driven**. That matters: if a load-flow run had flagged this branch at 320 A (107 % of the *static* 300 A rating), the first-principles rating shows it's genuinely within its thermal envelope on a breezy day.

**Now the still-air case.** Tom re-runs with the breeze dropping to **0.2 m/s** (everything else identical). Convection collapses toward its floor — forced convection falls to ≈ 22 W/m — so net cooling ≈ 22.0 W/m and:

$$I = \sqrt{\dfrac{22.0}{0.000329}} \approx \textbf{258 A}.$$

The rating has fallen to **258 A — now *below* the 300 A catalog figure** — purely because the wind dropped. Same conductor, same sun, same ambient: the rating swung from 339 A to 258 A on wind alone. That 320 A load-flow current that looked fine on a breezy afternoon is a **24 % thermal overload** on a still one. This is exactly why the catalog stamp is a starting point, not the last word, and why the worst-case rating pairs a hot ambient with *little to no* wind.

### 6.7 Tips & gotchas ▶

- **Always supply a solar figure for a daytime rating.** Omitting `solar_heat_flux_w_m2` assumes a clear night and *overstates* the rating — the one default that isn't conservative.
- **Worst case = hot ambient + near-zero wind + full sun.** Wind dominates cooling, so the design-day rating lives at low wind; don't let an optimistic breeze into a firm rating.
- **Read the breakdown, not just the number.** If radiation and solar are tiny next to convection (the usual outdoor case), the rating is wind-driven and swings with weather; a resistance- or radiation-limited case behaves differently.
- **A missing-fields error names the column.** An under-cataloged spec fails with the exact unset field (`emissivity`, `diameter_mm`, …) — fill the catalog, don't guess.
- **`ampacity_amps: 0` is a real answer.** It means the sun alone (at T_c) more than offsets cooling — the conductor can't even sit at its rated temperature under that solar load, let alone carry current. Check for an unreasonably high solar flux or a tiny/black conductor.
- **This is steady-state.** For a short-time emergency rating (minutes, using thermal mass), this study isn't the tool — it assumes the conductor has reached equilibrium.

---

*Next chapter: **Arc-flash (IEEE 1584)** — incident energy, the arc-flash boundary, and PPE categories, derived from the fault and coordination engines. Tell me to proceed (or name a later chapter).*

---

## Chapter 7 — Arc-flash hazard (IEEE 1584)

The three chapters before this each stood on their own engine. This one stands on **two** at once, and computes almost no electrical physics of its own. An **arc-flash** is the explosive release of energy when a fault arcs across air inside equipment — the hazard that dictates what protective clothing a worker must wear to open a panel. Its severity depends on two things VoltEdge already knows how to compute: **how much fault current flows** (Chapter 4) and **how long protection takes to clear it** (Chapter 5). The arc-flash study (`/analysis/arc-flash`) drives both engines, adds the **IEEE 1584-2002** empirical burn model on top, and reports, per node, the **incident energy** a worker would receive, the **arc-flash boundary** distance, and the **PPE category** — the practical output that goes on an equipment label.

### 7.1 What it is and when to run it ▶

The study answers, for every energised point downstream of a start: **if an arc struck here, how much thermal energy (cal/cm²) would reach a worker at the working distance, and what PPE protects against it?** Unlike the other studies, it sweeps *many* nodes in one run — it inherits the fault engine's whole reached tree, producing a hazard result per node with a resolved fault current.

Run it to:

- **produce or refresh equipment arc-flash labels** — the incident energy, boundary, and PPE category are exactly the label fields;
- **check that protection is fast enough for safe work** — the same slow upstream device that *coordinates* fine can leave a downstream board at a **dangerous** energy, because clearing time drives incident energy linearly;
- **justify a protection change on safety grounds** — show the before/after incident energy when you add a fast local device;
- **screen a network for dangerous points** — anything above 40 cal/cm² is "no live work."

⚠️ It's radial and single-scenario like its parents — one source, a three-phase arc at each reached node — and it implements the **1584-2002** empirical fit (validity 0.208–15 kV, 0.7–106 kA); the 2018 coefficient model is a later opt-in. Results outside that envelope are still computed but flagged `out_of_range`.

### 7.2 What makes it "derived" ∑

`runArcFlash` does two walks of the same substrate and joins them:

1. **`runFaultCurrent(... faultThreePhase)`** — the *authoritative* bolted fault current and voltage base per node, **identical** to what `/analysis/fault-current` reports for the same node (embedded-generation infeed included), so the two studies never disagree. These are IEEE 1584's two primary inputs.
2. **A second, independent walk** (`buildTree` → `resolveNodeStates` → `accumulateSeriesImpedance`) to get the tree, paths, and device states the **coordination engine's evaluators** need — the fault response doesn't carry them. This deliberate second walk keeps the fault and coordination engines untouched (the same trade the dispatch router makes).

Everything genuinely new is the **1584 empirical model** — arcing current, incident energy, boundary — plus the **equipment geometry** it needs (electrode gap, enclosure, working distance) that no other study uses.

### 7.3 The geometry inputs and their precedence ∑

IEEE 1584 needs geometry the electrical model doesn't: how the electrodes are arranged, how wide the gap is, how far the worker stands, whether the system is grounded. VoltEdge resolves each with the package's usual **"Both" precedence — declared per-feature value > study-level request override > IEEE typical default** — and flags anything that fell back to a typical (`geometry_assumed`) rather than silently trusting it:

| Input | Declared field | Fallback |
|---|---|---|
| Electrode configuration | `arc_electrode_configuration` | `switchgear` (enclosed — conservative, commonest work location) |
| Electrode gap | `arc_conductor_gap_mm` | class/voltage typical (Table 4) |
| Working distance | `arc_working_distance_mm` | **request `working_distance_mm`** → class/voltage typical |
| Grounding | `arc_system_grounded` | `false` (ungrounded — conservative) |

The **electrode configuration** does a lot of work: it sets **enclosed-vs-open** (the K terms below), the **distance exponent x**, and the class's typical gap and working distance. Four canonical classes — `switchgear`, `mcc_panelboard`, `cable`, `open_air` — with `normalizeElectrodeConfig` folding looser synonyms (and even the 1584-**2018** five-electrode codes VCB/VCBB/HCB/VOA/HOA) onto them, so a field populated for a future 2018 run still reads sensibly here.

💡 **The defaults all lean conservative** — enclosed box and ungrounded both *maximise* predicted energy, the safe direction for a hazard study. A missing input never flatters the result; it just gets flagged. (A request-level working distance counts as a real analyst input, not an assumption; grounding defaulting to ungrounded isn't treated as an "assumed geometry" trigger either.)

### 7.4 Reading the result ▶

Each node in the response carries:

| Field | Meaning |
|---|---|
| `bolted_fault_current_amps` | the prospective bolted current here (from the fault engine) |
| `arcing_current_amps` | the *actual arc* current — **less than** bolted (arc impedance), and what drives protection timing |
| `clearing_time_s` | how long the upstream primary device takes to clear the **arcing** current |
| `clearing_device` | which device clears it (nil if none resolved) |
| `incident_energy_cal_cm2` | **the headline** — thermal energy at the working distance |
| `arc_flash_boundary_mm` | distance at which energy falls to 1.2 cal/cm² (the "second-degree-burn" line) |
| `ppe_category` / `ppe_label` | 0 minimal, 1–4 NFPA 70E, **−1 dangerous (no live work)** |
| `electrode_configuration`, `working_distance_mm`, `gap_mm`, `enclosed`, `grounded` | the geometry actually used |
| `clearing_time_assumed` | true → no device resolved, time capped at the max |
| `geometry_assumed` | true → ≥ 1 geometry input fell back to a typical |
| `reduced_current_governs` | true → the 0.85·I_a sensitivity case was worse (§7.5) |
| `out_of_range` | true → V or I_bf outside 1584-2002 validity |

⚠️ **`ppe_category: -1` ("Dangerous — no live work") is the result to hunt for.** It means incident energy exceeds 40 cal/cm² — beyond any PPE rating; the equipment must be de-energised before work. It's almost always driven by a **slow clearing time**, not a huge current.

### 7.5 Method — arcing current, incident energy, boundary ∑

**Step 1 — arcing current** (`arcingCurrentAmps`, 1584-2002 eqs 1–2). The arc doesn't carry the full bolted current; arc resistance drops it. Below 1 kV the reduction depends on enclosure and gap (eq 1); at ≥ 1 kV it's a simpler log-log fit (eq 2). The arcing current is typically 40–60 % of bolted at LV. **This is what protection sees** — the study evaluates clearing time at the arcing current, not the bolted current.

**Step 2 — clearing time** (`arcClearingTime`). This reuses the coordination engine's device evaluators **unchanged**, but drives them with the *arcing* current referred to each device's own base (`through = I_arc · V_fault / V_device`, power conserved across transformers). It walks the fault→source path and takes the **first device that resolves an operate time** (the primary) — the same selection as coordination, but without grading. If the operate time exceeds the cap it's clamped; if **no** device resolves, the time is capped and `clearing_time_assumed = true`. The cap defaults to **2.0 s** (IEEE guidance where a worker can't self-evacuate), overridable via `max_clearing_time_s`.

**Step 3 — incident energy & boundary** (`incidentEnergyAndBoundary`, eqs 3–5). Normalized energy (at 0.2 s, 610 mm):

$$\lg E_n = K_1 + K_2 + 1.081\,\lg I_a + 0.0011\,G$$

with **K₁ = −0.555 enclosed / −0.792 open**, **K₂ = −0.113 grounded / 0 ungrounded**, G the gap. Then the energy at the actual time and distance:

$$E = C_f \cdot E_n \cdot \dfrac{t}{0.2} \cdot \left(\dfrac{610}{D}\right)^{x}$$

where C_f = 1.5 at ≤ 1 kV else 1.0, t is clearing time, D the working distance, x the distance exponent. Two things to internalise: **energy is *linear* in clearing time** (halve t, halve the burn) and **falls off with distance to the power x**. The arc-flash boundary inverts eq 4 for the distance where E = 1.2 cal/cm². *(The model is verified against the standard's worked example: 0.48 kV, 20 kA, box, grounded, G = 32, D = 457 → ~7.29 cal/cm², ~1555 mm.)*

**Step 4 — the arcing-current sensitivity.** A *lower* arcing current can sit further out on the TCC and clear **slower**, sometimes giving *more* energy despite less current. So the study computes everything **twice** — at I_a and at **0.85·I_a** — and the **worse (higher) incident energy governs** (`reduced_current_governs` flags when the 85 % case won).

**Step 5 — PPE category** (`ppeCategory`): < 1.2 → 0 (minimal); ≤ 4 → Cat 1; ≤ 8 → Cat 2; ≤ 25 → Cat 3; ≤ 40 → Cat 4; **> 40 → −1 dangerous.**

### 7.6 Worked example — the arc-flash hazard on the TX-Feeder7-014 LV board ▶∑

**▶** This is the same 415 V LV board from Chapter 4, where a three-phase bolted fault came to **16.4 kA**. Dana's crew needs to work on it live, so Tom runs an arc-flash study to size their PPE. The board is enclosed LV **switchgear** (default geometry: box, gap 32 mm, x = 1.473, working distance 610 mm, ungrounded). Critically, its **only upstream protection is the Feeder 7 head relay** (IEC standard-inverse, pickup 200 A, TD 0.2) on the 11 kV side — there's no fast LV main device.

**∑** The study works through it:

- **Arcing current** (eq 1, enclosed, 415 V, 16.4 kA bolted): I_a ≈ **8.47 kA** — about 52 % of bolted, as expected at LV.
- **Clearing time.** Referred to the 11 kV relay: `through = 8 470 · 415 / 11 000 ≈ 319 A`, only **1.6×** the relay's 200 A pickup. Out on the shallow tail of the standard-inverse curve, the relay would take ~3 s — past the **2.0 s cap**, so `clearing_time_s = 2.0 s`, `clearing_device =` the feeder breaker's relay.
- **Incident energy** (eqs 3–4): E_n = 10^(−0.555 + 1.081·lg 8.47 + 0.0011·32) ≈ 3.04; E = 1.5 · 3.04 · (2.0/0.2) · (610/610)^1.473 ≈ **45.6 cal/cm²**.
- **Arc-flash boundary** (eq 5): ≈ **7.2 m**.
- **Sensitivity:** the 0.85·I_a case (7.2 kA) also clears at the 2.0 s cap and yields ~38 cal/cm² — *less* than the full-current case, so the full arcing current governs (`reduced_current_governs = false`).

**PPE category: −1 — "Dangerous — no live work."**

**▶** Tom's read: this board is **lethal to work on live**, and the culprit isn't the current — it's the **2-second clearing time**. The LV arc looks like a mere 1.6× overload to the distant feeder relay, which crawls. The 7.2 m arc-flash boundary means even bystanders well back are at risk. His recommendation writes itself.

**The fix, quantified.** Tom proposes an **LV main breaker** on the board that clears an 8.5 kA arc in **~0.05 s**. Because incident energy is *linear* in clearing time, re-running with `clearing_time_s = 0.05` instead of 2.0 scales the energy by 0.05/2.0:

$$E = 45.6 \times \dfrac{0.05}{2.0} \approx \textbf{1.14 cal/cm}^2 \;\Rightarrow\; \text{PPE Category 0 (minimal)}.$$

Same fault, same board — **45.6 cal/cm² becomes 1.14 cal/cm²** purely by clearing 40× faster. The study turns "the board is dangerous" into a costed, defensible safety case: one fast local device moves the crew from *no live work permitted* to *minimal PPE*. This is the arc-flash study's whole point — **incident energy is a protection-speed problem far more than a fault-current problem.**

### 7.7 Tips & gotchas ▶

- **Chase the −1 ("dangerous") nodes first, and look at their clearing time.** It's almost always a slow or unresolved upstream device, not extreme current — and slowness is the cheapest thing to fix.
- **`clearing_time_assumed: true` is a warning, not a rating.** No device resolved, so the time is just the 2 s cap; the real hazard could be better *or* worse. Resolve the protection before trusting the number.
- **LV boards fed only through MV protection are the classic trap.** The arc looks like a small overload upstream and clears slowly — high energy. A fast LV main device is the usual remedy.
- **`geometry_assumed: true` means typicals filled in the geometry.** Fine for screening; for a *label* you post on the panel, populate the declared fields (`arc_conductor_gap_mm`, `arc_working_distance_mm`, `arc_electrode_configuration`) so the number is engineered, not assumed.
- **Working distance matters a lot** — energy falls off as (610/D)^x. The default per-class distance is conservative; use the real one for a firm label.
- **`out_of_range` means extrapolated.** Above 15 kV or outside the current envelope the 2002 fit is off its data; treat the number as indicative and await the 2018 model for EHV.
- **The arcing current, not the bolted current, drives the timing.** A device that clears the bolted current fast may be much slower at the ~50 %-lower arcing current — which is exactly why the study evaluates protection at I_a.

---

*Next chapter: **Capacity & planning** — N-1 contingency, DER hosting capacity, and EV feeder headroom: the three "how much more can this network take?" studies built on the load-flow sweep. Tell me to proceed (or name a later chapter).*

---

## Chapter 8 — Capacity & planning

Every study so far has asked *"what is the network doing now?"* — the voltage profile, the fault current, the arc-flash hazard of the system as it stands. This chapter's three studies ask the planner's question instead: **"how much *more* can it take?"** Three flavours of "more":

- **N-1 contingency** (`/analysis/contingency`) — a feeder is lost; how much of its load can a *neighbouring* feeder pick up by closing a tie?
- **DER hosting capacity** (`/analysis/hosting-capacity`) — how much distributed generation (rooftop PV, a solar farm) can connect at a point before the feeder over-volts or overloads?
- **EV feeder headroom** (`/analysis/ev-hosting`) — the mirror: how much extra charging load can connect before the feeder sags below its voltage floor or overloads?

What unites them is that **none adds new electrical physics.** All three stand on the Chapter 2 radial sweep and reduce a planning question to *"re-sweep the feeder under a hypothetical change, and check the same two limits load flow already checks — voltage band and 100 % loading."* The hypothetical differs (a tie closed; a generator injected; a load added), but the machinery underneath is shared.

### 8.1 N-1 contingency & backfeed restoration ▶

**▶** When a feeder loses its source — a fault at the substation, a cable dig-in — its customers are out until either the fault is fixed or their section is **transferred** onto a healthy neighbour through a normally-open tie. The contingency study answers, from the *donor* feeder's point of view: **for each tie I could close, how much outaged load would I pick up, and would my feeder still hold voltage and stay within thermal ratings once I did?**

You run it *proactively* (which ties give a viable restoration plan for each feeder?) or *live* during an outage (which switching move restores the most customers safely?). The request is the donor feeder's source and sending-end voltage, an optional specific `tie` to evaluate, and the acceptable post-transfer band (default ANSI 0.95–1.05):

Each candidate tie comes back as a **`restorationOption`**:

| Field | Meaning |
|---|---|
| `restored_load_kw` / `_kvar` | extra demand the donor picks up by closing this tie |
| `restored_node_count` / `restored_nodes` | what section gets re-energised |
| `min_voltage_pu` / `worst_node` | the worst post-transfer voltage and where |
| `violations` | nodes outside the band after transfer |
| `overloaded` | branches/transformers over 100 % after transfer |
| `normal_state` | the tie's declared posture — `NORMALLY_OPEN` ties rank **first** |
| `feasible` | **true only if** the transfer restores load AND introduces no violation and no overload |
| `note` | why an option is infeasible or trivial (a loop; no load gained) |

⚠️ **`feasible: true` is the switching move an operator can make as-is.** A `false` with a violation or overload means the transfer *works electrically* but leaves the donor out of limits — a partial transfer or load shed might still help, but not a straight close.

**∑** `runContingency` sweeps the donor **as it stands** (the base case); its **barrier hits are exactly the open boundary switches** a transfer could close, so with no explicit `tie` the study evaluates *every* one of them, ranked NORMALLY_OPEN-first (a purpose-built standby path is the natural first choice). For each candidate it re-sweeps with that tie's `device_state` **overridden closed** in memory (`runSweep(..., map[node]bool{c: true}, ...)`) — **no connectivity is mutated**. Then it computes nothing new: **restored load = the delta in the source's accumulated demand** (base vs swept `cumKW`), restored nodes = everything newly reached, overloads reuse load flow's own `formatResults` loading check, violations reuse Chapter 2's band judging. Two special results are reported honestly rather than as failures: closing a tie that forms a **loop/mesh** (a parallel path, not a radial transfer) is caught as a `buildTree` validation error and flagged (*"run LOOP_CHECK_TRACE"*); a tie that **dead-ends** (restores no load) says so.

### 8.2 DER hosting capacity ▶

**▶** A solar developer asks to connect a PV array at a particular point on Feeder 7. Too much injection and midday backfeed pushes local voltage *above* the 1.05 pu ceiling, or pushes reverse current through a conductor past its rating. The hosting study answers: **what's the largest generator nameplate (kW) this point can accept before the first limit binds?** Left without a specific `node`, it scans **every apparatus node** on the feeder — the "where does this feeder host the most DER?" map (conductors are line segments, not interconnection points, so they're skipped).

Each candidate returns a **`hostingCandidate`**:

| Field | Meaning |
|---|---|
| `hosting_capacity_kw` | the largest injection with no over-voltage and no overload |
| `binding_constraint` | `overvoltage` / `thermal` / `pre_existing` / `none` |
| `binding_feature` | the node or branch that limit acts on |
| `worst_voltage_pu` / `worst_loading_pct` | how close each limit sits *at* the capacity |
| `unbounded` | true → the search cap was reached without binding |
| `note` | e.g. the base case already violates → **zero headroom** |

`power_factor` defaults to **unity** (real-power-only PV); at pf < 1 the generator also injects kVAR (`kW·tan(acos(pf))`), which lifts voltage *further* and so *lowers* the capacity. Only the **upper** voltage bound can bind a generator (injection raises voltage).

**∑** `runHosting` resolves the feeder **once** then binary-searches per node. A generator is modelled as **negative demand** — `evaluate` subtracts the trial injection from the candidate's own `ownKW`/`ownKVAR` and re-runs *only* the in-memory backward/forward sweep (no queries), restoring the node's load afterward so candidates don't contaminate each other. Feasible = worst voltage ≤ `max_voltage_pu` AND no branch over 100 %. The **binding constraint at the capacity** is whichever limit sits closest to its ceiling: `ovMargin = maxPu − worstHighPu` vs `thMargin = 1 − worstLoadingPct/100` — the limit the *next* kW would cross.

### 8.3 EV feeder headroom ▶

**▶** The exact mirror. A rapid-charging hub wants to connect at a point on Feeder 7; too much charging load sags local voltage *below* the 0.95 pu floor, or overloads a conductor. The EV study answers: **how much additional charging load can this point absorb before the first limit binds?** Same per-node scan, same result shape (`ev_hosting_capacity_kw`, binding constraint, worst values), with two things flipped from hosting:

- it **adds positive demand** (load) instead of injecting negative demand;
- it binds on **under-voltage** (the 0.95 floor) instead of over-voltage.

Thermal binds identically in both. `power_factor` < 1 makes the load draw kVAR too, deepening the volt drop and *lowering* headroom.

💡 The headroom this study reports is the **uncapped** physical ceiling. **Managed / smart charging** is then the operator's lever: because the study sizes the raw added load, an operator can install *more* nameplate than the headroom and stay within it by capping the *coincident* draw — the headroom is reported against that lever, not pre-limited by it.

**∑** `runEvHosting` is `runHosting` with the sign and the bound flipped: `evaluate` **adds** `addKW` to the candidate's own demand, and feasible = worst voltage ≥ `min_voltage_pu` AND no overload. Binding at capacity: `uvMargin = worstLowPu − minPu` vs `thMargin`. (One nicety: a feeder with no rated node has no voltage to constrain, so worst-low is reported as nominal 1.0 to keep the JSON finite and the voltage limit reading as satisfied.)

### 8.4 The shared search machinery ∑

Both hosting studies (and, per-tie, contingency) lean on one search pattern worth understanding once:

1. **Resolve once, sweep many.** The query-heavy work — walk the tree, load every node's electrical inputs — happens **exactly once** per study. Every trial is then a pure in-memory backward/forward sweep. A binary search runs dozens of sweeps per candidate; making them queryless is what keeps a feeder-wide scan fast.
2. **Base-case gate.** Evaluate at zero injection/load first. If the feeder **already** violates a limit, there's no headroom at all — report **0**, `binding_constraint = pre_existing`, and say which limit (a pre-existing over-voltage or overload isn't the DER's/EV's fault, and the study won't blame it).
3. **Exponential ramp to bracket.** Starting at `rampStartKW = 50 kW`, **double** the trial until it becomes infeasible or hits the search cap (`defaultMaxInjectionKW = 100 MW`). `lo` tracks the largest known-feasible value, `hi` the smallest known-infeasible one. Reach the cap still feasible → `unbounded`, `binding_constraint = none` (capacity is *at least* the cap).
4. **Bisect to pin.** Binary-search `[lo feasible, hi infeasible]` for up to `hostingBisectIters = 40` iterations or until the bracket narrows below `hostingResolutionKW = 0.1 kW` — pinning the capacity to a fraction of a kW.
5. **Name the binding limit** by the smallest remaining margin, as above.

This is the "resolve-once + in-memory re-sweep + ramp-then-bisect" engine the plan memory records; contingency shares steps 1–2 and the override trick, the two capacity studies share all five.

### 8.5 Worked example — restoring, then re-loading, the Oakwood section ▶∑

**▶ Contingency.** A cable fault takes out the source end of Feeder 7, dropping the whole Oakwood Zone. Priya (dispatcher) needs a restoration plan. The neighbouring **Feeder 9** (11 kV, base load 3.2 MW) is the donor; two ties bridge the boundary: **TIE-79-A** (declared `NORMALLY_OPEN`, a purpose-built standby) and **TIE-79-B** (a normally-open sectionaliser of convenience). She runs `/analysis/contingency` from Feeder 9's source; the base sweep halts at both ties, and they're evaluated NOP-first.

**∑** The study re-sweeps Feeder 9 with each tie overridden closed:

| Tie | Restored load | Restored nodes | Worst voltage | Overloaded | Feasible |
|---|---|---|---|---|---|
| **TIE-79-A** (NOP) | +0.8 MW | 46 | 0.958 pu | — | ✅ **true** |
| **TIE-79-B** | +1.4 MW | 71 | 0.941 pu ⚠️ | 1 cable @ 108 % ⚠️ | ❌ false |

**▶** Priya's read: **close TIE-79-A**. It restores 0.8 MW (46 nodes) and Feeder 9 still holds 0.958 pu — within the band, nothing overloaded. TIE-79-B would restore *more* (the whole 1.4 MW), but it drives the far end to 0.941 pu (below the 0.95 floor) and pushes a cable to 108 % — a straight close there just moves the problem onto Feeder 9. The purpose-built NOP tie, ranked first, is exactly the safe move; TIE-79-B needs a partial transfer or load shed. She restores 46 nodes in one switching action.

**▶ Hosting capacity.** Weeks later, a developer wants to connect **rooftop-scale PV at the Oakwood LV board** (the 415 V node off TX-Feeder7-014, base voltage 0.969 pu from Chapter 2). Tom runs `/analysis/hosting-capacity` naming that node, unity PF.

**∑** From 50 kW the injection doubles — 50, 100, 200, 400 (still feasible), 800 kW (over-voltage) — then bisects between 400 and 800. As injection grows, the LV board's voltage climbs from 0.969 toward the 1.05 ceiling. The search pins **`hosting_capacity_kw ≈ 480 kW`**, at which `worst_voltage_pu = 1.050` (at the LV board itself) with `worst_loading_pct = 82 %` — so `binding_constraint = overvoltage`, `binding_feature =` the LV board (the over-voltage margin ran out before the thermal one).

**▶** Tom's read: the point hosts **~480 kW** of PV before midday backfeed hits the voltage ceiling; it's **voltage-limited, not thermal** (the cable still had 18 % headroom). If the developer ran the inverter at 0.95 PF *injecting* vars, capacity would fall — the extra kVAR lifts voltage faster. To host more, the answer is voltage management (an inverter absorbing vars, or a regulator), not a bigger cable.

**▶ EV headroom.** Same LV board, opposite question: a charging hub wants to add rapid chargers there. Tom runs `/analysis/ev-hosting`, unity PF.

**∑** Now the search **adds** load — 50, 100, 200 (feasible), 400 kW (under-voltage) — and bisects. The LV board sags from 0.969 toward the 0.95 floor. Capacity pins at **`ev_hosting_capacity_kw ≈ 250 kW`**, `worst_voltage_pu = 0.950` at the LV board, `worst_loading_pct = 71 %` → `binding_constraint = undervoltage`.

**▶** Tom's read: the board absorbs **~250 kW** of *coincident* charging before it hits the voltage floor — again voltage-limited. But that's not the whole story: with **managed charging**, the hub can install, say, **400 kW** of charger nameplate and configure the site controller to cap coincident draw at 250 kW, staying inside the headroom while serving more vehicles across the day. The study gives the physical ceiling; the operator's smart-charging lever does the rest.

**The symmetry, in one place.** Same node, same feeder: it **hosts 480 kW of generation** (rising to 1.05 pu) but only **absorbs 250 kW of load** (falling to 0.95 pu) — the asymmetry just reflects that it starts at 0.969 pu, closer to the floor than the ceiling. Both numbers come from the identical ramp-then-bisect engine with the sign of the demand and the binding bound flipped.

### 8.6 Tips & gotchas ▶

- **`feasible: true` (contingency) is a do-it-now move; `false` isn't a dead end.** A false-with-violation often still supports a *partial* transfer or a load shed — read `restored_load_kw` and `worst_node`, not just the flag.
- **NORMALLY_OPEN ties are offered first for a reason.** They're the paths designed for transfer; an ad-hoc close (a sectionaliser) may restore more but overload the donor. Rank drives the order, not the merit — always check feasibility.
- **`binding_constraint: pre_existing` means fix the base case first.** The feeder already violates a limit with *no* DER/EV added; hosting there is 0 until the underlying problem is resolved.
- **`unbounded: true` / `binding: none` means the cap, not the feeder, stopped the search.** Capacity is *at least* the reported figure — raise `max_injection_kw` / `max_added_kw` if you need the true number.
- **Watch the binding constraint, not just the kW.** `overvoltage`/`undervoltage` calls for voltage management (regulator, var control); `thermal` calls for reconductoring. The remedy differs entirely.
- **Power factor moves the answer.** A DER at leading/lagging PF injects vars and cuts hosting capacity; an EV load below unity draws vars and cuts headroom. Model the real PF for a firm number.
- **Feeder-wide scans are cheap — use them.** Leaving `node` unset sizes every apparatus node in one resolve-once pass; that's the map of where DER/EV fits best, not just whether one point works.

---

*Next chapter: **Appendix** — the assumptions, precedence rules, symbol glossary, and a one-page formula sheet spanning all the studies, plus how the dispatch router picks an engine. Tell me to proceed (or name a later chapter).*

---

## Chapter 9 — Appendix: assumptions, precedence, and the formula sheet

This closing chapter is the reference the previous eight lean on. It gathers, in one place, **every endpoint**, the **conventions** they all share, the **precedence rules** that decide which input wins, the **assumptions** each model makes and *which direction they err*, the **symbols and formulas**, and the **honesty flags** a result carries. Keep it beside you when reading any study — it's the decoder ring.

### 9.1 The endpoint index ▶

Every study lives under `/analysis`, is **POST**, is **read-only** (nothing here ever writes; they compute over what connectivity and the materials catalog have already committed), and requires the `analysis:run` permission. The twelve routes:

| Endpoint | Study | Chapter |
|---|---|---|
| `/load-flow` | Radial forward-backward sweep — per-node voltage, per-branch loading | 2 |
| `/voltage-profile` | Same sweep, reported as a per-unit profile vs the ANSI band + I²R loss summary | 2 |
| `/meshed-load-flow` | Weakly-meshed load flow (Tier 1 loop compensation) | 3 |
| `/nodal-load-flow` | Full nodal Y-bus load flow (Tier 2, Gauss-Seidel; multi-slack / PV) | 3 |
| `/load-flow-auto` | Dispatch **router** — classifies topology and auto-selects the engine | 3 / §9.5 |
| `/fault-current` | Series-impedance accumulation — bolted 3-phase (or SLG) fault current per node | 4 |
| `/coordination` | Time-overcurrent protective-device grading on the source→fault path | 5 |
| `/ampacity` | IEEE 738 steady-state thermal ampacity for one conductor | 6 |
| `/arc-flash` | IEEE 1584-2002 incident energy, boundary, PPE category | 7 |
| `/contingency` | N-1 / backfeed restoration — load a donor feeder picks up per tie | 8 |
| `/hosting-capacity` | DER hosting capacity — largest injection before over-voltage/thermal | 8 |
| `/ev-hosting` | EV feeder headroom — largest added load before under-voltage/thermal | 8 |

💡 **Errors are typed consistently.** A bad input → `400` (validation), a missing feature/class → `404`, anything else → `500`. The message always names the offending field or feature.

### 9.2 The shared substrate & conventions ∑

Everything in the package rests on the Chapter 1 network model, and these conventions hold *everywhere*:

- **A node is `(classID, featureID)`** — the same feature in two classes is two nodes; this pair is the key in every map (`states`, `paths`, `nodeByID`).
- **`nominal_voltage_v` is a required input, never derived.** It's the source's sending-end (slack) voltage; `≤ 0` is rejected. The one study that types it differently is ampacity's `ambient_temp_c` (a pointer, because 0 °C is a real value with no sentinel).
- **Per-unit judging is on the *local* base.** Every node carries its own voltage base (set by the transformers between it and the source); a 0.969 reading at a 415 V node means 402 V. Voltage bands (0.95–1.05) are judged in per-unit of that local base, so one band spans all voltage levels.
- **Impedances are per-phase, line-to-neutral ohms**, accumulated along the series path; transformers refer upstream impedance by the **turns-ratio squared** and rebase the per-unit world below them.
- **Graceful degradation is a design rule, not an afterthought.** A missing material spec, an unset field, an unresolved device — none aborts the study; each is computed as far as possible and **flagged** (`material_resolved`, `geometry_assumed`, `clearing_time_assumed`, `zero_seq_estimated`, `out_of_range`, `unbounded`, …). A result never silently hides an assumption.
- **Barriers stop the walk.** Open devices (`device_state = OPEN`, normally-open ties) halt tree-building; the features where the walk stopped are reported as `barriers`/`barrier_hits` — and are exactly the restoration candidates the contingency study reuses.

### 9.3 The precedence ladder ∑

A recurring pattern: when a quantity can come from several places, the **most specific, most authoritative source wins**, and anything that fell back to a default is flagged. The ladder, highest first:

1. **Declared per-feature value** — a field on the asset itself (e.g. `arc_working_distance_mm`, `arc_conductor_gap_mm`, a generator's `fault_contribution_ka`).
2. **Study-level request override** — a value in the request body (e.g. `working_distance_mm`, `min/max_voltage_pu`, `cti_seconds`, `fault_impedance_ohm`).
3. **Engineered default / typical** — a standard-body typical (IEEE 1584 Table 4 geometry, 1584 typical inverter multiple), a catalog figure, or a package constant.

Worked instances you've seen:

| Quantity | Declared | Request | Default |
|---|---|---|---|
| Arc-flash working distance | `arc_working_distance_mm` | `working_distance_mm` | class/voltage typical |
| Arc-flash electrode config | `arc_electrode_configuration` | — | `switchgear` (enclosed) |
| Generator fault infeed | `fault_contribution_ka` → `subtransient_reactance_pct` (X″d) | — | inverter 1.2× / none |
| Voltage band | per-request `min/max_voltage_pu` | — | ANSI 0.95–1.05 |
| Fault impedance | — | `fault_impedance_ohm` (Z_f) | 0 (bolted, worst case) |

⚠️ **Defaults lean to the safe/conservative side of the *question being asked*.** For a hazard study (arc-flash) the default *maximises* predicted energy (enclosed box, ungrounded); for a fault study the default is a bolted fault (Z_f = 0, the highest current); for ampacity, omitted wind is conservative but omitted solar is *not* — the one asymmetry to remember (§6.2).

### 9.4 Assumptions and their safe direction ∑

Each model trades exactness for a simpler, well-understood input. The assumptions, and which way each errs:

| Study | Key assumption | Direction it errs |
|---|---|---|
| Radial load flow | Linearized voltage drop ΔV ≈ (P·R + Q·X)/V, **3 fixed forward iterations** (Kersting ladder, not Newton) | Small under-estimate of drop on very heavily loaded feeders; excellent on distribution radials |
| Loss summary | I²R series losses only (no shunt/iron losses); incomplete if any branch impedance is unresolved | `losses_complete = false` marks it a **lower bound** |
| Meshed (Tier 1) | Loop closed by **compensation** injecting circulating power at the load power factor | Exact for balanced loops; approximate as R/X ratios diverge |
| Nodal (Tier 2) | Ideal transformers (off-nominal taps not modelled), Gauss-Seidel to tolerance | `converged`/`max_mismatch_pu` report honesty |
| Fault current | Bolted (Z_f = 0) unless given; unresolved impedance omitted | **Optimistic upper bound** on current when `material_resolved = false` |
| SLG zero-sequence | Line Z0 ≈ 3× Z1 when unmeasured; earth-return network per vector group | `zero_seq_estimated = true` flags the estimate |
| Coordination / arc-flash | Curves **tabulated** (log-log interpolated), not IDMT formulas at runtime | `extrapolated` when current is off the curve's data |
| Ampacity | **Constant air properties** (not film-temperature-derived), steady-state only | Planning-level; no short-time/emergency rating |
| Arc-flash | IEEE 1584-**2002** empirical fit, valid 0.208–15 kV / 0.7–106 kA | `out_of_range` flags extrapolation; defaults maximise energy |
| Hosting / EV / contingency | Re-sweep under the hypothetical; same two limits (band + 100 % loading) | Inherits the radial sweep's assumptions above |

### 9.5 The dispatch router — how `/load-flow-auto` picks an engine ∑

One entry point solves any topology by classifying it **once** (from `buildTree`'s loop count) and routing:

| Condition | Engine chosen | Reason |
|---|---|---|
| An `engine` override in the request | that engine | explicit analyst choice |
| Multi-slack `sources[]` or PV `regulated[]` present | **nodal** (Y-bus) | only the nodal engine models multiple slacks / PV setpoints |
| 0 loops (radial tree) | **radial** ladder sweep | fastest, exact for a radial |
| 1–3 weak loops (`routerMaxWeakLoops = 3`) | **meshed** (Tier 1 compensation) | a ring main / closed link box, cheaply compensated |
| More than 3 loops (dense mesh) | **nodal** (Y-bus) | too meshed for compensation; solve the admittance matrix directly |

The response envelope reports which engine ran and why (`engine`, `engine_reason`, `loop_count`), so a caller never has to guess. This is the practical front door: **use `/load-flow-auto` unless you have a reason to force a specific engine.**

### 9.6 Symbol glossary ∑

| Symbol | Meaning | Units |
|---|---|---|
| V, V_base | node voltage / its local base | V |
| pu | per-unit = V / V_base | — |
| P, Q, S | real / reactive / apparent power | kW, kVAR, kVA |
| R, X, \|Z\| | resistance / reactance / impedance magnitude | Ω (per-phase L-N) |
| Z_f | fault impedance (0 = bolted) | Ω |
| Z1, Z2, Z0 | positive / negative / zero-sequence impedance | Ω |
| I_bf, I_a | bolted fault current / arcing current | A |
| CTI | coordination time interval | s |
| t | device operate / clearing time | s |
| E_n, E | normalized / actual incident energy | cal/cm² |
| AFB | arc-flash boundary | mm |
| T_c, T_a | conductor max operating / ambient temperature | °C |
| q_c, q_r, q_s | convective / radiative loss, solar gain | W/m |
| α, ε, γ | resistance temp coefficient / emissivity / absorptivity | /°C, —, — |
| D, G, x | working distance / electrode gap / distance exponent | mm, mm, — |
| k = Q/P | reactive-to-real ratio at a load (mesh compensation) | — |

### 9.7 One-page formula sheet ∑

**Load flow (radial ladder).** Linearized drop across a branch, applied over 3 forward iterations:
$$\Delta V \approx \frac{P\,R + Q\,X}{V}, \qquad \text{loss}_{\text{branch}} = I^2 R.$$

**Fault current (3-phase bolted).** Accumulate series impedance source→node, then:
$$I_{bf} = \frac{V_{base}}{|Z_{series} + Z_f|}, \qquad Z_{transformer\ referred} = Z_{up}\cdot n^2 + Z_{own}.$$

**Fault current (SLG).**
$$I_{SLG} = \frac{3\,V}{|Z_1 + Z_2 + Z_0 + 3Z_f|}, \qquad Z_2 = Z_1.$$

**Generator infeed (3-phase superposition).** Added contribution on a common base:
$$Z_{eff} = Z_g + Z_f - 2\,Z_{lca}.$$

**Coordination — inverse-time relay.** Read off the tabulated curve:
$$t_{operate} = \text{time\_dial}\times \text{curve}\!\left(\frac{I}{I_{pickup}}\right), \qquad \text{margin} = t_{backup} - t_{primary} \ge \text{CTI}.$$
Through-current referral across a transformer: $I_{dev}\,V_{dev} = I_{fault}\,V_{fault}$.

**Ampacity (IEEE 738, closed form).**
$$q_c + q_r = q_s + I^2 R(T_c) \;\Rightarrow\; I = \sqrt{\frac{q_c + q_r - q_s}{R(T_c)}},$$
$$R(T_c) = \frac{R_{20}}{1000}\bigl(1 + \alpha(T_c - 20)\bigr), \quad q_r = \pi D\,\varepsilon\,\sigma\bigl((T_c{+}273.15)^4 - (T_a{+}273.15)^4\bigr), \quad q_s = \gamma\,\Phi_{solar}\,D.$$

**Arc-flash (IEEE 1584-2002).**
$$\lg E_n = K_1 + K_2 + 1.081\lg I_a + 0.0011\,G, \qquad E = C_f\,E_n\,\frac{t}{0.2}\left(\frac{610}{D}\right)^{x}.$$
Energy is **linear in clearing time**; evaluate at I_a and 0.85·I_a, worse governs.

**Reactive power at a power factor** (hosting/EV):
$$Q = P\cdot\tan(\arccos(\text{pf})).$$

### 9.8 Defaults & constants ∑

| Constant | Value | Where |
|---|---|---|
| Voltage band (ANSI C84.1 Range A) | 0.95–1.05 pu | all band-judging studies |
| Forward sweep iterations | 3 (fixed) | radial ladder |
| Default CTI | 0.3 s | coordination |
| Max clearing time cap | 2.0 s | arc-flash |
| Arcing-current sensitivity factor | 0.85 | arc-flash |
| Arc-flash validity | 0.208–15 kV, 0.7–106 kA | arc-flash |
| Resistance reference temperature | 20 °C | ampacity |
| Constant air properties | ρ=1.029, μ=1.9e-5, k=0.028 | ampacity |
| DER search cap / start / resolution | 100 MW / 50 kW / 0.1 kW | hosting, EV |
| Bisection iterations | 40 | hosting, EV |
| Router weak-loop threshold | 3 | dispatch router |
| Nodal base VA / GS tolerance | 1 MVA / 1e-9 | nodal |

### 9.9 The honesty flags — reading a result's confidence ▶

Across every study, a family of boolean/label fields tells you *how much to trust the number*. Learn to scan for them first:

| Flag | On which studies | Meaning when set the "bad" way |
|---|---|---|
| `material_resolved: false` | load flow, fault | impedance unresolved → optimistic (drop under-, fault over-stated) |
| `losses_complete: false` | voltage profile | loss figure is a lower bound |
| `zero_seq_estimated: true` | SLG fault | Z0 was assumed (≈3× Z1), not measured |
| `ground_path_resolved: false` | SLG fault | earth-return path incompletely modelled |
| `geometry_assumed: true` | arc-flash | ≥ 1 geometry input used an IEEE typical — fine to screen, not to label |
| `clearing_time_assumed: true` | arc-flash | no upstream device resolved; time capped at 2 s |
| `extrapolated: true` | coordination, arc-flash | fault current is off the end of the tabulated curve |
| `out_of_range: true` | arc-flash | V or I_bf outside the 1584-2002 envelope |
| `binding_constraint: pre_existing` | hosting, EV | the base case already violates → zero headroom |
| `unbounded: true` / `binding: none` | hosting, EV | the search *cap* stopped it, not the feeder — capacity is ≥ the figure |
| `converged: false` | nodal, meshed | the iterative solve didn't reach tolerance — read `max_mismatch_pu` |

💡 **The rule of thumb:** a clean result (no flags set the bad way) is an engineered number you can put on a drawing; a flagged one is a *screening* number — good enough to rank and prioritise, but populate the missing data before it becomes a design decision or a safety label.

---

*This completes **Engineering Studies** — the deep-dive reference across all twelve `/analysis` endpoints. The companion documents are the [user handbook](user-handbook.md) (how-to voice) and, next to be written, the admin & break-glass guide (developer reference).*
