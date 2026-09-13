# Phase 51 — Meshed-LV power flow (future)

Status: **PROPOSED / not built.** This is a scoping document, written after the
Phase 50 discussion established that the whole power-flow engine
(`api/internal/powerflow`) is **radial-only** — `runSweep`'s `buildTree` rejects
any topology where a node has more than one parent (`invalid(...)`), because the
backward-forward (Kersting ladder) sweep needs a unique source-to-node path.

## Why this phase exists

The radial assumption is **correct for MV** — distribution MV is built meshed but
*operated* radially (normally-open ties; the very thing Phase 50's create-tie and
contingency donor study model). It is also correct for **radial LV** (one
distribution transformer feeding a tree of LV conductors → meters), which is what
every current instance seed encodes.

It is **not** correct for **meshed LV**, which — in UK / European / much of
African (11/33/0.4 kV) practice — is common: adjacent distribution-transformer LV
feeders interconnected through normally-*closed* link boxes, LV ring mains, or
dense urban secondary/network grids fed from multiple transformers into a common
mesh. Where the network is genuinely meshed, `buildTree` will `invalid(...)` and
**refuse** the study rather than mis-solve it (fails safe, but fails): no
voltage-profile, no losses, and — the case that actually bites — **no DER hosting
capacity at the LV interconnection level, which is exactly where rooftop PV
connects.** Phase 50 Slice 3 as built is an MV-feeder / radial-LV hosting tool,
not a meshed-LV-secondary one.

### Current-data reality (verified 2026-09-12)

No existing seed encodes a closed LV loop. Every LV instance seed
(`test_network_example.sql`, `substation_network_example.sql`,
`lv_metering_example.sql`) wires a strict radial tree
(`DIST_XFMR T2 → LV_CONDUCTOR → METER → SERVICE_POINT`). `seed/loop_check_example.sql`
is a *trace-time* paper-loop detector (`LOOP_CHECK_TRACE`,
`stop_at_open_devices = true`), not a live closed loop. So the engine will not
trip on today's data — and there is no fixture proving it correctly *rejects*
meshed LV either. Any meshed-LV work must ship its own closed-loop seed.

## Two implementation tiers

The right solver depends on **how** the LV is meshed. Scope this as two tiers and
pick per the customer's actual network, because the cost gap between them is large.

### Tier 1 — Weak-mesh loop compensation (bolts onto the existing sweep) — **BUILT (verify owed)**

For **weakly meshed** LV — a handful of normally-closed link boxes / ring mains,
not a dense grid. `POST /analysis/meshed-load-flow` (`powerflow/mesh.go`). Reuses
everything already built:

1. `buildTree` gained an optional `breaks *[]meshBreak` accumulator: when the
   caller passes it (only the meshed run does), a second path to an already-
   visited node is **recorded** as a loop-closing edge and skipped, leaving a
   radial spanning tree — instead of the `invalid(...)` every radial caller
   still gets by passing `nil`. `runSweep` threads the same `breaks` through, so
   the meshed run gets the ordinary radial base solve for free.
2. Each broken loop is two tree paths from a lowest common ancestor to the two
   endpoints of the loop-closing edge (`loopBranches`); the loop's series
   impedance is the sum of both segments' branch R/X (`loopImpedance`).
3. The base sweep leaves a voltage mismatch `M = V(existing) − V(closer)` across
   each still-open loop. `compensate` injects a **circulating power** around the
   loop — added to the branches toward `closer`, subtracted from those toward
   `existing` — solved by a per-loop linear step and re-`forwardSweep`, iterated
   (`meshCompIterations`, `meshConvergedV`) until every mismatch closes.

Fidelity (documented honestly in `mesh.go`'s header — planning figure, not a
full AC solve):

- **Constant-denominator superposition.** Holding `V` at the loop's own base
  makes the drop `(P·R + Q·X)/V` linear in `(P, Q)`, so one linear solve per loop
  closes it and the outer passes only refine the `V` denominators / reconcile
  branch-sharing loops (Gauss-Seidel, adequate for *weak* meshing).
- **Circulating power at the load power factor** (`cQ = k·cP`, `k = Q/P` of the
  demand crossing the break). The magnitude-drop continuity the endpoints must
  satisfy is one scalar equation (`cP·R_loop + cQ·X_loop = −V·M`) in two unknowns,
  so a second assumption pins `(cP, cQ)`. Keying the transferred power to the
  load's own power factor is the physical choice: it reduces to purely real
  circulation for a real load and reproduces the exact **50/50 split two
  identical parallel conductors must carry** — which the naive "circulate at the
  loop-impedance angle" (`cQ/cP = X_loop/R_loop`) does *not*, injecting spurious
  reactive circulation that mis-splits even the symmetric case. It is the honest
  stand-in for the phasor-angle continuity equation this magnitude-only engine
  can't form. (This was caught working the symmetric ring by hand against the
  validation seed below — a good argument for the seed.)
- **Base voltage per endpoint** (`loopRefVoltage`) so an LV ring's circulating
  flow isn't scaled by an MV nominal.

Safety: a loop that meets at an **open device** (a normally-open tie the radial
barrier already halts at) is left **open, not compensated** — so the meshed run
never silently energizes a NOP tie. A zero-impedance ideal-wire loop is flagged,
not divided by. The endpoint **degrades to an ordinary load flow** (0 loops, 0
compensation passes) on radial input. It does **not** generalize to dense
multi-source grids — that's Tier 2.

The response is the same per-node voltage/loading/overload shape as load-flow
(the loops are closed in the returned state) plus a `loops[]` report (each loop's
endpoints, series R/X, residual mismatch, whether every branch resolved, whether
it was compensated).

**Validation seed — BUILT** (`seed/lv_ring_network_example.sql`, task
`seed-lv-ring-network`). The first seed to encode a genuine closed LV loop:
`RING-SRC → MV-R → TX-R (11 kV→400 V)`, then **two** LV legs `LVA` (~100 m) and
`LVB` (~200 m, exactly twice LVA's length, same spec) both feeding one meeting
meter `MTR-R → SP-R` (30 kW at **unity** pf). `LVB → MTR-R` is the single
loop-closing edge; the loop's LCA is `TX-R`, so the transformer/MV drop is common
to both legs and the ring math is pure LV. Unity pf makes the loop load pf `k = 0`
so the converged split is exact inverse-resistance — **LVA carries 20 kW, LVB
10 kW (2:1)**, circulating `cP = 10 kW`, residual `< 1e-4 V`. The 2:1 asymmetry
(not 50/50) also exercises the segA/segB sign convention. A plain radial
`/load-flow` on the same start still `invalid(...)`s (no unique source→node path)
— the seed proves both the rejection and the compensated solution.

Working this seed's hand-check **caught a real bug** in the first cut: closing the
loop at the *loop-impedance angle* (`cQ/cP = X_loop/R_loop`) injects spurious
reactive circulation and mis-splits even two identical parallel conductors (~75/25
for `X≈R` instead of the required 50/50). Fixed to close at the **load power
factor** (see the fidelity bullet above) — this is exactly the value a static
review can't surface and a validation fixture can.

**Owed follow-ups:** `cd api && go build ./...`, then a live psql apply of the
seed and a `meshed-load-flow` re-check against the 2:1 / `<1e-4 V` numbers above;
and a frontend panel (the 10th mutually-exclusive analysis, parallel to
load-flow's).

### Tier 2 — Full nodal solver (a distinct engine)

**Scoped in full in `docs/phase-52-nodal-powerflow.md`** (Phase 52 — the same
engine `gap-embedded-generation` calls "Tier B" grid-forming/multi-slack; build
once, both gaps close). The summary below stands; the phase doc adds the slice
plan, reuse map, linear-solver decision, and validation-seed requirement.

For **dense urban secondary/network grids** (multi-source, heavily meshed). Needs
a genuine nodal method:

- Build the **Y-bus admittance matrix** from the resolved branch impedances.
- Solve with **Newton-Raphson** (or Gauss-Seidel for a simpler first cut),
  supporting **multiple slack/source** buses (network grids are fed from many
  transformers at once — the single-slack `start` assumption dies here).
- Reconcile with the existing `nodeState` model so `formatResults`' loading /
  voltage-band judging (and therefore Slice 1 profile + Slice 3 hosting) can be
  reused on the solved state.

Keep this **separate** from the radial sweep: MV and radial-LV stay on the fast
ladder method; only genuinely-meshed LV sections route to the nodal solver.

## Routing / dispatch

`buildTree`'s non-radial detection becomes the **dispatch signal**, not just a
hard error: when it finds a mesh, classify it (weak ring vs dense grid) and route
to Tier 1 or Tier 2 instead of returning `invalid(...)`. Radial input keeps the
current fast path untouched. This preserves the "MV is correct today, no caveat"
guarantee while extending coverage downward into LV.

## Not in this phase (still)

Unbalanced/per-phase LV load flow (meshed *and* unbalanced is a further step),
volt-VAR / dynamic-inverter DER control, arc flash, LL/LLG faults — see
`enhancement-backlog-distribution-utility`. This phase is specifically about
making the **balanced** power flow tolerate meshed LV topology.
