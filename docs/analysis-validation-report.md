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
| **Unbalanced load-flow (Tier 2)** | Carson phase-impedance **mutual** (off-diagonal) terms; coupled per-phase `ΔV = Z·I` drop, single section and 2-section composition | **IEEE 13-node / Kersting** published Config 601 matrix (`Z601`) | 0.01 Ω/mile (mutual); exact (drop) | mutual terms match to 4 dp; coupled drop + 2-section composition exact; coupling provably applied (differs from diagonal-only) | `unbalanced_integration_test.go`, `unbalanced_composition_test.go` |

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

- ⚠️ **Full IEEE-13 node-voltage solution.** The line-impedance build (Carson **mutual** terms vs the published `Z601`) and the coupled per-phase drop **are** validated (§1). What is **not** attempted is a full published-node-voltage match for the whole IEEE 13 feeder — it includes a voltage regulator, an in-line transformer, shunt capacitors and line charging that this planning engine documents it does **not** model (`unbalanced_coupled.go` HONEST LIMITS). Separately, the diagonal **self**-impedance term is logged-not-asserted pending an authoritative Carson cross-check (e.g. OpenDSS): the published 601 self-term and the engine's textbook modified-Carson + Kron self-term cannot both be a single Kron reduction of the same primitive.
- **End-to-end from the database — partly closed.** `runFaultCurrent` and `runCoordination` now have an **end-to-end integration test** (`integration_db_test.go`) that resolves the seeded network, builds the tree, computes the fault and loads the TCC curves — asserting the fault level, the `c`-factor applied live, and the coordination verdict. It skips without a database and runs in the **CI bootstrap job** (seeded Postgres). ⚠️ Still open: `runSweep` (load flow) has no end-to-end DB test yet.
- **Coordination above `gradePairs` — covered.** The device evaluation (reading each operate time off a loaded TCC curve, the through-current referral) is now exercised by the end-to-end coordination integration test against the seeded network (above), not just the `gradePairs` verdict kernel.
- ⚠️ **Meshed / nodal (Y-bus Newton) load-flow.** No validation against an independent reference solution.
- ⚠️ **Scale & concurrency.** Benchmarked only on toy networks; behaviour at real substation/feeder counts and under concurrent load is unproven (see the scale notes).
- ⚠️ **Arc-flash (IEEE 1584) is Experimental.** Derives from the clearing time; indicative only, **never** a basis for PPE selection or energised-work permits.
- ⚠️ **No IEEE 1366 major-event-day exclusion** in the reliability indices (see **Reliability & Indices**) — the SAIDI/SAIFI figures are raw, not MED-normalised.

---

## 5. Related

- **Engineering Studies** — the per-study Validated / Derived / Experimental tiers and the physics behind each number.
- **Switching & Protection** — the fault/coordination worked examples (and their `c`-factor notes).
- **Reliability & Indices** — the SAIDI/SAIFI/CAIDI/ENS methodology and its own honest limits.
