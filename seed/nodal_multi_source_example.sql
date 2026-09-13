-- seed/nodal_multi_source_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations; this lives under
-- /seed on purpose, same as every other seed file.
--
-- THE VALIDATION FIXTURE FOR PHASE 52 TIER 2 SLICE S3 (multi-slack + PV buses)
-- of the nodal (Y-bus) solver. Every existing instance seed has AT MOST ONE
-- infeed and NO grid-forming generation, so the S3 code paths -- multiple slack
-- buses (sources[]) and voltage-regulating PV buses (regulated[] /
-- voltage_setpoint_pu) -- are currently exercised by no live fixture at all.
-- This seed is a minimal DENSE-INFEED LV network with TWO ideal sources and one
-- PV generator whose answer is HAND-COMPUTABLE and, crucially, EXACT (a ratio
-- and a held magnitude that are invariant of the load, the generator, and the
-- convergence tolerance), so a live POST /analysis/nodal-load-flow can be
-- checked against arithmetic, not vibes.
--
-- ASCII TOPOLOGY (a 4-bus, 3-branch LV network -- NOT radial from any one root:
-- two independent 400 V infeeds feed one common load bus, which the radial
-- forward-backward sweep structurally cannot represent -- it has one slack):
--
--   SRC-A (DIST_XFMR, ideal 400 V slack; source_r=source_x=0)
--     | T2
--     +-- LVA (LV_CONDUCTOR, 400 V, ~100 m, R)      --+
--                                                      |
--   SRC-B (DIST_XFMR, ideal 400 V slack; src_r=x=0)   +--> MTR-M (METER) = BUS L
--     | T2                                             |       | T2
--     +-- LVB (LV_CONDUCTOR, 400 V, ~200 m, 2R)     --+        +-- SP-M (SERVICE_POINT, 60 kW, pf 1.0)  [LOAD]
--                                                      |
--                                            LG (LV_CONDUCTOR) --+--> GEN-P (GENERATOR, solar_pv, PV bus)
--                                                                     rated 20 kW, voltage_setpoint_pu = 1.02
--
-- Both LVA and LVB run their source's T2 -> MTR-M.T1; LG also runs MTR-M.T1 ->
-- GEN-P.T1. The meter is a zero-impedance junction, so MTR-M.T1/T2, SP-M and the
-- south ends of LVA/LVB/LG all contract into ONE bus (BUS L). GEN-P sits on its
-- OWN bus behind LG (a finite-impedance branch), so only GEN-P carries the PV
-- role. Four buses total: SRC-A, SRC-B, BUS L, GEN-P; three branches: LVA, LVB,
-- LG.
--
-- ============================ THE HAND CHECK ============================
-- Run: POST /analysis/nodal-load-flow with
--   { start: SRC-A, sources: [SRC-B], nominal_voltage_v: 400 }
-- (SRC-A is the start slack; SRC-B is the second slack. GEN-P is a PV bus by its
--  own voltage_setpoint_pu asset field -- no regulated[] entry needed.)
--
-- (1) MULTI-SLACK 2:1 SUPPLIED SPLIT -- the S3 multi-slack proof, EXACT.
--     Both sources are IDEAL (source_r=source_x=0) so each slack sits AT its own
--     bus, held at the run nominal: V_A = V_B = 400 <0 (same base, same angle).
--     LVA and LVB share one conductor_spec (WAVEFORM_185) and LVB is EXACTLY
--     twice LVA's length (both pure N-S spans: 0.0018 deg vs 0.0009 deg of
--     latitude), so Z_LVB = 2 * Z_LVA. Each source current into the common bus L:
--         I_A = (V_A - V_L) / Z_LVA        I_B = (V_B - V_L) / Z_LVB
--     Because V_A = V_B, the driving difference (400 - V_L) is IDENTICAL for both,
--     so
--         I_A / I_B = Z_LVB / Z_LVA = 2      EXACTLY,
--     independent of V_L, of the 60 kW load, of the generator, and of power
--     factor. Supplied power S = V * conj(I) at each slack shares the same
--     V = 400 <0, so
--         supplied_kw(SRC-A)   = 2 * supplied_kw(SRC-B)      EXACTLY, and
--         reactive_kvar(SRC-A) = 2 * reactive_kvar(SRC-B)    EXACTLY.
--     The 2:1 asymmetry (not a symmetric 50/50) is the multi-slack proof: it can
--     only arise if BOTH sources are held as slacks and their currents summed at
--     the shared bus. A single-slack solve (or the radial sweep) cannot produce
--     it.
--
-- (2) PV BUS HELD ABOVE THE SLACKS -- the S3 grid-forming proof, EXACT.
--     GEN-P carries voltage_setpoint_pu = 1.02 AND rated_kw = 20 (> 0), so
--     resolvePVSpec designates its bus a PV bus: the solver HOLDS its magnitude
--     at the setpoint (resetting |V| to 1.02 pu each pass, solving Q), so at
--     convergence
--         GEN-P.voltage_v = 1.02 * 400 = 408.0 V      EXACTLY,
--         GEN-P.role      = "pv",
--     which is ABOVE both 400 V slacks -- a voltage LIFT only a grid-forming (PV)
--     bus produces (a passive load bus can only sag below its infeed). Its
--     supplied_kw ~ 20 kW (the fixed real injection = the PV bus's scheduled P),
--     and reactive_kvar = whatever Q the solver found to hold 408 V (UNBOUNDED
--     here -- no reactive_limit_kvar set, so no PV->PQ clamp).
--
-- So a correct nodal-load-flow response for this run must show:
--     solver           = "gauss-seidel",  converged = true
--     slack_count      = 2
--     bus_count        = 4,  branch_count = 3
--     SRC-A.role = SRC-B.role = "slack";  supplied_kw(SRC-A) = 2*supplied_kw(SRC-B)
--     GEN-P.role = "pv";  GEN-P.voltage_v = 408.0 V  (> 400 V at both slacks)
-- and the plain radial POST /analysis/load-flow on the same start cannot model
-- the second infeed at all (it collapses to a single slack).
--
-- TO EXERCISE THE REQUEST-OVERRIDE HALF OF THE "BOTH" PV MODEL: re-run with
--     regulated: [ { feature: GEN-P, voltage_pu: 1.05 } ]
-- and GEN-P.voltage_v becomes 1.05 * 400 = 420.0 V (the request setpoint
-- overrides the 1.02 asset value); add q_min_kvar/q_max_kvar to watch the
-- PV->PQ clamp. TO CONFIRM the asset path drives PV with NO request: omit
-- regulated[] entirely (as above) and GEN-P still holds 408 V from its stored
-- voltage_setpoint_pu.
--
-- HARD PREREQUISITES -- every metadata seed below must be applied AND the classes
-- COMPILED (task compile CODE=...) BEFORE this seed, because it writes into the
-- physical gis.<class> tables and reads their electrical columns:
--
--   seed/connectivity_example.sql        -> DIST_XFMR (base: primary_voltage)
--   seed/load_flow_fields.sql            -> DIST_XFMR.source_r_ohm/source_x_ohm,
--                                           SERVICE_POINT.demand_kw/power_factor
--   seed/transformer_model_fields.sql    -> DIST_XFMR.secondary_voltage
--   seed/lv_conductors_example.sql       -> LV_CONDUCTOR (+ DIST_XFMR T2 ->
--                                           LV_CONDUCTOR rule) + conductor_spec
--   seed/lv_metering_example.sql         -> METER (+ LV_CONDUCTOR -> METER,
--                                           METER -> SERVICE_POINT rules)
--   seed/service_point_example.sql       -> SERVICE_POINT
--   seed/generator_example.sql           -> GENERATOR (+ LV_CONDUCTOR ->
--                                           GENERATOR T1 rule) + rated_kw/pf
--   seed/generator_pv_fields.sql         -> GENERATOR.voltage_setpoint_pu
--                                           (the asset-driven PV input)
--
-- then compile each participating class:
--   task compile CODE=DIST_XFMR
--   task compile CODE=LV_CONDUCTOR
--   task compile CODE=METER
--   task compile CODE=SERVICE_POINT
--   task compile CODE=GENERATOR      (AFTER generator_pv_fields, so the
--                                     voltage_setpoint_pu column materialises)
--
-- The DO block fails LOUDLY (RAISE EXCEPTION naming the missing prerequisite) if
-- any class, subtype, terminal, electrical column, or network_rule is absent, so
-- a half-set-up database aborts instead of writing a partial network. Fully
-- idempotent: fixed uuids (prefix 50c00000 = "source") with ON CONFLICT (id) DO
-- NOTHING for features, and the (from,to)+terminals unique key for connectivity;
-- disjoint from test_network (feed0000), lv_ring (c105ed00) and
-- generator_network (d9000000) so all coexist.
--
-- Apply with psql (NOT the migration runner), after the prerequisites above:
--   task seed-nodal-multi-source   (or the psql -f invocation directly)
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL is
-- reviewed statically -- a live psql apply against a fully-compiled DB and a
-- nodal-load-flow re-check against the hand numbers above are still owed to call
-- it verified.

DO $$
DECLARE
    -- feature_class ids
    fc_dist_xfmr uuid;
    fc_lv_cond   uuid;
    fc_meter     uuid;
    fc_sp        uuid;
    fc_gen       uuid;

    -- subtype INTEGER codes (the value stored in gis.<class>.subtype)
    st_xfmr_pad  integer;  -- DIST_XFMR 'pad_mounted'
    st_meter_wc  integer;  -- METER 'whole_current'
    st_sp_comm   integer;  -- SERVICE_POINT 'commercial'
    st_lv_oh     integer;  -- LV_CONDUCTOR 'overhead_lv_conductor'
    st_gen_pv    integer;  -- GENERATOR 'solar_pv'

    -- terminal ids (T1 of the sources is intentionally unused: both DIST_XFMRs
    -- are ideal LV source stand-ins connected only at T2, so no ratio branch is
    -- built -- each is a plain 400 V slack bus).
    t_xfmr_t2    uuid;
    t_meter_t1   uuid;
    t_meter_t2   uuid;
    t_sp_t1      uuid;
    t_gen_t1     uuid;

    -- network_rule ids
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

    SELECT code INTO st_lv_oh     FROM metadata.subtypes WHERE feature_class_id = fc_lv_cond AND name = 'overhead_lv_conductor';
    IF st_lv_oh IS NULL THEN RAISE EXCEPTION 'LV_CONDUCTOR subtype ''overhead_lv_conductor'' not found -- re-apply seed/lv_conductors_example.sql'; END IF;

    SELECT code INTO st_gen_pv    FROM metadata.subtypes WHERE feature_class_id = fc_gen AND name = 'solar_pv';
    IF st_gen_pv IS NULL THEN RAISE EXCEPTION 'GENERATOR subtype ''solar_pv'' not found -- re-apply seed/generator_example.sql'; END IF;

    -- =====================================================================
    -- 3. Resolve terminal ids (all subtype-agnostic here: subtype_id NULL).
    -- =====================================================================
    SELECT id INTO t_xfmr_t2  FROM metadata.terminals WHERE feature_class_id = fc_dist_xfmr AND code = 'T2' ORDER BY subtype_id NULLS LAST LIMIT 1;
    IF t_xfmr_t2 IS NULL THEN RAISE EXCEPTION 'DIST_XFMR terminal T2 not found -- re-apply seed/connectivity_example.sql'; END IF;

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
    IF to_regclass('gis.lv_conductor') IS NULL THEN RAISE EXCEPTION 'table gis.lv_conductor does not exist -- run: task compile CODE=LV_CONDUCTOR'; END IF;
    IF to_regclass('gis.meter') IS NULL THEN RAISE EXCEPTION 'table gis.meter does not exist -- run: task compile CODE=METER'; END IF;
    IF to_regclass('gis.service_point') IS NULL THEN RAISE EXCEPTION 'table gis.service_point does not exist -- run: task compile CODE=SERVICE_POINT'; END IF;
    IF to_regclass('gis.generator') IS NULL THEN RAISE EXCEPTION 'table gis.generator does not exist -- run: task compile CODE=GENERATOR'; END IF;

    -- DIST_XFMR: base voltage + the source-impedance pair (an ideal source sets
    -- both to 0) + secondary_voltage (written for clarity; no ratio branch is
    -- built because T1 is unconnected).
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'dist_xfmr' AND column_name = 'primary_voltage') THEN
        RAISE EXCEPTION 'gis.dist_xfmr is missing column primary_voltage -- apply seed/connectivity_example.sql then: task compile CODE=DIST_XFMR'; END IF;
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'dist_xfmr' AND column_name = 'secondary_voltage') THEN
        RAISE EXCEPTION 'gis.dist_xfmr is missing column secondary_voltage -- apply seed/transformer_model_fields.sql then: task compile CODE=DIST_XFMR'; END IF;
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'dist_xfmr' AND column_name = 'source_r_ohm') THEN
        RAISE EXCEPTION 'gis.dist_xfmr is missing column source_r_ohm -- apply seed/load_flow_fields.sql then: task compile CODE=DIST_XFMR'; END IF;
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'dist_xfmr' AND column_name = 'source_x_ohm') THEN
        RAISE EXCEPTION 'gis.dist_xfmr is missing column source_x_ohm -- apply seed/load_flow_fields.sql then: task compile CODE=DIST_XFMR'; END IF;

    -- LV_CONDUCTOR: conductor_spec (per-km impedance link -- resolves LVA/LVB/LG
    -- to finite series impedances; the 2:1 length ratio then gives Z_LVB = 2 Z_LVA).
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'lv_conductor' AND column_name = 'conductor_spec') THEN
        RAISE EXCEPTION 'gis.lv_conductor is missing column conductor_spec -- apply seed/lv_conductors_example.sql then: task compile CODE=LV_CONDUCTOR'; END IF;

    -- SERVICE_POINT: real demand inputs.
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'service_point' AND column_name = 'demand_kw') THEN
        RAISE EXCEPTION 'gis.service_point is missing column demand_kw -- apply seed/load_flow_fields.sql then: task compile CODE=SERVICE_POINT'; END IF;
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'service_point' AND column_name = 'power_factor') THEN
        RAISE EXCEPTION 'gis.service_point is missing column power_factor -- apply seed/load_flow_fields.sql then: task compile CODE=SERVICE_POINT'; END IF;

    -- GENERATOR: the injection input (rated_kw) + the grid-forming PV input
    -- (voltage_setpoint_pu). resolvePVSpec requires BOTH voltage_setpoint_pu > 0
    -- AND resolveGeneration > 0 for GEN-P to be a PV bus, so both must exist.
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'generator' AND column_name = 'rated_kw') THEN
        RAISE EXCEPTION 'gis.generator is missing column rated_kw -- apply seed/generator_example.sql then: task compile CODE=GENERATOR'; END IF;
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'generator' AND column_name = 'power_factor') THEN
        RAISE EXCEPTION 'gis.generator is missing column power_factor -- apply seed/generator_example.sql then: task compile CODE=GENERATOR'; END IF;
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'generator' AND column_name = 'voltage_setpoint_pu') THEN
        RAISE EXCEPTION 'gis.generator is missing column voltage_setpoint_pu -- apply seed/generator_pv_fields.sql then: task compile CODE=GENERATOR'; END IF;

    -- =====================================================================
    -- 5. Resolve the network_rule ids that authorize each connection.
    --    Matched on from/to feature_class + terminals (NULL-safe) with
    --    is_valid = true, so a retired rule never authorizes a connection.
    -- =====================================================================
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
    --    Fixed uuids (prefix 50c00000 = "source") + ON CONFLICT (id) DO NOTHING
    --    => idempotent, and disjoint from every other instance seed's prefix.
    -- =====================================================================

    -- SRC-A: the FIRST ideal 400 V infeed (the run's start slack). source_r =
    -- source_x = 0 so the slack sits AT this bus (no synthetic bus behind an
    -- impedance). Only T2 (LV side) is connected, so no transformer ratio branch
    -- is built -- it is a pure 400 V slack. secondary_voltage 400 is cosmetic
    -- here (the bus base comes from the run nominal).
    INSERT INTO gis.dist_xfmr
        (id, name, subtype, status, geom, primary_voltage, secondary_voltage, condition_rating,
         source_r_ohm, source_x_ohm)
    VALUES
        ('50c00000-0000-0000-0000-000000000001', 'SRC-A', st_xfmr_pad, 'published',
         ST_SetSRID(ST_MakePoint(-0.2200, 5.6000), 4326),
         '11KV', 400, 'GOOD', 0, 0)
    ON CONFLICT (id) DO NOTHING;

    -- SRC-B: the SECOND ideal 400 V infeed (sent as sources[0] in the request).
    -- Identical ideal slack; the ONLY asymmetry between the two infeeds is the
    -- 2:1 conductor-length ratio LVA:LVB, so any split other than 2:1 is a bug.
    INSERT INTO gis.dist_xfmr
        (id, name, subtype, status, geom, primary_voltage, secondary_voltage, condition_rating,
         source_r_ohm, source_x_ohm)
    VALUES
        ('50c00000-0000-0000-0000-000000000002', 'SRC-B', st_xfmr_pad, 'published',
         ST_SetSRID(ST_MakePoint(-0.2202, 5.6008), 4326),
         '11KV', 400, 'GOOD', 0, 0)
    ON CONFLICT (id) DO NOTHING;

    -- MTR-M: the whole-current meter at the common bus (BUS L) where the two LV
    -- infeed legs and the generator feeder all meet. A zero-impedance junction,
    -- so its T1/T2, SP-M, and the conductor ends all contract into one bus.
    INSERT INTO gis.meter
        (id, name, subtype, status, geom, meter_serial, account_ref, phase)
    VALUES
        ('50c00000-0000-0000-0000-000000000004', 'MTR-M', st_meter_wc, 'published',
         ST_SetSRID(ST_MakePoint(-0.2200, 5.5990), 4326), 'MTR-M-0001', 'ACC-M', 'L1L2L3N')
    ON CONFLICT (id) DO NOTHING;

    -- SP-M: the common load, 60 kW at unity pf (Q = 0). Its size does not affect
    -- the 2:1 slack split (that ratio is invariant of the load) -- it is here only
    -- to make real power flow so the split is observable.
    INSERT INTO gis.service_point
        (id, name, subtype, status, geom, demand_kw, power_factor)
    VALUES
        ('50c00000-0000-0000-0000-000000000006', 'SP-M', st_sp_comm, 'published',
         ST_SetSRID(ST_MakePoint(-0.2201, 5.5989), 4326), 60, 1.0)
    ON CONFLICT (id) DO NOTHING;

    -- GEN-P: the grid-forming PV generator. rated_kw = 20 (> 0, so it is a real
    -- injector) AND voltage_setpoint_pu = 1.02 -> resolvePVSpec makes its bus a
    -- PV bus held at 1.02 pu = 408 V. reactive_limit_kvar left NULL (unbounded Q,
    -- no PV->PQ clamp). Sits on its OWN bus behind LG so only THIS node carries
    -- the "pv" role.
    INSERT INTO gis.generator
        (id, name, subtype, status, geom, rated_kw, power_factor, voltage_setpoint_pu)
    VALUES
        ('50c00000-0000-0000-0000-000000000020', 'GEN-P', st_gen_pv, 'published',
         ST_SetSRID(ST_MakePoint(-0.2200, 5.5983), 4326), 20, 1.0, 1.02)
    ON CONFLICT (id) DO NOTHING;

    -- LVA: the SHORT 400 V infeed leg from SRC-A to BUS L (~100 m: 0.0009 deg of
    -- latitude, pure N-S). Impedance Z_LVA.
    INSERT INTO gis.lv_conductor
        (id, name, subtype, status, geom, phase, conductor_spec, lv_role)
    VALUES
        ('50c00000-0000-0000-0000-000000000011', 'LVA', st_lv_oh, 'published',
         ST_SetSRID(ST_GeomFromText('LINESTRING(-0.2200 5.6000, -0.2200 5.5991)'), 4326),
         'L1L2L3N', 'WAVEFORM_185', 'main')
    ON CONFLICT (id) DO NOTHING;

    -- LVB: the LONG 400 V infeed leg from SRC-B to BUS L -- EXACTLY twice LVA's
    -- length (~200 m: 0.0018 deg of latitude, pure N-S, same conductor_spec) so
    -- Z_LVB = 2 * Z_LVA. The 2:1 impedance ratio is what drives the exact 2:1
    -- supplied split. Geometry offset west is decorative; connectivity is declared.
    INSERT INTO gis.lv_conductor
        (id, name, subtype, status, geom, phase, conductor_spec, lv_role)
    VALUES
        ('50c00000-0000-0000-0000-000000000012', 'LVB', st_lv_oh, 'published',
         ST_SetSRID(ST_GeomFromText('LINESTRING(-0.2202 5.6008, -0.2202 5.5990)'), 4326),
         'L1L2L3N', 'WAVEFORM_185', 'main')
    ON CONFLICT (id) DO NOTHING;

    -- LG: the short 400 V feeder from BUS L to the PV generator. Finite impedance
    -- (a real branch) so GEN-P lands on its OWN bus -- the PV role attaches to
    -- GEN-P alone, not to the load bus.
    INSERT INTO gis.lv_conductor
        (id, name, subtype, status, geom, phase, conductor_spec, lv_role)
    VALUES
        ('50c00000-0000-0000-0000-000000000013', 'LG', st_lv_oh, 'published',
         ST_SetSRID(ST_GeomFromText('LINESTRING(-0.2200 5.5990, -0.2200 5.5984)'), 4326),
         'L1L2L3N', 'WAVEFORM_185', 'main')
    ON CONFLICT (id) DO NOTHING;

    -- =====================================================================
    -- 7. Insert the runtime.feature_connectivity rows (the actual graph).
    --    Edge endpoints carry NULL terminal ids. ON CONFLICT on the
    --    (from,to)+terminals unique key => idempotent. LVA/LVB/LG all present
    --    an end at MTR-M.T1 (BUS L); LVA/LVB present their other end at their
    --    source's T2; LG presents its other end at GEN-P.T1.
    -- =====================================================================

    -- 1. SRC-A.T2 -> LVA  (first infeed leg, source end)
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_xfmr_t2_lv, fc_dist_xfmr, '50c00000-0000-0000-0000-000000000001', t_xfmr_t2,
         fc_lv_cond, '50c00000-0000-0000-0000-000000000011', NULL, 'seed:nodal_multi_source_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 2. LVA -> MTR-M.T1  (first infeed leg, common-bus end)
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_lv_meter_t1, fc_lv_cond, '50c00000-0000-0000-0000-000000000011', NULL,
         fc_meter, '50c00000-0000-0000-0000-000000000004', t_meter_t1, 'seed:nodal_multi_source_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 3. SRC-B.T2 -> LVB  (second infeed leg, source end)
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_xfmr_t2_lv, fc_dist_xfmr, '50c00000-0000-0000-0000-000000000002', t_xfmr_t2,
         fc_lv_cond, '50c00000-0000-0000-0000-000000000012', NULL, 'seed:nodal_multi_source_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 4. LVB -> MTR-M.T1  (second infeed leg, common-bus end -- same node as LVA)
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_lv_meter_t1, fc_lv_cond, '50c00000-0000-0000-0000-000000000012', NULL,
         fc_meter, '50c00000-0000-0000-0000-000000000004', t_meter_t1, 'seed:nodal_multi_source_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 5. LG -> MTR-M.T1  (generator feeder, common-bus end)
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_lv_meter_t1, fc_lv_cond, '50c00000-0000-0000-0000-000000000013', NULL,
         fc_meter, '50c00000-0000-0000-0000-000000000004', t_meter_t1, 'seed:nodal_multi_source_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 6. LG -> GEN-P.T1  (generator feeder, generator end -- puts GEN-P on its
    --    own PV bus behind LG)
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_lv_gen_t1, fc_lv_cond, '50c00000-0000-0000-0000-000000000013', NULL,
         fc_gen, '50c00000-0000-0000-0000-000000000020', t_gen_t1, 'seed:nodal_multi_source_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 7. MTR-M.T2 -> SP-M.T1  (the common load off the meter's downstream side)
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_meter_t2_sp, fc_meter, '50c00000-0000-0000-0000-000000000004', t_meter_t2,
         fc_sp, '50c00000-0000-0000-0000-000000000006', t_sp_t1, 'seed:nodal_multi_source_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    RAISE NOTICE 'seed/nodal_multi_source_example.sql applied: 8 features + 7 connectivity rows, TWO ideal 400 V slacks (SRC-A, SRC-B) feeding one common bus via LVA:LVB = 1:2 impedance, plus a PV generator GEN-P (voltage_setpoint_pu 1.02). Run POST /analysis/nodal-load-flow start=SRC-A sources=[SRC-B] nominal_voltage_v=400; expect slack_count=2, supplied_kw(SRC-A)=2*supplied_kw(SRC-B) exactly, GEN-P role=pv at 408.0 V.';
END $$;
