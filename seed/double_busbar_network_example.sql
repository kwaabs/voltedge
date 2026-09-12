-- seed/double_busbar_network_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives under
-- /seed on purpose, same reasoning as every other seed file.
--
-- A real-feature-instance seed that builds ONE thing end to end: a working
-- DOUBLE-BUSBAR substation, as actual gis.* rows + runtime.feature_connectivity,
-- so trace/isolate/feeder see it as a real switchable network -- not a drawing.
-- It is the electrical MODEL half of "substation construction"; the DRAW half is
-- the existing "Double busbar" diagram template (seed/double_busbar_template.sql,
-- task seed-double-busbar), whose three bay slots bind onto FEEDER-1 / FEEDER-2
-- seeded here. Distinct uuid prefix (dbb50000-...) and map area (~-0.360, 5.760)
-- so it coexists with test (feed0000), substation (5b51) and coupler (c0501e00).
--
-- WHY IT NEEDS NO NEW CLASS OR RULE. A double busbar is fully expressible with
-- the existing primitives (see the double-busbar-expressible note): two main
-- MV_BUSBARs bridged by a coupler MV_BREAKER, and per feeder a pair of bus
-- SELECTOR isolators (MV_ISOLATOR) -- one onto each main bus -- landing on a
-- small per-bay bus the feeder taps off. The coupler and every selector wire
-- through busbar rules C (`MV_BUSBAR T2 -> {switch} T1`) and D (`{switch} T2 ->
-- MV_BUSBAR T1`), both set-generating over MV_BREAKER / MV_ISOLATOR /
-- MV_SECTIONALIZER in seed/mv_busbar_example.sql. No switch-to-switch rule
-- exists (only bus<->device and device<->conductor), which is EXACTLY why each
-- bay's two selectors join at a per-bay MV_BUSBAR (BAYBUS-n) rather than
-- directly: the bay bus is the electrical junction the rules already authorize.
--
-- ASCII TOPOLOGY (a 2-bay double busbar; FEEDER-1 selected onto Bus A, FEEDER-2
-- onto Bus B; the two mains tied by a normally-closed coupler):
--
--   BUS-A  (MV_BUSBAR, 11 kV) ---------------------------.   <-- trace start
--     |T2            |T2                 |T2             |T2
--   SEL-1A         SEL-2A              CPLR-1 (MV_BREAKER, BUS_COUPLER,
--   (MV_ISOLATOR,  (MV_ISOLATOR,        |   normal_state=NORMALLY_CLOSED,
--    N/C, CLOSED)   N/O, OPEN)          |   device_state=CLOSED)
--     |T2            |T2                |T2 -> BUS-B.T1
--   BAYBUS-1       BAYBUS-2           BUS-B (MV_BUSBAR, 11 kV)
--     |              |                  |T2            |T2
--     |              |                SEL-1B         SEL-2B
--     |              |                (N/O, OPEN)    (N/C, CLOSED)
--     |              |                  |T2 -> BAYBUS-1  |T2 -> BAYBUS-2
--   FEEDER-1       FEEDER-2
--   (MV_CONDUCTOR) (MV_CONDUCTOR)
--
--   Wiring (13 connectivity rows; every one authorized by an existing rule):
--     coupler:  BUS-A.T2  -> CPLR-1.T1   (rule C)   CPLR-1.T2 -> BUS-B.T1  (rule D)
--     bay 1:    BUS-A.T2  -> SEL-1A.T1   (rule C)   SEL-1A.T2 -> BAYBUS-1.T1 (rule D)
--               BUS-B.T2  -> SEL-1B.T1   (rule C)   SEL-1B.T2 -> BAYBUS-1.T1 (rule D)
--               BAYBUS-1.T2 -> FEEDER-1  (rule B)
--     bay 2:    BUS-A.T2  -> SEL-2A.T1   (rule C)   SEL-2A.T2 -> BAYBUS-2.T1 (rule D)
--               BUS-B.T2  -> SEL-2B.T1   (rule C)   SEL-2B.T2 -> BAYBUS-2.T1 (rule D)
--               BAYBUS-2.T2 -> FEEDER-2  (rule B)
--
-- HOW TO CONFIRM IT WORKS (the whole point -- three checks, all trace-only):
--   1. BUS SELECTION. Run a DOWNSTREAM trace from BUS-A. With the coupler CLOSED
--      and each feeder's one closed selector, the result reaches BOTH feeders:
--      FEEDER-1 via its own Bus-A selector (SEL-1A), FEEDER-2 across the coupler
--      to BUS-B then its Bus-B selector (SEL-2B). SEL-1B and SEL-2A are OPEN, so
--      neither feeder is reachable through its OTHER bus -- the selection is real.
--   2. THE COUPLER TIE. Set CPLR-1.device_state='OPEN' (edit the feature, or
--      UPDATE gis.mv_breaker SET device_state='OPEN' WHERE name='CPLR-1';) and
--      re-trace from BUS-A: BUS-B drops out, and with it FEEDER-2 -- whose only
--      closed selector is on the now-unreachable Bus B. FEEDER-1 stays (it is on
--      Bus A). That is the coupler behaving as a switchable barrier.
--   3. SELECTION TRANSFER (the defining double-busbar move). With the coupler
--      still OPEN, transfer FEEDER-2 onto Bus A: close its Bus-A selector
--      (UPDATE gis.mv_isolator SET device_state='CLOSED' WHERE name='SEL-2A';).
--      Re-trace from BUS-A: FEEDER-2 is reachable again -- now directly off Bus A,
--      no coupler. Flipping which selector is closed moves the feeder between
--      buses. (Do it live-safe in reverse -- close the incoming before opening the
--      outgoing -- but topologically either order proves the transfer.)
--   None of this needs engine code specific to double busbars: buses, coupler and
--   selectors are all plain MV_BUSBAR / MV_BREAKER / MV_ISOLATOR, so trace /
--   isolate / feeder treat every selector as an ordinary switch barrier.
--
-- IT ALSO EXERCISES the Phase 48 diagram NOP marker: SEL-1B and SEL-2A carry
-- normal_state=NORMALLY_OPEN, so once seed/switch_normal_state_fields.sql (Slice
-- 1b) is applied and MV_ISOLATOR recompiled, those two selectors render the amber
-- "normally open" ring on the diagram while the closed selectors stay unmarked --
-- the one-line reads at a glance for where the open points are.
--
-- HARD PREREQUISITES -- each metadata seed below must be applied AND its classes
-- COMPILED (task compile CODE=...) BEFORE this seed, because it writes into the
-- physical gis.<class> tables and reads their columns:
--
--   seed/connectivity_example.sql       -> MV_CONDUCTOR (+ VOLTAGE_CLASS incl.
--                                          11KV, CONDITION_RATING)
--   seed/mv_switching_example.sql       -> MV_BREAKER + MV_ISOLATOR (device_state,
--                                          condition_rating, subtypes, T1/T2)
--   seed/mv_busbar_example.sql          -> MV_BUSBAR (+ rules B/C/D over the
--                                          switch family incl. MV_ISOLATOR)
--   seed/bus_coupler_fields.sql         -> MV_BREAKER.switch_function + normal_state
--   seed/switch_normal_state_fields.sql -> MV_ISOLATOR.normal_state (Phase 48
--                                          Slice 1b)  <-- selectors need this column
--
-- then compile each participating class (order matters -- the field packs first):
--   task compile CODE=MV_CONDUCTOR
--   task compile CODE=MV_BREAKER      (AFTER bus_coupler_fields.sql)
--   task compile CODE=MV_ISOLATOR     (AFTER switch_normal_state_fields.sql)
--   task compile CODE=MV_BUSBAR
--
-- The DO block fails LOUDLY (RAISE EXCEPTION, naming the missing prerequisite) if
-- any required class, subtype, terminal, the normal_state columns, or a
-- network_rule is absent -- so a half-set-up DB aborts instead of writing a
-- partial network. Fully idempotent: feature rows use fixed uuids with ON
-- CONFLICT (id) DO NOTHING; connectivity rows ON CONFLICT on the
-- (from,to)+terminals unique key.
--
-- Apply with psql (NOT the migration runner), after the prerequisites above:
--   task seed-double-busbar-network   (or the psql -f invocation directly)
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL has
-- been reviewed statically -- a live psql apply against a fully-compiled DB + the
-- three trace checks above are still owed to call it verified.

DO $$
DECLARE
    -- feature_class ids
    fc_mv_busbar  uuid;
    fc_mv_breaker uuid;
    fc_mv_iso     uuid;
    fc_mv_cond    uuid;

    -- subtype INTEGER codes (the value stored in gis.<class>.subtype).
    -- MV_BUSBAR is subtype-less (the compiler omits the column), so it has none.
    st_brk_vac  integer;  -- MV_BREAKER 'vacuum_breaker'
    st_iso_sb   integer;  -- MV_ISOLATOR 'single_break'
    st_mv_oh    integer;  -- MV_CONDUCTOR 'overhead_mv_conductor'

    -- terminal ids (all subtype-agnostic here -> subtype_id NULLS LAST)
    t_bus_t1 uuid;
    t_bus_t2 uuid;
    t_brk_t1 uuid;
    t_brk_t2 uuid;
    t_iso_t1 uuid;
    t_iso_t2 uuid;

    -- network_rule ids
    r_bus_t2_brk_t1 uuid;  -- rule C: MV_BUSBAR T2 -> MV_BREAKER  T1
    r_brk_t2_bus_t1 uuid;  -- rule D: MV_BREAKER T2 -> MV_BUSBAR  T1
    r_bus_t2_iso_t1 uuid;  -- rule C: MV_BUSBAR T2 -> MV_ISOLATOR T1
    r_iso_t2_bus_t1 uuid;  -- rule D: MV_ISOLATOR T2 -> MV_BUSBAR T1
    r_bus_t2_mv     uuid;  -- rule B: MV_BUSBAR T2 -> MV_CONDUCTOR
BEGIN
    -- =====================================================================
    -- 1. Resolve feature_class ids (fail if a class was never seeded).
    -- =====================================================================
    SELECT id INTO fc_mv_busbar FROM metadata.feature_classes WHERE code = 'MV_BUSBAR';
    IF fc_mv_busbar IS NULL THEN RAISE EXCEPTION 'missing feature_class MV_BUSBAR -- apply seed/mv_busbar_example.sql then: task compile CODE=MV_BUSBAR'; END IF;

    SELECT id INTO fc_mv_breaker FROM metadata.feature_classes WHERE code = 'MV_BREAKER';
    IF fc_mv_breaker IS NULL THEN RAISE EXCEPTION 'missing feature_class MV_BREAKER -- apply seed/mv_switching_example.sql then: task compile CODE=MV_BREAKER'; END IF;

    SELECT id INTO fc_mv_iso FROM metadata.feature_classes WHERE code = 'MV_ISOLATOR';
    IF fc_mv_iso IS NULL THEN RAISE EXCEPTION 'missing feature_class MV_ISOLATOR -- apply seed/mv_switching_example.sql then: task compile CODE=MV_ISOLATOR'; END IF;

    SELECT id INTO fc_mv_cond FROM metadata.feature_classes WHERE code = 'MV_CONDUCTOR';
    IF fc_mv_cond IS NULL THEN RAISE EXCEPTION 'missing feature_class MV_CONDUCTOR -- apply seed/connectivity_example.sql then: task compile CODE=MV_CONDUCTOR'; END IF;

    -- =====================================================================
    -- 2. Resolve subtype INTEGER codes.
    -- =====================================================================
    SELECT code INTO st_brk_vac FROM metadata.subtypes WHERE feature_class_id = fc_mv_breaker AND name = 'vacuum_breaker';
    IF st_brk_vac IS NULL THEN RAISE EXCEPTION 'MV_BREAKER subtype ''vacuum_breaker'' not found -- re-apply seed/mv_switching_example.sql'; END IF;

    SELECT code INTO st_iso_sb FROM metadata.subtypes WHERE feature_class_id = fc_mv_iso AND name = 'single_break';
    IF st_iso_sb IS NULL THEN RAISE EXCEPTION 'MV_ISOLATOR subtype ''single_break'' not found -- re-apply seed/mv_switching_example.sql'; END IF;

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

    SELECT id INTO t_iso_t1 FROM metadata.terminals WHERE feature_class_id = fc_mv_iso AND code = 'T1' ORDER BY subtype_id NULLS LAST LIMIT 1;
    SELECT id INTO t_iso_t2 FROM metadata.terminals WHERE feature_class_id = fc_mv_iso AND code = 'T2' ORDER BY subtype_id NULLS LAST LIMIT 1;
    IF t_iso_t1 IS NULL OR t_iso_t2 IS NULL THEN RAISE EXCEPTION 'MV_ISOLATOR terminals T1/T2 not found -- re-apply seed/mv_switching_example.sql'; END IF;

    -- =====================================================================
    -- 4. Guard the compiled tables + the coupler/selector state COLUMNS.
    --    The selectors carry normal_state, added to MV_ISOLATOR by the Phase
    --    48 Slice 1b field pack; if that column is missing the pack was never
    --    applied or MV_ISOLATOR never recompiled after it.
    -- =====================================================================
    IF to_regclass('gis.mv_busbar')   IS NULL THEN RAISE EXCEPTION 'table gis.mv_busbar does not exist -- run: task compile CODE=MV_BUSBAR'; END IF;
    IF to_regclass('gis.mv_breaker')  IS NULL THEN RAISE EXCEPTION 'table gis.mv_breaker does not exist -- run: task compile CODE=MV_BREAKER'; END IF;
    IF to_regclass('gis.mv_isolator') IS NULL THEN RAISE EXCEPTION 'table gis.mv_isolator does not exist -- run: task compile CODE=MV_ISOLATOR'; END IF;
    IF to_regclass('gis.mv_conductor') IS NULL THEN RAISE EXCEPTION 'table gis.mv_conductor does not exist -- run: task compile CODE=MV_CONDUCTOR'; END IF;

    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'mv_breaker' AND column_name = 'switch_function') THEN
        RAISE EXCEPTION 'gis.mv_breaker is missing column switch_function -- apply seed/bus_coupler_fields.sql then: task compile CODE=MV_BREAKER'; END IF;
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'mv_breaker' AND column_name = 'normal_state') THEN
        RAISE EXCEPTION 'gis.mv_breaker is missing column normal_state -- apply seed/bus_coupler_fields.sql then: task compile CODE=MV_BREAKER'; END IF;
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'mv_isolator' AND column_name = 'normal_state') THEN
        RAISE EXCEPTION 'gis.mv_isolator is missing column normal_state -- apply seed/switch_normal_state_fields.sql (Phase 48 Slice 1b) then: task compile CODE=MV_ISOLATOR'; END IF;

    -- =====================================================================
    -- 5. Resolve the network_rule ids that authorize each connection.
    --    Rules B/C/D are set-generating over the switch family in
    --    mv_busbar_example.sql, so C/D exist for MV_ISOLATOR as well as
    --    MV_BREAKER, and B for MV_CONDUCTOR.
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

    SELECT id INTO r_bus_t2_iso_t1 FROM metadata.network_rules
     WHERE from_feature_class_id = fc_mv_busbar AND to_feature_class_id = fc_mv_iso
       AND from_terminal_id IS NOT DISTINCT FROM t_bus_t2 AND to_terminal_id IS NOT DISTINCT FROM t_iso_t1
       AND is_valid = true LIMIT 1;
    IF r_bus_t2_iso_t1 IS NULL THEN RAISE EXCEPTION 'network_rule "MV_BUSBAR T2 -> MV_ISOLATOR T1" (rule C) not found/valid -- apply seed/mv_busbar_example.sql (after seed/mv_switching_example.sql)'; END IF;

    SELECT id INTO r_iso_t2_bus_t1 FROM metadata.network_rules
     WHERE from_feature_class_id = fc_mv_iso AND to_feature_class_id = fc_mv_busbar
       AND from_terminal_id IS NOT DISTINCT FROM t_iso_t2 AND to_terminal_id IS NOT DISTINCT FROM t_bus_t1
       AND is_valid = true LIMIT 1;
    IF r_iso_t2_bus_t1 IS NULL THEN RAISE EXCEPTION 'network_rule "MV_ISOLATOR T2 -> MV_BUSBAR T1" (rule D) not found/valid -- apply seed/mv_busbar_example.sql (after seed/mv_switching_example.sql)'; END IF;

    SELECT id INTO r_bus_t2_mv FROM metadata.network_rules
     WHERE from_feature_class_id = fc_mv_busbar AND to_feature_class_id = fc_mv_cond
       AND from_terminal_id IS NOT DISTINCT FROM t_bus_t2 AND to_terminal_id IS NULL
       AND is_valid = true LIMIT 1;
    IF r_bus_t2_mv IS NULL THEN RAISE EXCEPTION 'network_rule "MV_BUSBAR T2 -> MV_CONDUCTOR" (rule B) not found/valid -- apply seed/mv_busbar_example.sql'; END IF;

    -- =====================================================================
    -- 6. Insert the feature rows (status 'published' so they are live/traced).
    --    Fixed uuids + ON CONFLICT (id) DO NOTHING => idempotent.
    -- =====================================================================

    -- The two main busbars (Bus A / Bus B), 11 kV. No subtype column.
    INSERT INTO gis.mv_busbar (id, name, status, geom, voltage_class, condition_rating) VALUES
        ('dbb50000-0000-0000-0000-000000000001', 'BUS-A', 'published', ST_SetSRID(ST_MakePoint(-0.3600, 5.7600), 4326), '11KV', 'GOOD'),
        ('dbb50000-0000-0000-0000-000000000002', 'BUS-B', 'published', ST_SetSRID(ST_MakePoint(-0.3600, 5.7580), 4326), '11KV', 'GOOD')
    ON CONFLICT (id) DO NOTHING;

    -- CPLR-1: the bus coupler -- a plain MV_BREAKER labelled BUS_COUPLER,
    -- designed NORMALLY_CLOSED, currently CLOSED (ties Bus A to Bus B).
    INSERT INTO gis.mv_breaker
        (id, name, subtype, status, geom, device_state, condition_rating, switch_function, normal_state)
    VALUES
        ('dbb50000-0000-0000-0000-000000000003', 'CPLR-1', st_brk_vac, 'published',
         ST_SetSRID(ST_MakePoint(-0.3600, 5.7590), 4326), 'CLOSED', 'GOOD', 'BUS_COUPLER', 'NORMALLY_CLOSED')
    ON CONFLICT (id) DO NOTHING;

    -- The four bus-selector isolators. Per bay one selector is CLOSED (the
    -- selected bus) and one OPEN; normal_state records the design posture, which
    -- is what the diagram NOP marker reads. Bay 1 is on Bus A, Bay 2 on Bus B.
    INSERT INTO gis.mv_isolator
        (id, name, subtype, status, geom, device_state, condition_rating, normal_state)
    VALUES
        -- Bay 1 selectors: onto Bus A (closed), onto Bus B (open)
        ('dbb50000-0000-0000-0000-000000000011', 'SEL-1A', st_iso_sb, 'published', ST_SetSRID(ST_MakePoint(-0.3580, 5.7600), 4326), 'CLOSED', 'GOOD', 'NORMALLY_CLOSED'),
        ('dbb50000-0000-0000-0000-000000000012', 'SEL-1B', st_iso_sb, 'published', ST_SetSRID(ST_MakePoint(-0.3580, 5.7580), 4326), 'OPEN',   'GOOD', 'NORMALLY_OPEN'),
        -- Bay 2 selectors: onto Bus A (open), onto Bus B (closed)
        ('dbb50000-0000-0000-0000-000000000021', 'SEL-2A', st_iso_sb, 'published', ST_SetSRID(ST_MakePoint(-0.3560, 5.7600), 4326), 'OPEN',   'GOOD', 'NORMALLY_OPEN'),
        ('dbb50000-0000-0000-0000-000000000022', 'SEL-2B', st_iso_sb, 'published', ST_SetSRID(ST_MakePoint(-0.3560, 5.7580), 4326), 'CLOSED', 'GOOD', 'NORMALLY_CLOSED')
    ON CONFLICT (id) DO NOTHING;

    -- The per-bay buses (BAYBUS-n): the junction each bay's two selectors land
    -- on and the feeder taps off. Plain 11 kV MV_BUSBARs, no subtype.
    INSERT INTO gis.mv_busbar (id, name, status, geom, voltage_class, condition_rating) VALUES
        ('dbb50000-0000-0000-0000-000000000013', 'BAYBUS-1', 'published', ST_SetSRID(ST_MakePoint(-0.3580, 5.7570), 4326), '11KV', 'GOOD'),
        ('dbb50000-0000-0000-0000-000000000023', 'BAYBUS-2', 'published', ST_SetSRID(ST_MakePoint(-0.3560, 5.7565), 4326), '11KV', 'GOOD')
    ON CONFLICT (id) DO NOTHING;

    -- The two outgoing feeders (11 kV spans off each bay bus). conductor_spec
    -- left NULL (optional; per-km impedance is irrelevant to a topology trace).
    INSERT INTO gis.mv_conductor (id, name, subtype, status, geom, voltage_class, condition_rating) VALUES
        ('dbb50000-0000-0000-0000-000000000014', 'FEEDER-1', st_mv_oh, 'published',
         ST_SetSRID(ST_GeomFromText('LINESTRING(-0.3580 5.7570, -0.3560 5.7570)'), 4326), '11KV', 'GOOD'),
        ('dbb50000-0000-0000-0000-000000000024', 'FEEDER-2', st_mv_oh, 'published',
         ST_SetSRID(ST_GeomFromText('LINESTRING(-0.3560 5.7565, -0.3540 5.7565)'), 4326), '11KV', 'GOOD')
    ON CONFLICT (id) DO NOTHING;

    -- =====================================================================
    -- 7. Insert the runtime.feature_connectivity rows (the actual graph).
    --    Conductor endpoints carry NULL terminal ids. ON CONFLICT on the
    --    (from,to)+terminals unique key => idempotent.
    -- =====================================================================

    INSERT INTO runtime.feature_connectivity
        (network_rule_id, from_feature_class_id, from_feature_id, from_terminal_id,
         to_feature_class_id, to_feature_id, to_terminal_id, established_by)
    VALUES
        -- coupler: BUS-A.T2 -> CPLR-1.T1 (rule C), CPLR-1.T2 -> BUS-B.T1 (rule D)
        (r_bus_t2_brk_t1, fc_mv_busbar, 'dbb50000-0000-0000-0000-000000000001', t_bus_t2,
         fc_mv_breaker, 'dbb50000-0000-0000-0000-000000000003', t_brk_t1, 'seed:double_busbar_network_example'),
        (r_brk_t2_bus_t1, fc_mv_breaker, 'dbb50000-0000-0000-0000-000000000003', t_brk_t2,
         fc_mv_busbar, 'dbb50000-0000-0000-0000-000000000002', t_bus_t1, 'seed:double_busbar_network_example'),

        -- bay 1: Bus A -> SEL-1A -> BAYBUS-1, Bus B -> SEL-1B -> BAYBUS-1, BAYBUS-1 -> FEEDER-1
        (r_bus_t2_iso_t1, fc_mv_busbar, 'dbb50000-0000-0000-0000-000000000001', t_bus_t2,
         fc_mv_iso, 'dbb50000-0000-0000-0000-000000000011', t_iso_t1, 'seed:double_busbar_network_example'),
        (r_iso_t2_bus_t1, fc_mv_iso, 'dbb50000-0000-0000-0000-000000000011', t_iso_t2,
         fc_mv_busbar, 'dbb50000-0000-0000-0000-000000000013', t_bus_t1, 'seed:double_busbar_network_example'),
        (r_bus_t2_iso_t1, fc_mv_busbar, 'dbb50000-0000-0000-0000-000000000002', t_bus_t2,
         fc_mv_iso, 'dbb50000-0000-0000-0000-000000000012', t_iso_t1, 'seed:double_busbar_network_example'),
        (r_iso_t2_bus_t1, fc_mv_iso, 'dbb50000-0000-0000-0000-000000000012', t_iso_t2,
         fc_mv_busbar, 'dbb50000-0000-0000-0000-000000000013', t_bus_t1, 'seed:double_busbar_network_example'),
        (r_bus_t2_mv, fc_mv_busbar, 'dbb50000-0000-0000-0000-000000000013', t_bus_t2,
         fc_mv_cond, 'dbb50000-0000-0000-0000-000000000014', NULL, 'seed:double_busbar_network_example'),

        -- bay 2: Bus A -> SEL-2A -> BAYBUS-2, Bus B -> SEL-2B -> BAYBUS-2, BAYBUS-2 -> FEEDER-2
        (r_bus_t2_iso_t1, fc_mv_busbar, 'dbb50000-0000-0000-0000-000000000001', t_bus_t2,
         fc_mv_iso, 'dbb50000-0000-0000-0000-000000000021', t_iso_t1, 'seed:double_busbar_network_example'),
        (r_iso_t2_bus_t1, fc_mv_iso, 'dbb50000-0000-0000-0000-000000000021', t_iso_t2,
         fc_mv_busbar, 'dbb50000-0000-0000-0000-000000000023', t_bus_t1, 'seed:double_busbar_network_example'),
        (r_bus_t2_iso_t1, fc_mv_busbar, 'dbb50000-0000-0000-0000-000000000002', t_bus_t2,
         fc_mv_iso, 'dbb50000-0000-0000-0000-000000000022', t_iso_t1, 'seed:double_busbar_network_example'),
        (r_iso_t2_bus_t1, fc_mv_iso, 'dbb50000-0000-0000-0000-000000000022', t_iso_t2,
         fc_mv_busbar, 'dbb50000-0000-0000-0000-000000000023', t_bus_t1, 'seed:double_busbar_network_example'),
        (r_bus_t2_mv, fc_mv_busbar, 'dbb50000-0000-0000-0000-000000000023', t_bus_t2,
         fc_mv_cond, 'dbb50000-0000-0000-0000-000000000024', NULL, 'seed:double_busbar_network_example')
    ON CONFLICT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id) DO NOTHING;

    RAISE NOTICE 'double_busbar_network_example: BUS-A / BUS-B tied by CPLR-1, FEEDER-1 selected on Bus A, FEEDER-2 on Bus B. Trace downstream from BUS-A (reaches both); open CPLR-1 (FEEDER-2 drops); close SEL-2A to transfer FEEDER-2 onto Bus A.';
END $$;
