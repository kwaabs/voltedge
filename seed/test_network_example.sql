-- seed/test_network_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives
-- under /seed on purpose, same reasoning as every other seed file.
--
-- THIS IS THE FIRST SEED THAT INSERTS REAL FEATURE INSTANCES. Every other
-- seed/*.sql seeds only METADATA (feature_classes, subtypes, fields,
-- domains, terminals, network_rules -- the schema of the network). This one
-- writes actual ROWS into the compiled physical gis.* tables plus the
-- runtime.feature_connectivity rows that wire them together, so tracing,
-- load flow, and fault analysis have a small but real radial feeder to run
-- against on a fresh / CI / teammate database.
--
-- ASCII TOPOLOGY (a strict radial tree, branching only at LV-1):
--
--   SUB-SOURCE  (DIST_XFMR, 11 kV slack / Thevenin source; unity ratio)
--     | T2
--     +-- MV-1 (MV_CONDUCTOR, 11 kV)
--          | T1
--          SW-101 (MV_SWITCH, CLOSED) T2
--          |
--          +-- MV-2 (MV_CONDUCTOR, 11 kV)
--               | T1
--               TX-101 (DIST_XFMR, 11 kV -> 400 V step-down) T2
--               |
--               +-- LV-1 (LV_CONDUCTOR, 400 V)
--                    +-- MTR-A (METER) T2 --> SP-A (SERVICE_POINT, commercial, 15 kW)
--                    +-- MTR-B (METER) T2 --> SP-B (SERVICE_POINT, industrial, 30 kW)
--
--   P-1, P-2, P-3 (POLE) are physical support structures only -- network_role
--   'none', no terminals, deliberately NOT connected into the graph.
--
-- HARD PREREQUISITES -- every one of these metadata seeds must be applied AND
-- the classes it defines COMPILED (task compile CODE=...) BEFORE this seed,
-- because this seed writes into the physical gis.<class> tables the compiler
-- produces and reads the electrical field COLUMNS those seeds add:
--
--   seed/connectivity_example.sql        -> MV_CONDUCTOR, DIST_XFMR (base)
--   seed/load_flow_fields.sql            -> SERVICE_POINT.demand_kw/power_factor,
--                                           DIST_XFMR.rated_kva/source_r_ohm/source_x_ohm
--   seed/transformer_model_fields.sql    -> DIST_XFMR.secondary_voltage/impedance_pct/x_r_ratio
--   seed/zero_seq_fields.sql             -> DIST_XFMR.z0_pct/source_r0_ohm/source_x0_ohm
--   seed/mv_conductor_specs.sql          -> MV_LINE_SPEC + MV_CONDUCTOR.conductor_spec
--   seed/mv_lv_switch_example.sql        -> MV_SWITCH (+ its network_rules)
--   seed/lv_conductors_example.sql       -> LV_CONDUCTOR (+ DIST_XFMR T2 -> LV_CONDUCTOR)
--   seed/lv_metering_example.sql         -> METER (+ LV_CONDUCTOR -> METER, METER -> SERVICE_POINT)
--   seed/service_point_example.sql       -> SERVICE_POINT (+ DIST_XFMR T2 -> MV_CONDUCTOR)
--   seed/pole_example.sql                -> POLE
--
-- then, once the metadata is in place, compile each participating class:
--   task compile CODE=DIST_XFMR
--   task compile CODE=MV_SWITCH
--   task compile CODE=MV_CONDUCTOR
--   task compile CODE=LV_CONDUCTOR
--   task compile CODE=METER
--   task compile CODE=SERVICE_POINT
--   task compile CODE=POLE
--
-- The DO block below fails LOUDLY (RAISE EXCEPTION, naming the missing
-- prerequisite) if any required class, subtype, terminal, electrical column,
-- or network_rule is absent -- so a half-set-up database aborts the whole
-- transaction instead of writing a partial network. It is fully idempotent:
-- feature rows use fixed uuids with ON CONFLICT (id) DO NOTHING, and
-- connectivity rows use ON CONFLICT on the (from,to)+terminals unique key.
--
-- Apply with psql (NOT the migration runner), after the prerequisites above:
--   task seed-test-network   (or the psql -f invocation directly)
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL has
-- been reviewed statically -- a live psql apply against a fully-compiled DB +
-- a trace / load-flow / fault-current re-check are still owed to call it
-- verified.

DO $$
DECLARE
    -- feature_class ids
    fc_dist_xfmr uuid;
    fc_mv_switch uuid;
    fc_mv_cond   uuid;
    fc_lv_cond   uuid;
    fc_meter     uuid;
    fc_sp        uuid;
    fc_pole      uuid;

    -- subtype INTEGER codes (the value stored in gis.<class>.subtype)
    st_xfmr_pad  integer;  -- DIST_XFMR 'pad_mounted'
    st_sw_std    integer;  -- MV_SWITCH 'standard'
    st_meter_wc  integer;  -- METER 'whole_current'
    st_sp_comm   integer;  -- SERVICE_POINT 'commercial'
    st_sp_ind    integer;  -- SERVICE_POINT 'industrial'
    st_pole_conc integer;  -- POLE 'concrete'
    st_mv_oh     integer;  -- MV_CONDUCTOR 'overhead_mv_conductor'
    st_lv_oh     integer;  -- LV_CONDUCTOR 'overhead_lv_conductor'

    -- terminal ids
    t_xfmr_t1    uuid;
    t_xfmr_t2    uuid;
    t_sw_t1      uuid;
    t_sw_t2      uuid;
    t_meter_t1   uuid;
    t_meter_t2   uuid;
    t_sp_t1      uuid;

    -- network_rule ids
    r_xfmr_t2_mv  uuid;  -- DIST_XFMR T2 -> MV_CONDUCTOR
    r_mv_sw_t1    uuid;  -- MV_CONDUCTOR -> MV_SWITCH T1
    r_sw_t2_mv    uuid;  -- MV_SWITCH T2 -> MV_CONDUCTOR
    r_mv_xfmr_t1  uuid;  -- MV_CONDUCTOR -> DIST_XFMR T1
    r_xfmr_t2_lv  uuid;  -- DIST_XFMR T2 -> LV_CONDUCTOR
    r_lv_meter_t1 uuid;  -- LV_CONDUCTOR -> METER T1
    r_meter_t2_sp uuid;  -- METER T2 -> SERVICE_POINT T1
BEGIN
    -- =====================================================================
    -- 1. Resolve feature_class ids (fail if a class was never seeded).
    -- =====================================================================
    SELECT id INTO fc_dist_xfmr FROM metadata.feature_classes WHERE code = 'DIST_XFMR';
    IF fc_dist_xfmr IS NULL THEN RAISE EXCEPTION 'missing feature_class DIST_XFMR -- apply seed/connectivity_example.sql then: task compile CODE=DIST_XFMR'; END IF;

    SELECT id INTO fc_mv_switch FROM metadata.feature_classes WHERE code = 'MV_SWITCH';
    IF fc_mv_switch IS NULL THEN RAISE EXCEPTION 'missing feature_class MV_SWITCH -- apply seed/mv_lv_switch_example.sql then: task compile CODE=MV_SWITCH'; END IF;

    SELECT id INTO fc_mv_cond FROM metadata.feature_classes WHERE code = 'MV_CONDUCTOR';
    IF fc_mv_cond IS NULL THEN RAISE EXCEPTION 'missing feature_class MV_CONDUCTOR -- apply seed/connectivity_example.sql then: task compile CODE=MV_CONDUCTOR'; END IF;

    SELECT id INTO fc_lv_cond FROM metadata.feature_classes WHERE code = 'LV_CONDUCTOR';
    IF fc_lv_cond IS NULL THEN RAISE EXCEPTION 'missing feature_class LV_CONDUCTOR -- apply seed/lv_conductors_example.sql then: task compile CODE=LV_CONDUCTOR'; END IF;

    SELECT id INTO fc_meter FROM metadata.feature_classes WHERE code = 'METER';
    IF fc_meter IS NULL THEN RAISE EXCEPTION 'missing feature_class METER -- apply seed/lv_metering_example.sql then: task compile CODE=METER'; END IF;

    SELECT id INTO fc_sp FROM metadata.feature_classes WHERE code = 'SERVICE_POINT';
    IF fc_sp IS NULL THEN RAISE EXCEPTION 'missing feature_class SERVICE_POINT -- apply seed/service_point_example.sql then: task compile CODE=SERVICE_POINT'; END IF;

    SELECT id INTO fc_pole FROM metadata.feature_classes WHERE code = 'POLE';
    IF fc_pole IS NULL THEN RAISE EXCEPTION 'missing feature_class POLE -- apply seed/pole_example.sql then: task compile CODE=POLE'; END IF;

    -- =====================================================================
    -- 2. Resolve subtype INTEGER codes (the value stored in .subtype).
    -- =====================================================================
    SELECT code INTO st_xfmr_pad  FROM metadata.subtypes WHERE feature_class_id = fc_dist_xfmr AND name = 'pad_mounted';
    IF st_xfmr_pad IS NULL THEN RAISE EXCEPTION 'DIST_XFMR subtype ''pad_mounted'' not found -- re-apply seed/connectivity_example.sql'; END IF;

    SELECT code INTO st_sw_std    FROM metadata.subtypes WHERE feature_class_id = fc_mv_switch AND name = 'standard';
    IF st_sw_std IS NULL THEN RAISE EXCEPTION 'MV_SWITCH subtype ''standard'' not found -- re-apply seed/mv_lv_switch_example.sql'; END IF;

    SELECT code INTO st_meter_wc  FROM metadata.subtypes WHERE feature_class_id = fc_meter AND name = 'whole_current';
    IF st_meter_wc IS NULL THEN RAISE EXCEPTION 'METER subtype ''whole_current'' not found -- re-apply seed/lv_metering_example.sql'; END IF;

    SELECT code INTO st_sp_comm   FROM metadata.subtypes WHERE feature_class_id = fc_sp AND name = 'commercial';
    IF st_sp_comm IS NULL THEN RAISE EXCEPTION 'SERVICE_POINT subtype ''commercial'' not found -- re-apply seed/service_point_example.sql'; END IF;

    SELECT code INTO st_sp_ind    FROM metadata.subtypes WHERE feature_class_id = fc_sp AND name = 'industrial';
    IF st_sp_ind IS NULL THEN RAISE EXCEPTION 'SERVICE_POINT subtype ''industrial'' not found -- re-apply seed/service_point_example.sql'; END IF;

    SELECT code INTO st_pole_conc FROM metadata.subtypes WHERE feature_class_id = fc_pole AND name = 'concrete';
    IF st_pole_conc IS NULL THEN RAISE EXCEPTION 'POLE subtype ''concrete'' not found -- re-apply seed/pole_example.sql'; END IF;

    SELECT code INTO st_mv_oh     FROM metadata.subtypes WHERE feature_class_id = fc_mv_cond AND name = 'overhead_mv_conductor';
    IF st_mv_oh IS NULL THEN RAISE EXCEPTION 'MV_CONDUCTOR subtype ''overhead_mv_conductor'' not found -- re-apply seed/connectivity_example.sql'; END IF;

    SELECT code INTO st_lv_oh     FROM metadata.subtypes WHERE feature_class_id = fc_lv_cond AND name = 'overhead_lv_conductor';
    IF st_lv_oh IS NULL THEN RAISE EXCEPTION 'LV_CONDUCTOR subtype ''overhead_lv_conductor'' not found -- re-apply seed/lv_conductors_example.sql'; END IF;

    -- =====================================================================
    -- 3. Resolve terminal ids (all subtype-agnostic here: subtype_id NULL).
    -- =====================================================================
    SELECT id INTO t_xfmr_t1  FROM metadata.terminals WHERE feature_class_id = fc_dist_xfmr AND code = 'T1' ORDER BY subtype_id NULLS LAST LIMIT 1;
    SELECT id INTO t_xfmr_t2  FROM metadata.terminals WHERE feature_class_id = fc_dist_xfmr AND code = 'T2' ORDER BY subtype_id NULLS LAST LIMIT 1;
    IF t_xfmr_t1 IS NULL OR t_xfmr_t2 IS NULL THEN RAISE EXCEPTION 'DIST_XFMR terminals T1/T2 not found -- re-apply seed/connectivity_example.sql'; END IF;

    SELECT id INTO t_sw_t1    FROM metadata.terminals WHERE feature_class_id = fc_mv_switch AND code = 'T1' ORDER BY subtype_id NULLS LAST LIMIT 1;
    SELECT id INTO t_sw_t2    FROM metadata.terminals WHERE feature_class_id = fc_mv_switch AND code = 'T2' ORDER BY subtype_id NULLS LAST LIMIT 1;
    IF t_sw_t1 IS NULL OR t_sw_t2 IS NULL THEN RAISE EXCEPTION 'MV_SWITCH terminals T1/T2 not found -- re-apply seed/mv_lv_switch_example.sql'; END IF;

    SELECT id INTO t_meter_t1 FROM metadata.terminals WHERE feature_class_id = fc_meter AND code = 'T1' ORDER BY subtype_id NULLS LAST LIMIT 1;
    SELECT id INTO t_meter_t2 FROM metadata.terminals WHERE feature_class_id = fc_meter AND code = 'T2' ORDER BY subtype_id NULLS LAST LIMIT 1;
    IF t_meter_t1 IS NULL OR t_meter_t2 IS NULL THEN RAISE EXCEPTION 'METER terminals T1/T2 not found -- re-apply seed/lv_metering_example.sql'; END IF;

    SELECT id INTO t_sp_t1    FROM metadata.terminals WHERE feature_class_id = fc_sp AND code = 'T1' ORDER BY subtype_id NULLS LAST LIMIT 1;
    IF t_sp_t1 IS NULL THEN RAISE EXCEPTION 'SERVICE_POINT terminal T1 not found -- re-apply seed/service_point_example.sql'; END IF;

    -- =====================================================================
    -- 4. Guard the compiled ELECTRICAL COLUMNS. A class can be compiled
    --    (its gis table exists) but be missing a field column if the field
    --    seed was applied after compile and never recompiled. Check the
    --    columns this seed writes and RAISE an actionable message.
    -- =====================================================================
    IF to_regclass('gis.dist_xfmr') IS NULL THEN RAISE EXCEPTION 'table gis.dist_xfmr does not exist -- run: task compile CODE=DIST_XFMR'; END IF;
    IF to_regclass('gis.mv_switch') IS NULL THEN RAISE EXCEPTION 'table gis.mv_switch does not exist -- run: task compile CODE=MV_SWITCH'; END IF;
    IF to_regclass('gis.mv_conductor') IS NULL THEN RAISE EXCEPTION 'table gis.mv_conductor does not exist -- run: task compile CODE=MV_CONDUCTOR'; END IF;
    IF to_regclass('gis.lv_conductor') IS NULL THEN RAISE EXCEPTION 'table gis.lv_conductor does not exist -- run: task compile CODE=LV_CONDUCTOR'; END IF;
    IF to_regclass('gis.meter') IS NULL THEN RAISE EXCEPTION 'table gis.meter does not exist -- run: task compile CODE=METER'; END IF;
    IF to_regclass('gis.service_point') IS NULL THEN RAISE EXCEPTION 'table gis.service_point does not exist -- run: task compile CODE=SERVICE_POINT'; END IF;
    IF to_regclass('gis.pole') IS NULL THEN RAISE EXCEPTION 'table gis.pole does not exist -- run: task compile CODE=POLE'; END IF;

    -- DIST_XFMR: base + load-flow + transformer-model + zero-sequence columns.
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'dist_xfmr' AND column_name = 'primary_voltage') THEN
        RAISE EXCEPTION 'gis.dist_xfmr is missing column primary_voltage -- apply seed/connectivity_example.sql then: task compile CODE=DIST_XFMR'; END IF;
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'dist_xfmr' AND column_name = 'secondary_voltage') THEN
        RAISE EXCEPTION 'gis.dist_xfmr is missing column secondary_voltage -- apply seed/transformer_model_fields.sql then: task compile CODE=DIST_XFMR'; END IF;
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'dist_xfmr' AND column_name = 'rated_kva') THEN
        RAISE EXCEPTION 'gis.dist_xfmr is missing column rated_kva -- apply seed/load_flow_fields.sql then: task compile CODE=DIST_XFMR'; END IF;
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'dist_xfmr' AND column_name = 'source_r_ohm') THEN
        RAISE EXCEPTION 'gis.dist_xfmr is missing column source_r_ohm -- apply seed/load_flow_fields.sql then: task compile CODE=DIST_XFMR'; END IF;
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'dist_xfmr' AND column_name = 'impedance_pct') THEN
        RAISE EXCEPTION 'gis.dist_xfmr is missing column impedance_pct -- apply seed/transformer_model_fields.sql then: task compile CODE=DIST_XFMR'; END IF;
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'dist_xfmr' AND column_name = 'x_r_ratio') THEN
        RAISE EXCEPTION 'gis.dist_xfmr is missing column x_r_ratio -- apply seed/transformer_model_fields.sql then: task compile CODE=DIST_XFMR'; END IF;
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'dist_xfmr' AND column_name = 'z0_pct') THEN
        RAISE EXCEPTION 'gis.dist_xfmr is missing column z0_pct -- apply seed/zero_seq_fields.sql then: task compile CODE=DIST_XFMR'; END IF;
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'dist_xfmr' AND column_name = 'source_r0_ohm') THEN
        RAISE EXCEPTION 'gis.dist_xfmr is missing column source_r0_ohm -- apply seed/zero_seq_fields.sql then: task compile CODE=DIST_XFMR'; END IF;
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'dist_xfmr' AND column_name = 'source_x0_ohm') THEN
        RAISE EXCEPTION 'gis.dist_xfmr is missing column source_x0_ohm -- apply seed/zero_seq_fields.sql then: task compile CODE=DIST_XFMR'; END IF;

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
    SELECT id INTO r_xfmr_t2_mv FROM metadata.network_rules
     WHERE from_feature_class_id = fc_dist_xfmr AND to_feature_class_id = fc_mv_cond
       AND from_terminal_id IS NOT DISTINCT FROM t_xfmr_t2 AND to_terminal_id IS NULL
       AND is_valid = true LIMIT 1;
    IF r_xfmr_t2_mv IS NULL THEN RAISE EXCEPTION 'network_rule "DIST_XFMR T2 -> MV_CONDUCTOR" not found/valid -- apply seed/service_point_example.sql'; END IF;

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

    SELECT id INTO r_mv_xfmr_t1 FROM metadata.network_rules
     WHERE from_feature_class_id = fc_mv_cond AND to_feature_class_id = fc_dist_xfmr
       AND from_terminal_id IS NULL AND to_terminal_id IS NOT DISTINCT FROM t_xfmr_t1
       AND is_valid = true LIMIT 1;
    IF r_mv_xfmr_t1 IS NULL THEN RAISE EXCEPTION 'network_rule "MV_CONDUCTOR -> DIST_XFMR T1" not found/valid -- apply seed/connectivity_example.sql'; END IF;

    SELECT id INTO r_xfmr_t2_lv FROM metadata.network_rules
     WHERE from_feature_class_id = fc_dist_xfmr AND to_feature_class_id = fc_lv_cond
       AND from_terminal_id IS NOT DISTINCT FROM t_xfmr_t2 AND to_terminal_id IS NULL
       AND is_valid = true LIMIT 1;
    IF r_xfmr_t2_lv IS NULL THEN RAISE EXCEPTION 'network_rule "DIST_XFMR T2 -> LV_CONDUCTOR" not found/valid -- apply seed/lv_conductors_example.sql'; END IF;

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

    -- SUB-SOURCE: 11 kV slack / Thevenin source. Unity ratio (secondary =
    -- primary 11000) so it acts as an ideal source bus; rated_kva/impedance_pct
    -- LEFT NULL -- the source impedance IS the source_r/x Thevenin pair.
    INSERT INTO gis.dist_xfmr
        (id, name, subtype, status, geom, primary_voltage, secondary_voltage, condition_rating,
         source_r_ohm, source_x_ohm, source_r0_ohm, source_x0_ohm)
    VALUES
        ('feed0000-0000-0000-0000-000000000001', 'SUB-SOURCE', st_xfmr_pad, 'published',
         ST_SetSRID(ST_MakePoint(-0.2000, 5.6000), 4326),
         '11KV', 11000, 'GOOD', 0.048, 0.48, 0.06, 0.60)
    ON CONFLICT (id) DO NOTHING;

    -- SW-101: closed, non-fused MV switch on the feeder.
    INSERT INTO gis.mv_switch
        (id, name, subtype, status, geom, device_state, is_fused, condition_rating)
    VALUES
        ('feed0000-0000-0000-0000-000000000002', 'SW-101', st_sw_std, 'published',
         ST_SetSRID(ST_MakePoint(-0.1980, 5.6000), 4326),
         'CLOSED', false, 'GOOD')
    ON CONFLICT (id) DO NOTHING;

    -- TX-101: the real 11 kV -> 400 V step-down with nameplate impedance.
    INSERT INTO gis.dist_xfmr
        (id, name, subtype, status, geom, primary_voltage, secondary_voltage, condition_rating,
         rated_kva, impedance_pct, x_r_ratio, z0_pct)
    VALUES
        ('feed0000-0000-0000-0000-000000000003', 'TX-101', st_xfmr_pad, 'published',
         ST_SetSRID(ST_MakePoint(-0.1960, 5.6000), 4326),
         '11KV', 400, 'GOOD', 500, 4.5, 6, 4.0)
    ON CONFLICT (id) DO NOTHING;

    -- MTR-A / MTR-B: whole-current meters, one per service point.
    INSERT INTO gis.meter
        (id, name, subtype, status, geom, meter_serial, account_ref, phase)
    VALUES
        ('feed0000-0000-0000-0000-000000000004', 'MTR-A', st_meter_wc, 'published',
         ST_SetSRID(ST_MakePoint(-0.1961, 5.5990), 4326), 'MTR-A-0001', 'ACC-A', 'L1L2L3N')
    ON CONFLICT (id) DO NOTHING;

    INSERT INTO gis.meter
        (id, name, subtype, status, geom, meter_serial, account_ref, phase)
    VALUES
        ('feed0000-0000-0000-0000-000000000005', 'MTR-B', st_meter_wc, 'published',
         ST_SetSRID(ST_MakePoint(-0.1959, 5.5990), 4326), 'MTR-B-0001', 'ACC-B', 'L1L2L3N')
    ON CONFLICT (id) DO NOTHING;

    -- SP-A / SP-B: the two loads (commercial 15 kW, industrial 30 kW).
    INSERT INTO gis.service_point
        (id, name, subtype, status, geom, demand_kw, power_factor)
    VALUES
        ('feed0000-0000-0000-0000-000000000006', 'SP-A', st_sp_comm, 'published',
         ST_SetSRID(ST_MakePoint(-0.1962, 5.5990), 4326), 15, 0.95)
    ON CONFLICT (id) DO NOTHING;

    INSERT INTO gis.service_point
        (id, name, subtype, status, geom, demand_kw, power_factor)
    VALUES
        ('feed0000-0000-0000-0000-000000000007', 'SP-B', st_sp_ind, 'published',
         ST_SetSRID(ST_MakePoint(-0.1959, 5.5989), 4326), 30, 0.90)
    ON CONFLICT (id) DO NOTHING;

    -- P-1 / P-2 / P-3: concrete distribution poles (support only, unconnected).
    INSERT INTO gis.pole
        (id, name, subtype, status, geom, pole_material, pole_use, height_class, condition_rating)
    VALUES
        ('feed0000-0000-0000-0000-000000000008', 'P-1', st_pole_conc, 'published',
         ST_SetSRID(ST_MakePoint(-0.1990, 5.6000), 4326), 'CONCRETE', 'DISTRIBUTION', 'CLASS_1', 'GOOD')
    ON CONFLICT (id) DO NOTHING;

    INSERT INTO gis.pole
        (id, name, subtype, status, geom, pole_material, pole_use, height_class, condition_rating)
    VALUES
        ('feed0000-0000-0000-0000-000000000009', 'P-2', st_pole_conc, 'published',
         ST_SetSRID(ST_MakePoint(-0.1970, 5.6000), 4326), 'CONCRETE', 'DISTRIBUTION', 'CLASS_1', 'GOOD')
    ON CONFLICT (id) DO NOTHING;

    INSERT INTO gis.pole
        (id, name, subtype, status, geom, pole_material, pole_use, height_class, condition_rating)
    VALUES
        ('feed0000-0000-0000-0000-00000000000a', 'P-3', st_pole_conc, 'published',
         ST_SetSRID(ST_MakePoint(-0.1960, 5.6001), 4326), 'CONCRETE', 'DISTRIBUTION', 'CLASS_1', 'GOOD')
    ON CONFLICT (id) DO NOTHING;

    -- MV-1 / MV-2: the 11 kV overhead conductor spans.
    INSERT INTO gis.mv_conductor
        (id, name, subtype, status, geom, voltage_class, condition_rating, conductor_spec)
    VALUES
        ('feed0000-0000-0000-0000-000000000011', 'MV-1', st_mv_oh, 'published',
         ST_SetSRID(ST_GeomFromText('LINESTRING(-0.2000 5.6000, -0.1980 5.6000)'), 4326),
         '11KV', 'GOOD', 'ACSR_100')
    ON CONFLICT (id) DO NOTHING;

    INSERT INTO gis.mv_conductor
        (id, name, subtype, status, geom, voltage_class, condition_rating, conductor_spec)
    VALUES
        ('feed0000-0000-0000-0000-000000000012', 'MV-2', st_mv_oh, 'published',
         ST_SetSRID(ST_GeomFromText('LINESTRING(-0.1980 5.6000, -0.1960 5.6000)'), 4326),
         '11KV', 'GOOD', 'ACSR_100')
    ON CONFLICT (id) DO NOTHING;

    -- LV-1: the 400 V run feeding both meters (branch point of the tree).
    INSERT INTO gis.lv_conductor
        (id, name, subtype, status, geom, phase, conductor_spec, lv_role)
    VALUES
        ('feed0000-0000-0000-0000-000000000013', 'LV-1', st_lv_oh, 'published',
         ST_SetSRID(ST_GeomFromText('LINESTRING(-0.1960 5.6000, -0.1960 5.5990)'), 4326),
         'L1L2L3N', 'WAVEFORM_185', 'main')
    ON CONFLICT (id) DO NOTHING;

    -- =====================================================================
    -- 7. Insert the runtime.feature_connectivity rows (the actual graph).
    --    Edge endpoints carry NULL terminal ids. ON CONFLICT on the
    --    (from,to)+terminals unique key => idempotent.
    -- =====================================================================

    -- 1. SUB-SOURCE.T2 -> MV-1
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_xfmr_t2_mv, fc_dist_xfmr, 'feed0000-0000-0000-0000-000000000001', t_xfmr_t2,
         fc_mv_cond, 'feed0000-0000-0000-0000-000000000011', NULL, 'seed:test_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 2. MV-1 -> SW-101.T1
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_mv_sw_t1, fc_mv_cond, 'feed0000-0000-0000-0000-000000000011', NULL,
         fc_mv_switch, 'feed0000-0000-0000-0000-000000000002', t_sw_t1, 'seed:test_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 3. SW-101.T2 -> MV-2
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_sw_t2_mv, fc_mv_switch, 'feed0000-0000-0000-0000-000000000002', t_sw_t2,
         fc_mv_cond, 'feed0000-0000-0000-0000-000000000012', NULL, 'seed:test_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 4. MV-2 -> TX-101.T1
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_mv_xfmr_t1, fc_mv_cond, 'feed0000-0000-0000-0000-000000000012', NULL,
         fc_dist_xfmr, 'feed0000-0000-0000-0000-000000000003', t_xfmr_t1, 'seed:test_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 5. TX-101.T2 -> LV-1
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_xfmr_t2_lv, fc_dist_xfmr, 'feed0000-0000-0000-0000-000000000003', t_xfmr_t2,
         fc_lv_cond, 'feed0000-0000-0000-0000-000000000013', NULL, 'seed:test_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 6. LV-1 -> MTR-A.T1
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_lv_meter_t1, fc_lv_cond, 'feed0000-0000-0000-0000-000000000013', NULL,
         fc_meter, 'feed0000-0000-0000-0000-000000000004', t_meter_t1, 'seed:test_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 7. MTR-A.T2 -> SP-A.T1
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_meter_t2_sp, fc_meter, 'feed0000-0000-0000-0000-000000000004', t_meter_t2,
         fc_sp, 'feed0000-0000-0000-0000-000000000006', t_sp_t1, 'seed:test_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 8. LV-1 -> MTR-B.T1  (second branch off the same LV run / terminal)
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_lv_meter_t1, fc_lv_cond, 'feed0000-0000-0000-0000-000000000013', NULL,
         fc_meter, 'feed0000-0000-0000-0000-000000000005', t_meter_t1, 'seed:test_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 9. MTR-B.T2 -> SP-B.T1
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_meter_t2_sp, fc_meter, 'feed0000-0000-0000-0000-000000000005', t_meter_t2,
         fc_sp, 'feed0000-0000-0000-0000-000000000007', t_sp_t1, 'seed:test_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    RAISE NOTICE 'seed/test_network_example.sql applied: 13 features + 9 connectivity rows (idempotent).';
END $$;
