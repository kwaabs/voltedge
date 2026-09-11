# Test network — a seedable radial feeder

`seed/test_network_example.sql` is the **first seed that inserts real feature
instances** (rows in the physical `gis.*` tables) plus the `runtime.feature_connectivity`
wiring that joins them. Every other `seed/*.sql` file seeds *metadata* only — feature
classes, fields, coded domains, terminals, network rules. This one seeds an actual
small distribution feeder you can trace, load-flow, fault and isolate against.

## Topology

A strict radial tree (the load-flow sweep rejects loops), branching only at the LV bus:

```
SUB-SOURCE  DIST_XFMR   11 kV slack / ideal source bus (unity ratio, Thevenin source_r/x set)
   │ T2
MV-1        MV_CONDUCTOR  ACSR_100
   │
SW-101      MV_SWITCH     CLOSED  ← the isolation point (flip to OPEN to de-energize downstream)
   │ T2
MV-2        MV_CONDUCTOR  ACSR_100
   │
TX-101      DIST_XFMR     11 kV → 400 V, 500 kVA, Z = 4.5 %, X/R = 6, Z0 = 4.0 %
   │ T2
LV-1        LV_CONDUCTOR  WAVEFORM_185
   ├── MTR-A  METER → SP-A  SERVICE_POINT  commercial, 15 kW @ 0.95 pf
   └── MTR-B  METER → SP-B  SERVICE_POINT  industrial, 30 kW @ 0.90 pf

P-1, P-2, P-3  POLE (concrete)  — unconnected, for map/relationship testing
```

There is **no dedicated source/slack feature class** in VoltEdge. The power-flow start
node is whatever the caller passes as the trace/flow start, and the slack voltage is the
`nominal_voltage_v` request parameter. By convention a `DIST_XFMR` with equal primary and
secondary voltage stands in for the substation supply: `SUB-SOURCE` is that stand-in
(11 kV in, 11 kV out, carrying the Thevenin `source_r/x/r0/x0_ohm` for fault studies,
with `rated_kva`/`impedance_pct` left NULL so it behaves as an ideal bus). `TX-101` is the
real step-down transformer.

Fixed UUIDs (`feed0000-…-0000000000NN`) so re-applying is idempotent
(`ON CONFLICT DO NOTHING`) and you can reference rows by hand.

## Prerequisites

Apply these metadata seeds, then compile the seven classes, **before** seeding the network.
The seed itself fails loudly (RAISE EXCEPTION) if any table/column/rule it needs is missing,
so a half-set-up database aborts cleanly instead of writing a partial network.

Metadata seeds: `connectivity_example`, `load_flow_fields`, `transformer_model_fields`,
`zero_seq_fields`, `mv_conductor_specs`, `mv_lv_switch_example`, `lv_conductors_example`,
`lv_metering_example`, `service_point_example`, `pole_example`.

Compile: `DIST_XFMR`, `MV_SWITCH`, `MV_CONDUCTOR`, `LV_CONDUCTOR`, `METER`,
`SERVICE_POINT`, `POLE` (e.g. `make compile CODE=DIST_XFMR`, or the Taskfile equivalent).

## Apply

```
task seed-test-network      # or:  make seed-test-network
```

## What to test once it's in

1. **Trace** — feeder/downstream trace from `SUB-SOURCE` should reach both `SP-A` and `SP-B`.
2. **Isolation** — set `SW-101` `device_state` to `OPEN`, re-trace: everything downstream of
   the switch de-energizes (an open `MV_SWITCH` is a seeded trace barrier).
3. **Power flow** — start `SUB-SOURCE`, `nominal_voltage_v = 11000`: expect MV voltage drop,
   a step down to ~400 V at `TX-101`, and both loads drawn.
4. **Fault** — 3-phase and single-line-to-ground fault at `TX-101`.
