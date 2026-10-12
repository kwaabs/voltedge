# Analysis Validation Report

**Written for:** an engineer or auditor deciding how far to trust VoltEdge's analysis numbers — what has been validated, against which external reference, to what tolerance, and, just as importantly, **what has not**.

This is the test-backed companion to the validation *tiers* in **Engineering Studies** (Validated / Derived / Experimental). Where those labels say "how much to trust a study", this report names the **actual tests**, their **reference**, and their **result**, so a claim is evidence, not assertion. Every row corresponds to a Go test under `api/internal/powerflow/` that runs in CI.

> **How to reproduce.** `cd api && go test ./internal/powerflow/ -v`. Each test logs its computed vs reference figures.

---

## 1. What is validated against an external reference

| Engine | What | Reference (independent of the code) | Tolerance | Result | Test |
|---|---|---|---|---|---|
| **Load-flow (balanced sweep)** | Receiving-end voltage on a radial feeder | **Exact closed-form 2-bus AC** solution (the biquadratic) | < 1% across realistic drops | 0.0023% @ 0.5% drop · 0.16% @ 4% · 0.73% @ 8%; consistently optimistic | `sweep_accuracy_test.go` |
| **Fault current (3φ, SLG)** | `I"k`, `i_p`, κ, impedance referral | **IEC 60909** worked example (630 kVA, uk 4%, X/R 6) | exact (deterministic) | `I"k = 23,870 A`, `i_p = 54,497 A` at `c = 1.05` — matches IEC `c·Un/(√3·Z)` | `fault_iec60909_test.go` |
| **Fault current — voltage factor c** | IEC 60909 `c` applied (1.10 MV / 1.05 LV) + override | **IEC 60909** definition of `I"k_max` | exact | default + override both pinned | `fault_iec60909_test.go` |
| **Peak asymmetry κ** | `κ = 1.02 + 0.98·e^(−3R/X)`, `i_p = κ√2·I` | **IEC 60909** formula, hand values | 1e-4 | matches at X/R = 1, 10, →∞ | `fault_integration_test.go` |
| **Coordination verdict** | `margin = t_backup − t_primary`, coordinated ⟺ `margin ≥ CTI` | hand calculation (Feeder 7) | exact | coordinated / miscoordinated / exact-margin / ordering / nil-vs-false all pinned | `coordination_test.go` |
| **Arc-flash model (IEEE 1584-2002)** | Arcing current (eq 1/2), incident energy (eq 3-4), arc-flash boundary (eq 5) | **IEEE 1584-2002** canonical worked example (480 V, 20 kA, box, grounded, G=32, D=457, t=0.2 s) | ~0.1 cal/cm²; ~15 mm | Ia ≈ 11.2 kA, E ≈ 7.29 cal/cm², AFB ≈ 1556 mm — matches the published example; eq 2 geometry-independence pinned | `arcflash_test.go` |
| **Unbalanced load-flow (Tier 2)** | Full Carson phase-impedance matrix — **self AND mutual** terms; coupled per-phase `ΔV = Z·I` drop, single section and 2-section composition | **IEEE 13-node / Kersting** published Config 601 matrix (`Z601`) | 0.01 Ω/mile (all 9 terms); exact (drop) | every term — self and mutual — matches to 4 dp (556,500 ACSR, Kron neutral reduction); coupled drop + 2-section composition exact; coupling provably applied (differs from diagonal-only) | `unbalanced_integration_test.go`, `unbalanced_composition_test.go` |
| **Nodal load-flow (Y-bus), radial — both solvers** | PQ-bus voltage from the iterative Y-bus solve on a slack→PQ series network; Gauss-Seidel *and* Newton | **Exact closed-form 2-bus AC** solution (the biquadratic) | 1e-7–1e-6 pu (light→heavy load) | both solvers converge to the exact AC root to ~1e-11 pu across light/moderate/heavy loads | `nodal_accuracy_test.go` |
| **Nodal load-flow, MESHED (loop) — both solvers** | 3-bus triangle (two paths to source) — recovery of bus voltages incl. loop flow; Gauss-Seidel *and* Newton | **Manufactured solution**: voltages chosen, injections `S_i = V_i·conj((Y·V)_i)` derived independently of the solver | 1e-7 pu (magnitude + angle) | both solvers recover the manufactured V at every bus; loop/off-diagonal coupling pinned | `nodal_accuracy_test.go` |

## 2. What is validated against a hand calculation (composition, not an external standard)

These pin that the *assembled* passes compose correctly — a pile of correct formulas wired together wrong still gives wrong answers — against an independent by-hand calculation, over small in-memory networks (no database):

| Engine | What | Test |
|---|---|---|
| Load-flow sweep | Backward demand accumulation, forward voltage, source impedance, transformer rebasing, branching | `sweep_integration_test.go` |
| Fault current | Series-impedance accumulation + transformer referral, √3 basis, SLG zero-sequence, generator infeed round-trip, transformer-as-source | `fault_integration_test.go` |
| Unbalanced load-flow | Carson build + coupled sweep — validated against IEEE Config 601, see §1 | `unbalanced_integration_test.go`, `unbalanced_composition_test.go` |
| Load forecast | Scenario projection kernels | `forecast_test.go` |
| Connectivity | Phase intersection, audit | `connectivity/phase_test.go`, `audit_test.go` |
| Export | Shapefile/CIM shaping | `export/export_test.go`, `cim_test.go` |

> **End-to-end (live database).** Beyond the in-memory kernels above, `runFaultCurrent` and `runCoordination` have a live integration test (`integration_db_test.go`) that drives the whole path — resolve the seeded network, build the tree, compute the fault, load the TCC curves — asserting the fault level, the `c`-factor applied live, and the coordination verdict. It skips when there is no database and runs in the CI **bootstrap** job against a seeded Postgres (§4).

## 3. Conformance notes

- **IEC 60909 voltage factor `c`.** The fault-current endpoint, SLG, arc-flash (via `runFaultCurrent`) and coordination all apply `c` — default `c_max` (1.10 above 1 kV, 1.05 at/below), per-request overridable, reported per node/response. Figures quoted in guides predating this change are the `c = 1.0` case; pass `voltage_factor_c: 1.0` to reproduce them.
- **Balanced sweep is planning-grade.** Single-phase, linearised (nominal-voltage drop denominator), so it is validated against a **balanced** reference — *not* the IEEE unbalanced feeders, which belong to the Phase-55 unbalanced engine (validated separately, §1).

---

## 4. What is NOT yet validated (the honest gaps)

Trust these only to the stated extent, and treat the following as open:

- ⚠️ **Full IEEE-13 node-voltage solution.** The line-impedance build — now the **full** `Z601` matrix, **self and mutual** terms (§1) — and the coupled per-phase drop **are** validated. What is **not** attempted is a full published-node-voltage match for the whole IEEE 13 feeder — it includes a voltage regulator, an in-line transformer, shunt capacitors and line charging that this planning engine documents it does **not** model (`unbalanced_coupled.go` HONEST LIMITS). (The self-impedance term, previously logged-not-asserted over an apparent Kron discrepancy, is now asserted: the mismatch was a wrong phase conductor in the test — 336,400 instead of Config 601's actual 556,500 ACSR — not an engine or method error. No OpenDSS cross-check was needed.)
- **End-to-end from the database — covered for fault, coordination, load flow (sweep) and nodal.** `integration_db_test.go` drives the real endpoints against the seeded network: `runFaultCurrent` / `runCoordination` (fault level, live `c`-factor, coordination verdict), `runLoadFlow` (the balanced sweep — whole DB-coupled assembly, with finite/positive/physically-bounded invariants), and `runNodalLoadFlow` with **both** solvers. The nodal check is the strongest: Gauss-Seidel and Newton assemble the identical Y-bus from the live database and must converge to the same per-node voltages (≤ 0.5 V apart on an 11 kV base) — an independent cross-check on the assembly needing no external reference. All skip without a database and run in the **CI bootstrap job** (seeded Postgres). These are invariant/cross-solver checks, not external-reference accuracy (that is the in-memory kernel tests' job, §1).
- **Coordination above `gradePairs` — covered.** The device evaluation (reading each operate time off a loaded TCC curve, the through-current referral) is now exercised by the end-to-end coordination integration test against the seeded network (above), not just the `gradePairs` verdict kernel.
- **Nodal (Y-bus) load-flow — both solvers validated, radial *and* meshed, kernel *and* end-to-end.** Both nodal kernels — Gauss-Seidel (`solveGaussSeidel`) and Newton-Raphson (`solveNewton`, newton.go) — converge to the **exact closed-form 2-bus AC** solution to ~1e-11 pu (radial) and recover a **manufactured solution** on a 3-bus meshed triangle — loop flow included — to 1e-7 pu in magnitude and angle (§1, `nodal_accuracy_test.go`, both solvers). End-to-end, the two solvers agree on the live seeded network (above). No remaining open nodal item.
- **Scale — compute kernels measured; concurrency still open.** The analysis solve's algorithmic scaling is now a measured fact (`powerflow/scale_bench_test.go`, DB-less): the balanced **sweep is ~O(N)** (0.4 ms / 7.8 ms / 90 ms at 1k / 10k / 50k nodes — scales to whole-network; a 20k-segment feeder solves to a finite monotonic profile), while the **nodal solver is O(N²)** on its dense Y-bus (Gauss-Seidel 76 ms → 1.05 s → 8.9 s at 100 → 400 → 1000 buses) and **Newton's dense Jacobian is O(N²) memory** (15 MB → 211 MB → **1.3 GB** at the same sizes). So the nodal path is **substation-scale (hundreds of buses), not whole-network** — a documented ceiling with a known, bounded fix (a sparse Y-bus/Jacobian). **Concurrency** now has a harness too (`powerflow/concurrency_db_test.go`): correctness under simultaneous load is **asserted** (320 concurrent fault runs must all match the baseline; run under `-race` it is a data-race probe over the DB-backed path), and the throughput-vs-concurrency saturation curve is measured. **First measured result (seeded Docker PG): throughput is flat at ~5 req/s from concurrency 1→64 with zero errors — the single primary saturates at low concurrency (the scaling guide's prediction), not the app pool.** A single fault run on the tiny network is ~165 ms, exposing a **query-chatty N+1 pattern** (one query per node in `buildTree`/resolve) as the lever: batch the per-node resolves to cut latency and primary load, then scale horizontally. ⚠️ Still open: the throughput numbers are hardware/pool dependent (read from the run); the `-race` probe needs cgo (Linux CI); and a **DB-backed load-flow-at-scale** needs a large-feeder seed generator.
- ⚠️ **Arc-flash (IEEE 1584) — model validated, study still Experimental.** The IEEE 1584-2002 **kernels** (arcing current, incident energy, arc-flash boundary) now match the standard's canonical worked example (§1, `arcflash_test.go`), so the *formula implementation* is no longer unverified. The **study** stays Experimental: its governing input is the arc duration (clearing time), which it takes from the protection model and which real arc behaviour, device condition and field geometry make uncertain; conservative defaults are used where an input is unknown; and a result is **never** a basis for PPE selection or an energised-work permit. The number is only as good as the clearing time and geometry behind it.
- **IEEE 1366 major-event-day exclusion — implemented** (see **Reliability & Indices**): the reliability report now returns MED-normalised SAIDI/SAIFI beside the raw figures, via the 2.5β threshold `T_MED = exp(α + 2.5β)` over the window's daily SAIDI (`oms/med.go`, the 2.5β kernel unit-tested in `oms/med_test.go`). ⚠️ Two documented simplifications remain: the threshold uses the **report window** rather than the strict five-year reference period, and a multi-day outage is attributed to its **start day** rather than clipped per calendar day.

---

## 5. Related

- **Engineering Studies** — the per-study Validated / Derived / Experimental tiers and the physics behind each number.
- **Switching & Protection** — the fault/coordination worked examples (and their `c`-factor notes).
- **Reliability & Indices** — the SAIDI/SAIFI/CAIDI/ENS methodology and its own honest limits.
