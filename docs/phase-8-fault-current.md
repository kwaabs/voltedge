# Phase 8 — symmetrical fault current

**Goal:** compute the available symmetrical (3-phase bolted) fault
current at every node downstream of one source feature -- Slice E of
the six-slice electrical-analysis plan, reusing `api/internal/powerflow`'s
own tree-walk, barrier handling, and branch-impedance resolution
directly rather than re-deriving any of it. See `docs/open-items.md`'s
Phase 8 section for the full plan and `docs/phase-8-load-flow.md` for
the load-flow engine this rides on top of.

**Status:** backend curl-verified end to end against a real Postgres,
including a hand-computed numerical check, the omit-on-zero-impedance
edge case, and open-device barrier stopping (reusing `DOWNSTREAM_TRACE`'s
`trace_barrier_rules`, unchanged from the load-flow engine). Frontend
built and Playwright-verified end to end -- see "Frontend" below.

## Design: one forward pass, no iteration, no demand

The load-flow engine's own forward-backward sweep exists because
voltage and demand are circularly dependent -- a branch's voltage drop
depends on the current flowing through it, which depends on demand,
which is measured against voltage. Fault current has none of that:
it's `V / |Z_total|`, where `Z_total` is a fixed property of the
network's own topology and impedance, entirely independent of what
demand (if any) is normally drawn through it. So this reuses the
sweep's ingredients -- `resolveNode`, `loadFlowBarriers`, `buildTree`,
`resolveNodeStates`, `resolveSourceImpedance` -- but not its iteration:
one forward pass over `tr.order` (BFS discovery order, so a parent is
always processed before its children -- the same guarantee
`backwardSweep` relies on in reverse) summing each node's own R+jX onto
its parent's already-accumulated total is exact, not an approximation
to refine.

`Z_total` at the start node is the source's own impedance
(`source_r_ohm`/`source_x_ohm`, zero if unset -- an ideal, stiff
source). Every other node's `Z_total` is its parent's total plus its
own branch impedance if it's a conductor (`network_role = 'edge'`,
same `resolveConductorImpedance` the load flow already uses), or
unchanged if it's a pass-through junction -- identical impedance-
propagation logic to the load-flow sweep's own forward pass, just
without the voltage-dependent drop term.

## Design: a full per-node table, not a single fault-point query

Rather than taking a specific "fault point" parameter and returning
one number, `POST /analysis/fault-current` mirrors `POST
/analysis/load-flow`'s own shape: one reading per node downstream of
`start`, so a planner sees available fault duty across the whole
network from a single call, the same way a load flow shows voltage
everywhere at once rather than requiring a query per point. The
per-node accumulation this needs is exactly what walking the tree
forward already produces for free.

## Design: `fault_current_amps` is omitted, not zero or infinite, when `Z_total` is zero

An ideal source (no `source_r_ohm`/`source_x_ohm` set) feeding a run of
conductors with no resolved material spec has `Z_total = 0` --
mathematically that makes fault current unbounded, not "0A" (the
opposite of what a caller would assume a bare number means) and not a
literal `Infinity` (not valid JSON). `FaultCurrentAmps` is a `*float64`,
left `nil` (omitted from the response) in this case -- the honest
reading is "insufficient impedance data to bound this," the same
degrade-gracefully stance `resolveConductorImpedance` already takes
when a branch's own material can't be resolved (drops nothing, isn't
an error).

## Backend

- `api/internal/powerflow/types.go` -- `faultCurrentRequest` (Start +
  NominalVoltageV, same shape and reasoning as `loadFlowRequest`'s own),
  `faultNodeResult` (FeatureClass, FeatureID, ROhmTotal, XOhmTotal,
  ZOhmTotal, FaultCurrentAmps *float64, MaterialResolved),
  `faultCurrentResponse` (Start, NominalVoltageV, Nodes, Barriers).
- `api/internal/powerflow/fault.go` (new) -- `runFaultCurrent` (entry
  point, reuses the load-flow engine's own tree-walk/barrier/impedance
  functions), `formatFaultResults` (the single forward accumulation
  pass).
- `api/internal/powerflow/handler.go` -- `POST /fault-current`, mounted
  alongside `/load-flow` at `/analysis`.

No new fields needed -- `DIST_XFMR.source_r_ohm`/`source_x_ohm` (Slice
B) and every conductor's own material-linked field are exactly the
inputs this needs, already in place.

## Frontend

Structurally a near-twin of the load-flow frontend: own store
(`fault-current-store.ts`), a highlight-layer effect in
`voltedge-map.tsx`, and a combined prompt/results panel
(`fault-current-panel.tsx`) in the same shared bottom-center slot,
kept as its own component rather than reusing `load-flow-panel.tsx`
because the result shape is genuinely different (impedance/fault-duty
columns -- Z total, R/X breakdown, fault current -- not voltage/
demand/loading). Two new `HighlightRole`s in `map-layers.ts`
(`fault-current`, `fault-current-barrier`, in fresh colors distinct
from every other role) -- no third "overload" role, since fault
current has no capacity-vs-rating concept of its own. "Run fault
current…" is offered unconditionally on the same header dropdown
`load-flow-panel.tsx`'s trigger already extended, regardless of the
selected feature's `network_role` -- a fault can meaningfully be
evaluated starting from any feature, same reasoning load flow's own
trigger already uses. Arming any of trace/isolate/load-flow/fault-
current now clears the other three, extending the mutual-exclusivity
chain each engine's own trigger already maintained.

Playwright-verified against the real seed topology: with
`source_r_ohm`/`source_x_ohm` set on the substation transformer, the
results table showed `Z = 0.0539Ω` (`R 0.0500 / X 0.0200`) and
`204,265A` at every reached node, matching this doc's own hand-
computed check to the displayed precision; the map highlight rendered
in the new role's own color along the full reached path. Starting
from a node with no source impedance of its own (`XF-1`, downstream of
the substation transformer but carrying none itself) correctly showed
`Z = 0.0000Ω` and an empty (not `0A`) Fault current cell, confirming
the omit-on-zero-`Z` backend behavior surfaces correctly in the UI.
Scratch source-impedance values removed afterward.

## How this was validated

Against the same real seed topology `docs/phase-8-load-flow.md`'s own
verification used (substation `DIST_XFMR` → `MV_CONDUCTOR` →
`DIST_SWITCH` → `MV_CONDUCTOR` → `DIST_SWITCH` → `MV_CONDUCTOR` →
`DIST_XFMR` "XF-1" → `SERVICE_POINT`), all against a local, non-Docker
Postgres:

1. Set `source_r_ohm=0.05`, `source_x_ohm=0.02` on the substation
   transformer, ran `POST /analysis/fault-current` from it at
   `nominal_voltage_v=11000`. Hand-computed
   `Z = hypot(0.05, 0.02) = 0.0538516…`,
   `I = 11000 / 0.0538516 = 204264.87A` -- matched the API's own
   response to full float precision. Every downstream node reported
   the identical `r_ohm_total`/`x_ohm_total`/`z_ohm_total`/
   `fault_current_amps`, correctly unchanged across every pass-through
   junction and unresolved-material conductor in this topology (none
   of `MV_CONDUCTOR`'s own fields resolve to a material spec in this
   seed, so zero branch impedance is the correct, expected reading --
   not a defect).
2. Nulled the source impedance and re-ran: `z_ohm_total = 0` at every
   node and `fault_current_amps` correctly **absent** from every node
   (not `0`, not `null` inside the object -- omitted entirely), per the
   omit-on-zero-`Z` design above.
3. Restored the source impedance, opened one `DIST_SWITCH`
   (`device_state = OPEN`) partway down the feeder, re-ran: the switch
   itself still appeared in `nodes` with its own resolved state (its
   own barrier check happens when the walk tries to go *past* it, not
   when it's reached), everything downstream of it was absent from
   `nodes`, and the switch appeared in `barriers` -- identical
   semantics to `buildTree`'s own established barrier handling, reused
   completely unchanged.
4. Confirmed the validation/not-found error paths (missing `start`,
   non-positive `nominal_voltage_v`, an unknown feature ID) return the
   same 400/404 shape every other `analysis`/`trace` endpoint does.
5. Removed every scratch attribute value afterward.

`go build ./... && go vet ./... && gofmt -l . && go test ./...` all
clean throughout.

## Try it yourself

```bash
curl -X POST http://localhost:3704/analysis/fault-current \
  -H "Content-Type: application/json" \
  -d '{"start":{"feature_class":"DIST_XFMR","feature_id":"<id>"},"nominal_voltage_v":11000}'
```

## What's still out of scope

- **Unbalanced / sequence-network analysis and protection
  coordination** -- explicitly out of scope for this slice (see
  `docs/open-items.md`'s design-discussion narrative): needs per-device
  sequence impedance and protective time-current curve data, a
  genuinely different, specialized modeling domain dedicated tools
  (ETAP/CYME) exist for. This engine only computes the symmetrical
  (positive-sequence, 3-phase bolted) case.
- **First-principles conductor ampacity** -- Slice F, not started.
