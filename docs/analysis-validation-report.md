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

## 2. What is validated against a hand calculation (composition, not an external standard)

These pin that the *assembled* passes compose correctly — a pile of correct formulas wired together wrong still gives wrong answers — against an independent by-hand calculation, over small in-memory networks (no database):

| Engine | What | Test |
|---|---|---|
| Load-flow sweep | Backward demand accumulation, forward voltage, source impedance, transformer rebasing, branching | `sweep_integration_test.go` |
| Fault current | Series-impedance accumulation + transformer referral, √3 basis, SLG zero-sequence, generator infeed round-trip, transformer-as-source | `fault_integration_test.go` |
| Unbalanced load-flow | (Phase-55 engine kernels) | `unbalanced_integration_test.go` |
| Load forecast | Scenario projection kernels | `forecast_test.go` |
| Connectivity | Phase intersection, audit | `connectivity/phase_test.go`, `audit_test.go` |
| Export | Shapefile/CIM shaping | `export/export_test.go`, `cim_test.go` |

## 3. Conformance notes

- **IEC 60909 voltage factor `c`.** The fault-current endpoint, SLG, arc-flash (via `runFaultCurrent`) and coordination all apply `c` — default `c_max` (1.10 above 1 kV, 1.05 at/below), per-request overridable, reported per node/response. Figures quoted in guides predating this change are the `c = 1.0` case; pass `voltage_factor_c: 1.0` to reproduce them.
- **Balanced sweep is planning-grade.** Single-phase, linearised (nominal-voltage drop denominator), so it is validated against a **balanced** reference — *not* the IEEE unbalanced feeders, which belong to the Phase-55 unbalanced engine (§4).

---

## 4. What is NOT yet validated (the honest gaps)

Trust these only to the stated extent, and treat the following as open:

- ⚠️ **Unbalanced load-flow vs IEEE 13/34/123.** The unbalanced engine has kernel tests but **no validation against the published IEEE per-phase solutions** — the industry reference for an unbalanced distribution solver. Until it exists, unbalanced results are "composes correctly", not "matches the standard."
- ⚠️ **End-to-end from the database.** Every test above drives the **in-memory kernels**; the full `runFaultCurrent` / `runCoordination` / `runSweep` path (resolving a real network, loading TCC curves from `metadata.protection_curves`, applying barriers) is **not** covered by an automated test — it needs the DB-backed CI harness.
- ⚠️ **Coordination above `gradePairs`.** The grading *verdict* is tested; the *device evaluation* (reading each operate time off a loaded TCC curve, the through-current referral across transformers) is DB-coupled and untested.
- ⚠️ **Meshed / nodal (Y-bus Newton) load-flow.** No validation against an independent reference solution.
- ⚠️ **Scale & concurrency.** Benchmarked only on toy networks; behaviour at real substation/feeder counts and under concurrent load is unproven (see the scale notes).
- ⚠️ **Arc-flash (IEEE 1584) is Experimental.** Derives from the clearing time; indicative only, **never** a basis for PPE selection or energised-work permits.
- ⚠️ **No IEEE 1366 major-event-day exclusion** in the reliability indices (see **Reliability & Indices**) — the SAIDI/SAIFI figures are raw, not MED-normalised.

---

## 5. Related

- **Engineering Studies** — the per-study Validated / Derived / Experimental tiers and the physics behind each number.
- **Switching & Protection** — the fault/coordination worked examples (and their `c`-factor notes).
- **Reliability & Indices** — the SAIDI/SAIFI/CAIDI/ENS methodology and its own honest limits.
