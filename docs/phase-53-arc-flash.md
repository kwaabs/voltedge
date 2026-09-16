# Phase 53 — Arc-flash hazard analysis (IEEE 1584)

Status: **Proposed (design).** From the distribution-utility enhancement backlog
(`memory/enhancement-backlog-distribution-utility.md`, second tier). This is the
natural next step after the fault/coordination and power-flow work: arc-flash is
almost entirely a *derived* analysis that sits on top of the two engines already
built — the 3-phase fault-current engine (bolted fault current + per-node voltage)
and the protection-coordination engine (protective-device clearing time). The new
work is the IEEE 1584 arcing-current / incident-energy math and the equipment
inputs that math needs; the electrical substrate is done.

Regulator- and safety-facing: incident energy (cal/cm²), arc-flash boundary, and
an NFPA 70E PPE category per energized location are what drive equipment labels
and work permits.

---

## The wall / why it's cheap

An arc-flash study answers, at each energized point: *if an arcing fault happened
here, how much thermal energy (cal/cm²) would a worker at the working distance
receive before the upstream device clears it, and how far away is that still
dangerous?* IEEE 1584 turns four inputs into that answer:

1. **Bolted 3-phase fault current** `Ibf` at the point, and the **system voltage**.
2. An **arcing current** `Ia < Ibf` (an arc has finite impedance), from an
   empirical fit over voltage, `Ibf`, electrode gap, and whether the equipment is
   in a box or open air.
3. The **clearing time** `t` — how long the *upstream protective device* takes to
   operate *at the arcing current* (not the bolted current — this is the subtle
   part: a lower arcing current can sit further out on the TCC and clear *slower*,
   raising the energy).
4. Equipment geometry: **electrode configuration**, **gap**, **enclosure**, and
   **working distance**.

Inputs (1) and (3) are exactly what we already compute:

| IEEE 1584 needs | Already have | Source |
| --- | --- | --- |
| Bolted 3-phase fault current per node | `faultNodeResult.FaultCurrentAmps` (amps) | `runFaultCurrent` (`fault.go:31`) |
| System / per-node voltage | `faultNodeResult.NominalBaseV` (volts) | same, `pathImpedance.baseV` |
| Upstream clearing device | primary device on `pathToFault` | `runCoordination` (`coordination.go:311`), device selection `coordination.go:364` |
| Operate time at a current | `interpLogLog(pts, x)` log-log TCC read | `curves.go:102`, driven via `evalBreaker`/`gradeAmpereCurve` |

So arc-flash = call the fault engine for `Ibf`+`V`, compute `Ia`, ask the
coordination engine for the clearing device's operate time **at `Ia`**, then apply
the IEEE 1584 incident-energy and boundary equations. Only the equipment-geometry
inputs (2/4) are genuinely new — none exist in the schema today (surveyed: no
`electrode`, `gap`, `enclosure`, `working_distance`, or `arc` fields anywhere).

---

## Reuse map (what static recon confirmed)

- **Fault current**: `runFaultCurrent(ctx, db, req)` → `faultCurrentResponse.Nodes`
  each carry `FeatureClass`/`FeatureID`, `FaultCurrentAmps *float64` (bolted 3ph,
  **amps**, nil when `|Z|==0`), `NominalBaseV`, and the R/X/|Z| decomposition.
  Request is `faultCurrentRequest{Start, NominalVoltageV, FaultType, FaultImpedanceOhm}`
  (`types.go:129`). Arc-flash calls it with `fault_type="three_phase"`, `Zf=0`.
- **Clearing time**: the coordination engine already walks `pathToFault` (fault-first,
  `coordination.go:395`) and marks the first device with a resolved operate time as
  `Role="primary"` (`coordination.go:364`). The per-device operate time lands in
  `coordinationDevice.OperateTimeS`. The numeric heart is `interpLogLog` (log-log
  interpolation of tabulated TCC points, `curves.go:102`); relay branch multiplies
  `time_dial` (`coordination.go:481`), fuse/recloser grade on the `total_clear` band
  (`gradeAmpereCurve`, `coordination.go:581`). Current is referred to each device's
  own voltage base (`coordination.go:317`). **Arc-flash drives this same path with
  the arcing current** instead of the bolted current to get `t`.
- **Endpoint pattern**: `Routes` (`handler.go:65`) — one `r.Post("/arc-flash", arcFlashHandler(db))`
  line; handler is the standard decode → `runArcFlash` → `writeJSON` / `writeError`
  copy (mirror `faultCurrentHandler`, `handler.go:97`). Package is read-only.
- **Frontend**: the mutually-exclusive analysis pattern — a store, a `useFloatingPanel`
  panel, an arm function that clears every other analysis store, a dropdown item, an
  optional red map highlight (the ~13 existing analyses, e.g. nodal/fault).

---

## New work

### A. IEEE 1584 engine (`api/internal/powerflow/arcflash.go`)

`runArcFlash(ctx, db, req)` — internally runs the fault sweep once, then per node
with a resolved `Ibf`:

1. **Arcing current** `Ia` from `Ibf` (kA), voltage `V` (kV), gap `G` (mm), and
   open-vs-box. IEEE 1584-2002 form (see *Model choice* below): for `V < 1 kV`
   `lg Ia = K + 0.662 lg Ibf + 0.0966 V + 0.000526 G + 0.5588 V lg Ibf − 0.00304 G lg Ibf`
   (`K = −0.153` open / `−0.097` box); for `V ≥ 1 kV` `lg Ia = 0.00402 + 0.983 lg Ibf`.
2. **Clearing time** `t`: identify the upstream primary clearing device (reuse the
   coordination selection) and evaluate its operate time **at `Ia`** (referred to
   the device base). Apply a configurable **maximum clearing time cap** (IEEE 1584
   guidance: default 2 s where a worker cannot self-evacuate) when the device is
   slow or unresolved.
3. **Arcing-current sensitivity**: also evaluate at `0.85·Ia` (the standard
   "reduced arcing current" check, because a lower `Ia` may clear slower) and take
   the **worse** incident energy of the two.
4. **Incident energy** `E` (cal/cm²) at the working distance `D` (mm):
   `lg En = K1 + K2 + 1.081 lg Ia + 0.0011 G`, then
   `E = 4.184 · Cf · En · (t/0.2) · (610^x / D^x)`, with `K1 = −0.792` open /`−0.555`
   box, `K2 = 0` ungrounded / `−0.113` grounded, `Cf = 1.0` (>1 kV) / `1.5` (≤1 kV),
   and the distance exponent `x` from the equipment-class table.
5. **Arc-flash boundary** `AFB`: the distance at which `E = 1.2 cal/cm²`.
6. **PPE category** (NFPA 70E): map `E` → CAT 1 (≤4) / 2 (≤8) / 3 (≤25) / 4 (≤40) /
   "dangerous — no live work" (>40); below 1.2 cal/cm² = minimal hazard.

Per-node result adds `bolted_fault_current_amps`, `arcing_current_amps`,
`clearing_time_s`, `clearing_device` (ref + role), `incident_energy_cal_cm2`,
`arc_flash_boundary_mm`, `ppe_category`, plus which inputs were assumed vs
resolved (honesty flags, like the fault engine's `material_resolved`).

### B. Equipment inputs — the "Both" pattern (`seed` + declared fields)

None of the IEEE 1584 geometry inputs exist. Add them as **declared, nullable
fields** on the energized equipment classes (switchgear, panels, transformers,
cabinets), plus **request overrides**, plus **IEEE-typical defaults** keyed by
voltage band + equipment type so a study runs on sparse data (this is exactly the
asset-field + request-override + default idiom used for PV in Phase 52 S3):

- `electrode_configuration` (coded: VCB / VCBB / HCB / VOA / HOA, or open/box).
- `conductor_gap_mm` (electrode gap; typical: LV panel 25, 5 kV gear 104, 15 kV gear 152).
- `enclosure_type` / box size (drives open-vs-box `K` and `x`).
- `working_distance_mm` (typical: LV 455, MV switchgear 610).
- distance exponent `x` derived from equipment type (table constant, not a field).

Defaults live in the engine as an IEEE 1584 typical-values table; a field or
request value overrides the default; the result flags every assumed input.

### C. Frontend (`arc-flash-store` + `arc-flash-panel` + dropdown + highlight)

Standard mutually-exclusive analysis: prompt = nominal voltage (+ optional max
clearing-time cap and working-distance override); result = per-node table with
incident energy, AFB, PPE **category badge** (colour-graded), clearing device, and
the assumed-input flags; red map highlight of the hazard nodes. Arm function clears
all other analysis stores; item added to the feature-panel analysis dropdown.

---

## Slice plan

- **S1 — engine core + endpoint + validation seed.** `arcflash.go`, `runArcFlash`,
  `POST /analysis/arc-flash`, IEEE 1584-2002 math, typical-values default table,
  clearing time via the coordination path at `Ia` (+ 0.85·`Ia` + cap). Validation
  seed with a **hand-computed** incident energy / AFB / PPE for one MV and one LV
  node (the acceptance test — arc-flash, like the solvers, is exactly what static
  review can't vouch for). Reuses an existing fault seed's topology where possible.
- **S2 — declared equipment fields. BUILT 2026-09-13.** Four nullable declared
  fields — `arc_electrode_configuration` (coded: switchgear / mcc_panelboard /
  cable / open_air), `arc_conductor_gap_mm`, `arc_working_distance_mm`,
  `arc_system_grounded` — on the energized work-location classes (DIST_XFMR,
  POWER_XFMR, MV_BUSBAR, MV_SWITCHGEAR_PANEL) via
  `seed/arc_flash_geometry_fields.sql`. `arcflash.go` reads them off the faulted
  node's own attributes (`arcGeometryFor(v, attrs, requestWD)`), resolving with
  the "Both" precedence **declared per-feature > request override > IEEE typical
  default**; `typicalGeometryFor`/`normalizeElectrodeConfig` map the electrode
  class → box + distance exponent x + typical gap/WD (the switchgear branch
  reproduces S1 exactly, so unpopulated networks are unchanged). Honesty:
  `geometry_assumed` now reflects whether any numeric fell back to a typical, and
  the result carries the resolved `electrode_configuration`. Owed: go build +
  live psql apply/compile + an arc-flash re-run showing a declared value flips
  `geometry_assumed` false and changes the incident energy.
- **S3 — frontend.** Store + panel + dropdown item + PPE-category highlight.
- **S4 (later) — IEEE 1584-2018 model.** The current standard supersedes 2002 with
  a five-electrode-configuration coefficient model interpolated across 600 V /
  2700 V / 14300 V, an enclosure-size correction, and its own arcing-current
  variation. Add it as an opt-in `standard: "ieee-1584-2018"` selector beside the
  2002 default — the same "keep the proven default, opt into the new model" idiom
  as the Phase 52 Newton `solver` field. Larger (coefficient tables), so a separate
  slice.

## Decisions to confirm

1. **Model: IEEE 1584-2002 first, 2018 as a pluggable S4?** *(recommended)* 2002 is
   dramatically simpler, hand-verifiable, and still widely used; it de-risks the
   whole fault→arcing→clearing→energy→boundary→PPE pipeline. 2018 is the current
   compliance standard but needs large coefficient tables and 3-voltage
   interpolation — better added once the pipeline is proven. *Alternative: go
   straight to 2018* (compliance-accurate from day one, bigger first slice, harder
   to hand-check).
2. **Clearing time source**: reuse the coordination engine's primary-device
   selection and evaluate its TCC at `Ia` (with a configurable max-time cap and the
   0.85·`Ia` sensitivity). Where no protective device resolves upstream, report the
   node with the cap time and an "assumed" flag rather than skipping it.
3. **Sparse-data strategy**: ship IEEE typical gaps / working distances / distance
   exponents keyed by voltage + equipment type so the study runs before any of the
   new fields are populated, with every assumption flagged in the result.

## Not in this phase

DC arc-flash; arc-flash from single-line-to-ground faults (IEEE 1584 is a 3-phase
model); label printing / export; time-based re-study scheduling. This is the
balanced 3-phase incident-energy study reusing the existing fault + coordination
engines.
