# Phase 50 — Advanced engineering analyses

Extends the power-flow engine (`api/internal/powerflow`) beyond the existing
load-flow / fault-current / coordination / ampacity set. All three slices below
build on the **shared radial sweep** now factored out as `runSweep` (sweep.go):
resolve → build tree → backward (demand) → forward (voltage). Every analysis
that needs per-node voltage and per-branch current/impedance reuses it rather
than re-solving.

## Slice 1 — Voltage profile & losses report — **BUILT (verify owed)**

`POST /analysis/voltage-profile` (powerflow/profile.go). Runs the same sweep as
load flow, then reports, off the swept state:

- **Per-node voltage profile:** each node's per-unit voltage (`voltage_v /
  nominal_base_v`), judged `within_limits` against a caller band that defaults
  to ANSI C84.1 Range A (0.95–1.05 pu). Nodes outside the band collect into
  `violations`.
- **Series losses:** per conductor branch, `I²·R` (the same `I = S/V` current
  load flow reports, the branch's own resolved `R`), summed to a feeder
  `total_loss_kw` and `loss_pct` (of total load). `losses_complete = false` when
  any branch lacked resolved R/X, so the total reads as the lower bound it is.
- **Summary:** total load kW/kVAR, total/percent losses, worst (lowest) per-unit
  voltage and its node, violation count.

No migration, no schema change: it reads existing conductor material specs and
`demand_kw`/`power_factor`. Web: `voltage-profile-store.ts` +
`voltage-profile-panel.tsx`, armed from the feature panel's analysis menu
("Voltage profile & losses…"), mutually exclusive with the other six analyses.

Units note: like the whole engine, figures are on whatever single voltage basis
the caller supplies `nominal_voltage_v` on (see the powerflow package doc) — the
losses are the single-phase-equivalent figure consistent with load-flow's own
currents, not silently ×3.

## Slice 2 — N-1 contingency / backfeed restoration — **BUILT (verify owed)**

`POST /analysis/contingency` (powerflow/contingency.go), framed as a **donor
study**: sweep a donor source feeder, then for each open boundary tie adjacent to
it, hypothetically close the tie and measure the load it can pick up and the
post-transfer violations. This fits the radial single-source sweep engine
directly — no multi-source solver, no topology mutation.

How it works, reusing what exists:
1. **Base sweep** of the donor `source` (`runSweep`, `closed = nil`) establishes
   the donor's already-served set (`baseReached`) and its base load.
2. **Candidate ties** are the base sweep's `barrierHits` — nodes reached but
   halted at an open device are exactly the open boundary points. (Or a single
   caller-named `tie`.) Each candidate's `normal_state` is read and candidates
   are **ranked NORMALLY_OPEN first** (`sort.SliceStable`), satisfying "consider
   every open boundary switch, rank NORMALLY_OPEN preferred."
3. **Per candidate**, re-run `runSweep` with an **in-memory** `closed`
   override (`{candidate: true}`) — the sweep takes the closed map without
   touching `runtime.feature_connectivity` (the plan's in-memory preference).
   The threaded `closed map[node]bool` flows sweep → buildTree → checkBarrier
   (an early `if closed[n]` opens just that device). A candidate that forms a
   loop/mesh trips `buildTree`'s non-radial guard; the per-candidate
   `isValidationError` catch reports it as a parallel path and continues rather
   than failing the whole study.
4. **Report per candidate:** restored load (donor source cumulative-kW delta),
   restored node set (closed-run order minus `baseReached`), worst per-unit
   voltage, undervoltage `violations` and thermal `overloaded` branches
   (Slice 1's judging + `formatResults`), and a `feasible` verdict (restored > 0,
   no violations, no overloads).

Web: `contingency-store.ts` + `contingency-panel.tsx`, armed from the feature
panel's analysis menu ("Contingency & backfeed…"), mutually exclusive with the
other seven analyses.

### Create-tie affordance (Part A)

A one-click composite that places a switch and wires it **across two feeders**
(conductor-to-conductor), born `normal_state = NORMALLY_OPEN` +
`device_state = OPEN` — the create-side companion to the contingency read-side
above (it manufactures the exact open boundary ties the study then evaluates).
Mirrors bus coupler's fan-out, cross-feeder instead of bus-to-bus:

- `api/internal/tie` — `POST /ties` (`CreateTie`, `db.RunInTx`:
  `crud.CreateFeature` → two `connectivity.EstablishConnection`, feeder A → switch
  `T1` (rule 1), switch `T2` → feeder B (rule 3), attributes defaulted
  NORMALLY_OPEN/OPEN non-clobber) + `GET /ties/options` (`ListTieOptions`, the
  switch classes carrying a NORMALLY_OPEN `normal_state` value that also satisfy
  both wiring rules, with their eligible feeder classes). Layered gate:
  features:write (mount) + connectivity:commit (POST), like coupler.
- Web: draw-store `tie` draft variant + `startTieDrawing`; `use-ties.ts`;
  `tie-feeder-picker.tsx`; the `draft.tie` save branch + two feeder pickers in
  `feature-create-form.tsx`; the "Feeder tie (NOP)" section in
  `new-feature-menu.tsx`.

Prerequisite: `seed/switch_normal_state_fields.sql` applied + the switch classes
compiled (`task compile CODE=MV_SWITCH`), else `GET /ties/options` is empty and
the affordance simply never shows.

## Slice 3 — DER hosting capacity — **BUILT (verify owed)**

`POST /analysis/hosting-capacity` (powerflow/hosting.go). How much distributed
generation a node (or every interconnection node on a feeder) can accept before
the first limit binds (over-voltage at any node, or thermal overload on any
branch/transformer).

How it works, reusing what exists:
1. A generator is modelled as **injection = negative demand** at the candidate
   node — the constant-power model already carries sign, so no new electrical
   model: `ownKW -= injKW`, `ownKVAR -= injKW·tan(acos(pf))`. Fixed power factor,
   **unity default** (real power only); at pf < 1 the generator also injects VARs.
2. The feeder is resolved **once** (tree walk + per-node load), then each trial
   injection is a pure in-memory re-sweep (`backwardSweep` + `forwardSweep`) — the
   binary search runs dozens of sweeps per candidate with zero extra queries.
3. Per candidate: an exponential ramp brackets the first infeasible injection,
   then a **bisection** pins the largest feasible one. Feasible = no node above
   `max_voltage_pu` (Slice 1's band) AND no branch/transformer over 100 % loading
   (load-flow's `formatResults` check — magnitude-based, so reverse export current
   counts). The largest feasible injection is the hosting capacity.
4. **Report per candidate node:** `hosting_capacity_kw`, the `binding_constraint`
   (`overvoltage` / `thermal`, or `pre_existing` when the base case already
   violates so there's no headroom, or `none`/`unbounded` when the search cap is
   reached), the `binding_feature`, and the worst voltage-pu / loading-pct AT the
   capacity (how close each limit sits). The binding constraint is whichever limit
   has the smaller remaining margin at capacity.

Reverse-flow note: with net injection the forward sweep lifts the source node
above nominal (the `start = slack` assumption still holds — the caller's nominal
is the sending-end reference — the linear ΔV approximation just carries a signed
drop). Volt-VAR / dynamic-inverter control is out of scope; this is the fixed-PF
planning figure.

Web: `hosting-store.ts` + `hosting-panel.tsx`, armed from the feature panel's
analysis menu ("DER hosting capacity…", Sun icon), the 9th mutually-exclusive
analysis; the result table ranks nodes best-hosting-first.

## Not in this phase

Unbalanced/per-phase load flow, capacitor/volt-VAR optimization, arc flash
(IEEE 1584), LL/LLG faults, harmonic/motor-starting, and MAIFI remain on the
backlog (see `enhancement-backlog-distribution-utility`). Slices 2 and 3 were
chosen with Slice 1 because both lean directly on Slice 1's limit-judging and
the shared sweep, keeping the incremental engine work small.
