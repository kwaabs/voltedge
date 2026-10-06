-- seed/protection_network_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives
-- under /seed on purpose, same reasoning as every other seed file.
--
-- A THIRD real-feature-instance seed (siblings: seed/test_network_example.sql,
-- seed/substation_network_example.sql), built specifically to exercise the
-- PROTECTION COORDINATION engine (POST /analysis/coordination -->
-- api/internal/powerflow/coordination.go). Where the other two seeds give
-- tracing / load flow / fault a network, this one gives the coordination
-- simulation a gradeable protection CHAIN: a source feeder with two operating
-- protective devices in series -- an MV_BREAKER graded by a wired
-- PROTECTIVE_RELAY (IDMT overcurrent) upstream, and an MV_FUSE downstream --
-- plus the instrument transformers (MV_CT / MV_VT) the relay senses. Running a
-- coordination from the source to a downstream fault node then grades the
-- BRK-1 (upstream, primary=relay) <-> FUSE-1 (downstream) pair and draws their
-- TCC curves.
--
-- It writes actual ROWS into three places:
--   * gis.* physical tables (the features),
--   * runtime.feature_connectivity (the in-line electrical graph), and
--   * runtime.feature_relationships (the NON-connectivity protection wiring --
--     relay trips breaker, relay senses CT/VT). This is the FIRST instance
--     seed to write feature_relationships; the relay itself is geometry-less
--     (network_role 'none') and lives ONLY on those relationship edges.
--
-- Its features use a distinct uuid prefix (9a070000-...) and a distinct map
-- area (~-0.35, 5.72) from the other two seeds, so all three coexist on one DB.
--
-- ASCII TOPOLOGY (radial; the relay + instrument transformers hang OFF the
-- line, they are not in series with it):
--
--   SUB-SOURCE  (POWER_XFMR 33/11 kV; grid-infeed source, tier 200 -- NOT a
--               DIST_XFMR, whose load-side tier 400 the tier guard would block)
--     | LV
--     +-- MV-1 (MV_CONDUCTOR, 11 kV) --------------------+--(tap)--> CT-1 (MV_CT,  protection core)
--          | T1                                          +--(tap)--> VT-1 (MV_VT,  line-to-ground)
--          BRK-1 (MV_BREAKER, CLOSED)  T2                        ^         ^
--          |          ^                                         |         |
--          |          | protection_trip  <---- RLY-1 (PROTECTIVE_RELAY, 50/51, IEC_SI)
--          |          |                              |  protection_sensing (senses CT-1, VT-1)
--          +-- MV-2 (MV_CONDUCTOR, 11 kV)            +--------------------+
--               | T1
--               FUSE-1 (MV_FUSE, 100T, curve_ref=FUSE_100T_EXAMPLE) T2
--               |
--               +-- MV-3 (MV_CONDUCTOR, 11 kV)   <-- suggested FAULT node
--                    | T1
--                    TX-1 (DIST_XFMR, 11 kV -> 400 V, 500 kVA) T2
--                    |
--                    +-- LV-1 (LV_CONDUCTOR, 400 V)
--                         +-- MTR-1 (METER) T2 --> SP-1 (SERVICE_POINT, residential, 12 kW)
--
-- HOW TO RUN THE PROTECTION SIMULATION once this seed is applied:
--   POST /analysis/coordination
--     { "start": {"feature_class":"POWER_XFMR",   "feature_id":"9a070000-0000-0000-0000-000000000001"},
--       "fault": {"feature_class":"MV_CONDUCTOR", "feature_id":"9a070000-0000-0000-0000-000000000013"},
--       "nominal_voltage_v": 11000 }
--   (SUB-SOURCE is a POWER_XFMR, not a DIST_XFMR -- see the tier note at step 7.)
--   (or in the web UI: select SUB-SOURCE -> "Protection coordination…" to arm
--    the source, select MV-3 -> "Set fault", then Run -- the TCC chart shows
--    RLY-1/BRK-1's IEC_SI curve above FUSE-1's total-clear curve, both crossing
--    the vertical fault-current line, with the coordinated/miscoordinated
--    verdict + the BRK-1<->FUSE-1 margin vs the 0.3 s CTI.)
-- The fault node can be any node the walk from SUB-SOURCE reaches; MV-3 is
-- chosen because it sits DOWNSTREAM of BOTH protective devices, so both land on
-- the series start->fault path and both get graded.
--
-- HARD PREREQUISITES -- every metadata seed below must be applied AND the
-- classes it defines COMPILED (task compile CODE=...) BEFORE this seed. The
-- protection classes additionally need two migrations (0075 widens the
-- relationship-kind CHECK to admit protection_trip/protection_sensing; 0076
-- adds the protection_curves catalog tables) applied via `task migrate`:
--
--   task migrate                         -> migrations 0075 + 0076 (relationship
--                                           kinds + protection_curves tables)
--   seed/connectivity_example.sql        -> MV_CONDUCTOR, DIST_XFMR (base), 11KV
--   seed/power_transformer_example.sql   -> POWER_XFMR (SUB-SOURCE's class, incl.
--                                           source_r_ohm/x_ohm/r0/x0) + the
--                                           POWER_XFMR LV -> MV_CONDUCTOR rule
--                                           (needs migration 0070 first)
--   seed/load_flow_fields.sql            -> SERVICE_POINT.demand_kw/power_factor,
--                                           DIST_XFMR.rated_kva/source_r_ohm/source_x_ohm
--   seed/transformer_model_fields.sql    -> DIST_XFMR.secondary_voltage/impedance_pct/x_r_ratio
--   seed/zero_seq_fields.sql             -> DIST_XFMR.z0_pct/source_r0_ohm/source_x0_ohm
--   seed/mv_conductor_specs.sql          -> MV_LINE_SPEC + MV_CONDUCTOR.conductor_spec
--   seed/lv_conductors_example.sql       -> LV_CONDUCTOR (+ DIST_XFMR T2 -> LV_CONDUCTOR)
--   seed/lv_metering_example.sql         -> METER (+ LV_CONDUCTOR -> METER, METER -> SERVICE_POINT)
--   seed/service_point_example.sql       -> SERVICE_POINT (+ DIST_XFMR T2 -> MV_CONDUCTOR)
--   seed/mv_switching_example.sql        -> MV_BREAKER (+ MV_CONDUCTOR<->MV_BREAKER rules)
--   seed/mv_protection_example.sql       -> MV_FUSE (+ MV_CONDUCTOR<->MV_FUSE rules)
--   seed/mv_measuring_example.sql        -> MV_CT + MV_VT (+ MV_CONDUCTOR -> MV_CT/MV_VT T1)
--   seed/protective_relay_example.sql    -> PROTECTIVE_RELAY (base class, device_number)
--   seed/mv_relay_example.sql            -> +model/condition_rating +the
--                                           protection_trip/protection_sensing relationship_classes
--   seed/protection_curves_example.sql   -> the TCC catalog (IEC_SI ... FUSE_100T_EXAMPLE)
--                                           + relay settings fields (curve_type,
--                                           pickup_current_a, time_dial, inst_pickup_a,
--                                           definite_time_s) + MV_FUSE.curve_ref/fuse_rating_a/fuse_speed
--
-- then, once the metadata is in place, compile each participating class:
--   task compile CODE=DIST_XFMR
--   task compile CODE=POWER_XFMR
--   task compile CODE=MV_CONDUCTOR
--   task compile CODE=LV_CONDUCTOR
--   task compile CODE=METER
--   task compile CODE=SERVICE_POINT
--   task compile CODE=MV_BREAKER
--   task compile CODE=MV_FUSE
--   task compile CODE=MV_CT
--   task compile CODE=MV_VT
--   task compile CODE=PROTECTIVE_RELAY
--
-- Task shortcuts for the prerequisites (see Taskfile.yml): seed-connectivity,
-- seed-power-transformer (then compile CODE=POWER_XFMR),
-- seed-load-flow-fields, seed-transformer-fields, seed-zero-seq-fields,
-- seed-mv-conductor-specs, seed-lv-conductors, seed-lv-metering,
-- seed-service-point, seed-mv-switching, seed-mv-protection, seed-mv-measuring,
-- seed-mv-relay (applies protective_relay_example.sql first), seed-protection-curves.
--
-- The DO block fails LOUDLY (RAISE EXCEPTION, naming the missing prerequisite)
-- if any required class, subtype, terminal, electrical column, network_rule, or
-- relationship_class is absent. It is fully idempotent: feature rows use fixed
-- uuids with ON CONFLICT (id) DO NOTHING, connectivity rows conflict on the
-- (from,to)+terminals key, and relationship rows conflict on
-- (relationship_class_id, origin_feature_id, destination_feature_id).
--
-- Apply with psql (NOT the migration runner), after the prerequisites above:
--   task seed-protection-network   (or the psql -f invocation directly)
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL has
-- been reviewed statically -- a live psql apply against a fully-compiled DB + a
-- POST /analysis/coordination re-check are still owed to call it verified.

DO $$
DECLARE
    -- feature_class ids
    fc_dist_xfmr  uuid;
    fc_power_xfmr uuid;  -- SUB-SOURCE's class (grid-infeed, tier 200) -- the
                         -- MV source must sit at/below MV tier or the trace-tier
                         -- guard (migration 0126) blocks the source->MV step.
    fc_mv_cond   uuid;
    fc_lv_cond   uuid;
    fc_breaker   uuid;
    fc_fuse      uuid;
    fc_relay     uuid;
    fc_ct        uuid;
    fc_vt        uuid;
    fc_meter     uuid;
    fc_sp        uuid;

    -- subtype INTEGER codes (the value stored in gis.<class>.subtype)
    st_dxf_pad   integer;  -- DIST_XFMR 'pad_mounted'
    st_pxf_2w    integer;  -- POWER_XFMR 'two_winding' (code, for .subtype)
    sid_pxf_2w   uuid;     -- POWER_XFMR 'two_winding' (id, to scope its terminals)
    st_brk_vac   integer;  -- MV_BREAKER 'vacuum_breaker'
    st_fuse_exp  integer;  -- MV_FUSE 'expulsion_fuse'
    st_relay_std integer;  -- PROTECTIVE_RELAY 'standard'
    st_ct_prot   integer;  -- MV_CT 'protection_ct'
    st_vt_ind    integer;  -- MV_VT 'inductive_vt'
    st_meter_wc  integer;  -- METER 'whole_current'
    st_sp_res    integer;  -- SERVICE_POINT 'residential'
    st_mv_oh     integer;  -- MV_CONDUCTOR 'overhead_mv_conductor'
    st_lv_oh     integer;  -- LV_CONDUCTOR 'overhead_lv_conductor'

    -- terminal ids
    t_dxf_t1   uuid;
    t_dxf_t2   uuid;
    t_pxf_lv   uuid;  -- POWER_XFMR LV (secondary) terminal -- SUB-SOURCE's MV feed
    t_brk_t1   uuid;
    t_brk_t2   uuid;
    t_fuse_t1  uuid;
    t_fuse_t2  uuid;
    t_ct_t1    uuid;
    t_vt_t1    uuid;
    t_meter_t1 uuid;
    t_meter_t2 uuid;
    t_sp_t1    uuid;

    -- network_rule ids
    r_pxf_lv_mv   uuid;  -- POWER_XFMR LV -> MV_CONDUCTOR (SUB-SOURCE -> MV-1)
    r_mv_brk_t1   uuid;  -- MV_CONDUCTOR -> MV_BREAKER T1
    r_brk_t2_mv   uuid;  -- MV_BREAKER T2 -> MV_CONDUCTOR
    r_mv_fuse_t1  uuid;  -- MV_CONDUCTOR -> MV_FUSE T1
    r_fuse_t2_mv  uuid;  -- MV_FUSE T2 -> MV_CONDUCTOR
    r_mv_ct_t1    uuid;  -- MV_CONDUCTOR -> MV_CT T1
    r_mv_vt_t1    uuid;  -- MV_CONDUCTOR -> MV_VT T1
    r_mv_dxf_t1   uuid;  -- MV_CONDUCTOR -> DIST_XFMR T1
    r_dxf_t2_lv   uuid;  -- DIST_XFMR T2 -> LV_CONDUCTOR
    r_lv_meter_t1 uuid;  -- LV_CONDUCTOR -> METER T1
    r_meter_t2_sp uuid;  -- METER T2 -> SERVICE_POINT T1

    -- relationship_class ids (the protection wiring, NOT connectivity)
    rc_trip_brk  uuid;  -- 'PROTECTIVE_RELAY trips MV_BREAKER'  (kind protection_trip)
    rc_sense_ct  uuid;  -- 'PROTECTIVE_RELAY senses MV_CT'      (kind protection_sensing)
    rc_sense_vt  uuid;  -- 'PROTECTIVE_RELAY senses MV_VT'      (kind protection_sensing)
BEGIN
    -- =====================================================================
    -- 1. Resolve feature_class ids (fail if a class was never seeded).
    -- =====================================================================
    SELECT id INTO fc_dist_xfmr FROM metadata.feature_classes WHERE code = 'DIST_XFMR';
    IF fc_dist_xfmr IS NULL THEN RAISE EXCEPTION 'missing feature_class DIST_XFMR -- apply seed/connectivity_example.sql then: task compile CODE=DIST_XFMR'; END IF;

    SELECT id INTO fc_power_xfmr FROM metadata.feature_classes WHERE code = 'POWER_XFMR';
    IF fc_power_xfmr IS NULL THEN RAISE EXCEPTION 'missing feature_class POWER_XFMR -- apply seed/power_transformer_example.sql then: task compile CODE=POWER_XFMR'; END IF;

    SELECT id INTO fc_mv_cond FROM metadata.feature_classes WHERE code = 'MV_CONDUCTOR';
    IF fc_mv_cond IS NULL THEN RAISE EXCEPTION 'missing feature_class MV_CONDUCTOR -- apply seed/connectivity_example.sql then: task compile CODE=MV_CONDUCTOR'; END IF;

    SELECT id INTO fc_lv_cond FROM metadata.feature_classes WHERE code = 'LV_CONDUCTOR';
    IF fc_lv_cond IS NULL THEN RAISE EXCEPTION 'missing feature_class LV_CONDUCTOR -- apply seed/lv_conductors_example.sql then: task compile CODE=LV_CONDUCTOR'; END IF;

    SELECT id INTO fc_breaker FROM metadata.feature_classes WHERE code = 'MV_BREAKER';
    IF fc_breaker IS NULL THEN RAISE EXCEPTION 'missing feature_class MV_BREAKER -- apply seed/mv_switching_example.sql then: task compile CODE=MV_BREAKER'; END IF;

    SELECT id INTO fc_fuse FROM metadata.feature_classes WHERE code = 'MV_FUSE';
    IF fc_fuse IS NULL THEN RAISE EXCEPTION 'missing feature_class MV_FUSE -- apply seed/mv_protection_example.sql then: task compile CODE=MV_FUSE'; END IF;

    SELECT id INTO fc_relay FROM metadata.feature_classes WHERE code = 'PROTECTIVE_RELAY';
    IF fc_relay IS NULL THEN RAISE EXCEPTION 'missing feature_class PROTECTIVE_RELAY -- apply seed/protective_relay_example.sql then: task compile CODE=PROTECTIVE_RELAY'; END IF;

    SELECT id INTO fc_ct FROM metadata.feature_classes WHERE code = 'MV_CT';
    IF fc_ct IS NULL THEN RAISE EXCEPTION 'missing feature_class MV_CT -- apply seed/mv_measuring_example.sql then: task compile CODE=MV_CT'; END IF;

    SELECT id INTO fc_vt FROM metadata.feature_classes WHERE code = 'MV_VT';
    IF fc_vt IS NULL THEN RAISE EXCEPTION 'missing feature_class MV_VT -- apply seed/mv_measuring_example.sql then: task compile CODE=MV_VT'; END IF;

    SELECT id INTO fc_meter FROM metadata.feature_classes WHERE code = 'METER';
    IF fc_meter IS NULL THEN RAISE EXCEPTION 'missing feature_class METER -- apply seed/lv_metering_example.sql then: task compile CODE=METER'; END IF;

    SELECT id INTO fc_sp FROM metadata.feature_classes WHERE code = 'SERVICE_POINT';
    IF fc_sp IS NULL THEN RAISE EXCEPTION 'missing feature_class SERVICE_POINT -- apply seed/service_point_example.sql then: task compile CODE=SERVICE_POINT'; END IF;

    -- =====================================================================
    -- 2. Resolve subtype INTEGER codes (the value stored in .subtype).
    -- =====================================================================
    SELECT code INTO st_dxf_pad FROM metadata.subtypes WHERE feature_class_id = fc_dist_xfmr AND name = 'pad_mounted';
    IF st_dxf_pad IS NULL THEN RAISE EXCEPTION 'DIST_XFMR subtype ''pad_mounted'' not found -- re-apply seed/connectivity_example.sql'; END IF;

    SELECT code, id INTO st_pxf_2w, sid_pxf_2w FROM metadata.subtypes WHERE feature_class_id = fc_power_xfmr AND name = 'two_winding';
    IF sid_pxf_2w IS NULL THEN RAISE EXCEPTION 'POWER_XFMR subtype ''two_winding'' not found -- re-apply seed/power_transformer_example.sql'; END IF;

    SELECT code INTO st_brk_vac FROM metadata.subtypes WHERE feature_class_id = fc_breaker AND name = 'vacuum_breaker';
    IF st_brk_vac IS NULL THEN RAISE EXCEPTION 'MV_BREAKER subtype ''vacuum_breaker'' not found -- re-apply seed/mv_switching_example.sql'; END IF;

    SELECT code INTO st_fuse_exp FROM metadata.subtypes WHERE feature_class_id = fc_fuse AND name = 'expulsion_fuse';
    IF st_fuse_exp IS NULL THEN RAISE EXCEPTION 'MV_FUSE subtype ''expulsion_fuse'' not found -- re-apply seed/mv_protection_example.sql'; END IF;

    SELECT code INTO st_relay_std FROM metadata.subtypes WHERE feature_class_id = fc_relay AND name = 'standard';
    IF st_relay_std IS NULL THEN RAISE EXCEPTION 'PROTECTIVE_RELAY subtype ''standard'' not found -- re-apply seed/protective_relay_example.sql'; END IF;

    SELECT code INTO st_ct_prot FROM metadata.subtypes WHERE feature_class_id = fc_ct AND name = 'protection_ct';
    IF st_ct_prot IS NULL THEN RAISE EXCEPTION 'MV_CT subtype ''protection_ct'' not found -- re-apply seed/mv_measuring_example.sql'; END IF;

    SELECT code INTO st_vt_ind FROM metadata.subtypes WHERE feature_class_id = fc_vt AND name = 'inductive_vt';
    IF st_vt_ind IS NULL THEN RAISE EXCEPTION 'MV_VT subtype ''inductive_vt'' not found -- re-apply seed/mv_measuring_example.sql'; END IF;

    SELECT code INTO st_meter_wc FROM metadata.subtypes WHERE feature_class_id = fc_meter AND name = 'whole_current';
    IF st_meter_wc IS NULL THEN RAISE EXCEPTION 'METER subtype ''whole_current'' not found -- re-apply seed/lv_metering_example.sql'; END IF;

    SELECT code INTO st_sp_res FROM metadata.subtypes WHERE feature_class_id = fc_sp AND name = 'residential';
    IF st_sp_res IS NULL THEN RAISE EXCEPTION 'SERVICE_POINT subtype ''residential'' not found -- re-apply seed/service_point_example.sql'; END IF;

    SELECT code INTO st_mv_oh FROM metadata.subtypes WHERE feature_class_id = fc_mv_cond AND name = 'overhead_mv_conductor';
    IF st_mv_oh IS NULL THEN RAISE EXCEPTION 'MV_CONDUCTOR subtype ''overhead_mv_conductor'' not found -- re-apply seed/connectivity_example.sql'; END IF;

    SELECT code INTO st_lv_oh FROM metadata.subtypes WHERE feature_class_id = fc_lv_cond AND name = 'overhead_lv_conductor';
    IF st_lv_oh IS NULL THEN RAISE EXCEPTION 'LV_CONDUCTOR subtype ''overhead_lv_conductor'' not found -- re-apply seed/lv_conductors_example.sql'; END IF;

    -- =====================================================================
    -- 3. Resolve terminal ids (all subtype-agnostic here: subtype_id NULL).
    --    MV_CT has only T1 (an in-line tap); MV_VT's T2 is a ground dead-end
    --    (no rule), so only its T1 is resolved.
    -- =====================================================================
    SELECT id INTO t_dxf_t1 FROM metadata.terminals WHERE feature_class_id = fc_dist_xfmr AND code = 'T1' ORDER BY subtype_id NULLS LAST LIMIT 1;
    SELECT id INTO t_dxf_t2 FROM metadata.terminals WHERE feature_class_id = fc_dist_xfmr AND code = 'T2' ORDER BY subtype_id NULLS LAST LIMIT 1;
    IF t_dxf_t1 IS NULL OR t_dxf_t2 IS NULL THEN RAISE EXCEPTION 'DIST_XFMR terminals T1/T2 not found -- re-apply seed/connectivity_example.sql'; END IF;

    -- POWER_XFMR terminals are SUBTYPE-SCOPED -- resolve LV on the two_winding subtype.
    SELECT id INTO t_pxf_lv FROM metadata.terminals WHERE feature_class_id = fc_power_xfmr AND subtype_id = sid_pxf_2w AND code = 'LV' LIMIT 1;
    IF t_pxf_lv IS NULL THEN RAISE EXCEPTION 'POWER_XFMR two_winding terminal LV not found -- re-apply seed/power_transformer_example.sql'; END IF;

    SELECT id INTO t_brk_t1 FROM metadata.terminals WHERE feature_class_id = fc_breaker AND code = 'T1' ORDER BY subtype_id NULLS LAST LIMIT 1;
    SELECT id INTO t_brk_t2 FROM metadata.terminals WHERE feature_class_id = fc_breaker AND code = 'T2' ORDER BY subtype_id NULLS LAST LIMIT 1;
    IF t_brk_t1 IS NULL OR t_brk_t2 IS NULL THEN RAISE EXCEPTION 'MV_BREAKER terminals T1/T2 not found -- re-apply seed/mv_switching_example.sql'; END IF;

    SELECT id INTO t_fuse_t1 FROM metadata.terminals WHERE feature_class_id = fc_fuse AND code = 'T1' ORDER BY subtype_id NULLS LAST LIMIT 1;
    SELECT id INTO t_fuse_t2 FROM metadata.terminals WHERE feature_class_id = fc_fuse AND code = 'T2' ORDER BY subtype_id NULLS LAST LIMIT 1;
    IF t_fuse_t1 IS NULL OR t_fuse_t2 IS NULL THEN RAISE EXCEPTION 'MV_FUSE terminals T1/T2 not found -- re-apply seed/mv_protection_example.sql'; END IF;

    SELECT id INTO t_ct_t1 FROM metadata.terminals WHERE feature_class_id = fc_ct AND code = 'T1' ORDER BY subtype_id NULLS LAST LIMIT 1;
    IF t_ct_t1 IS NULL THEN RAISE EXCEPTION 'MV_CT terminal T1 not found -- re-apply seed/mv_measuring_example.sql'; END IF;

    SELECT id INTO t_vt_t1 FROM metadata.terminals WHERE feature_class_id = fc_vt AND code = 'T1' ORDER BY subtype_id NULLS LAST LIMIT 1;
    IF t_vt_t1 IS NULL THEN RAISE EXCEPTION 'MV_VT terminal T1 not found -- re-apply seed/mv_measuring_example.sql'; END IF;

    SELECT id INTO t_meter_t1 FROM metadata.terminals WHERE feature_class_id = fc_meter AND code = 'T1' ORDER BY subtype_id NULLS LAST LIMIT 1;
    SELECT id INTO t_meter_t2 FROM metadata.terminals WHERE feature_class_id = fc_meter AND code = 'T2' ORDER BY subtype_id NULLS LAST LIMIT 1;
    IF t_meter_t1 IS NULL OR t_meter_t2 IS NULL THEN RAISE EXCEPTION 'METER terminals T1/T2 not found -- re-apply seed/lv_metering_example.sql'; END IF;

    SELECT id INTO t_sp_t1 FROM metadata.terminals WHERE feature_class_id = fc_sp AND code = 'T1' ORDER BY subtype_id NULLS LAST LIMIT 1;
    IF t_sp_t1 IS NULL THEN RAISE EXCEPTION 'SERVICE_POINT terminal T1 not found -- re-apply seed/service_point_example.sql'; END IF;

    -- =====================================================================
    -- 4. Guard the compiled tables + ELECTRICAL / PROTECTION COLUMNS.
    -- =====================================================================
    IF to_regclass('gis.dist_xfmr') IS NULL THEN RAISE EXCEPTION 'table gis.dist_xfmr does not exist -- run: task compile CODE=DIST_XFMR'; END IF;
    IF to_regclass('gis.mv_conductor') IS NULL THEN RAISE EXCEPTION 'table gis.mv_conductor does not exist -- run: task compile CODE=MV_CONDUCTOR'; END IF;
    IF to_regclass('gis.lv_conductor') IS NULL THEN RAISE EXCEPTION 'table gis.lv_conductor does not exist -- run: task compile CODE=LV_CONDUCTOR'; END IF;
    IF to_regclass('gis.mv_breaker') IS NULL THEN RAISE EXCEPTION 'table gis.mv_breaker does not exist -- run: task compile CODE=MV_BREAKER'; END IF;
    IF to_regclass('gis.mv_fuse') IS NULL THEN RAISE EXCEPTION 'table gis.mv_fuse does not exist -- run: task compile CODE=MV_FUSE'; END IF;
    IF to_regclass('gis.protective_relay') IS NULL THEN RAISE EXCEPTION 'table gis.protective_relay does not exist -- run: task compile CODE=PROTECTIVE_RELAY'; END IF;
    IF to_regclass('gis.mv_ct') IS NULL THEN RAISE EXCEPTION 'table gis.mv_ct does not exist -- run: task compile CODE=MV_CT'; END IF;
    IF to_regclass('gis.mv_vt') IS NULL THEN RAISE EXCEPTION 'table gis.mv_vt does not exist -- run: task compile CODE=MV_VT'; END IF;
    IF to_regclass('gis.meter') IS NULL THEN RAISE EXCEPTION 'table gis.meter does not exist -- run: task compile CODE=METER'; END IF;
    IF to_regclass('gis.service_point') IS NULL THEN RAISE EXCEPTION 'table gis.service_point does not exist -- run: task compile CODE=SERVICE_POINT'; END IF;

    -- DIST_XFMR: source Thevenin + step-down nameplate columns.
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'dist_xfmr' AND column_name = 'source_r_ohm') THEN
        RAISE EXCEPTION 'gis.dist_xfmr is missing column source_r_ohm -- apply seed/load_flow_fields.sql then: task compile CODE=DIST_XFMR'; END IF;
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'dist_xfmr' AND column_name = 'secondary_voltage') THEN
        RAISE EXCEPTION 'gis.dist_xfmr is missing column secondary_voltage -- apply seed/transformer_model_fields.sql then: task compile CODE=DIST_XFMR'; END IF;

    -- POWER_XFMR (SUB-SOURCE): the table + the grid-infeed source column the fault
    -- engine reads off the start node (added to POWER_XFMR by the updated
    -- seed/power_transformer_example.sql).
    IF to_regclass('gis.power_xfmr') IS NULL THEN RAISE EXCEPTION 'table gis.power_xfmr does not exist -- apply seed/power_transformer_example.sql then: task compile CODE=POWER_XFMR'; END IF;
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'power_xfmr' AND column_name = 'source_r_ohm') THEN
        RAISE EXCEPTION 'gis.power_xfmr is missing column source_r_ohm -- re-apply the updated seed/power_transformer_example.sql then: task compile CODE=POWER_XFMR'; END IF;

    -- PROTECTIVE_RELAY: the IDMT setting columns the coordination engine reads.
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'protective_relay' AND column_name = 'device_number') THEN
        RAISE EXCEPTION 'gis.protective_relay is missing column device_number -- apply seed/protective_relay_example.sql then: task compile CODE=PROTECTIVE_RELAY'; END IF;
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'protective_relay' AND column_name = 'curve_type') THEN
        RAISE EXCEPTION 'gis.protective_relay is missing column curve_type -- apply seed/protection_curves_example.sql then: task compile CODE=PROTECTIVE_RELAY'; END IF;
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'protective_relay' AND column_name = 'pickup_current_a') THEN
        RAISE EXCEPTION 'gis.protective_relay is missing column pickup_current_a -- apply seed/protection_curves_example.sql then: task compile CODE=PROTECTIVE_RELAY'; END IF;

    -- MV_FUSE: curve_ref (the fuse's link into the TCC catalog).
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'mv_fuse' AND column_name = 'curve_ref') THEN
        RAISE EXCEPTION 'gis.mv_fuse is missing column curve_ref -- apply seed/protection_curves_example.sql then: task compile CODE=MV_FUSE'; END IF;

    -- MV_CT / MV_VT: ratio columns.
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'mv_ct' AND column_name = 'ct_ratio') THEN
        RAISE EXCEPTION 'gis.mv_ct is missing column ct_ratio -- run: task compile CODE=MV_CT'; END IF;
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'mv_vt' AND column_name = 'vt_ratio') THEN
        RAISE EXCEPTION 'gis.mv_vt is missing column vt_ratio -- run: task compile CODE=MV_VT'; END IF;

    -- MV_CONDUCTOR: conductor_spec (per-km impedance link, drives fault level).
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'mv_conductor' AND column_name = 'conductor_spec') THEN
        RAISE EXCEPTION 'gis.mv_conductor is missing column conductor_spec -- apply seed/mv_conductor_specs.sql then: task compile CODE=MV_CONDUCTOR'; END IF;

    -- SERVICE_POINT: real demand inputs.
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'service_point' AND column_name = 'demand_kw') THEN
        RAISE EXCEPTION 'gis.service_point is missing column demand_kw -- apply seed/load_flow_fields.sql then: task compile CODE=SERVICE_POINT'; END IF;

    -- =====================================================================
    -- 5. Resolve the network_rule ids that authorize each connection.
    -- =====================================================================
    SELECT id INTO r_pxf_lv_mv FROM metadata.network_rules
     WHERE from_feature_class_id = fc_power_xfmr AND to_feature_class_id = fc_mv_cond
       AND from_terminal_id IS NOT DISTINCT FROM t_pxf_lv AND to_terminal_id IS NULL
       AND is_valid = true LIMIT 1;
    IF r_pxf_lv_mv IS NULL THEN RAISE EXCEPTION 'network_rule "POWER_XFMR LV -> MV_CONDUCTOR" not found/valid -- apply seed/power_transformer_example.sql'; END IF;

    SELECT id INTO r_mv_brk_t1 FROM metadata.network_rules
     WHERE from_feature_class_id = fc_mv_cond AND to_feature_class_id = fc_breaker
       AND from_terminal_id IS NULL AND to_terminal_id IS NOT DISTINCT FROM t_brk_t1
       AND is_valid = true LIMIT 1;
    IF r_mv_brk_t1 IS NULL THEN RAISE EXCEPTION 'network_rule "MV_CONDUCTOR -> MV_BREAKER T1" not found/valid -- apply seed/mv_switching_example.sql'; END IF;

    SELECT id INTO r_brk_t2_mv FROM metadata.network_rules
     WHERE from_feature_class_id = fc_breaker AND to_feature_class_id = fc_mv_cond
       AND from_terminal_id IS NOT DISTINCT FROM t_brk_t2 AND to_terminal_id IS NULL
       AND is_valid = true LIMIT 1;
    IF r_brk_t2_mv IS NULL THEN RAISE EXCEPTION 'network_rule "MV_BREAKER T2 -> MV_CONDUCTOR" not found/valid -- apply seed/mv_switching_example.sql'; END IF;

    SELECT id INTO r_mv_fuse_t1 FROM metadata.network_rules
     WHERE from_feature_class_id = fc_mv_cond AND to_feature_class_id = fc_fuse
       AND from_terminal_id IS NULL AND to_terminal_id IS NOT DISTINCT FROM t_fuse_t1
       AND is_valid = true LIMIT 1;
    IF r_mv_fuse_t1 IS NULL THEN RAISE EXCEPTION 'network_rule "MV_CONDUCTOR -> MV_FUSE T1" not found/valid -- apply seed/mv_protection_example.sql'; END IF;

    SELECT id INTO r_fuse_t2_mv FROM metadata.network_rules
     WHERE from_feature_class_id = fc_fuse AND to_feature_class_id = fc_mv_cond
       AND from_terminal_id IS NOT DISTINCT FROM t_fuse_t2 AND to_terminal_id IS NULL
       AND is_valid = true LIMIT 1;
    IF r_fuse_t2_mv IS NULL THEN RAISE EXCEPTION 'network_rule "MV_FUSE T2 -> MV_CONDUCTOR" not found/valid -- apply seed/mv_protection_example.sql'; END IF;

    SELECT id INTO r_mv_ct_t1 FROM metadata.network_rules
     WHERE from_feature_class_id = fc_mv_cond AND to_feature_class_id = fc_ct
       AND from_terminal_id IS NULL AND to_terminal_id IS NOT DISTINCT FROM t_ct_t1
       AND is_valid = true LIMIT 1;
    IF r_mv_ct_t1 IS NULL THEN RAISE EXCEPTION 'network_rule "MV_CONDUCTOR -> MV_CT T1" not found/valid -- apply seed/mv_measuring_example.sql'; END IF;

    SELECT id INTO r_mv_vt_t1 FROM metadata.network_rules
     WHERE from_feature_class_id = fc_mv_cond AND to_feature_class_id = fc_vt
       AND from_terminal_id IS NULL AND to_terminal_id IS NOT DISTINCT FROM t_vt_t1
       AND is_valid = true LIMIT 1;
    IF r_mv_vt_t1 IS NULL THEN RAISE EXCEPTION 'network_rule "MV_CONDUCTOR -> MV_VT T1" not found/valid -- apply seed/mv_measuring_example.sql'; END IF;

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
    -- 6. Resolve the relationship_class ids (the protection wiring). Matched
    --    by (origin=PROTECTIVE_RELAY, destination=device, kind) -- exactly how
    --    the coordination engine queries feature_relationships -- so a rename
    --    of the human-facing name can't break this seed.
    -- =====================================================================
    SELECT id INTO rc_trip_brk FROM metadata.relationship_classes
     WHERE origin_feature_class_id = fc_relay AND destination_feature_class_id = fc_breaker
       AND kind = 'protection_trip' LIMIT 1;
    IF rc_trip_brk IS NULL THEN RAISE EXCEPTION 'relationship_class PROTECTIVE_RELAY -protection_trip-> MV_BREAKER not found -- apply migration 0075 (task migrate) + seed/mv_relay_example.sql'; END IF;

    SELECT id INTO rc_sense_ct FROM metadata.relationship_classes
     WHERE origin_feature_class_id = fc_relay AND destination_feature_class_id = fc_ct
       AND kind = 'protection_sensing' LIMIT 1;
    IF rc_sense_ct IS NULL THEN RAISE EXCEPTION 'relationship_class PROTECTIVE_RELAY -protection_sensing-> MV_CT not found -- apply seed/mv_measuring_example.sql before seed/mv_relay_example.sql (so the sensing class is generated)'; END IF;

    SELECT id INTO rc_sense_vt FROM metadata.relationship_classes
     WHERE origin_feature_class_id = fc_relay AND destination_feature_class_id = fc_vt
       AND kind = 'protection_sensing' LIMIT 1;
    IF rc_sense_vt IS NULL THEN RAISE EXCEPTION 'relationship_class PROTECTIVE_RELAY -protection_sensing-> MV_VT not found -- apply seed/mv_measuring_example.sql before seed/mv_relay_example.sql (so the sensing class is generated)'; END IF;

    -- =====================================================================
    -- 7. Insert the feature rows (status 'published' so they are live).
    --    Fixed uuids + ON CONFLICT (id) DO NOTHING => idempotent.
    -- =====================================================================

    -- SUB-SOURCE: the 33/11 kV primary-substation source. Modelled as a POWER_XFMR
    -- (grid-infeed class, trace_tier 200) -- NOT a DIST_XFMR: a DIST_XFMR sits at
    -- the load-side tier (400), so the cross-layer tier guard (migration 0126)
    -- refuses the downstream source->MV step (400 -> MV 300) and the whole walk
    -- dies at the source. POWER_XFMR (200 -> MV 300) is a legitimate downstream
    -- step. Its LV (11 kV) winding feeds MV-1; the source_r/x pair (referred to
    -- 11 kV) fixes the prospective fault current the coordination engine grades on.
    --
    -- MIGRATION (idempotent): an earlier version of this seed instanced SUB-SOURCE
    -- as a DIST_XFMR under the SAME id. Drop that superseded row and its old source
    -- edge first so re-applying cleanly replaces the class while keeping the
    -- 9a07...001 start id stable. No-op on a fresh DB (nothing to delete).
    DELETE FROM runtime.feature_connectivity
     WHERE from_feature_id = '9a070000-0000-0000-0000-000000000001' AND from_feature_class_id = fc_dist_xfmr;
    DELETE FROM gis.dist_xfmr WHERE id = '9a070000-0000-0000-0000-000000000001';

    INSERT INTO gis.power_xfmr
        (id, name, subtype, status, geom, primary_voltage, secondary_voltage, condition_rating,
         source_r_ohm, source_x_ohm, source_r0_ohm, source_x0_ohm)
    VALUES
        ('9a070000-0000-0000-0000-000000000001', 'SUB-SOURCE', st_pxf_2w, 'published',
         ST_SetSRID(ST_MakePoint(-0.3500, 5.7200), 4326),
         '33KV', 11000, 'GOOD', 0.048, 0.48, 0.06, 0.60)
    ON CONFLICT (id) DO NOTHING;

    -- BRK-1: the upstream MV breaker (CLOSED). Its trip decision is delegated
    -- to RLY-1 via the protection_trip relationship below -- the breaker row
    -- itself carries no protection settings.
    INSERT INTO gis.mv_breaker
        (id, name, subtype, status, geom, device_state, condition_rating)
    VALUES
        ('9a070000-0000-0000-0000-000000000002', 'BRK-1', st_brk_vac, 'published',
         ST_SetSRID(ST_MakePoint(-0.3480, 5.7200), 4326), 'CLOSED', 'GOOD')
    ON CONFLICT (id) DO NOTHING;

    -- FUSE-1: the downstream 100T expulsion fuse. curve_ref points at the
    -- FUSE_100T_EXAMPLE catalog curve -- the engine grades it on that curve's
    -- total_clear band. (device_state CLOSED = fuse intact / carrying.)
    INSERT INTO gis.mv_fuse
        (id, name, subtype, status, geom, device_state, condition_rating,
         fuse_rating_a, fuse_speed, curve_ref)
    VALUES
        ('9a070000-0000-0000-0000-000000000003', 'FUSE-1', st_fuse_exp, 'published',
         ST_SetSRID(ST_MakePoint(-0.3460, 5.7200), 4326), 'CLOSED', 'GOOD',
         100, 'T', 'FUSE_100T_EXAMPLE')
    ON CONFLICT (id) DO NOTHING;

    -- TX-1: the 11 kV -> 400 V step-down feeding the LV load.
    INSERT INTO gis.dist_xfmr
        (id, name, subtype, status, geom, primary_voltage, secondary_voltage, condition_rating,
         rated_kva, impedance_pct, x_r_ratio, z0_pct)
    VALUES
        ('9a070000-0000-0000-0000-000000000004', 'TX-1', st_dxf_pad, 'published',
         ST_SetSRID(ST_MakePoint(-0.3440, 5.7200), 4326), '11KV', 400, 'GOOD', 500, 4.5, 6, 4.0)
    ON CONFLICT (id) DO NOTHING;

    -- RLY-1: the 50/51 overcurrent relay that grades BRK-1. GEOMETRY-LESS
    -- (network_role 'none' -> gis.protective_relay has no geom / territory_id
    -- columns): it lives only on the relationship edges written in step 9.
    -- IEC Standard Inverse curve; the engine reads curve_type + pickup_current_a
    -- + time_dial (+ inst_pickup_a for the instantaneous element).
    --
    -- inst_pickup_a = 15000 A is set ABOVE the ~8.3 kA fault at MV-3 (below FUSE-1),
    -- so the relay's instantaneous (50) element does NOT reach past the downstream
    -- fuse: at that fault the relay falls back to its IEC_SI inverse (51) curve
    -- (~0.46 s at TD 0.30), which clears AFTER FUSE-1 (~0.022 s) by more than the
    -- 0.3 s CTI -> a COORDINATED pair. A low instantaneous (e.g. 2500 A) would trip
    -- the breaker first and mis-coordinate. DO UPDATE so re-applying reasserts the
    -- setting over any pre-existing RLY-1 row.
    INSERT INTO gis.protective_relay
        (id, name, subtype, status, device_number,
         curve_type, pickup_current_a, time_dial, inst_pickup_a,
         manufacturer, model, condition_rating)
    VALUES
        ('9a070000-0000-0000-0000-000000000005', 'RLY-1', st_relay_std, 'published', '50_51',
         'IEC_SI', 100, 0.30, 15000,
         'ExampleCo', 'PR-100', 'GOOD')
    ON CONFLICT (id) DO UPDATE SET inst_pickup_a = EXCLUDED.inst_pickup_a;

    -- CT-1: protection-class current transformer feeding RLY-1 (200/5 A).
    INSERT INTO gis.mv_ct
        (id, name, subtype, status, geom, ct_ratio, condition_rating)
    VALUES
        ('9a070000-0000-0000-0000-000000000006', 'CT-1', st_ct_prot, 'published',
         ST_SetSRID(ST_MakePoint(-0.3490, 5.7202), 4326), '200_5', 'GOOD')
    ON CONFLICT (id) DO NOTHING;

    -- VT-1: inductive voltage transformer feeding RLY-1 (11 kV / 110 V).
    INSERT INTO gis.mv_vt
        (id, name, subtype, status, geom, vt_ratio, condition_rating)
    VALUES
        ('9a070000-0000-0000-0000-000000000007', 'VT-1', st_vt_ind, 'published',
         ST_SetSRID(ST_MakePoint(-0.3490, 5.7198), 4326), '11KV_110', 'GOOD')
    ON CONFLICT (id) DO NOTHING;

    -- MV conductors: the three 11 kV spans stitching source -> breaker ->
    -- fuse -> step-down.
    INSERT INTO gis.mv_conductor
        (id, name, subtype, status, geom, voltage_class, condition_rating, conductor_spec)
    VALUES
        ('9a070000-0000-0000-0000-000000000011', 'MV-1', st_mv_oh, 'published',
         ST_SetSRID(ST_GeomFromText('LINESTRING(-0.3500 5.7200, -0.3480 5.7200)'), 4326),
         '11KV', 'GOOD', 'ACSR_100'),
        ('9a070000-0000-0000-0000-000000000012', 'MV-2', st_mv_oh, 'published',
         ST_SetSRID(ST_GeomFromText('LINESTRING(-0.3480 5.7200, -0.3460 5.7200)'), 4326),
         '11KV', 'GOOD', 'ACSR_100'),
        ('9a070000-0000-0000-0000-000000000013', 'MV-3', st_mv_oh, 'published',
         ST_SetSRID(ST_GeomFromText('LINESTRING(-0.3460 5.7200, -0.3440 5.7200)'), 4326),
         '11KV', 'GOOD', 'ACSR_100')
    ON CONFLICT (id) DO NOTHING;

    -- LV-1: the 400 V run feeding the meter.
    INSERT INTO gis.lv_conductor
        (id, name, subtype, status, geom, phase, conductor_spec, lv_role)
    VALUES
        ('9a070000-0000-0000-0000-000000000021', 'LV-1', st_lv_oh, 'published',
         ST_SetSRID(ST_GeomFromText('LINESTRING(-0.3440 5.7200, -0.3440 5.7190)'), 4326),
         'L1L2L3N', 'WAVEFORM_185', 'main')
    ON CONFLICT (id) DO NOTHING;

    -- MTR-1 + SP-1: a single residential load closing out the feeder.
    INSERT INTO gis.meter
        (id, name, subtype, status, geom, meter_serial, account_ref, phase)
    VALUES
        ('9a070000-0000-0000-0000-000000000031', 'MTR-1', st_meter_wc, 'published',
         ST_SetSRID(ST_MakePoint(-0.3441, 5.7189), 4326), 'MTR-1-0001', 'ACC-1', 'L1L2L3N')
    ON CONFLICT (id) DO NOTHING;

    INSERT INTO gis.service_point
        (id, name, subtype, status, geom, demand_kw, power_factor)
    VALUES
        ('9a070000-0000-0000-0000-000000000041', 'SP-1', st_sp_res, 'published',
         ST_SetSRID(ST_MakePoint(-0.3442, 5.7189), 4326), 12, 0.95)
    ON CONFLICT (id) DO NOTHING;

    -- =====================================================================
    -- 8. Insert the runtime.feature_connectivity rows (the in-line graph).
    --    Edge endpoints (conductors) carry NULL terminal ids. CT-1/VT-1 are
    --    taps off MV-1 (T1 only). ON CONFLICT on the (from,to)+terminals key.
    -- =====================================================================

    -- 1. SUB-SOURCE.LV -> MV-1  (POWER_XFMR 11 kV winding out to the feeder)
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_pxf_lv_mv, fc_power_xfmr, '9a070000-0000-0000-0000-000000000001', t_pxf_lv,
         fc_mv_cond, '9a070000-0000-0000-0000-000000000011', NULL, 'seed:protection_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 2. MV-1 -> BRK-1.T1
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_mv_brk_t1, fc_mv_cond, '9a070000-0000-0000-0000-000000000011', NULL,
         fc_breaker, '9a070000-0000-0000-0000-000000000002', t_brk_t1, 'seed:protection_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 3. BRK-1.T2 -> MV-2
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_brk_t2_mv, fc_breaker, '9a070000-0000-0000-0000-000000000002', t_brk_t2,
         fc_mv_cond, '9a070000-0000-0000-0000-000000000012', NULL, 'seed:protection_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 4. MV-2 -> FUSE-1.T1
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_mv_fuse_t1, fc_mv_cond, '9a070000-0000-0000-0000-000000000012', NULL,
         fc_fuse, '9a070000-0000-0000-0000-000000000003', t_fuse_t1, 'seed:protection_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 5. FUSE-1.T2 -> MV-3
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_fuse_t2_mv, fc_fuse, '9a070000-0000-0000-0000-000000000003', t_fuse_t2,
         fc_mv_cond, '9a070000-0000-0000-0000-000000000013', NULL, 'seed:protection_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 6. MV-3 -> TX-1.T1
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_mv_dxf_t1, fc_mv_cond, '9a070000-0000-0000-0000-000000000013', NULL,
         fc_dist_xfmr, '9a070000-0000-0000-0000-000000000004', t_dxf_t1, 'seed:protection_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 7. TX-1.T2 -> LV-1
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_dxf_t2_lv, fc_dist_xfmr, '9a070000-0000-0000-0000-000000000004', t_dxf_t2,
         fc_lv_cond, '9a070000-0000-0000-0000-000000000021', NULL, 'seed:protection_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 8. LV-1 -> MTR-1.T1
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_lv_meter_t1, fc_lv_cond, '9a070000-0000-0000-0000-000000000021', NULL,
         fc_meter, '9a070000-0000-0000-0000-000000000031', t_meter_t1, 'seed:protection_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 9. MTR-1.T2 -> SP-1.T1
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_meter_t2_sp, fc_meter, '9a070000-0000-0000-0000-000000000031', t_meter_t2,
         fc_sp, '9a070000-0000-0000-0000-000000000041', t_sp_t1, 'seed:protection_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 10. MV-1 -> CT-1.T1  (protection CT tapped onto the breaker's line side)
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_mv_ct_t1, fc_mv_cond, '9a070000-0000-0000-0000-000000000011', NULL,
         fc_ct, '9a070000-0000-0000-0000-000000000006', t_ct_t1, 'seed:protection_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 11. MV-1 -> VT-1.T1  (line VT tapped onto the same span; T2 = ground dead-end)
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_mv_vt_t1, fc_mv_cond, '9a070000-0000-0000-0000-000000000011', NULL,
         fc_vt, '9a070000-0000-0000-0000-000000000007', t_vt_t1, 'seed:protection_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- =====================================================================
    -- 9. Insert the runtime.feature_relationships rows (the protection wiring
    --    the coordination engine reads). origin = RLY-1 in every case; the
    --    engine finds the relay for a breaker by matching kind='protection_trip'
    --    + destination=breaker and taking the origin as the relay. Conflict key
    --    = (relationship_class_id, origin_feature_id, destination_feature_id).
    -- =====================================================================

    -- RLY-1 -protection_trip-> BRK-1  (this edge is what makes BRK-1 gradeable)
    INSERT INTO runtime.feature_relationships
        (relationship_class_id, origin_feature_class_id, origin_feature_id,
         destination_feature_class_id, destination_feature_id, established_by)
    VALUES
        (rc_trip_brk, fc_relay, '9a070000-0000-0000-0000-000000000005',
         fc_breaker, '9a070000-0000-0000-0000-000000000002', 'seed:protection_network_example')
    ON CONFLICT (relationship_class_id, origin_feature_id, destination_feature_id) DO NOTHING;

    -- RLY-1 -protection_sensing-> CT-1  (modeling/annotation; engine doesn't read it)
    INSERT INTO runtime.feature_relationships
        (relationship_class_id, origin_feature_class_id, origin_feature_id,
         destination_feature_class_id, destination_feature_id, established_by)
    VALUES
        (rc_sense_ct, fc_relay, '9a070000-0000-0000-0000-000000000005',
         fc_ct, '9a070000-0000-0000-0000-000000000006', 'seed:protection_network_example')
    ON CONFLICT (relationship_class_id, origin_feature_id, destination_feature_id) DO NOTHING;

    -- RLY-1 -protection_sensing-> VT-1
    INSERT INTO runtime.feature_relationships
        (relationship_class_id, origin_feature_class_id, origin_feature_id,
         destination_feature_class_id, destination_feature_id, established_by)
    VALUES
        (rc_sense_vt, fc_relay, '9a070000-0000-0000-0000-000000000005',
         fc_vt, '9a070000-0000-0000-0000-000000000007', 'seed:protection_network_example')
    ON CONFLICT (relationship_class_id, origin_feature_id, destination_feature_id) DO NOTHING;

    RAISE NOTICE 'seed/protection_network_example.sql applied: 12 features + 11 connectivity rows + 3 protection relationships (idempotent). Run POST /analysis/coordination start=SUB-SOURCE fault=MV-3 nominal_voltage_v=11000.';
END $$;
