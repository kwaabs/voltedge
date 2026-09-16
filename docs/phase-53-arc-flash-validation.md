# Phase 53 S1–S2 — arc-flash engine validation (IEEE 1584-2002)

Arc-flash, like the power-flow solvers, is exactly the kind of numeric work static
code review cannot vouch for — a transposed coefficient or a units slip produces a
plausible-looking but wrong cal/cm². So S1 ships two acceptance checks: a
**self-contained unit test** that pins the pure IEEE 1584 math against the
standard's own published worked example, and an **integration recipe** that runs
the whole `Ibf → Ia → clearing time → energy → boundary → PPE` pipeline against an
existing seed. Both are owed a live run (`api/internal/powerflow` can't be built
here — Bash is EPERM-blocked; see `memory/bash-tool-blocked.md`).

The engine is `api/internal/powerflow/arcflash.go`; the endpoint is
`POST /analysis/arc-flash`.

---

## 1. Unit test — the standard's worked example (hard, self-contained)

IEEE 1584-2002 works a low-voltage example that every reference reproduces. It
exercises `arcingCurrentAmps` and `incidentEnergyAndBoundary` in isolation, with no
network involved. Inputs:

| Input | Value |
| --- | --- |
| System voltage `V` | 0.48 kV (Cf = 1.5) |
| Bolted fault current `Ibf` | 20 kA |
| Electrode configuration | box (enclosed) → K = −0.097, K1 = −0.555 |
| Grounding | grounded → K2 = −0.113 |
| Electrode gap `G` | 32 mm |
| Distance exponent `x` | 1.473 |
| Working distance `D` | 457 mm |
| Clearing time `t` | 0.2 s |

Arcing current (eq 1, V < 1 kV):

```
lg Ia = −0.097 + 0.662·lg20 + 0.0966·0.48 + 0.000526·32
        + 0.5588·0.48·lg20 − 0.00304·32·lg20
      = 1.04986   →   Ia = 10^1.04986 = 11.22 kA
```

Normalized incident energy (eq 3) then incident energy (eq 4, reported in cal/cm²):

```
lg En = −0.555 − 0.113 + 1.081·1.04986 + 0.0011·32 = 0.50210
En    = 10^0.50210 = 3.177 (J/cm², normalized)
E     = Cf·En·(t/0.2)·(610/D)^x
      = 1.5·3.177·1·(610/457)^1.473 = 7.29 cal/cm²
```

Arc-flash boundary (eq 5, distance at which E = 1.2 cal/cm²):

```
AFB = 610·(Cf·En·(t/0.2) / 1.2)^(1/x)
    = 610·(1.5·3.177 / 1.2)^(1/1.473) = 1556 mm
```

**Expected result (acceptance):**

| Quantity | Value |
| --- | --- |
| Arcing current `Ia` | ≈ 11.22 kA |
| Incident energy `E` | ≈ 7.29 cal/cm² |
| Arc-flash boundary | ≈ 1556 mm |
| PPE category | **2** (4 < E ≤ 8 cal/cm²) |

These are the numbers the code's formulas reproduce (the derivation above is
mirrored in `arcflash.go`'s function comments). Any drift here is a coefficient or
units bug in the pure math, independent of the network.

---

## 2. Integration recipe — reuse `seed/protection_network_example.sql`

That seed already gives everything the full pipeline needs: a Thévenin source, MV
conductors, a graded protection chain (RLY-1/BRK-1 upstream, FUSE-1 downstream),
an 11 kV → 400 V transformer (TX-1), and an LV run — i.e. both an **MV** hazard
node (MV-3, 11 kV, downstream of both protective devices) and an **LV** hazard node
(LV-1, 400 V, downstream of TX-1). No new seed is needed; reusing it keeps the
arc-flash check on the same topology the coordination check already validates.

Apply the seed exactly as its header documents (it is the coordination seed), then:

```
POST /analysis/arc-flash
  { "start": {"feature_class":"DIST_XFMR","feature_id":"9a070000-0000-0000-0000-000000000001"},
    "nominal_voltage_v": 11000 }
```

The response carries one `arcFlashNodeResult` per reached node with a resolved
bolted fault current. Two nodes anchor the acceptance:

### MV-3 (11 kV, `...0013`)

- Geometry defaulted to the 15 kV switchgear typical (gap 153 mm, x 0.973,
  WD 910 mm, box, ungrounded), `geometry_assumed = true`.
- `Cf = 1.0` (V > 1 kV); arcing current uses eq 2: `lg Ia = 0.00402 + 0.983·lg Ibf`,
  so `Ia ≈ Ibf` (a hair under).
- Clearing device should be **FUSE-1** or **BRK-1** (whichever resolves first on
  the fault→source path *at the arcing current*), `clearing_time_assumed = false`.
- **Check:** take the live `bolted_fault_current_amps` at MV-3, feed it through the
  formulas above (with the MV geometry + the reported `clearing_time_s`), and
  confirm `incident_energy_cal_cm2`, `arc_flash_boundary_mm`, and `ppe_category`
  match. Cross-check `clearing_time_s` against `POST /analysis/coordination` for the
  same start/fault — the coordination engine reports the same device's operate time
  (there evaluated at the *bolted* current; here at the *arcing* current, so a
  slightly longer time is expected and correct).

### LV-1 (400 V, `...0021`)

- Geometry defaulted to the LV switchgear typical (gap 32 mm, x 1.473, WD 610 mm,
  box, ungrounded), `Cf = 1.5`.
- Arcing current uses eq 1 (the LV form). Worked illustratively for an assumed
  `Ibf = 16 kA` (the live value governs — TX-1 is 500 kVA / 4.5 %, so the LV bolted
  current lands near 14–16 kA):

  ```
  lg Ia = −0.097 + 0.662·lg16 + 0.0966·0.4 + 0.000526·32
          + 0.5588·0.4·lg16 − 0.00304·32·lg16 = 0.9075
  Ia    = 10^0.9075 = 8.08 kA
  ```

  Referred to the 11 kV protection: `8.08 kA · 400/11000 ≈ 294 A`, which the
  upstream device (RLY-1 pickup 100 A, or FUSE-1 100T) clears — so `clearing_time_s`
  is resolved, not capped. With that `t`, incident energy at the 610 mm working
  distance follows eq 3–4 with `Cf = 1.5`.
- **Check:** as for MV-3 — plug the live `bolted_fault_current_amps` + reported
  `clearing_time_s` into the LV formulas and confirm the energy / boundary / PPE.

### What "passes" means

1. Unit test (§1) reproduced to ~1 %.
2. Every reached node has a physically sane `arcing_current_amps < bolted_fault_current_amps`.
3. MV-3 and LV-1 resolve a real upstream `clearing_device` (not capped).
4. `incident_energy_cal_cm2` at each node equals the hand value from its live
   `Ibf` + reported `t` + the (flagged) default geometry.
5. `reduced_current_governs` set only where the 0.85·Ia case is genuinely worse.

---

## 3. S2 — declared geometry override (`seed/arc_flash_geometry_fields.sql`)

S2 adds the four declared per-feature fields (`arc_electrode_configuration`,
`arc_conductor_gap_mm`, `arc_working_distance_mm`, `arc_system_grounded`) to the
energized work-location classes, including **DIST_XFMR** — and TX-1 in the
protection seed is a DIST_XFMR that the arc-flash walk reaches as a hazard node
(11 kV primary base). So the override path is checkable on the *same* seed, with
one field write and no new topology.

Recipe (after the S2 field seed is applied and DIST_XFMR recompiled):

1. **Baseline.** Run the §2 study; note TX-1's `incident_energy_cal_cm2`,
   `gap_mm` (defaulted 153), `electrode_configuration` (`switchgear`), and
   `geometry_assumed` (`true`).
2. **Declare geometry on TX-1**, e.g. set `arc_electrode_configuration = 'open_air'`
   and `arc_conductor_gap_mm = 152`, `arc_working_distance_mm = 914` on
   `gis.dist_xfmr` id `...0004`.
3. **Re-run** the same study. Acceptance:
   - TX-1's `electrode_configuration` now reports `open_air`, `enclosed = false`,
     `gap_mm = 152`, `working_distance_mm = 914`.
   - `geometry_assumed` flips to **`false`** (electrode + gap + WD all declared).
   - `incident_energy_cal_cm2` **changes** — open-air (K1 = −0.792, box→open) and
     the larger working distance/exponent both lower it versus the enclosed
     default; the number must move, and in the expected direction.
   - Every *other* node (no declared fields) is byte-for-byte unchanged from the
     baseline — the switchgear default branch reproduces S1 exactly.
4. **Precedence spot-check:** pass `working_distance_mm` in the request AND keep
   `arc_working_distance_mm` on TX-1. TX-1 must report the *declared* 914 (declared
   wins over request); a node without the declared field must report the request
   value (request wins over the typical default).

---

## Notes / honesty flags exercised

- `geometry_assumed` is `true` for a node whose geometry fell back to an IEEE
  typical value (the whole network, until S2 fields are populated). It flips to
  `false` on a node where the electrode configuration, gap, and working distance
  are all supplied by declared fields (or a request WD). `working_distance_mm` is
  also overridable at the request level; a declared per-feature value wins over it.
- `clearing_time_assumed` is `true` only where no upstream protective device
  resolves — then `clearing_time_s` is the cap (`max_clearing_time_s`, default 2 s).
- `out_of_range` flags a node outside IEEE 1584-2002's validity envelope
  (0.208–15 kV, 0.7–106 kA); the number is still computed by extrapolation.
- Bolted fault current is the fault engine's own per-node figure (grid + any
  embedded-generation infeed), so `POST /analysis/arc-flash` and
  `POST /analysis/fault-current` never disagree on `Ibf` for the same node.

Owed once runnable: `cd api && go build ./...`, then the two checks above against a
live, fully-compiled DB with the protection seed applied.
