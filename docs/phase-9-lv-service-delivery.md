# Phase 9 — LV / service delivery

**Goal:** model the low-voltage network from the distribution
transformer's secondary all the way to the customer's point of supply --
the LV mains, service cables, cut-outs, link boxes, meters, meter
chambers, and (for prosumers) embedded generation -- so every real
service-delivery arrangement can be represented, traced, isolated, and
load-flowed, not just the single simplification the seed data carries
today (`SERVICE_POINT` wired *directly* to a `DIST_XFMR` secondary, one
meter per service, no phase).

**Status:** **Slices 0–3 have code landed (verification owed); Slices
4–6 remain design-only.** This doc is the scope-lock the rest of the
phase's slices are cut from, written before any code per this repo's
convention (every prior large build got a design doc first). As with the
recent frontend work, the environment can't spawn a shell (`bash.exe`
`EPERM`), so every landed slice carries the usual "statically reviewed,
live `psql`/compile/verify still owed" caveat -- nothing below has had a
live `psql` apply, `task compile`, or trace check run against it yet.

**Why a whole phase.** This isn't a patch to `SERVICE_POINT`. A proper
LV model reopens and finishes three standing open items at once -- the
"no distinct LV/secondary conductor feature class" gap
(`docs/phase-4-utility-traces.md`), **phase-aware tracing** (the biggest
Phase 4 remainder: `terminals.phase_capable` and
`network_rules.phase_rule` have existed unused since Phase 1), and gives
the Phase 8 load-flow engine its real downstream network. It's naturally
its own initiative, the same way Phase 8 (materials) was "a new phase,
not a continuation."

## The requirement: cover every service-delivery scenario

The model is only complete if it can represent all of these. Each is
mapped to the model in "Coverage" below.

1. **Single dwelling, whole-current, single-phase** -- the common house.
2. **Single dwelling fed direct off the transformer** -- rural, no LV main.
3. **Multi-occupancy with a meter chamber** -- one intake, a busbar, N
   tenant meters (the apartment case that started this).
4. **A street sharing one LV main** -- many services tapping a common run
   (the classic secondary network).
5. **Commercial, three-phase, CT-metered** -- large service, current
   transformers rather than whole-current.
6. **Meshed / interconnected urban LV with link boxes and normally-open
   points** -- LV isolation and loops.
7. **Prosumer / DER** -- PV or battery at the premise, power flowing back.
8. **Unmetered LV** -- street lighting fed off the LV network, no meter.

## The model

The LV network is a **continuation of the same graph** below the
transformer secondary -- it reuses `runtime.feature_connectivity`,
`metadata.terminals`, `metadata.network_rules`, and the existing trace
engine unchanged. Edges are conductors, junctions are devices/nodes,
leaves are points of supply. Metering variant (CT vs whole-current) and
phase (1ph/3ph) are **subtypes/fields, not separate classes** -- the
right granularity.

| Class | Geometry | `network_role` | Terminals | Purpose / scenarios |
|---|---|---|---|---|
| `LV_CONDUCTOR` | line | edge | — | The whole LV run below the transformer secondary, one class. **Construction (overhead/underground) is the subtype** (mirrors `MV_CONDUCTOR`); **functional role (main vs. service) is the `lv_role` field**, not a separate class. Carries `phase` + `conductor_spec`. 1,2,3,4,5,6,7,8 |
| `LV_LINK_BOX` | point | junction | N ways (fused) | Feeder pillar / LV cabinet / sectionalising point; a normally-open way is a barrier. 4,6 |
| `SERVICE_CUTOUT` | point | junction | line/load | The main service fuse at the intake -- the utility's last protective device before metering. All |
| `LV_BUSBAR` | point | junction | T1 in + T2 bar | The chamber's distribution board. Solid equipotential bar, so *one* tap terminal (T2) fans out to N tenant meters — no N-way schema needed. 3 |
| `METER` | point | junction | line/load | **Inline** -- power flows through it; carries `meter_serial`/`account_ref`, CT vs whole-current subtype, 1ph/3ph. Built as `point` (not `none`) so it stays a placeable/traceable node; containment, not null geometry, says it's in the chamber. 1,3,5 |
| `METER_CHAMBER` | point | container | — | Physical container asset (`network_role = container`, not a traced node); `containment` → busbar + meters. Located; the meter's physical home. 3 |
| `SERVICE_POINT` *(repurposed)* | point | junction (leaf) | load | The **point of supply** where load (and DER back-feed) applies. All |
| `DER_UNIT` *(optional)* | point | junction | line | PV/battery at a premise; bidirectional injection. 7 |

`DIST_XFMR` (secondary `T2`) is the LV source; `DIST_SWITCH` already
exists.

### Coverage

- **House:** `XFMR.T2 → LV_CONDUCTOR(main) → LV_CONDUCTOR(service, 1ph) → SERVICE_CUTOUT → METER → SERVICE_POINT`.
- **Rural direct:** no main run: `XFMR.T2 → LV_CONDUCTOR(service) → SERVICE_CUTOUT → METER → SERVICE_POINT`.
- **Apartment:** `… → SERVICE_CUTOUT (building main) → LV_BUSBAR (inside METER_CHAMBER) → METER×N → SERVICE_POINT×N`.
- **Shared street main:** one `LV_CONDUCTOR(main)`, many `LV_CONDUCTOR(service)` taps to premises.
- **Commercial CT/3ph:** `LV_CONDUCTOR(service, 3ph) → SERVICE_CUTOUT → METER(CT subtype) → SERVICE_POINT`.
- **Meshed LV:** `LV_CONDUCTOR(main) ↔ LV_LINK_BOX (normally-open) ↔ LV_CONDUCTOR(main)` -- isolation via `device_state`, loops via the existing `LOOP_CHECK_TRACE` config.
- **Prosumer:** `DER_UNIT` at the `SERVICE_POINT`; load flow handles reverse flow.
- **Street light:** `LV_CONDUCTOR(main) → LV_CONDUCTOR(service, unmetered) → STREET_LIGHT` (leaf, no meter).

## How it reuses what exists

- **Trace engine:** downstream from the transformer now reaches every LV
  node; isolation traces to LV cut-outs/link boxes; meshed LV uses the
  existing barrier + loop-detection. No new engine mechanism.
- **Load flow (Phase 8):** `SERVICE_POINT` is where (P,Q) applies; the
  radial forward-backward sweep extends through LV impedances; a meter is
  electrically pass-through; a `DER_UNIT` injects rather than draws. The
  LV model is exactly the downstream network that engine was designed for.
- **Materials (Phase 8):** LV cable specs attach via a domain value's
  `material_spec_id` -- already supported, no change.
- **Symbology:** the new classes get rules through the admin UI built in
  the Phase 3 symbology-admin slice (`docs/phase-3-symbology-admin-ui.md`).

## Slice plan

Dependency-ordered; each slice is independently seed-able and verifiable
(trace + load-flow), so the phase lands incrementally rather than as one
un-reviewable drop.

| Slice | Content | Closes / advances |
|---|---|---|
| **0** *(code landed, verify owed)* | `PHASE` domain + phase fields + activate `phase_rule` matching | Phase 4 phase-aware items |
| **1** *(code landed, verify owed)* | Single `LV_CONDUCTOR` edge (construction = OH/UG subtype, role = `lv_role` field) + LV cable specs + `network_rules`; retire the direct `XFMR.T2 → SERVICE_POINT` hop so supply is fed via LV | "no distinct LV/secondary conductor" open item |
| **2** *(code landed, verify owed)* | LV devices: `SERVICE_CUTOUT` (line/load fuse), `LV_LINK_BOX` (4-way node) + `device_state` OPEN barriers on the isolation traces | meshed/isolation coverage |
| **3** *(code landed, verify owed)* | `METER` as inline junction + `METER_CHAMBER` (`containment`, its first use) + `LV_BUSBAR` (single-tap, scales to N meters); metered chain + `SERVICE_POINT` → point-of-supply leaf | multi-occupancy / metering |
| **4** | `DER_UNIT` + bidirectional load-flow hooks | prosumer coverage |
| **5** | Unmetered (`STREET_LIGHT`) + CT-metering nuances | remaining scenarios |
| **6** | LV load flow | ties into the Phase 8 engine |

### Slice 0 (starting here): the phase model

"All scenarios" includes single-phase services on a three-phase LV
network, which the system **cannot express today** -- there is no `PHASE`
domain, and `terminals.phase_capable` / `network_rules.phase_rule` have
been carried unused since Phase 1. Slice 0 lays that foundation so every
LV conductor and service built afterward is phase-aware from the start
rather than retrofitted (the reason we start here rather than with the
conductors):

- **A `PHASE` domain** (coded). Values cover the real cases: the three
  phases and neutral (`L1`/`L2`/`L3`/`N`), the single-phase-plus-neutral
  connections a service actually takes (`L1N`/`L2N`/`L3N`), and the
  full three-phase set (`L1L2L3N`). Exact value set to be finalised in
  the slice, but this is an ordinary coded domain -- authorable through
  the existing domains admin page, no new mechanism.
- **A phase field** on phase-bearing classes (conductors, services,
  meters) recording which phase(s) a given feature carries -- a
  `PHASE`-domained coded field, exactly like any other coded field, so
  the compiler and CRUD engine need nothing new.
- **Activate `phase_rule`.** `network_rules.phase_rule` already exists as
  a text expression column that nothing evaluates
  (`docs/phase-2-connectivity.md`). Slice 0 gives the connect matcher a
  real phase-set intersection step so a single-phase service can only
  connect to a compatible phase. **A rule opts in** by setting
  `phase_rule` to the recognized token `phases_intersect`; any other
  content (including the connectivity seed's legacy free-text notes, and
  `NULL`) stays descriptive and enforces nothing -- so every pre-existing
  rule's behavior is unchanged and the gate lands **dormant**, firing
  only for the LV rules (Slice 1+) that opt in. When a rule has opted in,
  both endpoints must carry a `phase` value and their line phases
  (L1/L2/L3) must intersect; neutral is ignored for compatibility (two
  services sharing only `N`, e.g. `L1N` and `L2N`, are on different
  phases and rejected). This is the substantive engine work of the slice;
  the domain and field are the cheap part.

  *Landed (design + code, verification owed):* the domain and field ship
  as `seed/phase_model_example.sql` (`task seed-phase-model`, nullable
  `phase` field so it's additive over existing rows). The gate is
  `api/internal/connectivity/phase.go` -- `checkPhaseCompatibility`
  called from `matchRuleEitherDirection` after a rule matches in either
  direction, plus `resolveEndpoint` now capturing each feature's `phase`.
  Pure-logic coverage is `phase_test.go`. A phase-selective *trace*
  ("trace L2") is **not** in this landing -- the gate is connect-time
  only; trace-side phase filtering rides a later slice once phased LV
  edges exist to trace. As everywhere here, the shell can't spawn
  (`bash.exe` `EPERM`), so this is statically reviewed with a live
  `psql` apply + `task compile CODE=MV_CONDUCTOR`/`DIST_XFMR` +
  `go test ./...`/`go build`/`gofmt` still owed.

Scope note: Slice 0 delivers the *data model and connect-time
enforcement* for phase. Full phase-balance reporting and phase-aware
load flow ride on later slices (5-6) once there are real phased LV
services to run them against.

## Decisions made

- **Construction is the subtype; functional role is a field — settled
  once, for MV and LV alike.** Overhead vs. underground is *always* the
  subtype (`LV_CONDUCTOR` carries `overhead_lv_conductor` /
  `underground_lv_conductor`, mirroring `MV_CONDUCTOR` exactly); a class's
  functional role, where it has one, is *always* a field (LV's coded
  `lv_role`: main / service). This replaced an abandoned two-class
  `LV_MAIN`/`LV_SERVICE` split (role as class) and a briefly-considered
  role-as-subtype variant — one class, construction as subtype, role as
  field. Nothing is lost by not making construction a field:
  `symbology_rules` (migrations/0012) key off `subtype_id` *and/or*
  `domain_value_id` (most-specific-wins), so an overhead-main vs.
  underground-service render distinction is still fully expressible
  against (subtype × `lv_role`). `MV_CONDUCTOR` needed zero rework.
- **Meter is inline in the network, not a leaf record.** The
  devil's-advocate meter-chamber discussion settled this: inside a
  chamber each meter sits on a real per-tenant circuit and power flows
  through it, so `METER` is a two-terminal junction (line/load), not the
  geometry-less billing record the earlier "asset-only" compromise
  proposed. It still carries the CIS/MDM join keys (`meter_serial`,
  `account_ref`) -- GIS holds keys, the CIS/MDM stays the system of
  record (see the CIS-integration discussion; that connector is a
  separate, later, auth-gated item, not part of this phase).
- **`METER_CHAMBER` uses `containment`, `SERVICE_POINT → METER` (single
  dwelling) would use `composite`.** `containment` in this system means
  *spatial* containment; a physical chamber literally contains its meters
  (its first genuine use), whereas an abstract point-of-supply owning a
  single meter is `composite` (ownership). A house needs no chamber --
  the model supports both shapes.
- **CT vs whole-current, and 1ph vs 3ph, are subtypes/fields, not
  classes.** Modelling CTs/VTs as their own features is a granularity we
  don't need; the metering arrangement is an attribute of the meter.
- **Repurpose `SERVICE_POINT` as the point of supply, don't add a new
  leaf class.** It already *is* the customer connection; the only change
  is that it's fed through the LV chain instead of directly off the
  transformer.

## Prerequisites and risks

- **Scale: L-XL, a phase not a seed.** Realistic only as the sliced
  initiative above.
- **Repointing `SERVICE_POINT` is destructive on a live DB.** Its current
  direct-to-transformer `network_rules` and any established connections
  get replaced by the LV chain, and the compiler has **no field-drop /
  retype path** (the open "column rename / data type change" item). A
  fresh seed apply is clean; an existing deployment needs a hand-written
  migration. Slice 1 must call this out explicitly.
- **Phase-aware matching is real engine work.** Slice 0's domain/field
  are trivial; the `phase_rule` evaluation in the connect/trace matcher
  is the part that needs care and test coverage.
- **Environment caveat.** No shell here (`bash.exe` `EPERM`) -- every
  slice ships statically reviewed with a live `psql` apply + `task
  compile` + trace/load-flow verification still owed.

## Related docs

- `docs/phase-4-utility-traces.md` -- the "no distinct LV/secondary
  conductor" gap this phase closes, and the trace engine it reuses.
- `docs/phase-4-safety-traces.md` / `docs/phase-4-trace-engine.md` --
  phase-aware tracing and `respect_phase`, the open items Slice 0 starts.
- `docs/phase-8-materials.md` -- the material specs LV cables reuse and
  the load-flow engine this phase feeds.
- `docs/phase-2-connectivity.md` -- `network_rules` and the currently
  unused `phase_rule` Slice 0 activates.
- `docs/open-items.md` -- the new Phase 9 section tracking these slices.
