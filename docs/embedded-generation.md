# Embedded generation (distributed generation / DER)

Status: **Tier A BUILT (static, verification owed)** — inventoried asset, PQ
injection into load flow, **and fault-current infeed**. Tier B proposed.

This covers *individual generation into the network* — diesel plants, solar PV,
wind, small hydro, batteries, gas CHP — as first-class inventoried assets, not
just as the grid infeed or an analysis knob. It participates in load flow (as a
fixed negative-demand injection) and in the three-phase fault study (as a second
fault source).

## Before this: two indirect notions of generation, no generator asset

1. **The grid SOURCE is an instance, not a class.** A load flow starts from a
   slack `DIST_XFMR` (`GRID-SRC` / `SUB-SOURCE`) carrying `source_r_ohm` /
   `source_x_ohm` — a Thévenin grid-infeed stand-in. Supply-path finds it
   topologically (`api/internal/trace/supply.go`).
2. **DER existed only as an analysis knob.** The Phase 50 hosting-capacity study
   (`powerflow/hosting.go`) models a *candidate* generator as negative demand
   injected at a node to answer "how much *could* this feeder host" — never
   stored or inventoried.

`functional_role_backfill.sql` explicitly reserved the `'source'` electrical role
for "a future generation / grid-infeed class." Tier A is that class.

## Tier A — inventoried generation + fixed PQ injection (the 80 % case)

A `GENERATOR` feature class (`seed/generator_example.sql`):

- **`generation_type` subtypes**: `diesel`, `solar_pv`, `wind`, `hydro`,
  `battery`, `gas_chp`.
- **`functional_role = 'source'`** (stamped in `functional_role_backfill.sql`).
- **Dispatch fields** (all nullable, graceful-degradation contract): `rated_kw`
  (nameplate), `dispatch_kw` (optional actual-output override), `power_factor`.
- **Fault-contribution fields** (nullable): `fault_contribution_ka`,
  `subtransient_reactance_pct` — see [Fault-current infeed](#fault-current-infeed-tier-a).
- **One terminal (T1)**, a network leaf. Three points of common coupling:
  `LV_CONDUCTOR`, `DIST_XFMR` T2 (LV bus), or `MV_CONDUCTOR` → `GENERATOR` T1.

**Engine:** `resolveGeneration` (`powerflow/resolve.go`) reads the injection
magnitude — `dispatch_kw` when present (authoritative even at 0 = curtailed),
else `rated_kw`; Q = P·tan(acos(pf)), same rule loads use. `resolveNodeStates`
(`powerflow/sweep.go`) nets it in as `ownKW -= genKW` (negative demand). The
backward sweep carries the injection up the tree as reduced upstream flow; the
forward sweep reads it as a local voltage rise. This is the exact machinery
`hosting.go` already used — now driven by the asset's nameplate/dispatch instead
of a bisection search. A node carrying both `demand_kw` and a generation field
nets them (behind-the-meter PV under a load).

**Validation fixture** (`seed/generator_network_example.sql`, task
`seed-generator-network`): a radial LV feeder with a 50 kW load and a `GEN-1`
solar-PV generator (rated 40 kW, dispatch 30 kW) on the same LV bus.
Hand-checkable: `dispatch_kw = 30` overrides the 40 kW nameplate, so upstream flow
is 50 − 30 = **20 kW** (TX loading ~4 %, not 10 %) and the `GEN-1` node reports
cumulative **~ −30 kW**. Clearing `dispatch_kw` and re-running jumps the injection
to the 40 kW nameplate (upstream flow → 10 kW) — proving the precedence.

## Fault-current infeed (Tier A)

A generator is a **second fault source**, so the fault-current study
(`POST /analysis/fault-current`) now adds its infeed to the bolted **three-phase**
fault at each node by **superposition** — previously it modelled zero generator
contribution (grid Thévenin infeed only). The engine
(`api/internal/powerflow/genfault.go`):

- Resolves each `GENERATOR`'s internal (reactive) fault impedance in **ohms** on
  its own terminal base, on the same `V²/S` basis as the source Thévenin impedance
  and transformer `%Z`, so the shared `I = V/Z` (line-to-line) convention makes
  grid and generator currents combine coherently.
- For each fault node `f`, sums over every generator `g`: the loop is `g`'s
  internal impedance in series with the radial path `g → f`, read off a common
  (root) base as `Z(g) + Z(f) − 2·Z(lca)` (`accumulateRootBaseImpedance` + an LCA
  walk — the per-node `pathImpedance` is rebased per node and can't be subtracted
  across transformers). The contribution is `V_g/|Z_loop|`, referred to the
  fault's own side.
- `fault.go` reports `fault_current_amps` as the **TOTAL** (grid + generators) and
  surfaces the split in `grid_contribution_amps` / `generator_contribution_amps`
  **only where a generator actually feeds** — a network with no generation reads
  exactly as before.

**Characterisation precedence** (graceful degradation, mirrors
`dispatch_kw → rated_kw`), in `resolveGeneratorFaultSpec` (`resolve.go`):

1. `fault_contribution_ka` — explicit prospective kA, authoritative (any subtype;
   the natural input for a current-limited inverter). `Z = V/(√3·kA·1000)`.
2. `subtransient_reactance_pct` — a **synchronous** machine's X″d, percent on its
   rated-kVA base. `Z = (X″d/100)·V²/S`.
3. **Inverter subtypes** (`solar_pv` / `battery`) with neither set — default
   `~1.2×` rated current (power-electronics limit). `Z = V²/(1.2·S)`.
4. A **synchronous** subtype (`diesel` / `hydro` / `gas_chp` / `wind`) with neither
   set gets **no default** — assuming an X″d would badly misstate its far larger
   infeed — so it contributes nothing until surveyed.

The rated current is derived from the **nameplate** `rated_kw` (not `dispatch_kw`):
a curtailed-but-connected machine feeds the fault at full sub-transient current.
`operational_state = ISOLATED` (a trace barrier excluding it from the tree) is what
zeroes its infeed.

**Scope / limits (Tier A):** three-phase symmetrical fault only. Generator infeed
into an **SLG** (single-line-to-ground) fault needs the machine grounding and its
interface-transformer zero-sequence connection — data not yet collected — so the
SLG figures (`groundfault.go`) stay **grid-only**. The path-superposition is exact
when the sources feed the fault through disjoint impedance (an embedded generator
downstream of an upstream fault — the switchgear-duty case) and a slightly
conservative over-statement otherwise (the safe direction for a rating check).

**Validation:** the same `seed/generator_network_example.sql` fixture — `GEN-1`
(solar PV, nameplate 40 kW) carries no explicit fault field, so it takes the
inverter fallback: `Z_int = 400²/(1.2·40000) = 3.333 Ω`, and a fault at the LV bus
reports `generator_contribution_amps ≈ 120 A` (exactly 1.2× the line-to-line-
referred rated current) on top of the grid infeed. Nameplate-driven: the figure
holds even though `GEN-1` dispatches only 30 kW.

## Tier B — grid-forming generation (proposed, not built)

Grid-*forming* generation — a backup diesel islanding a feeder, a microgrid, PV
voltage regulation — is a **PV bus or a second slack**, which the radial
forward-backward sweep structurally cannot do (one slack, one source→node path).
That folds into the meshed **Tier 2** nodal solver (multi-slack Y-bus / Newton;
see `docs/phase-51-meshed-lv-powerflow.md`), not the radial ladder. A leading
(VAR-absorbing) inverter mode is likewise Tier B; Tier A injects P and Q at the
stated power factor.

## Owed verification (shell is EPERM-blocked here — reviewed statically)

- `cd api && go build ./...` (resolve.go, sweep.go, genfault.go, fault.go,
  types.go changes).
- Apply `seed/generator_example.sql` (now adds `fault_contribution_ka` +
  `subtransient_reactance_pct`), `task compile CODE=GENERATOR`, apply
  `seed/functional_role_backfill.sql`, apply `seed/generator_network_example.sql`.
- `POST /analysis/load-flow` from `GEN-SRC`; confirm TX-G through-power ~20 kW and
  the `GEN-1` node cumulative ~ −30 kW against the hand numbers above.
- `POST /analysis/fault-current` from `GEN-SRC`; confirm the LV-bus / `GEN-1` node
  reports `generator_contribution_amps ≈ 120 A` and `fault_current_amps` = grid +
  ~120 A, with `grid_contribution_amps` unchanged from the no-generation figure.
- A frontend surface (a generator shows on the map / feature panel; a load-flow or
  fault result labels the injecting node / the grid-vs-generator split) is not yet
  built.
