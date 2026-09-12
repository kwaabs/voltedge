-- seed/oms_planned_example.sql
-- Purpose: Phase 49 Slice 3 (planned outages + switching orders) demo data -- a
-- ready-to-switch SCHEDULED planned outage so you can drive the WHOLE S3 chain
-- end to end without hand-typing anything: open Outages -> a violet 'scheduled'
-- incident -> Switching -> Generate switching order -> tick the steps -> Begin
-- outage (materialises interruptions) -> Restore. It is a SEED (psql), NOT a
-- migration: apply.sh only scans /migrations, and this depends on compiled gis.*
-- feature tables the runner never touches (same reasoning as seed/oms_example.sql).
--
-- WHY SW-1 ON THE SUBSTATION NETWORK:
--   A planned outage is booked around operating ONE device; the generator traces
--   that device's DOWNSTREAM cone for the affected snapshot and its nearest
--   UPSTREAM isolation point (api/internal/oms SwitchingDeviceClasses =
--   MV_SWITCH / MV_ISOLATOR / MV_RECLOSER / MV_BREAKER) for the source-side earth.
--   SW-1 (MV_SWITCH) on Feeder 1 sits BELOW RC-1 and ABOVE DTX-1, so:
--       * downstream cone = SP-1/SP-2/SP-3  (the three Feeder-1 service points)
--       * upstream boundary = RC-1 (MV_RECLOSER)  -> a real earthing point
--   giving a full switching order: open SW-1, verify 3 SPs de-energised, earth at
--   RC-1; restore = remove earths, close SW-1. (seed/oms_example.sql instead blames
--   RC-1 for an UNPLANNED incident -- the two demos are independent incidents.)
--
--   Feeder-1 path (from seed/substation_network_example.sql):
--       RC-1 (MV_RECLOSER) -> F1-MV-2 -> SW-1 (MV_SWITCH) -> F1-MV-3 -> DTX-1
--                                                     -> F1-LV-1 -> MTR-1 -> SP-1
--                                                                 -> MTR-2 -> SP-2
--                                                     -> LV-SW-1  -> MTR-3 -> SP-3
--
-- WHAT IT WRITES:
--   1. customers_served on the three Feeder-1 SPs (only where still NULL, so a
--      re-run never clobbers an operator's edit; same non-destructive rule as
--      seed/oms_example.sql). Makes the affected-customer total meaningful (43).
--   2. ONE oms.incidents row, fixed uuid, ON CONFLICT (id) DO NOTHING =>
--      idempotent: outage_type 'planned', status 'scheduled', device = SW-1,
--      planned window tomorrow, affected snapshot computed from the SW-1 cone.
--      The switching order itself is generated LIVE by the app (POST
--      /oms/incidents/{id}/switching-orders) -- NOT hand-rolled here, so the
--      steps always match GenerateSwitchingOrder's real trace output.
--
-- HARD PREREQUISITES (fails LOUDLY naming the fix if anything is missing):
--   * migrations 0087/0088 AND 0089 applied              -> `task migrate`
--       (0089 adds status 'scheduled' + planned_start/_end + the switching tables)
--   * gis.mv_switch compiled (SW-1 must exist)           -> `task seed-substation-network`
--       (and ITS prerequisites, incl. `task compile CODE=MV_SWITCH`)
--   * gis.service_point compiled WITH customers_served    -> re-run
--       seed/service_point_example.sql then `task compile CODE=SERVICE_POINT`
--
-- Apply (psql, NOT the migration runner), after the prerequisites above:
--   task seed-oms-planned        (or the psql -f invocation directly)
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL has
-- been reviewed statically -- a live psql apply against a fully-seeded DB plus a
-- Generate/Begin/Restore click-through are still owed to call it verified.

DO $$
DECLARE
    -- The operated device and the three SPs in its downstream cone, all from
    -- seed/substation_network_example.sql.
    sw_1 constant uuid := '5b510000-0000-0000-0000-000000000008';  -- SW-1 (MV_SWITCH)
    sp_1 constant uuid := '5b510000-0000-0000-0000-000000000041';  -- SP-1 residential
    sp_2 constant uuid := '5b510000-0000-0000-0000-000000000042';  -- SP-2 commercial
    sp_3 constant uuid := '5b510000-0000-0000-0000-000000000043';  -- SP-3 residential
    inc_id constant uuid := '0a160000-0000-0000-0000-000000000001';

    aff_sps  integer;
    aff_cust integer;
BEGIN
    -- =====================================================================
    -- 1. Guard the prerequisites.
    -- =====================================================================
    IF to_regclass('oms.incidents') IS NULL THEN
        RAISE EXCEPTION 'table oms.incidents does not exist -- run: task migrate (applies migration 0087_create_oms.sql)';
    END IF;

    IF NOT EXISTS (SELECT 1 FROM information_schema.columns
                   WHERE table_schema = 'oms' AND table_name = 'incidents'
                     AND column_name = 'planned_start') THEN
        RAISE EXCEPTION 'oms.incidents is missing column planned_start -- run: task migrate (applies migration 0089_oms_planned_switching.sql, which also adds status ''scheduled'' + the switching tables)';
    END IF;

    IF to_regclass('gis.mv_switch') IS NULL THEN
        RAISE EXCEPTION 'table gis.mv_switch does not exist -- run: task compile CODE=MV_SWITCH';
    END IF;

    IF NOT EXISTS (SELECT 1 FROM gis.mv_switch WHERE id = sw_1 AND deleted_at IS NULL) THEN
        RAISE EXCEPTION 'switch SW-1 (%) not found -- run: task seed-substation-network (this planned outage operates SW-1 on that network)', sw_1;
    END IF;

    IF NOT EXISTS (SELECT 1 FROM information_schema.columns
                   WHERE table_schema = 'gis' AND table_name = 'service_point'
                     AND column_name = 'customers_served') THEN
        RAISE EXCEPTION 'gis.service_point is missing column customers_served -- re-apply seed/service_point_example.sql then: task compile CODE=SERVICE_POINT';
    END IF;

    -- =====================================================================
    -- 2. Give the three affected SPs a real customer count (only where still
    --    unset -- non-destructive, mirrors seed/oms_example.sql). NULL => 1 in
    --    the app, so this is purely to make the snapshot total meaningful.
    -- =====================================================================
    UPDATE gis.service_point SET customers_served = 24 WHERE id = sp_1 AND customers_served IS NULL;
    UPDATE gis.service_point SET customers_served = 1  WHERE id = sp_2 AND customers_served IS NULL;
    UPDATE gis.service_point SET customers_served = 18 WHERE id = sp_3 AND customers_served IS NULL;

    -- =====================================================================
    -- 3. Compute the affected snapshot from SW-1's downstream cone (SP-1/2/3),
    --    the same coalesce(customers_served, 1) rule oms.AffectedCustomers uses.
    --    Kept in the seed (rather than a live trace) so the stored snapshot on
    --    the scheduled incident reads correctly before it is executed; the app
    --    re-traces live when the switching order is generated.
    -- =====================================================================
    SELECT count(*), coalesce(sum(coalesce(customers_served, 1)), 0)
      INTO aff_sps, aff_cust
      FROM gis.service_point
     WHERE id IN (sp_1, sp_2, sp_3) AND deleted_at IS NULL;

    -- =====================================================================
    -- 4. Book the planned outage (fixed uuid + ON CONFLICT (id) DO NOTHING =>
    --    idempotent). status 'scheduled' + outage_type 'planned' so the Outages
    --    board shows the violet 'scheduled' badge and a "Begin outage" action;
    --    the window is tomorrow morning. started_at defaults to now() and is
    --    reset to the real execution time when the outage is begun (confirmed).
    -- =====================================================================
    INSERT INTO oms.incidents
        (id, predicted_device_class, predicted_device_id, outage_type, cause_code,
         status, planned_start, planned_end, affected_customers, affected_service_points, notes)
    VALUES
        (inc_id, 'MV_SWITCH', sw_1, 'planned', 'planned_maintenance',
         'scheduled',
         date_trunc('day', now()) + interval '1 day 8 hours',
         date_trunc('day', now()) + interval '1 day 11 hours',
         aff_cust, aff_sps,
         'Planned: replace SW-1 disconnector; isolate the Feeder 1 downstream section, earth at RC-1, restore on completion.')
    ON CONFLICT (id) DO NOTHING;

    RAISE NOTICE 'seed/oms_planned_example.sql applied: 1 scheduled planned outage on SW-1 (% SP / % customers affected). Generate its switching order in the app, then Begin outage -> Restore. Idempotent.', aff_sps, aff_cust;
END $$;
