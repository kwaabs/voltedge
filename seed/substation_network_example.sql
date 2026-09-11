-- seed/substation_network_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives
-- under /seed on purpose, same reasoning as every other seed file.
--
-- A SECOND real-feature-instance seed (the first is seed/test_network_example.sql),
-- deliberately LARGER and centred on a real 33/11 kV SUBSTATION so tracing,
-- load flow, and fault analysis have a multi-voltage, multi-feeder network to
-- run against -- three voltage levels (33 kV / 11 kV / 400 V), a real
-- POWER_XFMR + MV_BUSBAR substation, two outgoing MV feeders, and a richer LV
-- side (an LV switch, six meters / service points). It writes actual ROWS into
-- the compiled physical gis.* tables plus the runtime.feature_connectivity rows
-- that wire them together. Its features use a distinct uuid prefix
-- (5b510000-...) and a distinct map area from test_network_example (5b51 vs.
-- feed0000; ~-0.296,5.700 vs. -0.198,5.600), so BOTH seeds can coexist on one
-- database without colliding.
--
-- ASCII TOPOLOGY (a strict radial tree; branch points at BUS-A, F1-LV-1,
-- F2-MV-2, F2-LV-1):
--
--   GRID-SRC  (DIST_XFMR, 33 kV slack / Thevenin source; unity ratio)
--     | T2
--     +-- HV-1 (MV_CONDUCTOR, 33 kV)
--          | HV
--          PTX-1 (POWER_XFMR, two_winding, 33 kV -> 11 kV) LV
--          |
--          +-- BUS-FEED (MV_CONDUCTOR, 11 kV)
--               | T1
--               BUS-A (MV_BUSBAR, 11 kV)   <-- substation bus, branch point
--               | T2
--               +=== FEEDER 1 ==========================================
--               |    F1-MV-1 (MV_CONDUCTOR 11 kV)
--               |     | T1
--               |    RC-1 (MV_RECLOSER, CLOSED) T2
--               |     +-- F1-MV-2 (MV_CONDUCTOR 11 kV)
--               |          | T1
--               |         SW-1 (MV_SWITCH, CLOSED) T2
--               |          +-- F1-MV-3 (MV_CONDUCTOR 11 kV)
--               |               | T1
--               |              DTX-1 (DIST_XFMR, 11 kV -> 400 V, 500 kVA) T2
--               |               +-- F1-LV-1 (LV_CONDUCTOR 400 V, main)   <-- branch
--               |                    +-- MTR-1 --> SP-1 (residential,  8 kW)
--               |                    +-- MTR-2 --> SP-2 (commercial,  20 kW)
--               |                    +-- LV-SW-1 (LV_SWITCH, CLOSED, fused) T2
--               |                         +-- F1-LV-2 (LV_CONDUCTOR 400 V, service)
--               |                              +-- MTR-3 --> SP-3 (residential, 6 kW)
--               |
--               +=== FEEDER 2 ==========================================
--                    F2-MV-1 (MV_CONDUCTOR 11 kV)
--                     | T1
--                    SW-2 (MV_SWITCH, CLOSED) T2
--                     +-- F2-MV-2 (MV_CONDUCTOR 11 kV)   <-- branch: two xfmrs
--                          +-- DTX-2 (DIST_XFMR, 11 kV -> 400 V, 315 kVA) T2
--                          |    +-- F2-LV-1 (LV_CONDUCTOR 400 V, main)   <-- branch
--                          |         +-- MTR-4 --> SP-4 (commercial, 25 kW)
--                          |         +-- MTR-5 --> SP-5 (industrial, 40 kW)
--                          +-- DTX-3 (DIST_XFMR, 11 kV -> 400 V, 200 kVA) T2
--                               +-- F2-LV-2 (LV_CONDUCTOR 400 V, main)
--                                    +-- MTR-6 --> SP-6 (residential, 10 kW)
--
--   P-1..P-4 (POLE) are physical support structures only -- network_role
--   'none', no terminals, deliberately NOT connected into the graph.
--
-- WHY the substation transformer reaches the bus through a conductor span,
-- and the source is still a DIST_XFMR:
--   * No network_rule authorizes POWER_XFMR -> MV_BUSBAR directly (or bus ->
--     MV_SWITCH/MV_RECLOSER). The only valid rules are POWER_XFMR LV ->
--     MV_CONDUCTOR and MV_CONDUCTOR -> MV_BUSBAR T1, so PTX-1 reaches BUS-A
--     through the short BUS-FEED span -- and every feeder leaves the bus as a
--     MV_CONDUCTOR (BUS-A T2 -> MV_CONDUCTOR), never straight into a device.
--   * Only DIST_XFMR carries the Thevenin source_r_ohm/source_x_ohm(+r0/x0)
--     fields the power-flow/fault engine reads at the slack node (they were
--     added to DIST_XFMR only -- load_flow_fields.sql / zero_seq_fields.sql).
--     POWER_XFMR has rated_mva + impedance_pct but no source impedance, so it
--     cannot be the slack. GRID-SRC is therefore a unity-ratio 33 kV DIST_XFMR
--     acting as the incoming-grid Thevenin equivalent, exactly as SUB-SOURCE
--     is in test_network_example.sql -- just one voltage level higher.
--
-- HARD PREREQUISITES -- every metadata seed below must be applied AND the
-- classes it defines COMPILED (task compile CODE=...) BEFORE this seed, because
-- this seed writes into the physical gis.<class> tables the compiler produces
-- and reads the electrical field COLUMNS those seeds add:
--
--   seed/connectivity_example.sql        -> MV_CONDUCTOR, DIST_XFMR (base),
--                                           VOLTAGE_CLASS incl. 33KV
--   seed/load_flow_fields.sql            -> SERVICE_POINT.demand_kw/power_factor,
--                                           DIST_XFMR.rated_kva/source_r_ohm/source_x_ohm
--   seed/transformer_model_fields.sql    -> DIST_XFMR.secondary_voltage/impedance_pct/x_r_ratio
--   seed/zero_seq_fields.sql             -> DIST_XFMR + POWER_XFMR .z0_pct,
--                                           DIST_XFMR.source_r0_ohm/source_x0_ohm
--   seed/mv_conductor_specs.sql          -> MV_LINE_SPEC + MV_CONDUCTOR.conductor_spec
--   seed/power_transformer_example.sql   -> POWER_XFMR (+ its network_rules to MV_CONDUCTOR)
--   seed/mv_busbar_example.sql           -> MV_BUSBAR (+ MV_CONDUCTOR<->MV_BUSBAR rules)
--   seed/mv_recloser_example.sql         -> MV_RECLOSER (+ its network_rules)
--   seed/mv_lv_switch_example.sql        -> MV_SWITCH + LV_SWITCH (+ their network_rules)
--   seed/lv_conductors_example.sql       -> LV_CONDUCTOR (+ DIST_XFMR T2 -> LV_CONDUCTOR)
--   seed/lv_metering_example.sql         -> METER (+ LV_CONDUCTOR -> METER, METER -> SERVICE_POINT)
--   seed/service_point_example.sql       -> SERVICE_POINT (+ DIST_XFMR T2 -> MV_CONDUCTOR)
--   seed/pole_example.sql                -> POLE
--
-- then, once the metadata is in place, compile each participating class:
--   task compile CODE=DIST_XFMR
--   task compile CODE=POWER_XFMR
--   task compile CODE=MV_BUSBAR
--   task compile CODE=MV_RECLOSER
--   task compile CODE=MV_SWITCH
--   task compile CODE=LV_SWITCH
--   task compile CODE=MV_CONDUCTOR
--   task compile CODE=LV_CONDUCTOR
--   task compile CODE=METER
--   task compile CODE=SERVICE_POINT
--   task compile CODE=POLE
--
-- The DO block below fails LOUDLY (RAISE EXCEPTION, naming the missing
-- prerequisite) if any required class, subtype, terminal, electrical column, or
-- network_rule is absent -- so a half-set-up database aborts the whole
-- transaction instead of writing a partial network. It is fully idempotent:
-- feature rows use fixed uuids with ON CONFLICT (id) DO NOTHING, and
-- connectivity rows use ON CONFLICT on the (from,to)+terminals unique key.
--
-- Apply with psql (NOT the migration runner), after the prerequisites above:
--   task seed-substation-network   (or the psql -f invocation directly)
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL has
-- been reviewed statically -- a live psql apply against a fully-compiled DB + a
-- trace / load-flow / fault-current re-check are still owed to call it verified.

DO $$
DECLARE
    -- feature_class ids
    fc_dist_xfmr   uuid;
    fc_power_xfmr  uuid;
    fc_mv_busbar   uuid;
    fc_mv_cond     uuid;
    fc_lv_cond     uuid;
    fc_mv_recloser uuid;
    fc_mv_switch   uuid;
    fc_lv_switch   uuid;
    fc_meter       uuid;
    fc_sp          uuid;
    fc_pole        uuid;

    -- subtype INTEGER codes (the value stored in gis.<class>.subtype)
    st_dxf_pad    integer;  -- DIST_XFMR 'pad_mounted'
    st_ptx_2w     integer;  -- POWER_XFMR 'two_winding' (code)
    st_ptx_2w_id  uuid;     -- POWER_XFMR 'two_winding' (subtype id, for its
                            -- SUBTYPE-SCOPED HV/LV terminals -- unlike every
                            -- other class here, POWER_XFMR terminals hang off
                            -- the subtype, so resolve them by subtype_id, not
                            -- the subtype_id-NULLS-LAST shortcut)
    st_rc_std     integer;  -- MV_RECLOSER 'standard'
    st_sw_std     integer;  -- MV_SWITCH 'standard'
    st_lvsw_std   integer;  -- LV_SWITCH 'standard'
    st_meter_wc   integer;  -- METER 'whole_current'
    st_meter_ct   integer;  -- METER 'ct_metered'
    st_sp_res     integer;  -- SERVICE_POINT 'residential'
    st_sp_comm    integer;  -- SERVICE_POINT 'commercial'
    st_sp_ind     integer;  -- SERVICE_POINT 'industrial'
    st_pole_conc  integer;  -- POLE 'concrete'
    st_mv_oh      integer;  -- MV_CONDUCTOR 'overhead_mv_conductor'
    st_lv_oh      integer;  -- LV_CONDUCTOR 'overhead_lv_conductor'

    -- terminal ids
    t_dxf_t1    uuid;
    t_dxf_t2    uuid;
    t_ptx_hv    uuid;  -- POWER_XFMR two_winding HV (primary)
    t_ptx_lv    uuid;  -- POWER_XFMR two_winding LV (secondary)
    t_bus_t1    uuid;
    t_bus_t2    uuid;
    t_rc_t1     uuid;
    t_rc_t2     uuid;
    t_sw_t1     uuid;
    t_sw_t2     uuid;
    t_lvsw_t1   uuid;
    t_lvsw_t2   uuid;
    t_meter_t1  uuid;
    t_meter_t2  uuid;
    t_sp_t1     uuid;

    -- network_rule ids
    r_dxf_t2_mv    uuid;  -- DIST_XFMR T2 -> MV_CONDUCTOR      (source + n/a)
    r_mv_ptx_hv    uuid;  -- MV_CONDUCTOR -> POWER_XFMR HV
    r_ptx_lv_mv    uuid;  -- POWER_XFMR LV -> MV_CONDUCTOR
    r_mv_bus_t1    uuid;  -- MV_CONDUCTOR -> MV_BUSBAR T1
    r_bus_t2_mv    uuid;  -- MV_BUSBAR T2 -> MV_CONDUCTOR
    r_mv_rc_t1     uuid;  -- MV_CONDUCTOR -> MV_RECLOSER T1
    r_rc_t2_mv     uuid;  -- MV_RECLOSER T2 -> MV_CONDUCTOR
    r_mv_sw_t1     uuid;  -- MV_CONDUCTOR -> MV_SWITCH T1
    r_sw_t2_mv     uuid;  -- MV_SWITCH T2 -> MV_CONDUCTOR
    r_mv_dxf_t1    uuid;  -- MV_CONDUCTOR -> DIST_XFMR T1
    r_dxf_t2_lv    uuid;  -- DIST_XFMR T2 -> LV_CONDUCTOR
    r_lv_lvsw_t1   uuid;  -- LV_CONDUCTOR -> LV_SWITCH T1
    r_lvsw_t2_lv   uuid;  -- LV_SWITCH T2 -> LV_CONDUCTOR
    r_lv_meter_t1  uuid;  -- LV_CONDUCTOR -> METER T1
    r_meter_t2_sp  uuid;  -- METER T2 -> SERVICE_POINT T1
BEGIN
    -- =====================================================================
    -- 1. Resolve feature_class ids (fail if a class was never seeded).
    -- =====================================================================
    SELECT id INTO fc_dist_xfmr FROM metadata.feature_classes WHERE code = 'DIST_XFMR';
    IF fc_dist_xfmr IS NULL THEN RAISE EXCEPTION 'missing feature_class DIST_XFMR -- apply seed/connectivity_example.sql then: task compile CODE=DIST_XFMR'; END IF;

    SELECT id INTO fc_power_xfmr FROM metadata.feature_classes WHERE code = 'POWER_XFMR';
    IF fc_power_xfmr IS NULL THEN RAISE EXCEPTION 'missing feature_class POWER_XFMR -- apply seed/power_transformer_example.sql then: task compile CODE=POWER_XFMR'; END IF;

    SELECT id INTO fc_mv_busbar FROM metadata.feature_classes WHERE code = 'MV_BUSBAR';
    IF fc_mv_busbar IS NULL THEN RAISE EXCEPTION 'missing feature_class MV_BUSBAR -- apply seed/mv_busbar_example.sql then: task compile CODE=MV_BUSBAR'; END IF;

    SELECT id INTO fc_mv_cond FROM metadata.feature_classes WHERE code = 'MV_CONDUCTOR';
    IF fc_mv_cond IS NULL THEN RAISE EXCEPTION 'missing feature_class MV_CONDUCTOR -- apply seed/connectivity_example.sql then: task compile CODE=MV_CONDUCTOR'; END IF;

    SELECT id INTO fc_lv_cond FROM metadata.feature_classes WHERE code = 'LV_CONDUCTOR';
    IF fc_lv_cond IS NULL THEN RAISE EXCEPTION 'missing feature_class LV_CONDUCTOR -- apply seed/lv_conductors_example.sql then: task compile CODE=LV_CONDUCTOR'; END IF;

    SELECT id INTO fc_mv_recloser FROM metadata.feature_classes WHERE code = 'MV_RECLOSER';
    IF fc_mv_recloser IS NULL THEN RAISE EXCEPTION 'missing feature_class MV_RECLOSER -- apply seed/mv_recloser_example.sql then: task compile CODE=MV_RECLOSER'; END IF;

    SELECT id INTO fc_mv_switch FROM metadata.feature_classes WHERE code = 'MV_SWITCH';
    IF fc_mv_switch IS NULL THEN RAISE EXCEPTION 'missing feature_class MV_SWITCH -- apply seed/mv_lv_switch_example.sql then: task compile CODE=MV_SWITCH'; END IF;

    SELECT id INTO fc_lv_switch FROM metadata.feature_classes WHERE code = 'LV_SWITCH';
    IF fc_lv_switch IS NULL THEN RAISE EXCEPTION 'missing feature_class LV_SWITCH -- apply seed/mv_lv_switch_example.sql then: task compile CODE=LV_SWITCH'; END IF;

    SELECT id INTO fc_meter FROM metadata.feature_classes WHERE code = 'METER';
    IF fc_meter IS NULL THEN RAISE EXCEPTION 'missing feature_class METER -- apply seed/lv_metering_example.sql then: task compile CODE=METER'; END IF;

    SELECT id INTO fc_sp FROM metadata.feature_classes WHERE code = 'SERVICE_POINT';
    IF fc_sp IS NULL THEN RAISE EXCEPTION 'missing feature_class SERVICE_POINT -- apply seed/service_point_example.sql then: task compile CODE=SERVICE_POINT'; END IF;

    SELECT id INTO fc_pole FROM metadata.feature_classes WHERE code = 'POLE';
    IF fc_pole IS NULL THEN RAISE EXCEPTION 'missing feature_class POLE -- apply seed/pole_example.sql then: task compile CODE=POLE'; END IF;

    -- =====================================================================
    -- 2. Resolve subtype INTEGER codes (the value stored in .subtype).
    --    MV_BUSBAR has NO subtype (the compiler omits the column entirely
    --    for a subtype-less class), so it is deliberately absent here.
    -- =====================================================================
    SELECT code INTO st_dxf_pad FROM metadata.subtypes WHERE feature_class_id = fc_dist_xfmr AND name = 'pad_mounted';
    IF st_dxf_pad IS NULL THEN RAISE EXCEPTION 'DIST_XFMR subtype ''pad_mounted'' not found -- re-apply seed/connectivity_example.sql'; END IF;

    SELECT id, code INTO st_ptx_2w_id, st_ptx_2w FROM metadata.subtypes WHERE feature_class_id = fc_power_xfmr AND name = 'two_winding';
    IF st_ptx_2w_id IS NULL THEN RAISE EXCEPTION 'POWER_XFMR subtype ''two_winding'' not found -- re-apply seed/power_transformer_example.sql'; END IF;

    SELECT code INTO st_rc_std FROM metadata.subtypes WHERE feature_class_id = fc_mv_recloser AND name = 'standard';
    IF st_rc_std IS NULL THEN RAISE EXCEPTION 'MV_RECLOSER subtype ''standard'' not found -- re-apply seed/mv_recloser_example.sql'; END IF;

    SELECT code INTO st_sw_std FROM metadata.subtypes WHERE feature_class_id = fc_mv_switch AND name = 'standard';
    IF st_sw_std IS NULL THEN RAISE EXCEPTION 'MV_SWITCH subtype ''standard'' not found -- re-apply seed/mv_lv_switch_example.sql'; END IF;

    SELECT code INTO st_lvsw_std FROM metadata.subtypes WHERE feature_class_id = fc_lv_switch AND name = 'standard';
    IF st_lvsw_std IS NULL THEN RAISE EXCEPTION 'LV_SWITCH subtype ''standard'' not found -- re-apply seed/mv_lv_switch_example.sql'; END IF;

    SELECT code INTO st_meter_wc FROM metadata.subtypes WHERE feature_class_id = fc_meter AND name = 'whole_current';
    IF st_meter_wc IS NULL THEN RAISE EXCEPTION 'METER subtype ''whole_current'' not found -- re-apply seed/lv_metering_example.sql'; END IF;

    SELECT code INTO st_meter_ct FROM metadata.subtypes WHERE feature_class_id = fc_meter AND name = 'ct_metered';
    IF st_meter_ct IS NULL THEN RAISE EXCEPTION 'METER subtype ''ct_metered'' not found -- re-apply seed/lv_metering_example.sql'; END IF;

    SELECT code INTO st_sp_res FROM metadata.subtypes WHERE feature_class_id = fc_sp AND name = 'residential';
    IF st_sp_res IS NULL THEN RAISE EXCEPTION 'SERVICE_POINT subtype ''residential'' not found -- re-apply seed/service_point_example.sql'; END IF;

    SELECT code INTO st_sp_comm FROM metadata.subtypes WHERE feature_class_id = fc_sp AND name = 'commercial';
    IF st_sp_comm IS NULL THEN RAISE EXCEPTION 'SERVICE_POINT subtype ''commercial'' not found -- re-apply seed/service_point_example.sql'; END IF;

    SELECT code INTO st_sp_ind FROM metadata.subtypes WHERE feature_class_id = fc_sp AND name = 'industrial';
    IF st_sp_ind IS NULL THEN RAISE EXCEPTION 'SERVICE_POINT subtype ''industrial'' not found -- re-apply seed/service_point_example.sql'; END IF;

    SELECT code INTO st_pole_conc FROM metadata.subtypes WHERE feature_class_id = fc_pole AND name = 'concrete';
    IF st_pole_conc IS NULL THEN RAISE EXCEPTION 'POLE subtype ''concrete'' not found -- re-apply seed/pole_example.sql'; END IF;

    SELECT code INTO st_mv_oh FROM metadata.subtypes WHERE feature_class_id = fc_mv_cond AND name = 'overhead_mv_conductor';
    IF st_mv_oh IS NULL THEN RAISE EXCEPTION 'MV_CONDUCTOR subtype ''overhead_mv_conductor'' not found -- re-apply seed/connectivity_example.sql'; END IF;

    SELECT code INTO st_lv_oh FROM metadata.subtypes WHERE feature_class_id = fc_lv_cond AND name = 'overhead_lv_conductor';
    IF st_lv_oh IS NULL THEN RAISE EXCEPTION 'LV_CONDUCTOR subtype ''overhead_lv_conductor'' not found -- re-apply seed/lv_conductors_example.sql'; END IF;

    -- =====================================================================
    -- 3. Resolve terminal ids. All are subtype-agnostic (subtype_id NULL)
    --    EXCEPT POWER_XFMR's HV/LV, which are scoped to the two_winding
    --    subtype -- resolved by matching subtype_id explicitly.
    -- =====================================================================
    SELECT id INTO t_dxf_t1 FROM metadata.terminals WHERE feature_class_id = fc_dist_xfmr AND code = 'T1' ORDER BY subtype_id NULLS LAST LIMIT 1;
    SELECT id INTO t_dxf_t2 FROM metadata.terminals WHERE feature_class_id = fc_dist_xfmr AND code = 'T2' ORDER BY subtype_id NULLS LAST LIMIT 1;
    IF t_dxf_t1 IS NULL OR t_dxf_t2 IS NULL THEN RAISE EXCEPTION 'DIST_XFMR terminals T1/T2 not found -- re-apply seed/connectivity_example.sql'; END IF;

    SELECT id INTO t_ptx_hv FROM metadata.terminals WHERE feature_class_id = fc_power_xfmr AND code = 'HV' AND subtype_id = st_ptx_2w_id LIMIT 1;
    SELECT id INTO t_ptx_lv FROM metadata.terminals WHERE feature_class_id = fc_power_xfmr AND code = 'LV' AND subtype_id = st_ptx_2w_id LIMIT 1;
    IF t_ptx_hv IS NULL OR t_ptx_lv IS NULL THEN RAISE EXCEPTION 'POWER_XFMR two_winding terminals HV/LV not found -- re-apply seed/power_transformer_example.sql'; END IF;

    SELECT id INTO t_bus_t1 FROM metadata.terminals WHERE feature_class_id = fc_mv_busbar AND code = 'T1' ORDER BY subtype_id NULLS LAST LIMIT 1;
    SELECT id INTO t_bus_t2 FROM metadata.terminals WHERE feature_class_id = fc_mv_busbar AND code = 'T2' ORDER BY subtype_id NULLS LAST LIMIT 1;
    IF t_bus_t1 IS NULL OR t_bus_t2 IS NULL THEN RAISE EXCEPTION 'MV_BUSBAR terminals T1/T2 not found -- re-apply seed/mv_busbar_example.sql'; END IF;

    SELECT id INTO t_rc_t1 FROM metadata.terminals WHERE feature_class_id = fc_mv_recloser AND code = 'T1' ORDER BY subtype_id NULLS LAST LIMIT 1;
    SELECT id INTO t_rc_t2 FROM metadata.terminals WHERE feature_class_id = fc_mv_recloser AND code = 'T2' ORDER BY subtype_id NULLS LAST LIMIT 1;
    IF t_rc_t1 IS NULL OR t_rc_t2 IS NULL THEN RAISE EXCEPTION 'MV_RECLOSER terminals T1/T2 not found -- re-apply seed/mv_recloser_example.sql'; END IF;

    SELECT id INTO t_sw_t1 FROM metadata.terminals WHERE feature_class_id = fc_mv_switch AND code = 'T1' ORDER BY subtype_id NULLS LAST LIMIT 1;
    SELECT id INTO t_sw_t2 FROM metadata.terminals WHERE feature_class_id = fc_mv_switch AND code = 'T2' ORDER BY subtype_id NULLS LAST LIMIT 1;
    IF t_sw_t1 IS NULL OR t_sw_t2 IS NULL THEN RAISE EXCEPTION 'MV_SWITCH terminals T1/T2 not found -- re-apply seed/mv_lv_switch_example.sql'; END IF;

    SELECT id INTO t_lvsw_t1 FROM metadata.terminals WHERE feature_class_id = fc_lv_switch AND code = 'T1' ORDER BY subtype_id NULLS LAST LIMIT 1;
    SELECT id INTO t_lvsw_t2 FROM metadata.terminals WHERE feature_class_id = fc_lv_switch AND code = 'T2' ORDER BY subtype_id NULLS LAST LIMIT 1;
    IF t_lvsw_t1 IS NULL OR t_lvsw_t2 IS NULL THEN RAISE EXCEPTION 'LV_SWITCH terminals T1/T2 not found -- re-apply seed/mv_lv_switch_example.sql'; END IF;

    SELECT id INTO t_meter_t1 FROM metadata.terminals WHERE feature_class_id = fc_meter AND code = 'T1' ORDER BY subtype_id NULLS LAST LIMIT 1;
    SELECT id INTO t_meter_t2 FROM metadata.terminals WHERE feature_class_id = fc_meter AND code = 'T2' ORDER BY subtype_id NULLS LAST LIMIT 1;
    IF t_meter_t1 IS NULL OR t_meter_t2 IS NULL THEN RAISE EXCEPTION 'METER terminals T1/T2 not found -- re-apply seed/lv_metering_example.sql'; END IF;

    SELECT id INTO t_sp_t1 FROM metadata.terminals WHERE feature_class_id = fc_sp AND code = 'T1' ORDER BY subtype_id NULLS LAST LIMIT 1;
    IF t_sp_t1 IS NULL THEN RAISE EXCEPTION 'SERVICE_POINT terminal T1 not found -- re-apply seed/service_point_example.sql'; END IF;

    -- =====================================================================
    -- 4. Guard the compiled tables + ELECTRICAL COLUMNS. A class can be
    --    compiled (its gis table exists) but be missing a field column if
    --    the field seed was applied after compile and never recompiled.
    -- =====================================================================
    IF to_regclass('gis.dist_xfmr') IS NULL THEN RAISE EXCEPTION 'table gis.dist_xfmr does not exist -- run: task compile CODE=DIST_XFMR'; END IF;
    IF to_regclass('gis.power_xfmr') IS NULL THEN RAISE EXCEPTION 'table gis.power_xfmr does not exist -- run: task compile CODE=POWER_XFMR'; END IF;
    IF to_regclass('gis.mv_busbar') IS NULL THEN RAISE EXCEPTION 'table gis.mv_busbar does not exist -- run: task compile CODE=MV_BUSBAR'; END IF;
    IF to_regclass('gis.mv_conductor') IS NULL THEN RAISE EXCEPTION 'table gis.mv_conductor does not exist -- run: task compile CODE=MV_CONDUCTOR'; END IF;
    IF to_regclass('gis.lv_conductor') IS NULL THEN RAISE EXCEPTION 'table gis.lv_conductor does not exist -- run: task compile CODE=LV_CONDUCTOR'; END IF;
    IF to_regclass('gis.mv_recloser') IS NULL THEN RAISE EXCEPTION 'table gis.mv_recloser does not exist -- run: task compile CODE=MV_RECLOSER'; END IF;
    IF to_regclass('gis.mv_switch') IS NULL THEN RAISE EXCEPTION 'table gis.mv_switch does not exist -- run: task compile CODE=MV_SWITCH'; END IF;
    IF to_regclass('gis.lv_switch') IS NULL THEN RAISE EXCEPTION 'table gis.lv_switch does not exist -- run: task compile CODE=LV_SWITCH'; END IF;
    IF to_regclass('gis.meter') IS NULL THEN RAISE EXCEPTION 'table gis.meter does not exist -- run: task compile CODE=METER'; END IF;
    IF to_regclass('gis.service_point') IS NULL THEN RAISE EXCEPTION 'table gis.service_point does not exist -- run: task compile CODE=SERVICE_POINT'; END IF;
    IF to_regclass('gis.pole') IS NULL THEN RAISE EXCEPTION 'table gis.pole does not exist -- run: task compile CODE=POLE'; END IF;

    -- DIST_XFMR: base + load-flow + transformer-model + zero-sequence columns.
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'dist_xfmr' AND column_name = 'secondary_voltage') THEN
        RAISE EXCEPTION 'gis.dist_xfmr is missing column secondary_voltage -- apply seed/transformer_model_fields.sql then: task compile CODE=DIST_XFMR'; END IF;
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'dist_xfmr' AND column_name = 'rated_kva') THEN
        RAISE EXCEPTION 'gis.dist_xfmr is missing column rated_kva -- apply seed/load_flow_fields.sql then: task compile CODE=DIST_XFMR'; END IF;
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'dist_xfmr' AND column_name = 'source_r_ohm') THEN
        RAISE EXCEPTION 'gis.dist_xfmr is missing column source_r_ohm -- apply seed/load_flow_fields.sql then: task compile CODE=DIST_XFMR'; END IF;
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'dist_xfmr' AND column_name = 'source_r0_ohm') THEN
        RAISE EXCEPTION 'gis.dist_xfmr is missing column source_r0_ohm -- apply seed/zero_seq_fields.sql then: task compile CODE=DIST_XFMR'; END IF;

    -- POWER_XFMR: base + rated_mva/impedance + vector group + zero-sequence.
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'power_xfmr' AND column_name = 'primary_voltage') THEN
        RAISE EXCEPTION 'gis.power_xfmr is missing column primary_voltage -- apply seed/power_transformer_example.sql then: task compile CODE=POWER_XFMR'; END IF;
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'power_xfmr' AND column_name = 'secondary_voltage') THEN
        RAISE EXCEPTION 'gis.power_xfmr is missing column secondary_voltage -- apply seed/power_transformer_example.sql then: task compile CODE=POWER_XFMR'; END IF;
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'power_xfmr' AND column_name = 'rated_mva') THEN
        RAISE EXCEPTION 'gis.power_xfmr is missing column rated_mva -- apply seed/power_transformer_example.sql then: task compile CODE=POWER_XFMR'; END IF;
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'power_xfmr' AND column_name = 'impedance_pct') THEN
        RAISE EXCEPTION 'gis.power_xfmr is missing column impedance_pct -- apply seed/power_transformer_example.sql then: task compile CODE=POWER_XFMR'; END IF;
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'power_xfmr' AND column_name = 'vector_group') THEN
        RAISE EXCEPTION 'gis.power_xfmr is missing column vector_group -- apply seed/power_transformer_example.sql then: task compile CODE=POWER_XFMR'; END IF;
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'power_xfmr' AND column_name = 'z0_pct') THEN
        RAISE EXCEPTION 'gis.power_xfmr is missing column z0_pct -- apply seed/zero_seq_fields.sql then: task compile CODE=POWER_XFMR'; END IF;

    -- MV_CONDUCTOR: conductor_spec (per-km impedance link).
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'mv_conductor' AND column_name = 'conductor_spec') THEN
        RAISE EXCEPTION 'gis.mv_conductor is missing column conductor_spec -- apply seed/mv_conductor_specs.sql then: task compile CODE=MV_CONDUCTOR'; END IF;

    -- SERVICE_POINT: real demand inputs.
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'service_point' AND column_name = 'demand_kw') THEN
        RAISE EXCEPTION 'gis.service_point is missing column demand_kw -- apply seed/load_flow_fields.sql then: task compile CODE=SERVICE_POINT'; END IF;
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'service_point' AND column_name = 'power_factor') THEN
        RAISE EXCEPTION 'gis.service_point is missing column power_factor -- apply seed/load_flow_fields.sql then: task compile CODE=SERVICE_POINT'; END IF;

    -- =====================================================================
    -- 5. Resolve the network_rule ids that authorize each connection.
    --    Matched on from/to feature_class + terminals (NULL-safe) with
    --    is_valid = true, so a retired rule never authorizes a connection.
    -- =====================================================================
    SELECT id INTO r_dxf_t2_mv FROM metadata.network_rules
     WHERE from_feature_class_id = fc_dist_xfmr AND to_feature_class_id = fc_mv_cond
       AND from_terminal_id IS NOT DISTINCT FROM t_dxf_t2 AND to_terminal_id IS NULL
       AND is_valid = true LIMIT 1;
    IF r_dxf_t2_mv IS NULL THEN RAISE EXCEPTION 'network_rule "DIST_XFMR T2 -> MV_CONDUCTOR" not found/valid -- apply seed/service_point_example.sql'; END IF;

    SELECT id INTO r_mv_ptx_hv FROM metadata.network_rules
     WHERE from_feature_class_id = fc_mv_cond AND to_feature_class_id = fc_power_xfmr
       AND from_terminal_id IS NULL AND to_terminal_id IS NOT DISTINCT FROM t_ptx_hv
       AND is_valid = true LIMIT 1;
    IF r_mv_ptx_hv IS NULL THEN RAISE EXCEPTION 'network_rule "MV_CONDUCTOR -> POWER_XFMR HV" not found/valid -- apply seed/power_transformer_example.sql'; END IF;

    SELECT id INTO r_ptx_lv_mv FROM metadata.network_rules
     WHERE from_feature_class_id = fc_power_xfmr AND to_feature_class_id = fc_mv_cond
       AND from_terminal_id IS NOT DISTINCT FROM t_ptx_lv AND to_terminal_id IS NULL
       AND is_valid = true LIMIT 1;
    IF r_ptx_lv_mv IS NULL THEN RAISE EXCEPTION 'network_rule "POWER_XFMR LV -> MV_CONDUCTOR" not found/valid -- apply seed/power_transformer_example.sql'; END IF;

    SELECT id INTO r_mv_bus_t1 FROM metadata.network_rules
     WHERE from_feature_class_id = fc_mv_cond AND to_feature_class_id = fc_mv_busbar
       AND from_terminal_id IS NULL AND to_terminal_id IS NOT DISTINCT FROM t_bus_t1
       AND is_valid = true LIMIT 1;
    IF r_mv_bus_t1 IS NULL THEN RAISE EXCEPTION 'network_rule "MV_CONDUCTOR -> MV_BUSBAR T1" not found/valid -- apply seed/mv_busbar_example.sql'; END IF;

    SELECT id INTO r_bus_t2_mv FROM metadata.network_rules
     WHERE from_feature_class_id = fc_mv_busbar AND to_feature_class_id = fc_mv_cond
       AND from_terminal_id IS NOT DISTINCT FROM t_bus_t2 AND to_terminal_id IS NULL
       AND is_valid = true LIMIT 1;
    IF r_bus_t2_mv IS NULL THEN RAISE EXCEPTION 'network_rule "MV_BUSBAR T2 -> MV_CONDUCTOR" not found/valid -- apply seed/mv_busbar_example.sql'; END IF;

    SELECT id INTO r_mv_rc_t1 FROM metadata.network_rules
     WHERE from_feature_class_id = fc_mv_cond AND to_feature_class_id = fc_mv_recloser
       AND from_terminal_id IS NULL AND to_terminal_id IS NOT DISTINCT FROM t_rc_t1
       AND is_valid = true LIMIT 1;
    IF r_mv_rc_t1 IS NULL THEN RAISE EXCEPTION 'network_rule "MV_CONDUCTOR -> MV_RECLOSER T1" not found/valid -- apply seed/mv_recloser_example.sql'; END IF;

    SELECT id INTO r_rc_t2_mv FROM metadata.network_rules
     WHERE from_feature_class_id = fc_mv_recloser AND to_feature_class_id = fc_mv_cond
       AND from_terminal_id IS NOT DISTINCT FROM t_rc_t2 AND to_terminal_id IS NULL
       AND is_valid = true LIMIT 1;
    IF r_rc_t2_mv IS NULL THEN RAISE EXCEPTION 'network_rule "MV_RECLOSER T2 -> MV_CONDUCTOR" not found/valid -- apply seed/mv_recloser_example.sql'; END IF;

    SELECT id INTO r_mv_sw_t1 FROM metadata.network_rules
     WHERE from_feature_class_id = fc_mv_cond AND to_feature_class_id = fc_mv_switch
       AND from_terminal_id IS NULL AND to_terminal_id IS NOT DISTINCT FROM t_sw_t1
       AND is_valid = true LIMIT 1;
    IF r_mv_sw_t1 IS NULL THEN RAISE EXCEPTION 'network_rule "MV_CONDUCTOR -> MV_SWITCH T1" not found/valid -- apply seed/mv_lv_switch_example.sql'; END IF;

    SELECT id INTO r_sw_t2_mv FROM metadata.network_rules
     WHERE from_feature_class_id = fc_mv_switch AND to_feature_class_id = fc_mv_cond
       AND from_terminal_id IS NOT DISTINCT FROM t_sw_t2 AND to_terminal_id IS NULL
       AND is_valid = true LIMIT 1;
    IF r_sw_t2_mv IS NULL THEN RAISE EXCEPTION 'network_rule "MV_SWITCH T2 -> MV_CONDUCTOR" not found/valid -- apply seed/mv_lv_switch_example.sql'; END IF;

    SELECT id INTO r_mv_dxf_t1 FROM metadata.network_rules
     WHERE from_feature_class_id = fc_mv_cond AND to_feature_class_id = fc_dist_xfmr
       AND from_terminal_id IS NULL AND to_terminal_id IS NOT DISTINCT FROM t_dxf_t1
       AND is_valid = true LIMIT 1;
    IF r_mv_dxf_t1 IS NULL THEN RAISE EXCEPTION 'network_rule "MV_CONDUCTOR -> DIST_XFMR T1" not found/valid -- apply seed/connectivity_example.sql'; END IF;

    SELECT id INTO r_dxf_t2_lv FROM metadata.network_rules
     WHERE from_feature_class_id = fc_dist_xfmr AND to_feature_class_id = fc_lv_cond
       AND from_terminal_id IS NOT DISTINCT FROM t_dxf_t2 AND to_terminal_id IS NULL
       AND is_valid = true LIMIT 1;
    IF r_dxf_t2_lv IS NULL THEN RAISE EXCEPTION 'network_rule "DIST_XFMR T2 -> LV_CONDUCTOR" not found/valid -- apply seed/lv_conductors_example.sql'; END IF;

    SELECT id INTO r_lv_lvsw_t1 FROM metadata.network_rules
     WHERE from_feature_class_id = fc_lv_cond AND to_feature_class_id = fc_lv_switch
       AND from_terminal_id IS NULL AND to_terminal_id IS NOT DISTINCT FROM t_lvsw_t1
       AND is_valid = true LIMIT 1;
    IF r_lv_lvsw_t1 IS NULL THEN RAISE EXCEPTION 'network_rule "LV_CONDUCTOR -> LV_SWITCH T1" not found/valid -- apply seed/mv_lv_switch_example.sql'; END IF;

    SELECT id INTO r_lvsw_t2_lv FROM metadata.network_rules
     WHERE from_feature_class_id = fc_lv_switch AND to_feature_class_id = fc_lv_cond
       AND from_terminal_id IS NOT DISTINCT FROM t_lvsw_t2 AND to_terminal_id IS NULL
       AND is_valid = true LIMIT 1;
    IF r_lvsw_t2_lv IS NULL THEN RAISE EXCEPTION 'network_rule "LV_SWITCH T2 -> LV_CONDUCTOR" not found/valid -- apply seed/mv_lv_switch_example.sql'; END IF;

    SELECT id INTO r_lv_meter_t1 FROM metadata.network_rules
     WHERE from_feature_class_id = fc_lv_cond AND to_feature_class_id = fc_meter
       AND from_terminal_id IS NULL AND to_terminal_id IS NOT DISTINCT FROM t_meter_t1
       AND is_valid = true LIMIT 1;
    IF r_lv_meter_t1 IS NULL THEN RAISE EXCEPTION 'network_rule "LV_CONDUCTOR -> METER T1" not found/valid -- apply seed/lv_metering_example.sql'; END IF;

    SELECT id INTO r_meter_t2_sp FROM metadata.network_rules
     WHERE from_feature_class_id = fc_meter AND to_feature_class_id = fc_sp
       AND from_terminal_id IS NOT DISTINCT FROM t_meter_t2 AND to_terminal_id IS NOT DISTINCT FROM t_sp_t1
       AND is_valid = true LIMIT 1;
    IF r_meter_t2_sp IS NULL THEN RAISE EXCEPTION 'network_rule "METER T2 -> SERVICE_POINT T1" not found/valid -- apply seed/lv_metering_example.sql'; END IF;

    -- =====================================================================
    -- 6. Insert the feature rows (status 'published' so they are live).
    --    Fixed uuids + ON CONFLICT (id) DO NOTHING => idempotent.
    -- =====================================================================

    -- GRID-SRC: 33 kV incoming-grid slack / Thevenin source. Unity ratio
    -- (secondary = primary 33000) so it acts as an ideal source bus; the
    -- source impedance IS the source_r/x Thevenin pair.
    INSERT INTO gis.dist_xfmr
        (id, name, subtype, status, geom, primary_voltage, secondary_voltage, condition_rating,
         source_r_ohm, source_x_ohm, source_r0_ohm, source_x0_ohm)
    VALUES
        ('5b510000-0000-0000-0000-000000000001', 'GRID-SRC', st_dxf_pad, 'published',
         ST_SetSRID(ST_MakePoint(-0.3000, 5.7000), 4326),
         '33KV', 33000, 'GOOD', 0.50, 5.00, 0.60, 6.00)
    ON CONFLICT (id) DO NOTHING;

    -- PTX-1: the substation power transformer, 33 kV -> 11 kV, two-winding,
    -- Dyn11 (delta HV blocks upstream zero-sequence), 15 MVA, 10% impedance,
    -- OLTC parked at tap 0.
    INSERT INTO gis.power_xfmr
        (id, name, subtype, status, geom, primary_voltage, secondary_voltage, condition_rating,
         rated_mva, impedance_pct, x_r_ratio, vector_group, z0_pct,
         tap_position, tap_step_pct, tap_min, tap_max)
    VALUES
        ('5b510000-0000-0000-0000-000000000002', 'PTX-1', st_ptx_2w, 'published',
         ST_SetSRID(ST_MakePoint(-0.2980, 5.7000), 4326),
         '33KV', 11000, 'GOOD', 15, 10, 10, 'DYN11', 10, 0, 1.25, -8, 8)
    ON CONFLICT (id) DO NOTHING;

    -- BUS-A: the 11 kV substation busbar (no subtype column exists for a
    -- subtype-less class).
    INSERT INTO gis.mv_busbar
        (id, name, status, geom, voltage_class, condition_rating)
    VALUES
        ('5b510000-0000-0000-0000-000000000003', 'BUS-A', 'published',
         ST_SetSRID(ST_MakePoint(-0.2960, 5.7000), 4326), '11KV', 'GOOD')
    ON CONFLICT (id) DO NOTHING;

    -- DTX-1 / DTX-2 / DTX-3: the real 11 kV -> 400 V step-downs with
    -- nameplate impedance (500 / 315 / 200 kVA).
    INSERT INTO gis.dist_xfmr
        (id, name, subtype, status, geom, primary_voltage, secondary_voltage, condition_rating,
         rated_kva, impedance_pct, x_r_ratio, z0_pct)
    VALUES
        ('5b510000-0000-0000-0000-000000000004', 'DTX-1', st_dxf_pad, 'published',
         ST_SetSRID(ST_MakePoint(-0.2960, 5.7060), 4326), '11KV', 400, 'GOOD', 500, 4.5, 6, 4.0),
        ('5b510000-0000-0000-0000-000000000005', 'DTX-2', st_dxf_pad, 'published',
         ST_SetSRID(ST_MakePoint(-0.2960, 5.6960), 4326), '11KV', 400, 'GOOD', 315, 4.5, 5, 4.0),
        ('5b510000-0000-0000-0000-000000000006', 'DTX-3', st_dxf_pad, 'published',
         ST_SetSRID(ST_MakePoint(-0.2958, 5.6960), 4326), '11KV', 400, 'GOOD', 200, 4.0, 5, 3.5)
    ON CONFLICT (id) DO NOTHING;

    -- RC-1: the feeder-1 recloser (closed).
    INSERT INTO gis.mv_recloser
        (id, name, subtype, status, geom, device_state, condition_rating,
         reclose_attempts, reclose_interval_s, reset_time_s)
    VALUES
        ('5b510000-0000-0000-0000-000000000007', 'RC-1', st_rc_std, 'published',
         ST_SetSRID(ST_MakePoint(-0.2960, 5.7020), 4326), 'CLOSED', 'GOOD', 3, 2, 30)
    ON CONFLICT (id) DO NOTHING;

    -- SW-1 / SW-2: closed, non-fused MV switches (one per feeder).
    INSERT INTO gis.mv_switch
        (id, name, subtype, status, geom, device_state, is_fused, condition_rating)
    VALUES
        ('5b510000-0000-0000-0000-000000000008', 'SW-1', st_sw_std, 'published',
         ST_SetSRID(ST_MakePoint(-0.2960, 5.7040), 4326), 'CLOSED', false, 'GOOD'),
        ('5b510000-0000-0000-0000-000000000009', 'SW-2', st_sw_std, 'published',
         ST_SetSRID(ST_MakePoint(-0.2960, 5.6980), 4326), 'CLOSED', false, 'GOOD')
    ON CONFLICT (id) DO NOTHING;

    -- LV-SW-1: a fused LV switch splitting the F1 service branch off the main.
    INSERT INTO gis.lv_switch
        (id, name, subtype, status, geom, device_state, is_fused, fuse_rating_a, condition_rating)
    VALUES
        ('5b510000-0000-0000-0000-00000000000a', 'LV-SW-1', st_lvsw_std, 'published',
         ST_SetSRID(ST_MakePoint(-0.2940, 5.7060), 4326), 'CLOSED', true, 100, 'GOOD')
    ON CONFLICT (id) DO NOTHING;

    -- MV conductors: HV-1 (33 kV feed) is ACSR_150; the 11 kV feeder spans are
    -- ACSR_100.
    INSERT INTO gis.mv_conductor
        (id, name, subtype, status, geom, voltage_class, condition_rating, conductor_spec)
    VALUES
        ('5b510000-0000-0000-0000-000000000011', 'HV-1', st_mv_oh, 'published',
         ST_SetSRID(ST_GeomFromText('LINESTRING(-0.3000 5.7000, -0.2980 5.7000)'), 4326),
         '33KV', 'GOOD', 'ACSR_150'),
        ('5b510000-0000-0000-0000-000000000012', 'BUS-FEED', st_mv_oh, 'published',
         ST_SetSRID(ST_GeomFromText('LINESTRING(-0.2980 5.7000, -0.2960 5.7000)'), 4326),
         '11KV', 'GOOD', 'ACSR_100'),
        ('5b510000-0000-0000-0000-000000000013', 'F1-MV-1', st_mv_oh, 'published',
         ST_SetSRID(ST_GeomFromText('LINESTRING(-0.2960 5.7000, -0.2960 5.7020)'), 4326),
         '11KV', 'GOOD', 'ACSR_100'),
        ('5b510000-0000-0000-0000-000000000014', 'F1-MV-2', st_mv_oh, 'published',
         ST_SetSRID(ST_GeomFromText('LINESTRING(-0.2960 5.7020, -0.2960 5.7040)'), 4326),
         '11KV', 'GOOD', 'ACSR_100'),
        ('5b510000-0000-0000-0000-000000000015', 'F1-MV-3', st_mv_oh, 'published',
         ST_SetSRID(ST_GeomFromText('LINESTRING(-0.2960 5.7040, -0.2960 5.7060)'), 4326),
         '11KV', 'GOOD', 'ACSR_100'),
        ('5b510000-0000-0000-0000-000000000016', 'F2-MV-1', st_mv_oh, 'published',
         ST_SetSRID(ST_GeomFromText('LINESTRING(-0.2960 5.7000, -0.2960 5.6980)'), 4326),
         '11KV', 'GOOD', 'ACSR_100'),
        ('5b510000-0000-0000-0000-000000000017', 'F2-MV-2', st_mv_oh, 'published',
         ST_SetSRID(ST_GeomFromText('LINESTRING(-0.2960 5.6980, -0.2960 5.6960)'), 4326),
         '11KV', 'GOOD', 'ACSR_100')
    ON CONFLICT (id) DO NOTHING;

    -- LV conductors: 400 V runs. F1-LV-2 is the fused service branch (ABC_95);
    -- the mains are WAVEFORM_185.
    INSERT INTO gis.lv_conductor
        (id, name, subtype, status, geom, phase, conductor_spec, lv_role)
    VALUES
        ('5b510000-0000-0000-0000-000000000021', 'F1-LV-1', st_lv_oh, 'published',
         ST_SetSRID(ST_GeomFromText('LINESTRING(-0.2960 5.7060, -0.2940 5.7060)'), 4326),
         'L1L2L3N', 'WAVEFORM_185', 'main'),
        ('5b510000-0000-0000-0000-000000000022', 'F1-LV-2', st_lv_oh, 'published',
         ST_SetSRID(ST_GeomFromText('LINESTRING(-0.2940 5.7060, -0.2920 5.7060)'), 4326),
         'L1L2L3N', 'ABC_95', 'service'),
        ('5b510000-0000-0000-0000-000000000023', 'F2-LV-1', st_lv_oh, 'published',
         ST_SetSRID(ST_GeomFromText('LINESTRING(-0.2960 5.6960, -0.2940 5.6960)'), 4326),
         'L1L2L3N', 'WAVEFORM_185', 'main'),
        ('5b510000-0000-0000-0000-000000000024', 'F2-LV-2', st_lv_oh, 'published',
         ST_SetSRID(ST_GeomFromText('LINESTRING(-0.2958 5.6960, -0.2938 5.6950)'), 4326),
         'L1L2L3N', 'WAVEFORM_185', 'main')
    ON CONFLICT (id) DO NOTHING;

    -- Meters: whole-current, one per service point (MTR-5 CT-metered for the
    -- industrial load).
    INSERT INTO gis.meter
        (id, name, subtype, status, geom, meter_serial, account_ref, phase)
    VALUES
        ('5b510000-0000-0000-0000-000000000031', 'MTR-1', st_meter_wc, 'published',
         ST_SetSRID(ST_MakePoint(-0.2941, 5.7061), 4326), 'MTR-1-0001', 'ACC-1', 'L1N'),
        ('5b510000-0000-0000-0000-000000000032', 'MTR-2', st_meter_wc, 'published',
         ST_SetSRID(ST_MakePoint(-0.2941, 5.7059), 4326), 'MTR-2-0001', 'ACC-2', 'L1L2L3N'),
        ('5b510000-0000-0000-0000-000000000033', 'MTR-3', st_meter_wc, 'published',
         ST_SetSRID(ST_MakePoint(-0.2921, 5.7060), 4326), 'MTR-3-0001', 'ACC-3', 'L1N'),
        ('5b510000-0000-0000-0000-000000000034', 'MTR-4', st_meter_wc, 'published',
         ST_SetSRID(ST_MakePoint(-0.2941, 5.6961), 4326), 'MTR-4-0001', 'ACC-4', 'L1L2L3N'),
        ('5b510000-0000-0000-0000-000000000035', 'MTR-5', st_meter_ct, 'published',
         ST_SetSRID(ST_MakePoint(-0.2941, 5.6959), 4326), 'MTR-5-0001', 'ACC-5', 'L1L2L3N'),
        ('5b510000-0000-0000-0000-000000000036', 'MTR-6', st_meter_wc, 'published',
         ST_SetSRID(ST_MakePoint(-0.2939, 5.6951), 4326), 'MTR-6-0001', 'ACC-6', 'L1N')
    ON CONFLICT (id) DO NOTHING;

    -- Service points: the six loads (residential / commercial / industrial).
    INSERT INTO gis.service_point
        (id, name, subtype, status, geom, demand_kw, power_factor)
    VALUES
        ('5b510000-0000-0000-0000-000000000041', 'SP-1', st_sp_res,  'published',
         ST_SetSRID(ST_MakePoint(-0.2942, 5.7061), 4326), 8,  0.95),
        ('5b510000-0000-0000-0000-000000000042', 'SP-2', st_sp_comm, 'published',
         ST_SetSRID(ST_MakePoint(-0.2942, 5.7059), 4326), 20, 0.92),
        ('5b510000-0000-0000-0000-000000000043', 'SP-3', st_sp_res,  'published',
         ST_SetSRID(ST_MakePoint(-0.2922, 5.7060), 4326), 6,  0.95),
        ('5b510000-0000-0000-0000-000000000044', 'SP-4', st_sp_comm, 'published',
         ST_SetSRID(ST_MakePoint(-0.2942, 5.6961), 4326), 25, 0.90),
        ('5b510000-0000-0000-0000-000000000045', 'SP-5', st_sp_ind,  'published',
         ST_SetSRID(ST_MakePoint(-0.2942, 5.6959), 4326), 40, 0.88),
        ('5b510000-0000-0000-0000-000000000046', 'SP-6', st_sp_res,  'published',
         ST_SetSRID(ST_MakePoint(-0.2940, 5.6951), 4326), 10, 0.95)
    ON CONFLICT (id) DO NOTHING;

    -- Poles: concrete distribution poles (support only, unconnected).
    INSERT INTO gis.pole
        (id, name, subtype, status, geom, pole_material, pole_use, height_class, condition_rating)
    VALUES
        ('5b510000-0000-0000-0000-000000000051', 'P-1', st_pole_conc, 'published',
         ST_SetSRID(ST_MakePoint(-0.2960, 5.7020), 4326), 'CONCRETE', 'DISTRIBUTION', 'CLASS_1', 'GOOD'),
        ('5b510000-0000-0000-0000-000000000052', 'P-2', st_pole_conc, 'published',
         ST_SetSRID(ST_MakePoint(-0.2960, 5.7040), 4326), 'CONCRETE', 'DISTRIBUTION', 'CLASS_1', 'GOOD'),
        ('5b510000-0000-0000-0000-000000000053', 'P-3', st_pole_conc, 'published',
         ST_SetSRID(ST_MakePoint(-0.2960, 5.6980), 4326), 'CONCRETE', 'DISTRIBUTION', 'CLASS_1', 'GOOD'),
        ('5b510000-0000-0000-0000-000000000054', 'P-4', st_pole_conc, 'published',
         ST_SetSRID(ST_MakePoint(-0.2960, 5.6960), 4326), 'CONCRETE', 'DISTRIBUTION', 'CLASS_1', 'GOOD')
    ON CONFLICT (id) DO NOTHING;

    -- =====================================================================
    -- 7. Insert the runtime.feature_connectivity rows (the actual graph).
    --    Edge endpoints (conductors) carry NULL terminal ids. ON CONFLICT on
    --    the (from,to)+terminals unique key => idempotent.
    -- =====================================================================

    -- --- Substation: GRID-SRC -> HV-1 -> PTX-1 -> BUS-FEED -> BUS-A ---

    -- 1. GRID-SRC.T2 -> HV-1
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_dxf_t2_mv, fc_dist_xfmr, '5b510000-0000-0000-0000-000000000001', t_dxf_t2,
         fc_mv_cond, '5b510000-0000-0000-0000-000000000011', NULL, 'seed:substation_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 2. HV-1 -> PTX-1.HV
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_mv_ptx_hv, fc_mv_cond, '5b510000-0000-0000-0000-000000000011', NULL,
         fc_power_xfmr, '5b510000-0000-0000-0000-000000000002', t_ptx_hv, 'seed:substation_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 3. PTX-1.LV -> BUS-FEED
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_ptx_lv_mv, fc_power_xfmr, '5b510000-0000-0000-0000-000000000002', t_ptx_lv,
         fc_mv_cond, '5b510000-0000-0000-0000-000000000012', NULL, 'seed:substation_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 4. BUS-FEED -> BUS-A.T1
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_mv_bus_t1, fc_mv_cond, '5b510000-0000-0000-0000-000000000012', NULL,
         fc_mv_busbar, '5b510000-0000-0000-0000-000000000003', t_bus_t1, 'seed:substation_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- --- Feeder 1: BUS-A.T2 -> F1-MV-1 -> RC-1 -> F1-MV-2 -> SW-1 -> F1-MV-3 -> DTX-1 ---

    -- 5. BUS-A.T2 -> F1-MV-1
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_bus_t2_mv, fc_mv_busbar, '5b510000-0000-0000-0000-000000000003', t_bus_t2,
         fc_mv_cond, '5b510000-0000-0000-0000-000000000013', NULL, 'seed:substation_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 6. F1-MV-1 -> RC-1.T1
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_mv_rc_t1, fc_mv_cond, '5b510000-0000-0000-0000-000000000013', NULL,
         fc_mv_recloser, '5b510000-0000-0000-0000-000000000007', t_rc_t1, 'seed:substation_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 7. RC-1.T2 -> F1-MV-2
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_rc_t2_mv, fc_mv_recloser, '5b510000-0000-0000-0000-000000000007', t_rc_t2,
         fc_mv_cond, '5b510000-0000-0000-0000-000000000014', NULL, 'seed:substation_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 8. F1-MV-2 -> SW-1.T1
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_mv_sw_t1, fc_mv_cond, '5b510000-0000-0000-0000-000000000014', NULL,
         fc_mv_switch, '5b510000-0000-0000-0000-000000000008', t_sw_t1, 'seed:substation_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 9. SW-1.T2 -> F1-MV-3
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_sw_t2_mv, fc_mv_switch, '5b510000-0000-0000-0000-000000000008', t_sw_t2,
         fc_mv_cond, '5b510000-0000-0000-0000-000000000015', NULL, 'seed:substation_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 10. F1-MV-3 -> DTX-1.T1
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_mv_dxf_t1, fc_mv_cond, '5b510000-0000-0000-0000-000000000015', NULL,
         fc_dist_xfmr, '5b510000-0000-0000-0000-000000000004', t_dxf_t1, 'seed:substation_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- --- Feeder 1 LV: DTX-1 -> F1-LV-1 -> {MTR-1, MTR-2, LV-SW-1 -> F1-LV-2 -> MTR-3} ---

    -- 11. DTX-1.T2 -> F1-LV-1
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_dxf_t2_lv, fc_dist_xfmr, '5b510000-0000-0000-0000-000000000004', t_dxf_t2,
         fc_lv_cond, '5b510000-0000-0000-0000-000000000021', NULL, 'seed:substation_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 12. F1-LV-1 -> MTR-1.T1
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_lv_meter_t1, fc_lv_cond, '5b510000-0000-0000-0000-000000000021', NULL,
         fc_meter, '5b510000-0000-0000-0000-000000000031', t_meter_t1, 'seed:substation_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 13. MTR-1.T2 -> SP-1.T1
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_meter_t2_sp, fc_meter, '5b510000-0000-0000-0000-000000000031', t_meter_t2,
         fc_sp, '5b510000-0000-0000-0000-000000000041', t_sp_t1, 'seed:substation_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 14. F1-LV-1 -> MTR-2.T1
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_lv_meter_t1, fc_lv_cond, '5b510000-0000-0000-0000-000000000021', NULL,
         fc_meter, '5b510000-0000-0000-0000-000000000032', t_meter_t1, 'seed:substation_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 15. MTR-2.T2 -> SP-2.T1
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_meter_t2_sp, fc_meter, '5b510000-0000-0000-0000-000000000032', t_meter_t2,
         fc_sp, '5b510000-0000-0000-0000-000000000042', t_sp_t1, 'seed:substation_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 16. F1-LV-1 -> LV-SW-1.T1
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_lv_lvsw_t1, fc_lv_cond, '5b510000-0000-0000-0000-000000000021', NULL,
         fc_lv_switch, '5b510000-0000-0000-0000-00000000000a', t_lvsw_t1, 'seed:substation_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 17. LV-SW-1.T2 -> F1-LV-2
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_lvsw_t2_lv, fc_lv_switch, '5b510000-0000-0000-0000-00000000000a', t_lvsw_t2,
         fc_lv_cond, '5b510000-0000-0000-0000-000000000022', NULL, 'seed:substation_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 18. F1-LV-2 -> MTR-3.T1
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_lv_meter_t1, fc_lv_cond, '5b510000-0000-0000-0000-000000000022', NULL,
         fc_meter, '5b510000-0000-0000-0000-000000000033', t_meter_t1, 'seed:substation_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 19. MTR-3.T2 -> SP-3.T1
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_meter_t2_sp, fc_meter, '5b510000-0000-0000-0000-000000000033', t_meter_t2,
         fc_sp, '5b510000-0000-0000-0000-000000000043', t_sp_t1, 'seed:substation_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- --- Feeder 2: BUS-A.T2 -> F2-MV-1 -> SW-2 -> F2-MV-2 -> {DTX-2, DTX-3} ---

    -- 20. BUS-A.T2 -> F2-MV-1
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_bus_t2_mv, fc_mv_busbar, '5b510000-0000-0000-0000-000000000003', t_bus_t2,
         fc_mv_cond, '5b510000-0000-0000-0000-000000000016', NULL, 'seed:substation_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 21. F2-MV-1 -> SW-2.T1
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_mv_sw_t1, fc_mv_cond, '5b510000-0000-0000-0000-000000000016', NULL,
         fc_mv_switch, '5b510000-0000-0000-0000-000000000009', t_sw_t1, 'seed:substation_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 22. SW-2.T2 -> F2-MV-2
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_sw_t2_mv, fc_mv_switch, '5b510000-0000-0000-0000-000000000009', t_sw_t2,
         fc_mv_cond, '5b510000-0000-0000-0000-000000000017', NULL, 'seed:substation_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 23. F2-MV-2 -> DTX-2.T1  (branch: two transformers off the same span)
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_mv_dxf_t1, fc_mv_cond, '5b510000-0000-0000-0000-000000000017', NULL,
         fc_dist_xfmr, '5b510000-0000-0000-0000-000000000005', t_dxf_t1, 'seed:substation_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 24. F2-MV-2 -> DTX-3.T1
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_mv_dxf_t1, fc_mv_cond, '5b510000-0000-0000-0000-000000000017', NULL,
         fc_dist_xfmr, '5b510000-0000-0000-0000-000000000006', t_dxf_t1, 'seed:substation_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- --- Feeder 2 LV: DTX-2 -> F2-LV-1 -> {MTR-4, MTR-5}; DTX-3 -> F2-LV-2 -> MTR-6 ---

    -- 25. DTX-2.T2 -> F2-LV-1
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_dxf_t2_lv, fc_dist_xfmr, '5b510000-0000-0000-0000-000000000005', t_dxf_t2,
         fc_lv_cond, '5b510000-0000-0000-0000-000000000023', NULL, 'seed:substation_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 26. F2-LV-1 -> MTR-4.T1
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_lv_meter_t1, fc_lv_cond, '5b510000-0000-0000-0000-000000000023', NULL,
         fc_meter, '5b510000-0000-0000-0000-000000000034', t_meter_t1, 'seed:substation_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 27. MTR-4.T2 -> SP-4.T1
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_meter_t2_sp, fc_meter, '5b510000-0000-0000-0000-000000000034', t_meter_t2,
         fc_sp, '5b510000-0000-0000-0000-000000000044', t_sp_t1, 'seed:substation_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 28. F2-LV-1 -> MTR-5.T1
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_lv_meter_t1, fc_lv_cond, '5b510000-0000-0000-0000-000000000023', NULL,
         fc_meter, '5b510000-0000-0000-0000-000000000035', t_meter_t1, 'seed:substation_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 29. MTR-5.T2 -> SP-5.T1
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_meter_t2_sp, fc_meter, '5b510000-0000-0000-0000-000000000035', t_meter_t2,
         fc_sp, '5b510000-0000-0000-0000-000000000045', t_sp_t1, 'seed:substation_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 30. DTX-3.T2 -> F2-LV-2
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_dxf_t2_lv, fc_dist_xfmr, '5b510000-0000-0000-0000-000000000006', t_dxf_t2,
         fc_lv_cond, '5b510000-0000-0000-0000-000000000024', NULL, 'seed:substation_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 31. F2-LV-2 -> MTR-6.T1
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_lv_meter_t1, fc_lv_cond, '5b510000-0000-0000-0000-000000000024', NULL,
         fc_meter, '5b510000-0000-0000-0000-000000000036', t_meter_t1, 'seed:substation_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 32. MTR-6.T2 -> SP-6.T1
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_meter_t2_sp, fc_meter, '5b510000-0000-0000-0000-000000000036', t_meter_t2,
         fc_sp, '5b510000-0000-0000-0000-000000000046', t_sp_t1, 'seed:substation_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    RAISE NOTICE 'seed/substation_network_example.sql applied: 34 features + 32 connectivity rows (idempotent).';
END $$;
