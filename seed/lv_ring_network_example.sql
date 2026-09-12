-- seed/lv_ring_network_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations; this lives under
-- /seed on purpose, same as every other seed file.
--
-- THE FIRST SEED THAT ENCODES A CLOSED LV LOOP. Every other instance seed
-- (test_network_example, substation_network_example, lv_metering_example) wires
-- a strict radial tree, so the radial sweep's buildTree never sees a second path
-- to a node and the Phase 51 Tier 1 meshed-load-flow compensation has nothing to
-- run against. This seed builds a minimal weakly-meshed LV ring -- ONE closed
-- loop -- with a HAND-COMPUTABLE expected circulating split, so a live
-- POST /analysis/meshed-load-flow can be checked against arithmetic, not vibes.
--
-- ASCII TOPOLOGY (one closed LV ring; the ONLY non-radial edge is LVB->MTR-R):
--
--   RING-SRC  (DIST_XFMR, 11 kV slack / Thevenin source; unity ratio)
--     | T2
--     +-- MV-R (MV_CONDUCTOR, 11 kV)
--          | T1
--          TX-R (DIST_XFMR, 11 kV -> 400 V step-down) T2
--          |
--          +-- LVA (LV_CONDUCTOR, 400 V, ~100 m)  --+
--          +-- LVB (LV_CONDUCTOR, 400 V, ~200 m)  --+--> MTR-R (METER) T2
--                     (LVA and LVB both feed MTR-R.T1 -> the closed ring)  |
--                                                                          v
--                                                          SP-R (SERVICE_POINT, 30 kW, pf 1.0)
--
-- Both LVA and LVB run TX-R.T2 -> MTR-R.T1. buildTree walks the radial spanning
-- tree (LVA reaches MTR-R first, becoming its parent) and records the second
-- arrival -- LVB -> MTR-R -- as a meshBreak{closer: LVB, existing: MTR-R}. The
-- loop's lowest common ancestor is TX-R, so the loop is purely the two LV legs
-- (segA = [MTR-R, LVA], segB = [LVB]); the transformer + MV drop is common to
-- both legs and cancels in the break-point voltage mismatch.
--
-- THE HAND CHECK (why this seed exists):
--   * SP-R draws 30 kW at unity power factor, so Q = 0 and the loop load power
--     factor k = 0: the circulating power is purely real (cQ = 0).
--   * LVA and LVB share one conductor_spec (WAVEFORM_185); LVB's geometry is
--     EXACTLY twice LVA's length (both are pure north-south spans, 0.0018 deg vs
--     0.0009 deg of latitude), so R_LVB = 2 * R_LVA and the loop resistance is
--     R_LVA + R_LVB = 3 * R_LVA. (X is irrelevant here -- with k = 0 and Q = 0 it
--     enters neither the drop nor the solve; it is only reported.)
--   * With k = 0 the compensation split is exact inverse-resistance:
--         LVA carries  P * R_LVB / (R_LVA + R_LVB) = 30 * 2/3 = 20 kW
--         LVB carries  P * R_LVA / (R_LVA + R_LVB) = 30 * 1/3 = 10 kW
--     i.e. the circulating real power injected onto LVB is cP = 10 kW, and the
--     residual break-point mismatch converges below meshConvergedV (1e-4 V) in
--     one or two passes. The 2:1 asymmetry (not a symmetric 50/50) also proves
--     the segA/segB sign convention, not just convergence.
--
--   So a correct meshed-load-flow response for start = RING-SRC must show:
--     loops[0].compensated              = true
--     loops[0].impedance_resolved       = true
--     loops[0].loop_resistance_ohm      = 3 * R_LVA  (LVA + LVB)
--     abs(loops[0].residual_mismatch_v) < 1e-4
--     branch loading on LVA ~ twice that on LVB (20 kW vs 10 kW share)
--   and the plain radial /load-flow on the same start would instead refuse
--   (buildTree invalid(...)) because the ring has no unique source->node path.
--
-- HARD PREREQUISITES -- every metadata seed below must be applied AND the
-- classes COMPILED (task compile CODE=...) BEFORE this seed, because it writes
-- into the physical gis.<class> tables and reads their electrical columns:
--
--   seed/connectivity_example.sql        -> MV_CONDUCTOR, DIST_XFMR (base)
--   seed/load_flow_fields.sql            -> SERVICE_POINT.demand_kw/power_factor,
--                                           DIST_XFMR.rated_kva/source_r_ohm/source_x_ohm
--   seed/transformer_model_fields.sql    -> DIST_XFMR.secondary_voltage/impedance_pct/x_r_ratio
--   seed/mv_conductor_specs.sql          -> MV_LINE_SPEC + MV_CONDUCTOR.conductor_spec
--   seed/lv_conductors_example.sql       -> LV_CONDUCTOR (+ DIST_XFMR T2 -> LV_CONDUCTOR) + conductor_spec
--   seed/lv_metering_example.sql         -> METER (+ LV_CONDUCTOR -> METER, METER -> SERVICE_POINT)
--   seed/service_point_example.sql       -> SERVICE_POINT (+ DIST_XFMR T2 -> MV_CONDUCTOR)
--
-- then compile each participating class:
--   task compile CODE=DIST_XFMR
--   task compile CODE=MV_CONDUCTOR
--   task compile CODE=LV_CONDUCTOR
--   task compile CODE=METER
--   task compile CODE=SERVICE_POINT
--
-- The DO block fails LOUDLY (RAISE EXCEPTION naming the missing prerequisite) if
-- any class, subtype, terminal, electrical column, or network_rule is absent, so
-- a half-set-up database aborts instead of writing a partial network. Fully
-- idempotent: fixed uuids with ON CONFLICT (id) DO NOTHING for features, and the
-- (from,to)+terminals unique key for connectivity.
--
-- Apply with psql (NOT the migration runner), after the prerequisites above:
--   task seed-lv-ring-network   (or the psql -f invocation directly)
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL is
-- reviewed statically -- a live psql apply against a fully-compiled DB and a
-- meshed-load-flow re-check against the hand numbers above are still owed to
-- call it verified.

DO $$
DECLARE
    -- feature_class ids
    fc_dist_xfmr uuid;
    fc_mv_cond   uuid;
    fc_lv_cond   uuid;
    fc_meter     uuid;
    fc_sp        uuid;

    -- subtype INTEGER codes (the value stored in gis.<class>.subtype)
    st_xfmr_pad  integer;  -- DIST_XFMR 'pad_mounted'
    st_meter_wc  integer;  -- METER 'whole_current'
    st_sp_comm   integer;  -- SERVICE_POINT 'commercial'
    st_mv_oh     integer;  -- MV_CONDUCTOR 'overhead_mv_conductor'
    st_lv_oh     integer;  -- LV_CONDUCTOR 'overhead_lv_conductor'

    -- terminal ids
    t_xfmr_t1    uuid;
    t_xfmr_t2    uuid;
    t_meter_t1   uuid;
    t_meter_t2   uuid;
    t_sp_t1      uuid;

    -- network_rule ids
    r_xfmr_t2_mv  uuid;  -- DIST_XFMR T2 -> MV_CONDUCTOR
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

    SELECT id INTO fc_mv_cond FROM metadata.feature_classes WHERE code = 'MV_CONDUCTOR';
    IF fc_mv_cond IS NULL THEN RAISE EXCEPTION 'missing feature_class MV_CONDUCTOR -- apply seed/connectivity_example.sql then: task compile CODE=MV_CONDUCTOR'; END IF;

    SELECT id INTO fc_lv_cond FROM metadata.feature_classes WHERE code = 'LV_CONDUCTOR';
    IF fc_lv_cond IS NULL THEN RAISE EXCEPTION 'missing feature_class LV_CONDUCTOR -- apply seed/lv_conductors_example.sql then: task compile CODE=LV_CONDUCTOR'; END IF;

    SELECT id INTO fc_meter FROM metadata.feature_classes WHERE code = 'METER';
    IF fc_meter IS NULL THEN RAISE EXCEPTION 'missing feature_class METER -- apply seed/lv_metering_example.sql then: task compile CODE=METER'; END IF;

    SELECT id INTO fc_sp FROM metadata.feature_classes WHERE code = 'SERVICE_POINT';
    IF fc_sp IS NULL THEN RAISE EXCEPTION 'missing feature_class SERVICE_POINT -- apply seed/service_point_example.sql then: task compile CODE=SERVICE_POINT'; END IF;

    -- =====================================================================
    -- 2. Resolve subtype INTEGER codes (the value stored in .subtype).
    -- =====================================================================
    SELECT code INTO st_xfmr_pad  FROM metadata.subtypes WHERE feature_class_id = fc_dist_xfmr AND name = 'pad_mounted';
    IF st_xfmr_pad IS NULL THEN RAISE EXCEPTION 'DIST_XFMR subtype ''pad_mounted'' not found -- re-apply seed/connectivity_example.sql'; END IF;

    SELECT code INTO st_meter_wc  FROM metadata.subtypes WHERE feature_class_id = fc_meter AND name = 'whole_current';
    IF st_meter_wc IS NULL THEN RAISE EXCEPTION 'METER subtype ''whole_current'' not found -- re-apply seed/lv_metering_example.sql'; END IF;

    SELECT code INTO st_sp_comm   FROM metadata.subtypes WHERE feature_class_id = fc_sp AND name = 'commercial';
    IF st_sp_comm IS NULL THEN RAISE EXCEPTION 'SERVICE_POINT subtype ''commercial'' not found -- re-apply seed/service_point_example.sql'; END IF;

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

    SELECT id INTO t_meter_t1 FROM metadata.terminals WHERE feature_class_id = fc_meter AND code = 'T1' ORDER BY subtype_id NULLS LAST LIMIT 1;
    SELECT id INTO t_meter_t2 FROM metadata.terminals WHERE feature_class_id = fc_meter AND code = 'T2' ORDER BY subtype_id NULLS LAST LIMIT 1;
    IF t_meter_t1 IS NULL OR t_meter_t2 IS NULL THEN RAISE EXCEPTION 'METER terminals T1/T2 not found -- re-apply seed/lv_metering_example.sql'; END IF;

    SELECT id INTO t_sp_t1    FROM metadata.terminals WHERE feature_class_id = fc_sp AND code = 'T1' ORDER BY subtype_id NULLS LAST LIMIT 1;
    IF t_sp_t1 IS NULL THEN RAISE EXCEPTION 'SERVICE_POINT terminal T1 not found -- re-apply seed/service_point_example.sql'; END IF;

    -- =====================================================================
    -- 4. Guard the compiled ELECTRICAL COLUMNS this seed writes / the engine
    --    reads. A class can be compiled but missing a field column if the
    --    field seed was applied after compile and never recompiled.
    -- =====================================================================
    IF to_regclass('gis.dist_xfmr') IS NULL THEN RAISE EXCEPTION 'table gis.dist_xfmr does not exist -- run: task compile CODE=DIST_XFMR'; END IF;
    IF to_regclass('gis.mv_conductor') IS NULL THEN RAISE EXCEPTION 'table gis.mv_conductor does not exist -- run: task compile CODE=MV_CONDUCTOR'; END IF;
    IF to_regclass('gis.lv_conductor') IS NULL THEN RAISE EXCEPTION 'table gis.lv_conductor does not exist -- run: task compile CODE=LV_CONDUCTOR'; END IF;
    IF to_regclass('gis.meter') IS NULL THEN RAISE EXCEPTION 'table gis.meter does not exist -- run: task compile CODE=METER'; END IF;
    IF to_regclass('gis.service_point') IS NULL THEN RAISE EXCEPTION 'table gis.service_point does not exist -- run: task compile CODE=SERVICE_POINT'; END IF;

    -- DIST_XFMR: base + load-flow + transformer-model columns.
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

    -- MV_CONDUCTOR + LV_CONDUCTOR: conductor_spec (per-km impedance link).
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'mv_conductor' AND column_name = 'conductor_spec') THEN
        RAISE EXCEPTION 'gis.mv_conductor is missing column conductor_spec -- apply seed/mv_conductor_specs.sql then: task compile CODE=MV_CONDUCTOR'; END IF;
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'lv_conductor' AND column_name = 'conductor_spec') THEN
        RAISE EXCEPTION 'gis.lv_conductor is missing column conductor_spec -- apply seed/lv_conductors_example.sql then: task compile CODE=LV_CONDUCTOR'; END IF;

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
    --    Fixed uuids (prefix c105ed00 = "closed") + ON CONFLICT (id) DO
    --    NOTHING => idempotent, and disjoint from test_network_example's
    --    feed0000 prefix so both seeds can coexist.
    -- =====================================================================

    -- RING-SRC: 11 kV slack / Thevenin source. Unity ratio (secondary = primary
    -- 11000) so it acts as an ideal source bus; the source impedance IS the
    -- source_r/x Thevenin pair. Common to both loop legs (above the LCA).
    INSERT INTO gis.dist_xfmr
        (id, name, subtype, status, geom, primary_voltage, secondary_voltage, condition_rating,
         source_r_ohm, source_x_ohm)
    VALUES
        ('c105ed00-0000-0000-0000-000000000001', 'RING-SRC', st_xfmr_pad, 'published',
         ST_SetSRID(ST_MakePoint(-0.2000, 5.6010), 4326),
         '11KV', 11000, 'GOOD', 0.048, 0.48)
    ON CONFLICT (id) DO NOTHING;

    -- TX-R: the real 11 kV -> 400 V step-down with nameplate impedance. Its
    -- series drop is common to both loop legs (it is the loop's LCA) and so
    -- cancels in the break-point mismatch -- the ring math is pure LV.
    INSERT INTO gis.dist_xfmr
        (id, name, subtype, status, geom, primary_voltage, secondary_voltage, condition_rating,
         rated_kva, impedance_pct, x_r_ratio)
    VALUES
        ('c105ed00-0000-0000-0000-000000000003', 'TX-R', st_xfmr_pad, 'published',
         ST_SetSRID(ST_MakePoint(-0.2000, 5.6000), 4326),
         '11KV', 400, 'GOOD', 500, 4.5, 6)
    ON CONFLICT (id) DO NOTHING;

    -- MTR-R: the whole-current meter where the two LV legs reconverge -- the
    -- ring's meeting node (a zero-impedance junction in the loop). Its subtree
    -- load (SP-R, 30 kW at unity pf) is the demand the two legs share.
    INSERT INTO gis.meter
        (id, name, subtype, status, geom, meter_serial, account_ref, phase)
    VALUES
        ('c105ed00-0000-0000-0000-000000000004', 'MTR-R', st_meter_wc, 'published',
         ST_SetSRID(ST_MakePoint(-0.2001, 5.5990), 4326), 'MTR-R-0001', 'ACC-R', 'L1L2L3N')
    ON CONFLICT (id) DO NOTHING;

    -- SP-R: the single ring load. UNITY power factor (1.0) on purpose: Q = 0 so
    -- the loop load pf k = 0, the circulating power is purely real, and the split
    -- is exact inverse-resistance (see the hand check in the header).
    INSERT INTO gis.service_point
        (id, name, subtype, status, geom, demand_kw, power_factor)
    VALUES
        ('c105ed00-0000-0000-0000-000000000006', 'SP-R', st_sp_comm, 'published',
         ST_SetSRID(ST_MakePoint(-0.2001, 5.5989), 4326), 30, 1.0)
    ON CONFLICT (id) DO NOTHING;

    -- MV-R: the 11 kV overhead span from the source to the step-down.
    INSERT INTO gis.mv_conductor
        (id, name, subtype, status, geom, voltage_class, condition_rating, conductor_spec)
    VALUES
        ('c105ed00-0000-0000-0000-000000000011', 'MV-R', st_mv_oh, 'published',
         ST_SetSRID(ST_GeomFromText('LINESTRING(-0.2000 5.6010, -0.2000 5.6000)'), 4326),
         '11KV', 'GOOD', 'ACSR_100')
    ON CONFLICT (id) DO NOTHING;

    -- LVA: the SHORT 400 V ring leg (~100 m: 0.0009 deg of latitude, pure N-S).
    INSERT INTO gis.lv_conductor
        (id, name, subtype, status, geom, phase, conductor_spec, lv_role)
    VALUES
        ('c105ed00-0000-0000-0000-000000000013', 'LVA', st_lv_oh, 'published',
         ST_SetSRID(ST_GeomFromText('LINESTRING(-0.2000 5.6000, -0.2000 5.5991)'), 4326),
         'L1L2L3N', 'WAVEFORM_185', 'main')
    ON CONFLICT (id) DO NOTHING;

    -- LVB: the LONG 400 V ring leg -- EXACTLY twice LVA's length (~200 m: 0.0018
    -- deg of latitude, pure N-S, same conductor_spec) so R_LVB = 2 * R_LVA. Same
    -- span direction as LVA so the 2:1 length ratio is exact; connectivity is
    -- declared (below), so the geometry drawn offset to the west is decorative --
    -- LVB still topologically joins TX-R.T2 to MTR-R.T1, closing the ring.
    INSERT INTO gis.lv_conductor
        (id, name, subtype, status, geom, phase, conductor_spec, lv_role)
    VALUES
        ('c105ed00-0000-0000-0000-000000000014', 'LVB', st_lv_oh, 'published',
         ST_SetSRID(ST_GeomFromText('LINESTRING(-0.2002 5.6000, -0.2002 5.5982)'), 4326),
         'L1L2L3N', 'WAVEFORM_185', 'main')
    ON CONFLICT (id) DO NOTHING;

    -- =====================================================================
    -- 7. Insert the runtime.feature_connectivity rows (the actual graph).
    --    Edge endpoints carry NULL terminal ids. ON CONFLICT on the
    --    (from,to)+terminals unique key => idempotent. The ONE non-radial
    --    edge is row 5 (LVB -> MTR-R.T1) -- the loop-closing edge.
    -- =====================================================================

    -- 1. RING-SRC.T2 -> MV-R
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_xfmr_t2_mv, fc_dist_xfmr, 'c105ed00-0000-0000-0000-000000000001', t_xfmr_t2,
         fc_mv_cond, 'c105ed00-0000-0000-0000-000000000011', NULL, 'seed:lv_ring_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 2. MV-R -> TX-R.T1
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_mv_xfmr_t1, fc_mv_cond, 'c105ed00-0000-0000-0000-000000000011', NULL,
         fc_dist_xfmr, 'c105ed00-0000-0000-0000-000000000003', t_xfmr_t1, 'seed:lv_ring_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 3. TX-R.T2 -> LVA  (short leg)
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_xfmr_t2_lv, fc_dist_xfmr, 'c105ed00-0000-0000-0000-000000000003', t_xfmr_t2,
         fc_lv_cond, 'c105ed00-0000-0000-0000-000000000013', NULL, 'seed:lv_ring_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 4. TX-R.T2 -> LVB  (long leg -- same source terminal as LVA)
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_xfmr_t2_lv, fc_dist_xfmr, 'c105ed00-0000-0000-0000-000000000003', t_xfmr_t2,
         fc_lv_cond, 'c105ed00-0000-0000-0000-000000000014', NULL, 'seed:lv_ring_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 5. LVA -> MTR-R.T1
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_lv_meter_t1, fc_lv_cond, 'c105ed00-0000-0000-0000-000000000013', NULL,
         fc_meter, 'c105ed00-0000-0000-0000-000000000004', t_meter_t1, 'seed:lv_ring_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 6. LVB -> MTR-R.T1  <== THE LOOP-CLOSING EDGE (second path to MTR-R).
    --    buildTree records this as meshBreak{closer: LVB, existing: MTR-R} and
    --    keeps the spanning tree radial; mesh.go's compensate then closes it.
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_lv_meter_t1, fc_lv_cond, 'c105ed00-0000-0000-0000-000000000014', NULL,
         fc_meter, 'c105ed00-0000-0000-0000-000000000004', t_meter_t1, 'seed:lv_ring_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 7. MTR-R.T2 -> SP-R.T1
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_meter_t2_sp, fc_meter, 'c105ed00-0000-0000-0000-000000000004', t_meter_t2,
         fc_sp, 'c105ed00-0000-0000-0000-000000000006', t_sp_t1, 'seed:lv_ring_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    RAISE NOTICE 'seed/lv_ring_network_example.sql applied: 7 features + 7 connectivity rows, ONE closed LV ring (LVB->MTR-R). Run POST /analysis/meshed-load-flow start=RING-SRC; expect LVA:LVB load ~2:1, residual < 1e-4 V.';
END $$;
