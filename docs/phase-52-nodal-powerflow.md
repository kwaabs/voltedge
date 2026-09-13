# Phase 52 — Nodal power flow (Y-bus / Newton, multi-source)

Status: **S1 + S2 + S6 frontend BUILT (verification owed), S3–S5 + S7 proposed.** This is the "Tier 2"
the Phase 51 meshed-LV doc (`docs/phase-51-meshed-lv-powerflow.md`) names but
deliberately defers — a *distinct solver engine* beside the existing radial
sweep, not an extension of it. It is also the same engine the embedded-generation
work (`memory/gap-embedded-generation.md`) calls "Tier B" (grid-forming /
multi-slack generation). **Both gaps are the same wall — build the nodal solver
once and both close.**

### What is built (S1 + S2, 2026-09-12)

`api/internal/powerflow/nodal.go` + `POST /analysis/nodal-load-flow`
(`nodalLoadFlowHandler` in `handler.go`). The engine:

- **Complex layer** is Go's native `complex128` / `math/cmplx` (no third-party
  math), folded into `nodal.go` rather than a near-empty separate file.
- **General-graph walk** (`buildElectricalGraph`) — undirected (an electrical
  branch conducts both ways, unlike the sweep's downstream-only walk) and
  barrier-aware (an open device stops the walk and is reported, exactly as for the
  sweep). Needs the edge's `to_terminal_id`, which the sweep's `edgeRow` doesn't
  project, so a new `nodalEdge` projection carries both terminal ids.
- **Bus contraction** (`buildBuses`) via **port-based union-find** (`dsu`): a
  conductor with resolved R+jX and a transformer with a resolved ratio (except the
  `start`, always the slack bus) are SERIES elements split into two ports;
  everything else — junctions/meters/loads/source and any unresolved
  conductor/transformer — is a zero-impedance short that unions its neighbours.
  Two parallel conductors between the same pair of buses stay two branches (the
  meshed case the ladder can't do).
- **Per-unit Y-bus** on `baseVA = 1 MVA` with a per-bus voltage base propagated
  from the slack across transformer ratios (`propagateVoltageBases`). With the
  bases chosen in each transformer's turns ratio an at-nominal transformer is a
  1:1 branch and stamps identically to a conductor. Source impedance is a branch
  from an added ideal bus (held at nominal) to the start's bus. Off-nominal taps
  are deferred with the Newton core (every current seed is at-nominal); an ideal
  (un-surveyed %Z) transformer gets a negligible `idealXfmrXPU` reactance to keep
  the matrix non-singular.
- **Gauss-Seidel solve** (`solveGaussSeidel`), single slack, PQ buses only —
  `V_i = (1/Y_ii)[conj(S_i/V_i) − Σ_{j≠i} Y_ij V_j]`, slack fixed at 1∠0.
  Injection is `−(ownKW + j·ownKVAR)·1000/baseVA` so embedded generation reuses
  Tier A's negative-demand netting unchanged. Non-convergence is returned flagged,
  never silently trusted.
- **Reconcile** (`formatNodalResults`) back to the same per-node result shape as
  `/load-flow`: bus members report their bus voltage, branches report through
  current/power and loading (conductor vs ampacity, transformer vs rated kVA).

To reuse per-node electrical resolution, `resolveNodeStates` in `sweep.go` was
refactored to extract `resolveNodeState` (one node), which `nodal.go` calls per
reachable node.

**Validation (S2):** reuses `seed/lv_ring_network_example.sql` (task
`seed-lv-ring-network`, start = RING-SRC). The nodal solver reproduces the 2:1
LVA:LVB split (20 kW / 10 kW) **directly** — LVA and LVB become two parallel
conductor branches between the TX-R-secondary bus and the meeting-meter bus, split
by inverse impedance (LVB twice the length → half admittance → one-third of the
30 kW) — with no loop compensation, a topology-robust cross-check independent of
the upstream MV drop. A dense, genuinely-multi-source seed with a hand-solved
answer still belongs with S3 (multi-slack), where the single-slack assumption dies
— that is S7. Honest scope note: the nodal solve agrees with the radial ladder to
within linearization error, **not** bit-identically (the ladder's drop is
magnitude-only; nodal is the fuller AC solution).

**Verification owed** (`memory/bash-tool-blocked.md`): `cd api && go build ./...`,
then a live psql apply of `lv_ring_network_example.sql` and a
`nodal-load-flow` run from RING-SRC checked against the 20 kW / 10 kW split.

## Why a second engine (the architectural boundary)

The whole `api/internal/powerflow` engine today is a **radial forward-backward
ladder** (Kersting's method):

- `buildTree` (`graph.go`) walks strictly **downstream from one `start`** and
  **rejects any non-radial topology** — the moment a second independent path
  reaches an already-visited node it returns `invalid("...not radial...")`
  (unless the Phase 51 Tier 1 `breaks` accumulator is passed, which records the
  loop-closing edge and keeps a radial spanning tree). The backward and forward
  passes are *defined over a tree*: each node has exactly one parent, and power
  is accumulated up then voltage pushed down that unique path.
- The voltage drop is **magnitude-only**: `approxDropV` in `sweep.go` computes
  `ΔV ≈ (P·R + Q·X)/V` — a linearization with **no phasor angles**. There is no
  complex voltage, no bus angle, no reactive-loop circulation except the
  scalar-continuity stand-in Tier 1 bolts on.
- There is **exactly one slack**: the `start` feature, whose Thévenin source
  impedance `resolveSourceImpedance` reads. Every other node is a PQ load (or a
  negative-demand PQ injection for a generator, `resolveGeneration`).

This is *correct* for MV (built meshed, operated radial via normally-open ties)
and for radial LV, and it is fast. It **cannot** represent:

1. **Dense/multi-source meshes** — an urban LV secondary network fed from many
   transformers into a common grid; a node genuinely reached by many paths.
2. **Grid-forming generation** — a GENERATOR that *holds its terminal voltage*
   (a PV bus) or acts as a second slack, rather than injecting fixed PQ.
3. **True reactive-loop / angle behaviour** — which a magnitude-only drop can
   only approximate.

Crossing that boundary means replacing the *solver core* with a genuine nodal
method. Everything *around* the core — data resolution, the result model, the
frontend pattern — is reused.

## What is reused verbatim (the good news)

The expensive, already-verified parts stay:

- **All of `resolve.go`** — conductor R+jX from real geometry length × material
  spec (`resolveConductorImpedance`), transformer %Z → ohms with tap changer and
  three-winding star arms (`resolveTransformerModel`), source Thévenin impedance
  (`resolveSourceImpedance`), demand P+Q from kW+pf (`resolveDemand`), generator
  injection (`resolveGeneration`), transformer rating (`resolveTransformerRating`).
  The nodal solver consumes these outputs unchanged; it only re-expresses the
  resolved R+jX as a **complex admittance** `y = 1/(R+jX)`.
- **The connectivity read layer** — `edgeRow`, `downstreamNeighbors`, the barrier
  model (`loadFlowBarriers`/`checkBarrier`), the `featureClassLoader` cache. The
  graph walk changes shape (general graph, not downstream tree) but reads the
  same `runtime.feature_connectivity`.
- **The result surface** — `nodeState` / `formatResults` in `sweep.go` do the
  per-node loading %, voltage-band (ANSI C84.1) judging, and circuit-identity
  enrichment. The solver reconciles its solved per-bus voltages/flows back into
  `nodeState` so **voltage-profile (S1), contingency (S2), and hosting (S3) run
  on the solved state unchanged**.
- **The frontend pattern** — a store + a floating result panel (now via the
  shared `use-floating-panel.tsx` hook), a mutually-exclusive analysis armed
  from `feature-panel.tsx`.
- **The endpoint pattern** — `Routes` in `handler.go` mounts each analysis as a
  thin decode → `run*` → JSON handler; read-only, never writes.

## The new work, in dependency order

### 1. Complex-arithmetic layer
Go has native `complex128`, so this is light: helpers to build branch admittances
`y = 1/(R+jX)`, a small sparse/dense complex matrix type, and the P/Q ↔ complex
S = V·conj(I) bookkeeping. No third-party dependency required *for the arithmetic
itself* (see the linear-solver decision below).

### 2. Y-bus assembly (`nodal.go`, new file)
Walk the connectivity graph as a **general graph** (not a downstream tree — this
is the whole point). Assign each electrical node a bus index. Stamp each branch:
off-diagonal `Y[i][j] −= y_ij`, diagonal `Y[i][i] += y_ij` (+ any shunt).
Transformers stamp as **off-nominal-ratio π-models** (the turns ratio is already
computed by `resolveTransformerModel`, tap included). A node reached by three
paths is simply a matrix row with three off-diagonal entries — no `invalid(...)`.

### 3. Bus classification
- **Slack** (V, θ fixed): the source(s). *Plural* — see slice 5.
- **PV** (P, |V| fixed, Q solved): a grid-forming GENERATOR holding terminal
  voltage.
- **PQ** (P, Q fixed): loads, and PQ generators (negative-demand injection, the
  existing Tier A behaviour).

### 4. The iterative solver (the core)
Two options, and the recommended path is to build them in this order:

- **Gauss-Seidel first cut** — no Jacobian, no linear-algebra dependency; iterate
  bus voltages directly from the Y-bus and injected powers. Slower to converge
  and weaker on stiff/ill-conditioned networks, but it **proves Y-bus assembly,
  bus classification, multi-slack, and the whole dispatch/reconcile path
  end-to-end with the least new machinery.** This is the de-risking move.
- **Newton-Raphson** — assemble the **Jacobian** (∂P/∂θ, ∂P/∂|V|, ∂Q/∂θ,
  ∂Q/∂|V|) each iteration and **solve a sparse linear system** `J·Δx = mismatch`.
  This is the production-grade solver (quadratic convergence, robust), and the
  one genuinely new numerical dependency: **Go stdlib has no linear solver.**
  Decision required — hand-roll dense Gaussian elimination (fine for the
  hundreds-of-buses networks this product models), pull in `gonum`
  (`mat`/`sparse`), or stay on Gauss-Seidel. Recommend hand-rolled dense first
  (no new dep, bounded size), revisit `gonum` only if a real network is large
  enough to need sparsity.

Convergence on power-mismatch tolerance, with an iteration cap and a
non-convergence error (a network that won't solve is reported, never silently
returned wrong — same failure discipline as `buildTree`'s `invalid(...)`).

### 5. Multi-slack / PV-bus support
The capability that justifies the whole phase. The single-`start` slack
assumption dies here: a network is fed from **many transformers at once**, and a
grid-forming GENERATOR becomes a **PV bus or a second slack**. This is
`gap-embedded-generation` Tier B. The request grows from one `source` to a set of
source/slack refs (or auto-detected from a functional-role = source axis, cf.
`functional_role_taxonomy.md`).

### 6. Reconcile → `nodeState`
Map solved per-bus |V|/θ and branch flows back into the existing `nodeState`
shape so `formatResults` (loading %, voltage band, circuit identity) and the S1
profile / S2 contingency / S3 hosting studies reuse the solved state. Fault-
current stays on its own superposition path (unchanged).

### 7. Dispatch / routing
Per Phase 51: `buildTree`'s non-radial detection becomes the **router**, not a
hard error. Radial input keeps the fast ladder (MV + radial LV — "correct today,
no caveat"). Genuinely-meshed / multi-source sections route to the nodal solver.
Classification (weak ring → Tier 1 compensation vs dense/multi-source → Tier 2
nodal) is the dispatch decision.

### 8. Endpoint + frontend
Either a new `POST /analysis/nodal-load-flow` (mirrors the existing `run*`
handler shape) or an upgrade of the existing analyses to dispatch internally.
Recommend a **dedicated endpoint first** (clean to validate in isolation), fold
into the shared analyses once trusted. Frontend: a store + floating panel on the
`use-floating-panel.tsx` pattern (this also finally delivers the meshed-LF panel
Phase 51 still owes).

### 9. Validation seed (non-negotiable)
**No fixture exists for this** — even the Phase 51 Tier 1 ring
(`seed/lv_ring_network_example.sql`) is weak-mesh, single-source. Tier 2 needs a
**dense and/or multi-source** seed with a **known analytic answer** (e.g. a
two-transformer LV grid feeding a common mesh, hand-solvable, or checked against a
reference tool). This is not optional: the solver is exactly the kind of
numerical code a static review **cannot** vouch for — recall the Tier 1
circulating-power bug (closing at loop-impedance angle vs load pf) that only a
hand-worked seed surfaced. The seed is the acceptance test.

## Slice plan (proposed)

| Slice | Deliverable | Proves |
|-------|-------------|--------|
| **S1** ✅ | Complex layer + Y-bus assembly (`nodal.go`) over the general graph | matrix is built correctly from resolved impedances |
| **S2** ✅ | Gauss-Seidel solver, single slack, PQ only | end-to-end solve reconciles to `nodeState`, matches radial load-flow on a radial input |
| **S3** | Multi-slack + PV buses (grid-forming gen = Tier B) | the capability the radial engine can't do |
| **S4** | Dispatch: `buildTree` non-radial → route to nodal | radial keeps fast path; meshed routes here |
| **S5** | Newton-Raphson core (Jacobian + linear solve) | production convergence/robustness; GS retained as fallback |
| **S6** ✅ | Endpoint (S2) + store + floating panel + analysis-dropdown item | user-drivable |
| **S7** | Dense/multi-source validation seed + Taskfile task | acceptance test with known answer |

S1→S2 is the spine; S7's seed should be written **alongside S2** so every later
slice is checked against a known answer, not after.

## Effort & risk

- **Biggest lift / highest risk:** the Newton solver (S5) — Jacobian assembly and
  the sparse linear solve. Mitigated by S2's Gauss-Seidel proving everything
  *around* the solver first, and by S7's seed being the acceptance gate.
- **New dependency decision up front:** hand-rolled dense linear algebra vs
  `gonum`. Recommend hand-rolled dense (no new dep, bounded network size).
- **Reused for free:** all of `resolve.go`, the connectivity/barrier read layer,
  `nodeState`/`formatResults`, the store/panel frontend pattern, the handler
  shape.
- **Size:** a **multi-slice phase comparable to Phase 50 itself**, not a bolt-on
  like Phase 51 Tier 1.

## Not in this phase

Unbalanced / per-phase load flow (meshed *and* unbalanced is a further step),
volt-VAR / dynamic inverter DER control, arc flash, LL/LLG faults — see
`enhancement-backlog-distribution-utility`. This phase is the **balanced**
positive-sequence nodal solve: multi-source, meshed, angle-aware.

## Owed before any of this ships

Standing verification debt still applies (Bash blocked here,
`memory/bash-tool-blocked.md`): the Phase 51 Tier 1 `go build` + live psql seed
re-check, the embedded-generation Go/psql checks, and the 8-panel `npm run build`.
Tier 2 adds its own: `cd api && go build ./...`, a live apply of the validation
seed, and a solver re-check against its known-answer numbers.
