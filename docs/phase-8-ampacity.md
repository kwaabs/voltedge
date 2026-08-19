# Phase 8 — first-principles conductor ampacity (IEEE 738)

**Goal:** compute a conductor's own steady-state thermal ampacity from
first principles -- the current that raises it to its own rated
maximum operating temperature under given environmental conditions --
rather than always reading the material catalog's static
`ampacity_amps` figure. Slice F, the last of the six-slice
electrical-analysis plan. See `docs/open-items.md`'s Phase 8 section
for the full plan and `docs/phase-8-materials.md` for the thermal
catalog columns (Slice A) this rides on.

**Status:** backend curl-verified end to end against a real Postgres,
including an independent Python re-implementation of the same formula
matching the API's own response to full float precision, every
validation/error path, and the ambient-at-or-above-rating edge case.
No frontend, and deliberately not wired into the load-flow/fault-
current engines as a dynamic ampacity source yet -- see "What's still
out of scope" below.

## Design: real IEEE 738 physics, one deliberate simplification

Unlike the load-flow sweep (which solves a genuinely different,
simpler problem than full Newton-Raphson AC power flow) or fault
current (an exact closed form once you accept the symmetrical/
positive-sequence simplification), this slice's own name is the
promise: implement the real IEEE 738 steady-state heat-balance
equation, not a further-simplified stand-in. `qc + qr = qs + I^2 R(Tc)`
-- convective loss plus radiated loss equals solar heat gain plus
Joule heating -- evaluated directly at `Tc = max_operating_temp_c`
(the conductor's own rated ceiling), solved for `I`.

The one deliberate simplification: full IEEE 738 derives air density,
viscosity, and thermal conductivity from the actual film temperature
`(Tc+Ta)/2` via its own polynomial correlations, which themselves
depend on elevation and require a small iterative solve. This
implementation holds those three air properties **constant** at
typical values near a moderate film temperature (~40°C, sea level) --
the same "real physics, simplified input" tradeoff `docs/phase-8-load-
flow.md`'s own linearized voltage-drop approximation already
documents for a different equation. Everything else -- the forced/
natural convection formulas (IEEE 738's own two forced-convection
forms plus the natural-convection floor, `K_angle` wind-direction
factor, Reynolds number), the Stefan-Boltzmann radiation term, and the
solar heat-gain term -- is the real standard, transcribed faithfully.

This simplification has a useful side effect: with air properties
held constant, `qc` and `qr` become direct functions of `(Tc, Ta)`
that don't depend on the unknown current `I` at all (only `I²R`
does), so the heat balance is already closed-form --
`I = sqrt((qc+qr-qs) / R(Tc))` -- with no root-find needed. Full IEEE
738, whose air properties depend on the film temperature which itself
depends on `Tc` (already fixed here, since we evaluate at the rated
ceiling) but would depend on `I` if solving the inverse problem
("what temperature does this current produce"), needs iteration for
that direction; this endpoint only ever answers "what current reaches
the rated ceiling," which sidesteps it entirely.

## Design: request-time environmental inputs, not stored per conductor

`max_operating_temp_c`, `emissivity`, `absorptivity`, and
`resistance_temp_coefficient_per_c` (Slice A) are genuinely conductor-
intrinsic and belong on `metadata.material_specs`. Ambient temperature,
wind speed/angle, and solar heat flux are not -- they vary per
calculation run (a rating at noon in July differs from one at
midnight in January on the *same* conductor), so they're `POST
/analysis/ampacity`'s own request parameters, never stored.

`ambient_temp_c` is a `*float64`, not a plain `float64` like every
other numeric in this package's requests: 0°C is a real, common
ambient temperature, so unlike `nominal_voltage_v`'s own "`<=0` means
missing" gate, there's no sentinel invalid value to lean on -- explicit
presence via a pointer is the only honest way to require it, the same
underlying reasoning `nominal_voltage_v` itself is required for. Wind
speed and solar flux stay plain `float64` (0 is both a legitimate
value and, for wind, a safely conservative omitted-default -- see the
request type's own doc comment for why solar flux's omitted-default
is the opposite, non-conservative case a caller doing a daytime rating
needs to know about).

## Design: a single conductor feature, not a whole-network read-out

Unlike `/load-flow` and `/fault-current`, this endpoint takes one
conductor feature and returns one ampacity figure -- there's no tree to
walk (per `docs/open-items.md`'s own framing, "a standalone thermal-
equilibrium calc, doesn't touch the graph at all"). It resolves the
feature's own material-linked field the same by-field-name scanning
`resolveConductorImpedance` already does (kept as this file's own
independent copy rather than shared, since the two functions' own
required-field sets differ -- see `ampacity.go`'s own doc comment),
requires `network_role = 'edge'` (ampacity only means something for a
branch that carries current along its own length), and returns a
clear, field-naming error if the resolved spec is missing any of the
four thermal columns this calc needs.

## Backend

- `api/internal/powerflow/ampacity.go` (new) -- `runAmpacity` (entry
  point), `convectiveLossWM` (the IEEE 738 forced/natural convection
  formulas), `resolveAmpacityMaterialSpec` (this file's own field-
  scanning resolution), `missingAmpacityFields` (names exactly which
  catalog column(s) are unset, rather than a bare "incomplete spec"
  error).
- `api/internal/powerflow/handler.go` -- `POST /ampacity`, mounted
  alongside `/load-flow` and `/fault-current` at `/analysis`.

No new fields -- the four thermal columns (Slice A) and the existing
`diameter_mm`/`resistance_ohm_per_km` are exactly the inputs this
needs.

## How this was validated

Against a real, non-Docker Postgres:

1. Set `max_operating_temp_c=90`, `emissivity=0.6`, `absorptivity=0.6`,
   `resistance_temp_coefficient_per_c=0.00393` on the `WAVEFORM_185`
   material spec (Slice A's own thermal columns existed but had never
   been populated on any real spec) via the existing admin PATCH
   endpoint.
2. Created a scratch `LV_CONDUCTOR` feature (`conductor_spec:
   WAVEFORM_185`) -- no connectivity/terminals needed at all, since
   this endpoint never walks the graph.
3. Ran `POST /analysis/ampacity` at `ambient_temp_c=25`,
   `wind_speed_mps=0.6`, `wind_angle_deg=90`,
   `solar_heat_flux_w_m2=1000` -- got `ampacity_amps=699.43`.
   Independently re-implemented the exact same formula from scratch in
   Python (not just re-reading the Go source) and got
   `699.4339412616043` against the API's own
   `699.4339412616042` -- agreement to float precision.
4. Confirmed the validation/error paths: a non-conductor class
   (`DIST_XFMR`, `network_role != edge`) rejected with a clear
   message; a missing `ambient_temp_c` rejected; a material spec
   missing its thermal columns (`ABC_95`, never populated) rejected
   with the exact missing field names listed.
5. Confirmed the ambient-at-or-above-rating edge case: setting
   `ambient_temp_c=95` (above the 90°C rating) correctly returned
   `ampacity_amps=0` rather than a nonsensical negative-radicand
   crash or a wrong positive number.
6. Purged the two scratch `LV_CONDUCTOR` feature instances afterward.
   **Left `WAVEFORM_185`'s thermal values in place** rather than
   clearing them back to `NULL`: the admin material-spec update
   endpoint deliberately has no clear-to-null path for its numeric
   columns (documented in `updateMaterialSpecRequest`'s own doc
   comment -- "nothing... requires clearing one back out once set"),
   and the values set are realistic, standard figures for a PVC/XLPE-
   insulated LV cable, so leaving them is populating real catalog data
   the earlier slice left empty, not leftover scratch debris.

`go build ./... && go vet ./... && gofmt -l . && go test ./...` all
clean throughout.

## Try it yourself

```bash
curl -X POST http://localhost:3704/analysis/ampacity \
  -H "Content-Type: application/json" \
  -d '{"conductor":{"feature_class":"LV_CONDUCTOR","feature_id":"<id>"},"ambient_temp_c":25,"wind_speed_mps":0.6,"wind_angle_deg":90,"solar_heat_flux_w_m2":1000}'
```

## What's still out of scope

- **Not wired into the load-flow/fault-current engines as a dynamic
  ampacity source.** The original plan framing ("wires in as an
  optional dynamic ampacity source for the loading/capacity and
  transformer-loading checks... instead of always reading the static
  catalog `ampacity_amps`") is a follow-up integration, not this
  slice: `/load-flow`'s own `LoadingPct` still reads the static
  catalog figure. Wiring this in means deciding what environmental
  inputs a load-flow request itself would need to carry (ambient/wind/
  solar alongside `nominal_voltage_v`) -- a real design question left
  open, not an oversight.
- **Frontend** -- no trigger or results display yet.
- **Temperature-dependent air properties / iterative film-temperature
  solve** -- the deliberate simplification this whole slice makes; see
  "Design" above.
- **Transient (short-circuit) thermal rating** -- this is steady-state
  only, same as the rest of this six-slice plan's own scope.
