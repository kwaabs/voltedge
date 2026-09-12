-- seed/oms_example.sql
-- Purpose: Phase 49 OMS demo data -- the trouble calls that let you drive the
-- WHOLE predictive chain end to end without hand-typing anything: log calls ->
-- POST /oms/incidents/predict -> a real, device-attributed incident -> confirm
-- -> restore. It is a SEED (psql), NOT a migration: apply.sh only scans
-- /migrations, and this depends on compiled gis.* feature tables the runner
-- never touches (same reasoning as every other file under /seed).
--
-- WHY THE SUBSTATION NETWORK, NOT test_network:
--   The prediction engine rolls each call UP to the nearest PROTECTIVE device
--   (api/internal/oms ProtectiveDeviceClasses = MV_FUSE / MV_RECLOSER /
--   MV_BREAKER). test_network's only inline device is SW-101, an MV_SWITCH --
--   a sectionaliser, deliberately EXCLUDED as non-protective -- so calls there
--   roll up to nothing and land in an "unattributed" cluster. The substation
--   network (seed/substation_network_example.sql) has RC-1, a real MV_RECLOSER,
--   on Feeder 1 upstream of SP-1/SP-2/SP-3, so calls on those three service
--   points cluster cleanly onto ONE incident blamed on RC-1. That is the demo.
--
--   Feeder-1 path (all three SPs sit below RC-1; the only devices between them
--   and RC-1 are SW-1 [MV_SWITCH] and LV-SW-1 [LV_SWITCH], both non-protective):
--       RC-1 (MV_RECLOSER) -> ... -> DTX-1 -> F1-LV-1 -> MTR-1 -> SP-1
--                                            F1-LV-1 -> MTR-2 -> SP-2
--                                            F1-LV-1 -> LV-SW-1 -> MTR-3 -> SP-3
--
-- WHAT IT WRITES:
--   1. customers_served on the three Feeder-1 SPs (only where still NULL, so a
--      re-run never clobbers an operator's later edit). Without this every SP
--      counts as 1 customer (the app's NULL=>1 rule); setting it makes the
--      affected-customer total on the incident meaningful (24 + 1 + 18 = 43).
--   2. Four oms.trouble_calls with fixed uuids (ON CONFLICT (id) DO NOTHING =>
--      idempotent):
--        TC-1/2/3  status 'open', service_point_id = SP-1/2/3  -> feed predict.
--        TC-4      status 'open', address + location only, NO service_point_id
--                  -> stays in the association queue (predict ignores it); drop
--                     it next to SP-1 so "Snap to nearest" binds it to SP-1.
--
-- HARD PREREQUISITES (fails LOUDLY naming the fix if anything is missing):
--   * migrations 0087 (+ 0088 permissions) applied      -> `task migrate`
--   * gis.service_point compiled WITH customers_served   -> re-run
--       seed/service_point_example.sql then `task compile CODE=SERVICE_POINT`
--   * the substation network seeded (SP-1/2/3 must exist) ->
--       `task seed-substation-network` (and ITS prerequisites)
--
-- Apply (psql, NOT the migration runner), after the prerequisites above:
--   task seed-oms                (or the psql -f invocation directly)
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL has
-- been reviewed statically -- a live psql apply against a fully-seeded DB plus a
-- POST /oms/incidents/predict re-check are still owed to call it verified.

DO $$
DECLARE
    -- The three Feeder-1 service points from seed/substation_network_example.sql.
    sp_1 constant uuid := '5b510000-0000-0000-0000-000000000041';  -- SP-1 residential
    sp_2 constant uuid := '5b510000-0000-0000-0000-000000000042';  -- SP-2 commercial
    sp_3 constant uuid := '5b510000-0000-0000-0000-000000000043';  -- SP-3 residential
BEGIN
    -- =====================================================================
    -- 1. Guard the prerequisites.
    -- =====================================================================
    IF to_regclass('oms.trouble_calls') IS NULL THEN
        RAISE EXCEPTION 'table oms.trouble_calls does not exist -- run: task migrate (applies migration 0087_create_oms.sql)';
    END IF;

    IF to_regclass('gis.service_point') IS NULL THEN
        RAISE EXCEPTION 'table gis.service_point does not exist -- run: task compile CODE=SERVICE_POINT';
    END IF;

    IF NOT EXISTS (SELECT 1 FROM information_schema.columns
                   WHERE table_schema = 'gis' AND table_name = 'service_point'
                     AND column_name = 'customers_served') THEN
        RAISE EXCEPTION 'gis.service_point is missing column customers_served -- re-apply seed/service_point_example.sql then: task compile CODE=SERVICE_POINT';
    END IF;

    IF NOT EXISTS (SELECT 1 FROM gis.service_point WHERE id = sp_1 AND deleted_at IS NULL) THEN
        RAISE EXCEPTION 'service point SP-1 (%) not found -- run: task seed-substation-network (this demo rolls calls up to RC-1 on that network)', sp_1;
    END IF;

    -- =====================================================================
    -- 2. Give the three affected SPs a real customer count (only where still
    --    unset, so a re-run is non-destructive). NULL => 1 in the app, so this
    --    is purely to make the incident's affected-customer total meaningful.
    -- =====================================================================
    UPDATE gis.service_point SET customers_served = 24 WHERE id = sp_1 AND customers_served IS NULL;
    UPDATE gis.service_point SET customers_served = 1  WHERE id = sp_2 AND customers_served IS NULL;
    UPDATE gis.service_point SET customers_served = 18 WHERE id = sp_3 AND customers_served IS NULL;

    -- =====================================================================
    -- 3. Log the trouble calls (fixed uuids + ON CONFLICT (id) DO NOTHING =>
    --    idempotent). reported_at is staggered over the last few minutes so the
    --    queue reads like a real call-in sequence. All status 'open' so the
    --    prediction rollup (which consumes open + SP-resolved calls) picks the
    --    first three up; TC-4 carries no service_point_id, so it stays queued.
    -- =====================================================================

    -- TC-1/2/3: resolved calls on Feeder 1 -> roll up to RC-1.
    INSERT INTO oms.trouble_calls
        (id, service_point_id, reported_at, caller_ref, call_type, status, notes)
    VALUES
        ('0a150000-0000-0000-0000-000000000001', sp_1, now() - interval '9 minutes',
         '+233-24-000-0001', 'no_supply', 'open', 'Whole house dark; neighbours out too.'),
        ('0a150000-0000-0000-0000-000000000002', sp_2, now() - interval '6 minutes',
         '+233-24-000-0002', 'no_supply', 'open', 'Shop lost power, no lights on the street.'),
        ('0a150000-0000-0000-0000-000000000003', sp_3, now() - interval '4 minutes',
         '+233-24-000-0003', 'no_supply', 'open', 'No supply since a bang up the line.')
    ON CONFLICT (id) DO NOTHING;

    -- TC-4: an UNRESOLVED call -- address + location only, no service_point_id.
    -- Dropped next to SP-1 (-0.2942, 5.7061) so "Snap to nearest" resolves it to
    -- SP-1; until associated it is excluded from the prediction rollup.
    INSERT INTO oms.trouble_calls
        (id, address, location, reported_at, caller_ref, call_type, status, notes)
    VALUES
        ('0a150000-0000-0000-0000-000000000004',
         '12 Feeder Road, near SP-1',
         ST_SetSRID(ST_MakePoint(-0.2942, 5.7061), 4326),
         now() - interval '2 minutes',
         '+233-24-000-0004', 'no_supply', 'open',
         'Caller could not give a meter number -- needs snapping to nearest SP.')
    ON CONFLICT (id) DO NOTHING;

    RAISE NOTICE 'seed/oms_example.sql applied: 3 customer counts + 4 trouble calls (3 resolved on Feeder 1 -> predict clusters on RC-1, 1 unresolved for snap; idempotent).';
END $$;
