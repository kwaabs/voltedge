# Phase 8 — load flow engine

**Goal:** compute real per-node voltage and per-branch loading across a
radial (tree-shaped) distribution feeder, starting from one source
feature -- the engine `docs/phase-8-materials.md`'s own catalog was
built to feed, and the shared foundation the rest of the Phase 8
electrical-analysis slice-up (transformer loading %, symmetrical fault
current, first-principles ampacity) sits on top of. See
`docs/open-items.md`'s Phase 8 section for the full six-slice plan and
the design decisions locked before this slice started.

**Status:** backend curl-verified end to end against a real Postgres,
including a hand-computed numerical check (voltage drop, current, and
loading % all matched by hand), open-device barrier stopping, an
overload flag, and cycle/mesh rejection. No frontend yet -- that's the
next slice (`docs/open-items.md`'s Phase 8 table).

## Design: forward-backward sweep, not full AC power flow

A distribution feeder is a tree, not a general graph -- this project
already keeps it that way (`LOOP_CHECK_TRACE`, the loop/mesh detector
built in Phase 4). A **forward-backward sweep** (Kersting's method, the
standard technique for a radial feeder) is the right tool: a backward
pass accumulates downstream (P, Q) demand into every node, a forward
pass applies the standard linearized voltage-drop approximation
`ΔV ≈ (P·R + Q·X) / V` per branch, three iterations (each one only
refining which node's own voltage a branch's drop is measured against
-- demand itself doesn't change, since this pass assumes constant-power
loads, not voltage-dependent ones). Full Newton-Raphson AC power flow
would be solving a harder problem than the one this network actually
has.

## Design: every graph node can carry impedance or demand, not just "edges"

This project's connectivity graph doesn't have separate "edge" and
"node" object types the way a textbook graph does -- a conductor
feature (`MV_CONDUCTOR`, `LV_CONDUCTOR`) is itself a graph node, same
as a transformer or a meter, just one whose feature class happens to
have `network_role = 'edge'`. So the sweep's branch impedance lives on
**whichever node has that network role**, not on the connectivity rows
between nodes:

- A `network_role = 'edge'` node's own R+jX comes from its real
  geometry length (`ST_Length(geom::geography)`, real meters
  regardless of the class's own SRID -- same technique
  `api/internal/relationship`'s plausibility check already uses for
  `ST_Distance`) times whichever of its own fields currently holds a
  material-linked domain value (`metadata.MaterialSpec`, see
  `docs/phase-8-materials.md`). Deliberately not hardcoded to a field
  named `conductor_spec`: `resolveConductorImpedance` scans every field
  on the class, since `LV_CONDUCTOR` and `MV_CONDUCTOR` are each free
  to name their own material-linked field differently (in fact,
  `MV_CONDUCTOR` doesn't have one seeded at all today -- it resolves to
  zero impedance, degrading gracefully rather than failing the run).
- A `network_role = 'junction'`/`'container'` node (a meter, a cutout,
  a link box, a busbar, a chamber) contributes zero impedance --
  electrically pass-through, exactly as `docs/phase-9-lv-service-
  delivery.md` already describes a `METER`.
- Real (P, Q) demand lives on whichever node carries new `demand_kw` +
  `power_factor` fields -- today only `SERVICE_POINT`, added the same
  way every dynamic field in this project is: through the existing
  admin "add a field to an existing class" capability
  (`docs/phase-6-admin-metadata.md`), not a new migration. A node
  without the fields, or with them unset, contributes zero -- a sweep
  over an incompletely-surveyed network degrades gracefully rather than
  failing.
- Source impedance (the Thevenin equivalent behind wherever the sweep
  is started) lives on two new fields, `source_r_ohm`/`source_x_ohm`,
  added to `DIST_XFMR` the same way -- meaningful only on the specific
  feature a load flow is actually started from, harmless and unread
  everywhere else. Absent, it defaults to zero (an ideal, stiff
  source).

## Design: `nominal_voltage_v` is a required request parameter, not derived

MV and LV sit at very different nominal voltages, and there's no
reliable per-class field to read one from: `LV_CONDUCTOR` carries only
`phase`, no voltage class at all, and `DIST_XFMR` only carries its
*primary* side (`primary_voltage`), not the secondary a load flow
usually starts from. Rather than guess which field means "the
sending-end voltage at Start" -- fragile in a way an explicit parameter
isn't -- the request just states it. Phase-to-neutral or line-to-line
is the caller's own choice as long as it's applied consistently; the
engine works in per-unit-length R/X and treats every value in the
request and response as whatever single voltage basis was supplied.

## Design: cycle detection fails loudly, doesn't silently pick a path

`buildTree` walks downstream via BFS, barrier-aware (reusing
`DOWNSTREAM_TRACE`'s own `trace_barrier_rules` -- see below), and
rejects outright the moment a second, independent path reaches an
already-visited node. A load flow's backward/forward passes both
assume one unambiguous parent per node; a node reached two ways is a
mesh in the section being analyzed, which this project already has a
dedicated detector for (`LOOP_CHECK_TRACE`). Rather than silently
picking one path (wrong) or building a general-graph solver (a much
bigger undertaking than this slice), the request fails with a 400
naming the exact feature the two paths converge on -- the same "fail
loudly rather than silently compute something incorrect" precedent
`api/internal/compiler`'s own narrowing-`CHECK`-constraint handling
already established for this project.

## Design: barriers reuse `DOWNSTREAM_TRACE`, no new mechanism

Rather than inventing a parallel barrier configuration, the engine
loads `DOWNSTREAM_TRACE`'s own `trace_barrier_rules` (already covering
exactly `DIST_SWITCH`/`SERVICE_CUTOUT`/`LV_LINK_BOX` on
`device_state = OPEN`, seeded across Phase 4 and Phase 9) and checks
them live at each node, same as `api/internal/trace`'s own
`checkBarrier` -- device state is never baked into the graph, only
read fresh each run.

## Backend

- **`api/internal/powerflow`** (new package) — `POST /analysis/load-flow`.
  - `graph.go` — node identity, downstream-only neighbor lookup,
    barrier loading/checking (reusing `DOWNSTREAM_TRACE`), and
    `buildTree` (BFS + cycle rejection). Its own small copy of the
    graph-walk primitives `api/internal/trace` already has, same
    reasoning every package touching `runtime.feature_connectivity`
    already keeps its own copy for: this package's node model tracks a
    real tree (parent/children, for two full sweep passes) and
    per-node electrical state trace's membership-only BFS has no
    reason to carry.
  - `resolve.go` — `resolveConductorImpedance` (geometry length ×
    material spec), `resolveDemand` (P, Q from `demand_kw`/
    `power_factor`), `resolveSourceImpedance` (`source_r_ohm`/
    `source_x_ohm`).
  - `sweep.go` — `resolveNodeStates`, `backwardSweep`, `forwardSweep`,
    `runLoadFlow` (the whole engine's entry point).
  - `handler.go` — the chi router, mounted at `/analysis` in
    `api/cmd/server/main.go`.
- New fields (no migration, added via the existing admin add-field
  capability + a recompile): `SERVICE_POINT.demand_kw`,
  `SERVICE_POINT.power_factor`, `DIST_XFMR.source_r_ohm`,
  `DIST_XFMR.source_x_ohm` -- all nullable numeric.

## How this was validated

Built a real scratch topology against a local, non-Docker Postgres:
`DIST_XFMR` (source, `source_r_ohm=0.05`, `source_x_ohm=0.02`) →
`LV_CONDUCTOR` (`WAVEFORM_185`, ~43m) → `SERVICE_CUTOUT` →
`SERVICE_POINT` (`demand_kw=5`, `power_factor=0.95`), plus a
pre-existing, unrelated `SERVICE_POINT` also fed off the same
transformer (no demand set).

1. Ran the sweep at `nominal_voltage_v=230` and hand-computed the
   expected result: source-impedance drop
   `(5000·0.05 + 1643.4·0.02)/230 ≈ 1.23V`, conductor drop
   `(5000·0.0071 + 1643.4·0.00303)/228.77 ≈ 0.18V`, current
   `√(5000² + 1643.4²)/228.59 ≈ 23.02A`, loading `23.02/313 ≈ 7.36%` --
   every one matched the API's own response to the displayed precision.
   The unrelated pre-existing `SERVICE_POINT` correctly showed
   `cumulative_kw = 0` and passed voltage through unchanged.
2. Set `SERVICE_CUTOUT.device_state = OPEN`; re-ran; confirmed the
   `SERVICE_POINT` beyond it dropped out of `nodes` entirely and the
   cutout appeared in `barriers`.
3. Closed the cutout again, raised `demand_kw` to 500; confirmed
   `loading_pct` on the conductor exceeded 100% and the conductor
   appeared in `overloaded`.
4. Added a second `LV_CONDUCTOR` from the same transformer back into
   the same `SERVICE_CUTOUT` (a real mesh); confirmed the request
   failed with a 400 naming the exact converging feature, rather than
   silently computing a wrong answer.
5. Removed every piece of scratch data (connections, features, the
   source-impedance test values) afterward, confirming the topology
   read exactly as it did before.

`go build ./... && go vet ./... && gofmt -l . && go test ./...` all
clean throughout.

## Try it yourself

```bash
# Assumes a DIST_XFMR (source) already wired down to at least one
# SERVICE_POINT -- see docs/phase-9-lv-service-delivery.md's seed
# scripts for a real LV topology.
curl -X POST http://localhost:3704/analysis/load-flow \
  -H "Content-Type: application/json" \
  -d '{"start":{"feature_class":"DIST_XFMR","feature_id":"<id>"},"nominal_voltage_v":230}'
```

## What's still out of scope

- **Frontend** — no trigger, results table, or map color-coding yet;
  the next slice in the plan.
- **Transformer loading %** — rides on this engine once `DIST_XFMR`
  gains a `rated_kva` field; the sweep's own demand at that node is
  already computed, just not yet expressed against a rating.
- **Symmetrical fault current** — reuses this engine's branch-
  impedance-accumulation directly, walked from source to a fault point
  instead of source to load; not built yet.
- **Phase-aware sweep** — `docs/phase-9-lv-service-delivery.md`'s own
  phase model (Slice 0) is connect-time only; this sweep treats the
  whole network as a balanced single-line equivalent, same
  simplification locked when the six-slice plan was designed. Real
  per-phase load flow is that phase's own Slice 6, not this one.
- **Voltage-dependent (ZIP) loads** — every load is treated as
  constant-power; fine for a planning-level calc, not accurate for a
  detailed short-term voltage study.
