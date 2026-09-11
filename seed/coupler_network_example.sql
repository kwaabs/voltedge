-- seed/coupler_network_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives under
-- /seed on purpose, same reasoning as every other seed file.
--
-- A real-feature-instance seed that isolates ONE thing: the bus coupler (Phase
-- Bus-coupler). It builds the smallest topology that proves a coupler is a real,
-- switchable network node -- two busbars bridged by an MV_BREAKER whose
-- switch_function = BUS_COUPLER -- and NOTHING else, so a trace result is
-- unambiguous. It writes actual ROWS into the compiled gis.* tables plus the
-- runtime.feature_connectivity rows that wire them, using a distinct uuid prefix
-- (c0501e00-...) and map area (~-0.320, 5.720) so it coexists with
-- test_network_example (feed0000) and substation_network_example (5b51) on one DB.
--
-- ASCII TOPOLOGY (a two-bus tie -- BUS-B hangs off BUS-A ONLY through CPLR-1):
--
--   BUS-A  (MV_BUSBAR, 11 kV)         <-- start a downstream trace here
--     | T2
--    CPLR-1 (MV_BREAKER, switch_function=BUS_COUPLER, normal_state=NORMALLY_CLOSED,
--     |       device_state=CLOSED)    <-- the coupler / bus tie
--     | T2 -> BUS-B.T1
--   BUS-B  (MV_BUSBAR, 11 kV)
--     | T2
--    LOAD-FEED (MV_CONDUCTOR, 11 kV)  <-- reachable ONLY across the closed coupler
--
--   Wiring (busbar rules C + D are what make a coupler expressible with NO new
--   rule shape -- see seed/mv_busbar_example.sql):
--     BUS-A.T2 -> CPLR-1.T1   (rule C: MV_BUSBAR T2 -> {switch} T1)
--     CPLR-1.T2 -> BUS-B.T1   (rule D: {switch} T2 -> MV_BUSBAR T1)
--     BUS-B.T2 -> LOAD-FEED   (rule B: MV_BUSBAR T2 -> MV_CONDUCTOR)
--
-- HOW TO CONFIRM THE COUPLER WORKS (the whole point of this seed):
--   1. Run a DOWNSTREAM trace from BUS-A. With CPLR-1 CLOSED the result
--      includes CPLR-1, BUS-B and LOAD-FEED -- power crosses the tie.
--   2. Set CPLR-1.device_state = 'OPEN' (edit the feature, or
--      UPDATE gis.mv_breaker SET device_state='OPEN' WHERE name='CPLR-1';)
--      and re-run: the trace now HALTS at BUS-A -- BUS-B and LOAD-FEED drop out.
--   That flip proving a switchable barrier is the coupler behaving as an active
--   switch (not a passive joint), with zero engine code specific to couplers --
--   it is a plain MV_BREAKER, so trace/isolate/feeder all treat it as a switch.
--   The Slice 2 POST /couplers endpoint writes exactly these same two rule-C/D
--   connectivity rows; this seed is the hand-built target state it produces, so a
--   good trace here also validates what the endpoint commits.
--
-- HARD PREREQUISITES -- each metadata seed below must be applied AND its classes
-- COMPILED (task compile CODE=...) BEFORE this seed, because it writes into the
-- physical gis.<class> tables and reads their columns:
--
--   seed/connectivity_example.sql   -> MV_CONDUCTOR (+ VOLTAGE_CLASS incl. 11KV,
--                                       CONDITION_RATING)
--   seed/mv_switching_example.sql   -> MV_BREAKER (+ device_state/condition_rating,
--                                       vacuum_breaker subtype, T1/T2 terminals)
--   seed/mv_busbar_example.sql      -> MV_BUSBAR (+ rules B/C/D over MV_BREAKER)
--   seed/bus_coupler_fields.sql     -> MV_BREAKER.switch_function + normal_state
--                                       (Phase Bus-coupler Slice 1)  <-- under test
--
-- then compile each participating class:
--   task compile CODE=MV_CONDUCTOR
--   task compile CODE=MV_BREAKER      (AFTER bus_coupler_fields.sql, so the
--                                      switch_function/normal_state columns exist)
--   task compile CODE=MV_BUSBAR
--
-- The DO block fails LOUDLY (RAISE EXCEPTION, naming the missing prerequisite) if
-- any required class, subtype, terminal, the switch_function column, or a
-- network_rule is absent -- so a half-set-up DB aborts instead of writing a
-- partial network. Fully idempotent: feature rows use fixed uuids with ON
-- CONFLICT (id) DO NOTHING; connectivity rows ON CONFLICT on the
-- (from,to)+terminals unique key.
--
-- Apply with psql (NOT the migration runner), after the prerequisites above:
--   task seed-coupler-network   (or the psql -f invocation directly)
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL has
-- been reviewed statically -- a live psql apply against a fully-compiled DB + the
-- CLOSED/OPEN trace check above are still owed to call it verified.

DO $$
DECLARE
    -- feature_class ids
    fc_mv_busbar uuid;
    fc_mv_breaker uuid;
    fc_mv_cond   uuid;

    -- subtype INTEGER codes (the value stored in gis.<class>.subtype).
    -- MV_BUSBAR is subtype-less (the compiler omits the column), so it has none.
    st_brk_vac integer;  -- MV_BREAKER 'vacuum_breaker'
    st_mv_oh   integer;  -- MV_CONDUCTOR 'overhead_mv_conductor'

    -- terminal ids (all subtype-agnostic here -> subtype_id NULLS LAST)
    t_bus_t1 uuid;
    t_bus_t2 uuid;
    t_brk_t1 uuid;
    t_brk_t2 uuid;

    -- network_rule ids
    r_bus_t2_brk_t1 uuid;  -- rule C: MV_BUSBAR T2 -> MV_BREAKER T1
    r_brk_t2_bus_t1 uuid;  -- rule D: MV_BREAKER T2 -> MV_BUSBAR T1
    r_bus_t2_mv     uuid;  -- rule B: MV_BUSBAR T2 -> MV_CONDUCTOR
BEGIN
    -- =====================================================================
    -- 1. Resolve feature_class ids (fail if a class was never seeded).
    -- =====================================================================
    SELECT id INTO fc_mv_busbar FROM metadata.feature_classes WHERE code = 'MV_BUSBAR';
    IF fc_mv_busbar IS NULL THEN RAISE EXCEPTION 'missing feature_class MV_BUSBAR -- apply seed/mv_busbar_example.sql then: task compile CODE=MV_BUSBAR'; END IF;

    SELECT id INTO fc_mv_breaker FROM metadata.feature_classes WHERE code = 'MV_BREAKER';
    IF fc_mv_breaker IS NULL THEN RAISE EXCEPTION 'missing feature_class MV_BREAKER -- apply seed/mv_switching_example.sql then: task compile CODE=MV_BREAKER'; END IF;

    SELECT id INTO fc_mv_cond FROM metadata.feature_classes WHERE code = 'MV_CONDUCTOR';
    IF fc_mv_cond IS NULL THEN RAISE EXCEPTION 'missing feature_class MV_CONDUCTOR -- apply seed/connectivity_example.sql then: task compile CODE=MV_CONDUCTOR'; END IF;

    -- =====================================================================
    -- 2. Resolve subtype INTEGER codes.
    -- =====================================================================
    SELECT code INTO st_brk_vac FROM metadata.subtypes WHERE feature_class_id = fc_mv_breaker AND name = 'vacuum_breaker';
    IF st_brk_vac IS NULL THEN RAISE EXCEPTION 'MV_BREAKER subtype ''vacuum_breaker'' not found -- re-apply seed/mv_switching_example.sql'; END IF;

    SELECT code INTO st_mv_oh FROM metadata.subtypes WHERE feature_class_id = fc_mv_cond AND name = 'overhead_mv_conductor';
    IF st_mv_oh IS NULL THEN RAISE EXCEPTION 'MV_CONDUCTOR subtype ''overhead_mv_conductor'' not found -- re-apply seed/connectivity_example.sql'; END IF;

    -- =====================================================================
    -- 3. Resolve terminal ids (subtype-agnostic -> subtype_id NULLS LAST).
    -- =====================================================================
    SELECT id INTO t_bus_t1 FROM metadata.terminals WHERE feature_class_id = fc_mv_busbar AND code = 'T1' ORDER BY subtype_id NULLS LAST LIMIT 1;
    SELECT id INTO t_bus_t2 FROM metadata.terminals WHERE feature_class_id = fc_mv_busbar AND code = 'T2' ORDER BY subtype_id NULLS LAST LIMIT 1;
    IF t_bus_t1 IS NULL OR t_bus_t2 IS NULL THEN RAISE EXCEPTION 'MV_BUSBAR terminals T1/T2 not found -- re-apply seed/mv_busbar_example.sql'; END IF;

    SELECT id INTO t_brk_t1 FROM metadata.terminals WHERE feature_class_id = fc_mv_breaker AND code = 'T1' ORDER BY subtype_id NULLS LAST LIMIT 1;
    SELECT id INTO t_brk_t2 FROM metadata.terminals WHERE feature_class_id = fc_mv_breaker AND code = 'T2' ORDER BY subtype_id NULLS LAST LIMIT 1;
    IF t_brk_t1 IS NULL OR t_brk_t2 IS NULL THEN RAISE EXCEPTION 'MV_BREAKER terminals T1/T2 not found -- re-apply seed/mv_switching_example.sql'; END IF;

    -- =====================================================================
    -- 4. Guard the compiled tables + the Slice 1 coupler COLUMN. The whole
    --    point of this seed is switch_function; if that column is missing the
    --    field-pack was never applied or MV_BREAKER never recompiled after it.
    -- =====================================================================
    IF to_regclass('gis.mv_busbar') IS NULL THEN RAISE EXCEPTION 'table gis.mv_busbar does not exist -- run: task compile CODE=MV_BUSBAR'; END IF;
    IF to_regclass('gis.mv_breaker') IS NULL THEN RAISE EXCEPTION 'table gis.mv_breaker does not exist -- run: task compile CODE=MV_BREAKER'; END IF;
    IF to_regclass('gis.mv_conductor') IS NULL THEN RAISE EXCEPTION 'table gis.mv_conductor does not exist -- run: task compile CODE=MV_CONDUCTOR'; END IF;

    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'mv_breaker' AND column_name = 'switch_function') THEN
        RAISE EXCEPTION 'gis.mv_breaker is missing column switch_function -- apply seed/bus_coupler_fields.sql then: task compile CODE=MV_BREAKER'; END IF;
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'mv_breaker' AND column_name = 'normal_state') THEN
        RAISE EXCEPTION 'gis.mv_breaker is missing column normal_state -- apply seed/bus_coupler_fields.sql then: task compile CODE=MV_BREAKER'; END IF;

    -- =====================================================================
    -- 5. Resolve the network_rule ids that authorize each connection.
    --    Rules C/D are set-generating over MV_BREAKER in mv_busbar_example.sql,
    --    so they exist for MV_BREAKER specifically.
    -- =====================================================================
    SELECT id INTO r_bus_t2_brk_t1 FROM metadata.network_rules
     WHERE from_feature_class_id = fc_mv_busbar AND to_feature_class_id = fc_mv_breaker
       AND from_terminal_id IS NOT DISTINCT FROM t_bus_t2 AND to_terminal_id IS NOT DISTINCT FROM t_brk_t1
       AND is_valid = true LIMIT 1;
    IF r_bus_t2_brk_t1 IS NULL THEN RAISE EXCEPTION 'network_rule "MV_BUSBAR T2 -> MV_BREAKER T1" (rule C) not found/valid -- apply seed/mv_busbar_example.sql (after seed/mv_switching_example.sql)'; END IF;

    SELECT id INTO r_brk_t2_bus_t1 FROM metadata.network_rules
     WHERE from_feature_class_id = fc_mv_breaker AND to_feature_class_id = fc_mv_busbar
       AND from_terminal_id IS NOT DISTINCT FROM t_brk_t2 AND to_terminal_id IS NOT DISTINCT FROM t_bus_t1
       AND is_valid = true LIMIT 1;
    IF r_brk_t2_bus_t1 IS NULL THEN RAISE EXCEPTION 'network_rule "MV_BREAKER T2 -> MV_BUSBAR T1" (rule D) not found/valid -- apply seed/mv_busbar_example.sql (after seed/mv_switching_example.sql)'; END IF;

    SELECT id INTO r_bus_t2_mv FROM metadata.network_rules
     WHERE from_feature_class_id = fc_mv_busbar AND to_feature_class_id = fc_mv_cond
       AND from_terminal_id IS NOT DISTINCT FROM t_bus_t2 AND to_terminal_id IS NULL
       AND is_valid = true LIMIT 1;
    IF r_bus_t2_mv IS NULL THEN RAISE EXCEPTION 'network_rule "MV_BUSBAR T2 -> MV_CONDUCTOR" (rule B) not found/valid -- apply seed/mv_busbar_example.sql'; END IF;

    -- =====================================================================
    -- 6. Insert the feature rows (status 'published' so they are live/traced).
    --    Fixed uuids + ON CONFLICT (id) DO NOTHING => idempotent.
    -- =====================================================================

    -- BUS-A: the energized 11 kV busbar (trace start). No subtype column.
    INSERT INTO gis.mv_busbar
        (id, name, status, geom, voltage_class, condition_rating)
    VALUES
        ('c0501e00-0000-0000-0000-000000000001', 'BUS-A', 'published',
         ST_SetSRID(ST_MakePoint(-0.3200, 5.7200), 4326), '11KV', 'GOOD')
    ON CONFLICT (id) DO NOTHING;

    -- CPLR-1: THE bus coupler -- a plain MV_BREAKER labelled switch_function=
    -- BUS_COUPLER, designed NORMALLY_CLOSED, currently CLOSED. Flip device_state
    -- to OPEN to prove the tie is a switchable barrier (see header step 2).
    INSERT INTO gis.mv_breaker
        (id, name, subtype, status, geom, device_state, condition_rating,
         switch_function, normal_state)
    VALUES
        ('c0501e00-0000-0000-0000-000000000002', 'CPLR-1', st_brk_vac, 'published',
         ST_SetSRID(ST_MakePoint(-0.3190, 5.7200), 4326), 'CLOSED', 'GOOD',
         'BUS_COUPLER', 'NORMALLY_CLOSED')
    ON CONFLICT (id) DO NOTHING;

    -- BUS-B: the second 11 kV busbar, fed ONLY across the coupler.
    INSERT INTO gis.mv_busbar
        (id, name, status, geom, voltage_class, condition_rating)
    VALUES
        ('c0501e00-0000-0000-0000-000000000003', 'BUS-B', 'published',
         ST_SetSRID(ST_MakePoint(-0.3180, 5.7200), 4326), '11KV', 'GOOD')
    ON CONFLICT (id) DO NOTHING;

    -- LOAD-FEED: an 11 kV span off BUS-B, the far-side load reachable only when
    -- the coupler is closed. conductor_spec left NULL (optional; per-km impedance
    -- is irrelevant to a topology trace).
    INSERT INTO gis.mv_conductor
        (id, name, subtype, status, geom, voltage_class, condition_rating)
    VALUES
        ('c0501e00-0000-0000-0000-000000000004', 'LOAD-FEED', st_mv_oh, 'published',
         ST_SetSRID(ST_GeomFromText('LINESTRING(-0.3180 5.7200, -0.3160 5.7200)'), 4326),
         '11KV', 'GOOD')
    ON CONFLICT (id) DO NOTHING;

    -- =====================================================================
    -- 7. Insert the runtime.feature_connectivity rows (the actual graph).
    --    Conductor endpoints carry NULL terminal ids. ON CONFLICT on the
    --    (from,to)+terminals unique key => idempotent.
    -- =====================================================================

    -- 1. BUS-A.T2 -> CPLR-1.T1   (rule C)
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_bus_t2_brk_t1, fc_mv_busbar, 'c0501e00-0000-0000-0000-000000000001', t_bus_t2,
         fc_mv_breaker, 'c0501e00-0000-0000-0000-000000000002', t_brk_t1, 'seed:coupler_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 2. CPLR-1.T2 -> BUS-B.T1   (rule D)
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_brk_t2_bus_t1, fc_mv_breaker, 'c0501e00-0000-0000-0000-000000000002', t_brk_t2,
         fc_mv_busbar, 'c0501e00-0000-0000-0000-000000000003', t_bus_t1, 'seed:coupler_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    -- 3. BUS-B.T2 -> LOAD-FEED   (rule B)
    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        (r_bus_t2_mv, fc_mv_busbar, 'c0501e00-0000-0000-0000-000000000003', t_bus_t2,
         fc_mv_cond, 'c0501e00-0000-0000-0000-000000000004', NULL, 'seed:coupler_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    RAISE NOTICE 'coupler_network_example: BUS-A =(CPLR-1 coupler)= BUS-B -> LOAD-FEED seeded. Trace downstream from BUS-A; then set CPLR-1 device_state=OPEN and re-trace to see BUS-B/LOAD-FEED drop out.';
END $$;
