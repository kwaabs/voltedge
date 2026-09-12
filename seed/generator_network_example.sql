-- seed/generator_network_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations; this lives under
-- /seed on purpose, same as every other seed file.
--
-- THE VALIDATION FIXTURE FOR TIER A EMBEDDED GENERATION. A minimal RADIAL LV
-- feeder carrying one load AND one grid-tied GENERATOR on the same LV bus, with a
-- HAND-COMPUTABLE expected result, so a live POST /analysis/load-flow can be
-- checked against arithmetic. It proves the generator injects (negative demand)
-- and that resolveGeneration's dispatch/nameplate precedence is respected.
--
-- ASCII TOPOLOGY (strictly radial -- plain /load-flow runs, no meshing here):
--
--   GEN-SRC  (DIST_XFMR, 11 kV slack / Thevenin source; unity ratio)
--     | T2
--     +-- MV-G (MV_CONDUCTOR, 11 kV)
--          | T1
--          TX-G (DIST_XFMR, 11 kV -> 400 V step-down) T2
--          |
--          +-- LV-G (LV_CONDUCTOR, 400 V)
--                | (LV-G's downstream end is the LV bus where BOTH hang)
--                +-- MTR-G (METER) T2 -> SP-G (SERVICE_POINT, 50 kW, pf 1.0)  [LOAD]
--                +-- GEN-1 (GENERATOR, solar_pv; rated 40 kW, dispatch 30 kW)  [SOURCE]
--
-- Both MTR-G and GEN-1 are children of LV-G. GEN-1 is a leaf whose OWN net power
-- is NEGATIVE: resolveGeneration returns +30 kW (dispatch_kw overrides the 40 kW
-- rated_kw nameplate -- the precedence proof), resolveNodeStates nets it to
-- ownKW = 0 - 30 = -30 kW, and the backward sweep carries that up as reduced
-- upstream flow.
--
-- THE HAND CHECK (why this seed exists):
--   * SP-G draws 50 kW at unity pf (Q = 0). GEN-1 injects 30 kW at unity pf
--     (Q = 0). dispatch_kw = 30 is PRESENT, so it wins over rated_kw = 40.
--   * Backward-sweep cumulative real power:
--         SP-G   =  50            GEN-1  = -30
--         MTR-G  =  50            LV-G   =  50 + (-30) = 20
--         TX-G   =  20            MV-G   =  20            GEN-SRC = 20
--   * So EVERY branch upstream of the LV bus carries 20 kW net -- the 50 kW load
--     minus the 30 kW local injection. Without GEN-1 the same feeder carries the
--     full 50 kW, so TX-G loading falls from 50/500 = 10 % to 20/500 = 4 %, and
--     the voltage drop along MV-G / TX-G / LV-G is 20/50 = 40 % of the no-gen
--     drop (voltage RISES at the LV bus relative to the load-only case).
--
--   So a correct /load-flow response for start = GEN-SRC must show:
--     * the GEN-1 node reporting a NEGATIVE cumulative real power (~ -30 kW);
--     * TX-G through-power ~ 20 kW (loading ~ 4 %), NOT 50 kW;
--     * LV bus / meter voltage HIGHER than the same feeder with GEN-1 removed.
--   To confirm the dispatch/nameplate precedence: clear dispatch_kw on GEN-1 and
--   re-run -- injection jumps to the 40 kW nameplate and upstream flow drops to
--   50 - 40 = 10 kW.
--
-- THE FAULT-CURRENT HAND CHECK (POST /analysis/fault-current, start = GEN-SRC):
-- the same fixture doubles as the embedded-generation FAULT-INFEED proof. GEN-1
-- carries NO explicit fault field, so the engine takes the inverter fallback for
-- its solar_pv subtype (resolveGeneratorFaultSpec -> genFaultInverter): a
-- current-limited source contributing ~1.2x its RATED current. The fault figure
-- is driven by the NAMEPLATE rated_kw = 40 (NOT dispatch_kw = 30) -- a
-- part-loaded but still-connected inverter feeds the fault at its full rating.
--   * S_rated = rated_kw / pf = 40 / 1.0 = 40 kVA; terminal base V = 400 V (LV).
--   * Internal fault impedance (genFaultImpedanceOhm, inverter branch):
--         Z_int = V^2 / (1.2 * S) = 400^2 / (1.2 * 40000) = 3.3333 ohm.
--   * A bolted 3-phase fault AT the LV bus (GEN-1's own node): the series path
--     GEN-1 -> fault is zero (same node), so the loop is just Z_int, and GEN-1's
--     contribution is I = V / Z_int = 400 / 3.3333 = 120.0 A -- exactly 1.2x the
--     line-to-line-referred rated current (1.2 * 40000 / 400). Like the grid
--     figure this endpoint already reports, it is on the engine's basis-agnostic
--     V/Z (line-to-line) convention: sqrt(3) above the physical per-phase 69.3 A,
--     the SAME convention the grid infeed uses, so the two combine coherently.
--   * So a correct fault-current response at the LV bus / GEN-1 node must show:
--         grid_contribution_amps      = the grid-only figure this endpoint has
--                                       always reported (unchanged);
--         generator_contribution_amps = ~120.0 A (GEN-1's infeed);
--         fault_current_amps          = grid + ~120.0 A (the TOTAL prospective
--                                       duty the switchgear here must interrupt).
--     Nodes with no generator downstream contributing through them read exactly
--     as before -- the split fields appear only where a generator actually feeds.
--   To confirm nameplate-not-dispatch: GEN-1's contribution stays ~120 A even
--   though it dispatches only 30 kW. To exercise the OTHER two characterisations,
--   set fault_contribution_ka (authoritative kA, any subtype) or, for a
--   synchronous machine, subtransient_reactance_pct (X''d on its rated-kVA base).
--   An SLG fault stays grid-only (a documented Tier A limit -- genfault.go).
--
-- HARD PREREQUISITES -- every metadata seed below must be applied AND the classes
-- COMPILED (task compile CODE=...) BEFORE this seed, because it writes into the
-- physical gis.<class> tables and reads their electrical columns:
--
--   seed/connectivity_example.sql        -> MV_CONDUCTOR, DIST_XFMR (base)
--   seed/load_flow_fields.sql            -> SERVICE_POINT.demand_kw/power_factor,
--                                           DIST_XFMR.rated_kva/source_r_ohm/source_x_ohm
--   seed/transformer_model_fields.sql    -> DIST_XFMR.secondary_voltage/impedance_pct/x_r_ratio
--   seed/mv_conductor_specs.sql          -> MV_LINE_SPEC + MV_CONDUCTOR.conductor_spec
--   seed/lv_conductors_example.sql       -> LV_CONDUCTOR (+ DIST_XFMR T2 -> LV_CONDUCTOR) + conductor_spec
--   seed/lv_metering_example.sql         -> METER (+ LV_CONDUCTOR -> METER, METER -> SERVICE_POINT)
--   seed/service_point_example.sql       -> SERVICE_POINT (+ DIST_XFMR T2 -> MV_CONDUCTOR)
--   seed/generator_example.sql           -> GENERATOR (+ LV_CONDUCTOR -> GENERATOR T1)
--
-- then compile each participating class:
--   task compile CODE=DIST_XFMR
--   task compile CODE=MV_CONDUCTOR
--   task compile CODE=LV_CONDUCTOR
--   task compile CODE=METER
--   task compile CODE=SERVICE_POINT
--   task compile CODE=GENERATOR
--
-- The DO block fails LOUDLY (RAISE EXCEPTION naming the missing prerequisite) if
-- any class, subtype, terminal, electrical column, or network_rule is absent, so
-- a half-set-up database aborts instead of writing a partial network. Fully
-- idempotent: fixed uuids (prefix d9000000 = "DG", distributed generation) with
-- ON CONFLICT (id) DO NOTHING for features, and the (from,to)+terminals unique
-- key for connectivity.
--
-- Apply with psql (NOT the migration runner), after the prerequisites above:
--   task seed-generator-network   (or the psql -f invocation directly)
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL is
-- reviewed statically -- a live psql apply against a fully-compiled DB and a
-- load-flow re-check against the hand numbers above are still owed to call it
-- verified.

DO $$
DECLARE
    -- feature_class ids
    fc_dist_xfmr uuid;
    fc_mv_cond   uuid;
    fc_lv_cond   uuid;
    fc_meter     uuid;
    fc_sp        uuid;
    fc_gen       uuid;

    -- subtype INTEGER codes (the value stored in gis.<class>.subtype)
    st_xfmr_pad  integer;  -- DIST_XFMR 'pad_mounted'
    st_meter_wc  integer;  -- METER 'whole_current'
    st_sp_comm   integer;  -- SERVICE_POINT 'commercial'
    st_mv_oh     integer;  -- MV_CONDUCTOR 'overhead_mv_conductor'
    st_lv_oh     integer;  -- LV_CONDUCTOR 'overhead_lv_conductor'
    st_gen_pv    integer;  -- GENERATOR 'solar_pv'

    -- terminal ids
    t_xfmr_t1    uuid;
    t_xfmr_t2    uuid;
    t_meter_t1   uuid;
    t_meter_t2   uuid;
    t_sp_t1      uuid;
    t_gen_t1     uuid;

    -- network_rule ids
    r_xfmr_t2_mv  uuid;  -- DIST_XFMR T2 -> MV_CONDUCTOR
    r_mv_xfmr_t1  uuid;  -- MV_CONDUCTOR -> DIST_XFMR T1
    r_xfmr_t2_lv  uuid;  -- DIST_XFMR T2 -> LV_CONDUCTOR
    r_lv_meter_t1 uuid;  -- LV_CONDUCTOR -> METER T1
    r_meter_t2_sp uuid;  -- METER T2 -> SERVICE_POINT T1
    r_lv_gen_t1   uuid;  -- LV_CONDUCTOR -> GENERATOR T1
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

    SELECT id INTO fc_gen FROM metadata.feature_classes WHERE code = 'GENERATOR';
    IF fc_gen IS NULL THEN RAISE EXCEPTION 'missing feature_class GENERATOR -- apply seed/generator_example.sql then: task compile CODE=GENERATOR'; END IF;

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

    SELECT code INTO st_gen_pv    FROM metadata.subtypes WHERE feature_class_id = fc_gen AND name = 'solar_pv';
    IF st_gen_pv IS NULL THEN RAISE EXCEPTION 'GENERATOR subtype ''solar_pv'' not found -- re-apply seed/generator_example.sql'; END IF;

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

    SELECT id INTO t_gen_t1   FROM metadata.terminals WHERE feature_class_id = fc_gen AND code = 'T1' ORDER BY subtype_id NULLS LAST LIMIT 1;
    IF t_gen_t1 IS NULL THEN RAISE EXCEPTION 'GENERATOR terminal T1 not found -- re-apply seed/generator_example.sql'; END IF;

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
    IF to_regclass('gis.generator') IS NULL THEN RAISE EXCEPTION 'table gis.generator does not exist -- run: task compile CODE=GENERATOR'; END IF;

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

    -- GENERATOR: the injection inputs resolveGeneration reads.
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'generator' AND column_name = 'rated_kw') THEN
        RAISE EXCEPTION 'gis.generator is missing column rated_kw -- apply seed/generator_example.sql then: task compile CODE=GENERATOR'; END IF;
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'generator' AND column_name = 'dispatch_kw') THEN
        RAISE EXCEPTION 'gis.generator is missing column dispatch_kw -- apply seed/generator_example.sql then: task compile CODE=GENERATOR'; END IF;
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'generator' AND column_name = 'power_factor') THEN
        RAISE EXCEPTION 'gis.generator is missing column power_factor -- apply seed/generator_example.sql then: task compile CODE=GENERATOR'; END IF;
    -- The fault-infeed inputs resolveGeneratorFaultSpec reads. GEN-1 leaves both
    -- unset (inverter fallback), but the columns must exist for the fault study to
    -- read them -- guard so a fault re-check never silently sees no columns.
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'generator' AND column_name = 'fault_contribution_ka') THEN
        RAISE EXCEPTION 'gis.generator is missing column fault_contribution_ka -- apply seed/generator_example.sql then: task compile CODE=GENERATOR'; END IF;
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'generator' AND column_name = 'subtransient_reactance_pct') THEN
        RAISE EXCEPTION 'gis.generator is missing column subtransient_reactance_pct -- apply seed/generator_example.sql then: task compile CODE=GENERATOR'; END IF;

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

    SELECT id INTO r_lv_gen_t1 FROM metadata.network_rules
     WHERE from_feature_class_id = fc_lv_cond AND to_feature_class_id = fc_gen
       AND from_terminal_id IS NULL AND to_terminal_id IS NOT DISTINCT FROM t_gen_t1
       AND is_valid = true LIMIT 1;
    IF r_lv_gen_t1 IS NULL THEN RAISE EXCEPTION 'network_rule "LV_CONDUCTOR -> GENERATOR T1" not found/valid -- apply seed/generator_example.sql'; END IF;

    -- =====================================================================
    -- 6. Insert the feature rows (status 'published' so they are live).
    --    Fixed uuids (prefix d9000000 = "DG") + ON CONFLICT (id) DO NOTHING
    --    => idempotent, and disjoint from test_network_example's feed0000
    --    and lv_ring_network_example's c105ed00 prefixes so all coexist.
    -- =====================================================================

    -- GEN-SRC: 11 kV slack / Thevenin source (unity ratio). The GRID infeed --
    -- still an instance-level slack, distinct from the embedded GENERATOR below.
    INSERT INTO gis.dist_xfmr
        (id, name, subtype, status, geom, primary_voltage, secondary_voltage, condition_rating,
         source_r_ohm, source_x_ohm)
    VALUES
        ('d9000000-0000-0000-0000-000000000001', 'GEN-SRC', st_xfmr_pad, 'published',
         ST_SetSRID(ST_MakePoint(-0.1800, 5.6010), 4326),
         '11KV', 11000, 'GOOD', 0.048, 0.48)
    ON CONFLICT (id) DO NOTHING;

    -- TX-G: the 11 kV -> 400 V step-down feeding the LV bus.
    INSERT INTO gis.dist_xfmr
        (id, name, subtype, status, geom, primary_voltage, secondary_voltage, condition_rating,
         rated_kva, impedance_pct, x_r_ratio)
    VALUES
        ('d9000000-0000-0000-0000-000000000003', 'TX-G', st_xfmr_pad, 'published',
         ST_SetSRID(ST_MakePoint(-0.1800, 5.6000), 4326),
         '11KV', 400, 'GOOD', 500, 4.5, 6)
    ON CONFLICT (id) DO NOTHING;

    -- MTR-G: the whole-current meter fronting the load.
    INSERT INTO gis.meter
        (id, name, subtype, status, geom, meter_serial, account_ref, phase)
    VALUES
        ('d9000000-0000-0000-0000-000000000004', 'MTR-G', st_meter_wc, 'published',
         ST_SetSRID(ST_MakePoint(-0.1801, 5.5990), 4326), 'MTR-G-0001', 'ACC-G', 'L1L2L3N')
    ON CONFLICT (id) DO NOTHING;

    -- SP-G: the LOAD. 50 kW at unity pf (Q = 0) -- the clean load side of the
    -- hand check.
    INSERT INTO gis.service_point
        (id, name, subtype, status, geom, demand_kw, power_factor)
    VALUES
        ('d9000000-0000-0000-0000-000000000006', 'SP-G', st_sp_comm, 'published',
         ST_SetSRID(ST_MakePoint(-0.1801, 5.5989), 4326), 50, 1.0)
    ON CONFLICT (id) DO NOTHING;

    -- MV-G: the 11 kV overhead span from the source to the step-down.
    INSERT INTO gis.mv_conductor
        (id, name, subtype, status, geom, voltage_class, condition_rating, conductor_spec)
    VALUES
        ('d9000000-0000-0000-0000-000000000011', 'MV-G', st_mv_oh, 'published',
         ST_SetSRID(ST_GeomFromText('LINESTRING(-0.1800 5.6010, -0.1800 5.6000)'), 4326),
         '11KV', 'GOOD', 'ACSR_100')
    ON CONFLICT (id) DO NOTHING;

    -- LV-G: the 400 V LV feeder; its downstream end is the LV bus where BOTH the
    -- load (via MTR-G) and the generator (GEN-1) connect.
    INSERT INTO gis.lv_conductor
        (id, name, subtype, status, geom, phase, conductor_spec, lv_role)
    VALUES
        ('d9000000-0000-0000-0000-000000000013', 'LV-G', st_lv_oh, 'published',
         ST_SetSRID(ST_GeomFromText('LINESTRING(-0.1800 5.6000, -0.1800 5.5991)'), 4326),
         'L1L2L3N', 'WAVEFORM_185', 'main')
    ON CONFLICT (id) DO NOTHING;

    -- GEN-1: the EMBEDDED GENERATOR (grid-tied solar PV). rated_kw = 40 nameplate,
    -- dispatch_kw = 30 actual output -> resolveGeneration injects 30 kW (dispatch
    -- WINS over nameplate), pf 1.0 so Q = 0. resolveNodeStates nets this to
    -- ownKW = -30, a negative-demand leaf on the LV bus. No fault field set, so the
    -- fault study takes the solar_pv inverter fallback (~1.2x rated current); note
    -- it uses the 40 kW NAMEPLATE, not the 30 kW dispatch -- see the fault hand
    -- check in the header.
    INSERT INTO gis.generator
        (id, name, subtype, status, geom, rated_kw, dispatch_kw, power_factor)
    VALUES
        ('d9000000-0000-0000-0000-000000000020', 'GEN-1', st_gen_pv, 'published',
         ST_SetSRID(ST_MakePoint(-0.1799, 5.5990), 4326), 40, 30, 1.0)
    ON CONFLICT (id) DO NOTHING;

    -- =====================================================================
    -- 7. Insert the runtime.feature_connectivity rows (the actual graph).
    --    Edge endpoints carry NULL terminal ids. ON CONFLICT on the
    --    (from,to)+terminals unique key => idempotent. Strictly radial:
    --    LV-G fans out to MTR-G and GEN-1 (no second path to any node).
    -- =====================================================================

    -- 1. GEN-SRC.T2 -> MV-G
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_xfmr_t2_mv, fc_dist_xfmr, 'd9000000-0000-0000-0000-000000000001', t_xfmr_t2,
         fc_mv_cond, 'd9000000-0000-0000-0000-000000000011', NULL, 'seed:generator_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 2. MV-G -> TX-G.T1
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_mv_xfmr_t1, fc_mv_cond, 'd9000000-0000-0000-0000-000000000011', NULL,
         fc_dist_xfmr, 'd9000000-0000-0000-0000-000000000003', t_xfmr_t1, 'seed:generator_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 3. TX-G.T2 -> LV-G
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_xfmr_t2_lv, fc_dist_xfmr, 'd9000000-0000-0000-0000-000000000003', t_xfmr_t2,
         fc_lv_cond, 'd9000000-0000-0000-0000-000000000013', NULL, 'seed:generator_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 4. LV-G -> MTR-G.T1  (the load branch)
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_lv_meter_t1, fc_lv_cond, 'd9000000-0000-0000-0000-000000000013', NULL,
         fc_meter, 'd9000000-0000-0000-0000-000000000004', t_meter_t1, 'seed:generator_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 5. LV-G -> GEN-1.T1  (the GENERATION branch -- the negative-demand leaf)
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_lv_gen_t1, fc_lv_cond, 'd9000000-0000-0000-0000-000000000013', NULL,
         fc_gen, 'd9000000-0000-0000-0000-000000000020', t_gen_t1, 'seed:generator_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 6. MTR-G.T2 -> SP-G.T1
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_meter_t2_sp, fc_meter, 'd9000000-0000-0000-0000-000000000004', t_meter_t2,
         fc_sp, 'd9000000-0000-0000-0000-000000000006', t_sp_t1, 'seed:generator_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    RAISE NOTICE 'seed/generator_network_example.sql applied: 7 features + 6 connectivity rows, one radial LV feeder with an embedded GENERATOR (GEN-1, dispatch 30 kW, nameplate 40 kW). load-flow start=GEN-SRC: expect TX-G through-power ~20 kW (not 50), GEN-1 node cumulative ~ -30 kW. fault-current start=GEN-SRC: expect the LV-bus/GEN-1 node to report generator_contribution_amps ~120 A on top of the grid infeed (nameplate-driven, inverter fallback).';
END $$;
