# Phase — Bus coupler / bus tie

**Status:** design locked 2026-09-09. Slices 1–2 code-complete (verification
owed). Slice 3 specced.

## Why

A **bus coupler** (a.k.a. bus tie) is the switchable link between two busbars in
a substation — closed to parallel two bus sections, opened to split them for
maintenance or to run them off separate sources. A **bus-section** switch is the
same device splitting one bar into two sections. Neither existed as a nameable
thing in VoltEdge: you could *build* one (a breaker wired bus↔bus) but nothing
said "this breaker is the coupler," and there was no place to record that a tie
is a designed **normally-open point** rather than a breaker that merely happens
to be open right now.

## The decision: it's a breaker, not a new class, not a joint

The instinct to compare it to `MV_CABLE_JOINT` is half right (it *joins* two
things) but the wrong model. A cable joint is a **passive, always-closed,
zero-Z pass-through junction**; a coupler is an **active switch** whose open/
closed state reconfigures the network. So it belongs in the *switch* family, not
the *junction* family.

**A coupler is electrically an `MV_BREAKER`.** Wired between two `MV_BUSBAR`
instances through the existing busbar network rules **C** (`bus T2 → switch T1`)
and **D** (`switch T2 → bus T1`) from `seed/mv_busbar_example.sql`, it is already
a real, traced network node — so trace, isolate, feeder/section, load flow,
fault current, and protection coordination **all handle it today with no new
class, terminals, network_rules, or engine code.** Giving it its own class would
only duplicate `MV_BREAKER` and fragment the switch family (every trace config,
protection curve, and symbology rule keys off the device classes).

What's genuinely missing is **semantics + operational fields + placement
ergonomics** — none of which needs a new class. This is the same call
`BAY_ROLE` and `PROTECTIVE_RELAY.device_number` already made: *a functional
variant is a field, not a subtype or a class.*

## Slices

- **S1 — role + normal-state fields (CODE-COMPLETE):**
  `seed/bus_coupler_fields.sql` adds two nullable coded fields to `MV_BREAKER`
  (a field-pack in the proven `seed/load_flow_fields.sql` shape — nullable, no
  subtype rules, so columns add cleanly to the already-compiled breaker table):
  - `switch_function` — `FEEDER` / `INCOMER` / `BUS_COUPLER` / `BUS_SECTION` /
    `TRANSFORMER` (new `SWITCH_FUNCTION` domain, deliberately the **same value
    set as `BAY_ROLE`** so a coupler breaker and its coupler bay read
    consistently; `BUS_SECTION` covers the section-switch case with no extra
    work). An unset value reads as an ordinary feeder breaker.
  - `normal_state` — `NORMALLY_CLOSED` / `NORMALLY_OPEN` (new `NORMAL_STATE`
    domain). The **designed** posture, distinct from the live `device_state`: a
    `device_state=OPEN` on a `NORMALLY_CLOSED` tie is an abnormal switching
    state, a different fact from a designed open point. This is the hook Slice 3
    and the future switching-order / reliability work read.

  Nothing in the engine consumes these fields yet (pure labelling), so this
  slice is entirely additive. `task seed-bus-coupler-fields` +
  `task compile CODE=MV_BREAKER`. No migration.

- **S2 — `/couplers` placement composite (CODE-COMPLETE):** a fan-out endpoint
  in the exact machinery of `rmu` / `panel` / `duct` (`api/internal/coupler`).
  `POST /couplers` in one `RunInTx`: create the breaker (`CreateCoupler` forces
  `switch_function=BUS_COUPLER` when unset, never clobbering an explicit
  `BUS_SECTION`), then two `connectivity.EstablishConnection` calls wire it
  between the two picked buses — **Bus A.T2 → breaker.T1** (rule C) and
  **breaker.T2 → Bus B.T1** (rule D), all four terminals stated because the
  busbar rules pin them. `GET /couplers/options` returns each breaker class that
  carries a `switch_function` field with a `BUS_COUPLER` value **and** is
  authorized by both rules C+D against some bus class, plus those bus classes
  (so the frontend knows which classes to pick the two buses from) — empty until
  the S1 field-pack + busbar wiring exist, so the affordance self-gates.

  - **Breaker-only** (no inline flanking isolators): no switch-to-switch network
    rule exists (only device↔conductor and bus↔device), so inline isolators
    would need new rules — deferred, not built. The config/isolator option in
    the original spec is dropped for this reason.
  - **Error mapping**: unlike `rmu` (whose wiring is engine-internal, so a rule
    miss is a 500 seed-bug signal), a coupler's two buses are **user-picked**, so
    a connectivity rejection maps to **400** and a missing bus to **404**. This
    needed two new EXPORTED checkers `connectivity.IsValidationError` /
    `IsNotFoundError` (errors.As-based, so a wrapped error still matches) —
    added alongside the existing unexported pair.
  - **Gate**: mounted under the method-aware `features:read/features:write` gate
    (main.go), with an inner `connectivity:commit` on `POST` — the `/rmus` gate
    one capability shallower (a coupler writes no containment relationship).
  - **Frontend**: a "Bus coupler" section on the new-feature menu
    (`useCouplerOptions`) starts a **point** draw for the breaker
    (`startCouplerDrawing` stashes the eligible bus classes); the shared
    create-form gains a `draft.coupler` branch that renders two
    `CouplerBusPicker` inputs (scoped search-by-name over the bus classes, each
    excluding the other's pick) and `POST`s `/couplers` on Save. The breaker
    class stays in the generic list too (you still place ordinary feeder
    breakers). `BUS_SECTION` is the same endpoint with a different
    `switch_function`, so this one build also covers the section switch.

  **`go build` + web build PASS** (user-confirmed 2026-09-10). Still owed =
  functional round-trip: place a coupler between two seeded MV_BUSBAR instances
  and confirm both connections commit + a trace walks bus→breaker→bus (and halts
  when the breaker is OPEN); a bus pick with no authorizing rule returns 400.

  - **Verification seed (`seed/coupler_network_example.sql`, `task
    seed-coupler-network`):** the tightest possible functional proof, built to
    confirm "does it actually work?". A minimal real-instance seed
    (`*_network_example.sql` shape — writes actual `gis.*` rows +
    `runtime.feature_connectivity` rows, distinct uuid prefix `c0501e00` + map
    area ~-0.320, 5.720 so it coexists with the other network seeds) that
    isolates ONE thing: **`BUS-A` =[`CPLR-1` MV_BREAKER, switch_function=
    BUS_COUPLER, normal_state=NORMALLY_CLOSED, device_state=CLOSED]= `BUS-B` →
    `LOAD-FEED`**. It hand-builds the *exact same* rule-C/rule-D connectivity
    rows the `POST /couplers` endpoint commits (BUS-A.T2→CPLR-1.T1,
    CPLR-1.T2→BUS-B.T1), so a good trace here validates the endpoint's target
    state too. **The check:** a downstream trace from `BUS-A` reaches `BUS-B` +
    `LOAD-FEED` *only across the closed tie*; flip `CPLR-1.device_state` to
    `OPEN` and re-trace — `BUS-B`/`LOAD-FEED` drop out, proving the coupler is a
    switchable barrier with *zero* coupler-specific engine code (it's a plain
    `MV_BREAKER`, so trace/isolate/feeder treat it as a switch). Fails loudly
    (RAISE EXCEPTION) if any prerequisite is missing — including an explicit
    guard that `gis.mv_breaker.switch_function` exists (the S1 field-pack must be
    applied + `MV_BREAKER` recompiled). Prereqs: `seed-connectivity` +
    `seed-mv-switching` + `seed-mv-busbar` + `seed-bus-coupler-fields`, then
    compile `MV_CONDUCTOR` / `MV_BREAKER` / `MV_BUSBAR`. **Static-reviewed only —
    live psql apply + the CLOSED/OPEN trace check are still owed to call it
    verified** (shell can't spawn here).

- **S3 — operational integration (SPECCED, later):** surface `normal_state` in
  the switching-order / `operational_state` work (the open-point concept feeds
  isolation planning and, downstream, reliability/OMS), and optionally a
  role-specific coupler **glyph** keyed off `switch_function`'s `domain_value_id`
  (the same role-glyph nicety `BAY` deferred).

## Decisions locked

- **Coupler = `MV_BREAKER` + `switch_function` field**, not a new feature class
  — a coupler is electrically a breaker; a new class would duplicate it and
  break the switch family's engines.
- **Not the joint pattern** — a coupler is an active switch, not a passive
  zero-Z junction; `MV_CABLE_JOINT` would make it un-switchable.
- **`normal_state` is separate from `device_state`** — designed posture vs live
  state; a normally-closed tie found open is an abnormal switching state.
- **`switch_function` reuses `BAY_ROLE`'s value set** — coupler breaker and
  coupler bay stay consistent; `BUS_SECTION` folds the section switch in for
  free.
- **Fields on `MV_BREAKER` only** for now (couplers are breaker-based); extend to
  `MV_SWITCH` later only if fused/load-break couplers need modelling.

## Precedents copied

- `seed/load_flow_fields.sql` — the nullable field-pack over an already-compiled
  class (no subtype rules, so the column adds cleanly).
- `seed/bay_example.sql` — the `BAY_ROLE` value set + "functional variant is a
  field, not a subtype" call.
- `seed/mv_busbar_example.sql` — network rules C/D that already express a coupler
  (two bus instances joined through one switch).
- `api/internal/duct` / `rmu` / `panel` — the `RunInTx` composite fan-out spine
  for Slice 2's `POST /couplers`.
