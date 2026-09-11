# Phase 28 — MV↔LV load flow across the transformer

## Why

Phases 27 (MV) and 9 (LV) each modeled a complete network, but they meet at a
single object — `DIST_XFMR` — and that boundary is **electrically implicit**.
A full forward-backward-sweep load-flow engine already exists
([api/internal/powerflow](../api/internal/powerflow), Kersting's method, plus a
symmetrical fault-current engine), and a trace already walks MV → transformer →
LV as one connected graph. But the transformer is a **zero-impedance
pass-through with a single nominal voltage for the whole run**
([powerflow/types.go:55-64](../api/internal/powerflow/types.go#L55-L64)): the
sweep never rebases voltage across the turns ratio, and the transformer's own
series impedance is invisible. So a study that starts on the MV feeder and
crosses into LV reports LV nodes at MV voltage — wrong by the turns ratio.

Two data gaps compound it:

* **`MV_CONDUCTOR` carries no material-linked field** at all (only
  `voltage_class` + `condition_rating`,
  [seed/connectivity_example.sql:57-60](../seed/connectivity_example.sql#L57-L60)).
  `resolveConductorImpedance` scans a class's domain-linked fields for one whose
  value resolves to a `material_spec` — MV has none, so every MV branch drops
  zero volts. (The base CIM seed already notes this gap,
  [seed/cim_mapping_example.sql:113-115](../seed/cim_mapping_example.sql#L113-L115).)
* **The transformer has no secondary voltage, no %Z** — only `primary_voltage`
  (coded), `rated_kva`, and `source_r_ohm`/`source_x_ohm`
  ([seed/load_flow_fields.sql](../seed/load_flow_fields.sql)).

Phase 28 closes all three: model the transformer as a real two-winding,
voltage-transforming node with series impedance, seed MV conductor impedance,
and teach the existing sweep to rebase voltage (and the fault engine to limit
current) across the transformer — so a single study spans MV → transformer → LV
correctly, per unit or in volts, on each side's own base.

## Shape decisions (locked with the user before building)

* **Transformer *in* load flow**, not just data modeling — the engine change is
  the point. (Alternative "metadata only, leave the solver" was rejected.)
* **Ideal turns ratio *plus* series %Z** — the transformer both rebases voltage
  and produces real voltage drop / fault limiting from its nameplate impedance.
  (Alternative "ideal ratio only, zero series Z" was rejected.)
* **Seed MV conductor impedance now** — otherwise the MV side computes flat
  voltage and the whole point (an end-to-end study) is half-inert.

Everything reuses the engine's existing philosophy: new inputs are **nullable
numeric fields the engine reads by name and degrades gracefully when unset**
(exactly how `demand_kw` / `rated_kva` / `source_r_ohm` already behave —
[seed/load_flow_fields.sql:15-19](../seed/load_flow_fields.sql#L15-L19)), and
impedance comes from `metadata.material_specs` linked through
`domain_values.material_spec_id` (migration 0021), no schema change.

## The model

**Turns ratio (basis-independent).** `primary_voltage` is a coded
`VOLTAGE_CLASS` (`11KV`→11000, `33KV`→33000, parsed to volts in the engine);
`secondary_voltage` is a new nominal line-to-line volts field (e.g. 400). The
ratio `secondary/primary` is dimensionless, so it correctly rebases whatever
consistent voltage basis the run supplied (`nominal_voltage_v`) — line-to-line
or phase-to-neutral — because both nominals are quoted on the same basis.

**Series impedance (referred to the secondary).** From nameplate `%Z`
(`impedance_pct`) and `rated_kva`: `Z_base = V_secondary_LL² / S_rated_VA`
(three-phase base), `Z_ohm = %Z/100 · Z_base` (per-phase equivalent referred to
the LV side, consistent with how conductor R/X are per-phase ohms). `Z_ohm` is
split into R and X by `x_r_ratio` (X/R); when `impedance_pct` is unset the
transformer is an **ideal ratio** (zero series Z), and when `x_r_ratio` is unset
it falls back to a documented default X/R (graceful degradation, the same spirit
as `power_factor` defaulting to unity).

**In the sweep.** A per-node **voltage base** propagates down the tree; at a
transformer node (crossed mid-tree, i.e. not the start), the base switches to
`secondary_voltage`, the node's voltage becomes `V_primary · ratio` minus the
transformer's own `ΔV = (P·R + Q·X)/V` on the secondary base, and every LV node
below inherits the LV base. `voltage_drop_pct` is then measured against **each
node's own base**, and each node reports its `nominal_base_v` so a client can
tell MV nodes from LV nodes in one result set. A run that *starts at* the
transformer (the existing LV-feeder study) is unchanged: the start is the source
at the supplied nominal, no ratio applied.

## Slice plan

* **Slice 1 — electrical data readiness** (seeds, no engine change):
  * 1a — `DIST_XFMR` gains `secondary_voltage`, `impedance_pct`, `x_r_ratio`
    (nullable numeric, `electrical` group). `seed/transformer_model_fields.sql`.
  * 1b — MV conductor impedance: new `MV_LINE_SPEC` material-backed domain +
    `metadata.material_specs` rows + a `conductor_spec` field on `MV_CONDUCTOR`,
    plus the MV `ACLineSegment.r/.x` CIM mapping that closes the base seed's
    known gap. `seed/mv_conductor_specs.sql`.
* **Slice 2 — load-flow voltage transformation across `DIST_XFMR`** (engine):
  `resolve.go` transformer model + `sweep.go` per-node voltage base + ratio/%Z
  step + `types.go` `nominal_base_v`.
* **Slice 3 — fault current across the transformer** (engine, reuses the Slice 2
  model): add the transformer's series impedance into the accumulated path Z and
  rebase the pre-fault voltage beyond it, so LV prospective fault current is
  limited by transformer %Z instead of ignoring it.
* **Slice 4 — frontend surfacing** (web `load-flow-store` + results UI): show
  each node's `nominal_base_v` / voltage level and the transformer step, so a
  combined MV+LV result reads correctly. Scope confirmed after Slices 2-3.

Deliberately **out of scope** (noted, not built): a true two-winding
`PowerTransformer` with two `PowerTransformerEnd`s in the CIM export (a CIM
*encoder* change, not a seed — the current export emits `ratedS` per winding but
one voltage); constant-impedance/constant-current load models; time-series /
load-profile demand. These are follow-ups, not part of the locked scope.

---

## Slice 1 — electrical data readiness

### 1a — Transformer model fields (`seed/transformer_model_fields.sql`)

Three nullable numeric `electrical`-group fields on `DIST_XFMR`, no domain, no
subtype rules — the exact pattern of the five load-flow fields already seeded
([seed/load_flow_fields.sql](../seed/load_flow_fields.sql)), so an un-surveyed
transformer stays runnable and simply behaves as an ideal 1:? node until filled:

| field | meaning |
|---|---|
| `secondary_voltage` | nominal LV line-to-line volts (e.g. 400); with `primary_voltage` gives the turns ratio |
| `impedance_pct` | nameplate %Z; drives the series impedance referred to the LV side |
| `x_r_ratio` | X/R of that impedance; splits %Z into R and X |

Recompile `DIST_XFMR` after applying (the compiler adds the three nullable
columns to `gis.dist_xfmr`).

### 1b — MV conductor impedance (`seed/mv_conductor_specs.sql`)

Mirrors the LV cable catalog
([seed/lv_conductors_example.sql:92-122](../seed/lv_conductors_example.sql#L92-L122)):

1. `metadata.material_specs` rows for common MV conductors — bare overhead
   ACSR/AAAC (`category='conductor'`) and MV XLPE cable (`category='cable'`),
   each with `resistance_ohm_per_km` / `reactance_ohm_per_km` / `ampacity_amps`.
2. A new coded `MV_LINE_SPEC` domain whose values are **material-backed**
   (`domain_values.material_spec_id` = the catalog row, value code =
   `material_specs.code` by convention).
3. A `conductor_spec` field on `MV_CONDUCTOR` keyed to `MV_LINE_SPEC` —
   **nullable, no subtype rule** (unlike LV's required `conductor_spec`), so it
   is non-breaking for existing MV conductor features and degrades gracefully
   in the sweep. `resolveConductorImpedance` scans by the domain→spec link, not
   the field name, so it resolves MV impedance automatically once populated.
4. CIM: map `MV_CONDUCTOR.conductor_spec → ACLineSegment.r` / `.x`
   (`transform material_spec`), the same rows LV already has
   ([seed/cim_mapping_example.sql:125-126](../seed/cim_mapping_example.sql#L125-L126)),
   closing that seed's documented MV-impedance gap.

Recompile `MV_CONDUCTOR` after applying.

### Slice 1 — verification owed (Bash blocked here; run locally)

* `task seed-transformer-fields` + `task seed-mv-conductor-specs` apply cleanly
  and are idempotent.
* `task compile CODE=DIST_XFMR` adds `secondary_voltage` / `impedance_pct` /
  `x_r_ratio` (nullable) to `gis.dist_xfmr`; `task compile CODE=MV_CONDUCTOR`
  adds `conductor_spec`.
* `MV_LINE_SPEC` domain values each resolve to a `material_specs` row with r/x/
  ampacity; a load flow over an MV conductor with `conductor_spec` set now
  reports non-zero `voltage_drop_pct` and a `current_amps`/`ampacity_amps` pair.
* A CIM export of an MV conductor emits `ACLineSegment.r`/`.x`.

---

## Slice 2 — load-flow voltage transformation (`api/internal/powerflow`)

`resolve.go` — new `parseVoltageClassCode` (coded `11KV`→11000) and
`resolveTransformerModel(attrs, ratedKVA, hasRating) → transformerModel{ratio,
rOhm, xOhm, secondaryV, ok}`: `ok` only when both primary and secondary nominal
resolve; series R/X only when `impedance_pct` and a positive `rated_kva` are
present, split by `x_r_ratio` (documented default when unset).

`sweep.go` — `nodeState` gains `voltageBase` and the resolved `transformerModel`;
`resolveNodeStates` fills it; `forwardSweep` propagates the base down the tree
and, at a non-start transformer node, sets the node's voltage to
`V_primary·ratio − ΔV_xfmr(secondary base)` and switches the base to the
secondary. `formatResults` measures `voltage_drop_pct` against each node's own
base and emits it as `nominal_base_v`.

`types.go` — `nodeResult.NominalBaseV` (`nominal_base_v`): the voltage base the
node's `voltage_v` / `voltage_drop_pct` are on (the run's nominal for MV nodes,
the transformer secondary for LV nodes).

The start-at-transformer LV study is unchanged (start = source at nominal, ratio
not applied); a non-radial section still fails loudly (`buildTree`); barriers,
demand, and the transformer's own `rated_kva` loading check are untouched.

### Slice 2 — verification owed (Bash blocked here; run locally)

* `go build ./...` / `go vet ./...` clean.
* A load flow **started on the MV feeder** that crosses a `DIST_XFMR` with
  `secondary_voltage=400` reports MV nodes near the MV nominal and LV nodes near
  400 V, each with `nominal_base_v` set to its side's base and a sane
  `voltage_drop_pct` (measured against its own base, not the MV nominal).
* Setting `impedance_pct`/`x_r_ratio` visibly increases the LV-side drop vs an
  ideal-ratio transformer; clearing them recovers the ideal-ratio result.
* A load flow **started at the transformer** (existing LV study) returns the
  same numbers as before this slice (no regression).

---

## Slice 3 — fault current across the transformer (`api/internal/powerflow`)

The symmetrical fault engine ([fault.go](../api/internal/powerflow/fault.go))
accumulates series impedance *forward* (source → leaves) and reports
`I = V / |Z_total|` at each node. Before this slice it summed only conductor
branch impedance on a single voltage base — so an LV fault was measured against
the MV nominal and, worse, ignored the transformer %Z that actually **dominates**
an LV fault level. Slice 3 reuses the Slice 2 `transformerModel` (no new
resolution) and teaches the accumulation to cross the transformer:

`pathImpedance` gains a `baseV` (the voltage base the accumulated R/X are
referred to), seeded with the run's nominal at the start node. At a
**transformer node** crossed mid-tree, `accumulateSeriesImpedance`:

1. **Refers the upstream impedance onto the secondary base** — ohms scale by
   `ratio²` from the primary base to the secondary base (`Z_sec = Z_pri ·
   (V_sec/V_pri)²`), the standard per-unit referral.
2. **Adds the transformer's own series impedance** (`xfmr.rOhm`/`xOhm`, already
   referred to the secondary by `resolveTransformerModel`).
3. **Rebases `baseV` to the secondary**, so every node below reports
   `I = V_secondary / |Z_total(referred to secondary)|` — the correct LV
   prospective fault current, limited by transformer %Z.

`formatFaultResults` uses each node's own `baseV` instead of the single run
nominal and emits it as `nominal_base_v` (new field on `faultNodeResult`, mirrors
`nodeResult`). `material_resolved` now also flips **false** when a transformer on
the path contributed zero series impedance (no nameplate %Z / rating) — the
reported LV current is then an optimistic upper bound, exactly as it already is
for an unresolved conductor branch, since a missing series impedance can only
make the true `|Z|` larger and the true current smaller.

A fault study **started at the transformer** or **entirely on one side** is
unchanged (no transformer crossed mid-tree → `baseV` stays the run nominal, the
`ratio²` referral never fires).

### Slice 3 — verification owed (Bash blocked here; run locally)

* `go build ./...` / `go vet ./...` clean.
* A fault study **started on the MV feeder** crossing a `DIST_XFMR` reports LV
  nodes with `nominal_base_v` = the secondary voltage and a **finite, much
  lower** `fault_current_amps` than an ideal (no-%Z) transformer would give;
  MV-side nodes are unchanged from before this slice.
* Setting `impedance_pct` on the transformer visibly **reduces** the LV
  prospective fault current (more series Z); clearing it flips
  `material_resolved` false on LV nodes (optimistic upper bound).
* A fault study **entirely on the MV side** (or started at the transformer)
  returns the same numbers as before this slice (no regression).

---

## Slice 4 — frontend surfacing (`web/src`)

Scope confirmed with the user: **both result panels + the transformer input
fields**.

**The transformer inputs need no code.** The create/edit forms are
metadata-driven — [feature-edit-form.tsx](../web/src/components/map/feature-edit-form.tsx#L88)
renders `schema.fields.filter((f) => f.visible_on_update)`, and
`visible_on_create` / `visible_on_update` / `editable_after_publish` all default
`true` ([migrations/0008](../migrations/0008_create_subtypes_and_fields.sql#L31-L33)).
So `secondary_voltage` / `impedance_pct` / `x_r_ratio` on `DIST_XFMR` (and
`conductor_spec` on `MV_CONDUCTOR`) appear as editable fields automatically once
Slice 1's seeds are applied and the classes recompiled — nothing to build, the
same way `rated_kva` / `source_r_ohm` already surface.

**The result panels get a `Level` column** — the new `nominal_base_v` per node:

* Stores mirror the engine's new field:
  [`LoadFlowNodeResult.nominal_base_v`](../web/src/stores/load-flow-store.ts) and
  [`FaultCurrentNodeResult.nominal_base_v`](../web/src/stores/fault-current-store.ts).
* Both panels ([load-flow-panel.tsx](../web/src/components/map/load-flow-panel.tsx),
  [fault-current-panel.tsx](../web/src/components/map/fault-current-panel.tsx)) add
  a compact **Level** column (`formatVoltageLevel` → "11 kV" / "400 V") so MV
  nodes and the LV nodes below a `DIST_XFMR` read apart at a glance, and the
  load-flow `voltage_drop_pct` (now measured against each node's own base) is
  unambiguous. Panels widened to `36rem`, barrier-row `colSpan` bumped for the
  extra column; `—` shown for a node whose base is unset (an unreached node).

The transformer "step" itself isn't drawn as a distinct row — the `DIST_XFMR`
already appears as its own node in both tables, and its `Level` flipping from the
MV level to the LV level *is* the visible step.

### Slice 4 — verification owed (Bash blocked here; run locally)

* `web` typechecks/builds (`tsc` / vite build) clean.
* A combined MV→LV load flow shows the `DIST_XFMR` and everything below it at the
  LV `Level` (e.g. 400 V) and the MV nodes above at the MV level (e.g. 11 kV),
  each drop % against its own base.
* Editing a `DIST_XFMR` shows `secondary_voltage` / `impedance_pct` / `x_r_ratio`
  in the form (after Slice 1 seeds + recompile); editing an `MV_CONDUCTOR` shows
  `conductor_spec`.

---

## Status

* **Slice 1 (data readiness)** — code-complete (`seed/transformer_model_fields.sql`,
  `seed/mv_conductor_specs.sql`, Taskfile tasks). Verification owed (psql apply +
  recompile both classes).
* **Slice 2 (load-flow voltage transformation)** — code-complete (`resolve.go`,
  `sweep.go`, `types.go`). Verification owed (`go build`/`vet` + load-flow
  re-checks).
* **Slice 3 (fault current across the transformer)** — code-complete
  (`fault.go`, `types.go`). Verification owed (`go build`/`vet` + fault re-checks).
* **Slice 4 (frontend surfacing)** — code-complete (both result panels get a
  `Level`/`nominal_base_v` column; transformer input fields surface automatically
  via the metadata-driven form, no code). Verification owed (`web` build + the
  combined-study UI re-checks).

**Phase 28 code-complete** (all four slices); verification owed throughout
(psql/recompile/`go build`/`web build` — Bash is EPERM'd in this environment).
