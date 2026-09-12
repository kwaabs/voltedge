-- seed/pnc_devices_network_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives under
-- /seed on purpose, same reasoning as the other seed files.
--
-- A real-feature-INSTANCE seed whose only job is to PROVE the new P&C secondary
-- / DC-auxiliary & control layer (seed/pnc_control_devices_example.sql) is
-- functional end to end: that the four compiled classes accept a real row with
-- their required coded field, and that the SUBSTATION_PANEL containment
-- relationship classes actually resolve and take feature_relationships edges.
--
-- Unlike the electrical instance seeds (test_/substation_/protection_network),
-- this writes NO runtime.feature_connectivity: STATION_BATTERY, BATTERY_CHARGER,
-- CONTROL_IED and ANNUNCIATOR are all network_role 'none' (not AC power-path
-- nodes), so their ONLY graph is the physical containment in
-- runtime.feature_relationships. That is exactly the surface under test here.
--
-- TWO panels ("a couple"), each housing part of a control building, so the test
-- covers multi-instance + both new field groups:
--
--   PNL-DC   (SUBSTATION_PANEL, panel_type = AUXILIARY_DC)
--     +-- contains --> BAT-1 (STATION_BATTERY, VRLA, 110 V, 200 Ah)
--     +-- contains --> CHG-1 (BATTERY_CHARGER, SMPS, 110 V / 30 A)
--
--   PNL-CTRL (SUBSTATION_PANEL, panel_type = RTU_SCADA, control_mode LOCAL_REMOTE)
--     +-- contains --> IED-1 (CONTROL_IED, bay_controller, IEC 61850)
--     +-- contains --> ANN-1 (ANNUNCIATOR, 24 windows)
--
-- The panels are ALSO hung under a mapped SUBSTATION so the whole assembly is
-- reachable from the map: navigate the substation on the map -> its panels ->
-- the P&C devices in each panel (STATION_BATTERY / CONTROL_IED / ... are
-- geometry 'none', so they never appear on the map themselves -- containment is
-- the only way in). This link is best-effort: the seed prefers the double-busbar
-- example substation DBB-SUB (seed/double_busbar_diagram_example.sql), falls back
-- to any published SUBSTATION, and if none exists it just SKIPS this step with a
-- NOTICE rather than failing -- the panel<->device containment below is the core
-- proof and never depends on a mapped substation being present.
--
-- Distinct uuid prefix (bcde0000-...) so it coexists with every other seed.
-- Fully idempotent: fixed uuids + ON CONFLICT (id) DO NOTHING on features,
-- (relationship_class_id, origin_feature_id, destination_feature_id) on edges.
--
-- HARD PREREQUISITES (fails LOUDLY naming the fix if any is missing):
--   task seed-substation-panel        + task compile CODE=SUBSTATION_PANEL
--   task seed-pnc-control-devices      + task compile CODE=STATION_BATTERY
--                                                       BATTERY_CHARGER
--                                                       CONTROL_IED
--                                                       ANNUNCIATOR
--   (seed-pnc-control-devices also seeds the SUBSTATION_PANEL contains <device>
--    relationship classes this seed resolves.)
--
-- Apply with psql (NOT the migration runner):
--   task seed-pnc-devices-network
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL has
-- been reviewed statically -- a live psql apply against a compiled DB + a GET of
-- one feature + its containment is still owed to call it verified.

DO $$
DECLARE
    fc_panel uuid;
    fc_bat   uuid;
    fc_chg   uuid;
    fc_ied   uuid;
    fc_ann   uuid;

    st_panel integer;
    st_bat   integer;
    st_chg   integer;
    st_ied   integer;
    st_ann   integer;

    rc_pnl_bat uuid;  -- SUBSTATION_PANEL contains STATION_BATTERY
    rc_pnl_chg uuid;  -- SUBSTATION_PANEL contains BATTERY_CHARGER
    rc_pnl_ied uuid;  -- SUBSTATION_PANEL contains CONTROL_IED
    rc_pnl_ann uuid;  -- SUBSTATION_PANEL contains ANNUNCIATOR

    fc_sub     uuid;  -- SUBSTATION feature class (for the map-reachability link)
    rc_sub_pnl uuid;  -- SUBSTATION contains SUBSTATION_PANEL
    sub_id     uuid;  -- a concrete mapped SUBSTATION instance (DBB-SUB or any)
BEGIN
    -- =====================================================================
    -- 1. Resolve feature_class ids.
    -- =====================================================================
    SELECT id INTO fc_panel FROM metadata.feature_classes WHERE code = 'SUBSTATION_PANEL';
    IF fc_panel IS NULL THEN RAISE EXCEPTION 'missing feature_class SUBSTATION_PANEL -- apply seed/substation_panel_example.sql then: task compile CODE=SUBSTATION_PANEL'; END IF;

    SELECT id INTO fc_bat FROM metadata.feature_classes WHERE code = 'STATION_BATTERY';
    IF fc_bat IS NULL THEN RAISE EXCEPTION 'missing feature_class STATION_BATTERY -- apply seed/pnc_control_devices_example.sql then: task compile CODE=STATION_BATTERY'; END IF;

    SELECT id INTO fc_chg FROM metadata.feature_classes WHERE code = 'BATTERY_CHARGER';
    IF fc_chg IS NULL THEN RAISE EXCEPTION 'missing feature_class BATTERY_CHARGER -- apply seed/pnc_control_devices_example.sql then: task compile CODE=BATTERY_CHARGER'; END IF;

    SELECT id INTO fc_ied FROM metadata.feature_classes WHERE code = 'CONTROL_IED';
    IF fc_ied IS NULL THEN RAISE EXCEPTION 'missing feature_class CONTROL_IED -- apply seed/pnc_control_devices_example.sql then: task compile CODE=CONTROL_IED'; END IF;

    SELECT id INTO fc_ann FROM metadata.feature_classes WHERE code = 'ANNUNCIATOR';
    IF fc_ann IS NULL THEN RAISE EXCEPTION 'missing feature_class ANNUNCIATOR -- apply seed/pnc_control_devices_example.sql then: task compile CODE=ANNUNCIATOR'; END IF;

    -- =====================================================================
    -- 2. Resolve subtype INTEGER codes (all are the single 'standard' subtype).
    -- =====================================================================
    SELECT code INTO st_panel FROM metadata.subtypes WHERE feature_class_id = fc_panel AND name = 'standard';
    IF st_panel IS NULL THEN RAISE EXCEPTION 'SUBSTATION_PANEL subtype ''standard'' not found -- re-apply seed/substation_panel_example.sql'; END IF;

    SELECT code INTO st_bat FROM metadata.subtypes WHERE feature_class_id = fc_bat AND name = 'standard';
    IF st_bat IS NULL THEN RAISE EXCEPTION 'STATION_BATTERY subtype ''standard'' not found -- re-apply seed/pnc_control_devices_example.sql'; END IF;

    SELECT code INTO st_chg FROM metadata.subtypes WHERE feature_class_id = fc_chg AND name = 'standard';
    IF st_chg IS NULL THEN RAISE EXCEPTION 'BATTERY_CHARGER subtype ''standard'' not found -- re-apply seed/pnc_control_devices_example.sql'; END IF;

    SELECT code INTO st_ied FROM metadata.subtypes WHERE feature_class_id = fc_ied AND name = 'standard';
    IF st_ied IS NULL THEN RAISE EXCEPTION 'CONTROL_IED subtype ''standard'' not found -- re-apply seed/pnc_control_devices_example.sql'; END IF;

    SELECT code INTO st_ann FROM metadata.subtypes WHERE feature_class_id = fc_ann AND name = 'standard';
    IF st_ann IS NULL THEN RAISE EXCEPTION 'ANNUNCIATOR subtype ''standard'' not found -- re-apply seed/pnc_control_devices_example.sql'; END IF;

    -- =====================================================================
    -- 3. Guard the compiled tables exist (geometry 'none' -> no geom column).
    -- =====================================================================
    IF to_regclass('gis.substation_panel') IS NULL THEN RAISE EXCEPTION 'table gis.substation_panel does not exist -- run: task compile CODE=SUBSTATION_PANEL'; END IF;
    IF to_regclass('gis.station_battery') IS NULL THEN RAISE EXCEPTION 'table gis.station_battery does not exist -- run: task compile CODE=STATION_BATTERY'; END IF;
    IF to_regclass('gis.battery_charger') IS NULL THEN RAISE EXCEPTION 'table gis.battery_charger does not exist -- run: task compile CODE=BATTERY_CHARGER'; END IF;
    IF to_regclass('gis.control_ied') IS NULL THEN RAISE EXCEPTION 'table gis.control_ied does not exist -- run: task compile CODE=CONTROL_IED'; END IF;
    IF to_regclass('gis.annunciator') IS NULL THEN RAISE EXCEPTION 'table gis.annunciator does not exist -- run: task compile CODE=ANNUNCIATOR'; END IF;

    -- The required coded columns the create path enforces.
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'station_battery' AND column_name = 'battery_technology') THEN
        RAISE EXCEPTION 'gis.station_battery is missing column battery_technology -- re-apply seed/pnc_control_devices_example.sql then: task compile CODE=STATION_BATTERY'; END IF;
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'battery_charger' AND column_name = 'charger_type') THEN
        RAISE EXCEPTION 'gis.battery_charger is missing column charger_type -- re-apply seed/pnc_control_devices_example.sql then: task compile CODE=BATTERY_CHARGER'; END IF;
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'gis' AND table_name = 'control_ied' AND column_name = 'ied_role') THEN
        RAISE EXCEPTION 'gis.control_ied is missing column ied_role -- re-apply seed/pnc_control_devices_example.sql then: task compile CODE=CONTROL_IED'; END IF;

    -- =====================================================================
    -- 4. Resolve the containment relationship_class ids (origin = panel,
    --    destination = device, kind 'containment') -- matched the same way the
    --    app queries them, so a rename of the human name can't break this seed.
    -- =====================================================================
    SELECT id INTO rc_pnl_bat FROM metadata.relationship_classes
     WHERE origin_feature_class_id = fc_panel AND destination_feature_class_id = fc_bat AND kind = 'containment' LIMIT 1;
    IF rc_pnl_bat IS NULL THEN RAISE EXCEPTION 'relationship_class SUBSTATION_PANEL -containment-> STATION_BATTERY not found -- re-apply seed/pnc_control_devices_example.sql'; END IF;

    SELECT id INTO rc_pnl_chg FROM metadata.relationship_classes
     WHERE origin_feature_class_id = fc_panel AND destination_feature_class_id = fc_chg AND kind = 'containment' LIMIT 1;
    IF rc_pnl_chg IS NULL THEN RAISE EXCEPTION 'relationship_class SUBSTATION_PANEL -containment-> BATTERY_CHARGER not found -- re-apply seed/pnc_control_devices_example.sql'; END IF;

    SELECT id INTO rc_pnl_ied FROM metadata.relationship_classes
     WHERE origin_feature_class_id = fc_panel AND destination_feature_class_id = fc_ied AND kind = 'containment' LIMIT 1;
    IF rc_pnl_ied IS NULL THEN RAISE EXCEPTION 'relationship_class SUBSTATION_PANEL -containment-> CONTROL_IED not found -- re-apply seed/pnc_control_devices_example.sql'; END IF;

    SELECT id INTO rc_pnl_ann FROM metadata.relationship_classes
     WHERE origin_feature_class_id = fc_panel AND destination_feature_class_id = fc_ann AND kind = 'containment' LIMIT 1;
    IF rc_pnl_ann IS NULL THEN RAISE EXCEPTION 'relationship_class SUBSTATION_PANEL -containment-> ANNUNCIATOR not found -- re-apply seed/pnc_control_devices_example.sql'; END IF;

    -- =====================================================================
    -- 5. Insert the panels (geometry 'none' -> NO geom column).
    -- =====================================================================
    INSERT INTO gis.substation_panel
        (id, name, subtype, status, panel_type, panel_ref, control_mode, manufacturer, model, condition_rating)
    VALUES
        ('bcde0000-0000-0000-0000-000000000001', 'PNL-DC', st_panel, 'published',
         'AUXILIARY_DC', 'DC-01', NULL, 'ExampleCo', 'DCB-1', 'GOOD'),
        ('bcde0000-0000-0000-0000-000000000002', 'PNL-CTRL', st_panel, 'published',
         'RTU_SCADA', 'CTRL-01', 'LOCAL_REMOTE', 'ExampleCo', 'RTU-1', 'GOOD')
    ON CONFLICT (id) DO NOTHING;

    -- =====================================================================
    -- 6. Insert the four device instances (all geometry 'none').
    -- =====================================================================
    INSERT INTO gis.station_battery
        (id, name, subtype, status, battery_technology, nominal_voltage_v, capacity_ah, string_count)
    VALUES
        ('bcde0000-0000-0000-0000-000000000011', 'BAT-1', st_bat, 'published',
         'lead_acid_vrla', 110, 200, 1)
    ON CONFLICT (id) DO NOTHING;

    INSERT INTO gis.battery_charger
        (id, name, subtype, status, charger_type, rated_current_a, output_voltage_v)
    VALUES
        ('bcde0000-0000-0000-0000-000000000012', 'CHG-1', st_chg, 'published',
         'smps', 30, 110)
    ON CONFLICT (id) DO NOTHING;

    INSERT INTO gis.control_ied
        (id, name, subtype, status, ied_role, protocol, manufacturer)
    VALUES
        ('bcde0000-0000-0000-0000-000000000013', 'IED-1', st_ied, 'published',
         'bay_controller', 'iec61850', 'ExampleCo')
    ON CONFLICT (id) DO NOTHING;

    INSERT INTO gis.annunciator
        (id, name, subtype, status, window_count, manufacturer)
    VALUES
        ('bcde0000-0000-0000-0000-000000000014', 'ANN-1', st_ann, 'published',
         24, 'ExampleCo')
    ON CONFLICT (id) DO NOTHING;

    -- =====================================================================
    -- 7. Insert the containment edges (runtime.feature_relationships). BAT-1 +
    --    CHG-1 under PNL-DC; IED-1 + ANN-1 under PNL-CTRL.
    -- =====================================================================
    INSERT INTO runtime.feature_relationships
        (relationship_class_id, origin_feature_class_id, origin_feature_id,
         destination_feature_class_id, destination_feature_id, established_by)
    VALUES
        (rc_pnl_bat, fc_panel, 'bcde0000-0000-0000-0000-000000000001',
         fc_bat, 'bcde0000-0000-0000-0000-000000000011', 'seed:pnc_devices_network_example'),
        (rc_pnl_chg, fc_panel, 'bcde0000-0000-0000-0000-000000000001',
         fc_chg, 'bcde0000-0000-0000-0000-000000000012', 'seed:pnc_devices_network_example'),
        (rc_pnl_ied, fc_panel, 'bcde0000-0000-0000-0000-000000000002',
         fc_ied, 'bcde0000-0000-0000-0000-000000000013', 'seed:pnc_devices_network_example'),
        (rc_pnl_ann, fc_panel, 'bcde0000-0000-0000-0000-000000000002',
         fc_ann, 'bcde0000-0000-0000-0000-000000000014', 'seed:pnc_devices_network_example')
    ON CONFLICT (relationship_class_id, origin_feature_id, destination_feature_id) DO NOTHING;

    -- =====================================================================
    -- 8. BEST-EFFORT: hang both panels under a mapped SUBSTATION so they are
    --    reachable from the map (substation -> panel -> device). Prefers
    --    DBB-SUB (the double-busbar example), else any published SUBSTATION.
    --    Skips with a NOTICE (never fails) if the class/relationship/instance
    --    isn't present -- the proof above stands on its own.
    -- =====================================================================
    SELECT id INTO fc_sub FROM metadata.feature_classes WHERE code = 'SUBSTATION';
    IF fc_sub IS NULL THEN
        RAISE NOTICE 'SUBSTATION feature class not found -- skipping map-reachability link (panels remain unparented). Apply seed/substation_example.sql + task compile CODE=SUBSTATION to enable it.';
    ELSE
        SELECT id INTO rc_sub_pnl FROM metadata.relationship_classes
         WHERE origin_feature_class_id = fc_sub AND destination_feature_class_id = fc_panel AND kind = 'containment' LIMIT 1;
        IF rc_sub_pnl IS NULL THEN
            RAISE NOTICE 'relationship_class SUBSTATION -containment-> SUBSTATION_PANEL not found -- skipping map-reachability link. Apply seed/substation_panel_containment_example.sql to enable it.';
        ELSE
            -- Prefer DBB-SUB (seed/double_busbar_diagram_example.sql); else the
            -- oldest published substation, so this is stable across re-runs.
            SELECT id INTO sub_id FROM gis.substation
             WHERE id = 'dbb50000-0000-0000-0000-0000000000a0' AND deleted_at IS NULL;
            IF sub_id IS NULL THEN
                SELECT id INTO sub_id FROM gis.substation
                 WHERE status = 'published' AND deleted_at IS NULL
                 ORDER BY created_at, id LIMIT 1;
            END IF;

            IF sub_id IS NULL THEN
                RAISE NOTICE 'no mapped SUBSTATION instance found -- skipping map-reachability link. Apply seed/double_busbar_diagram_example.sql (creates DBB-SUB) or any substation instance to enable it.';
            ELSE
                INSERT INTO runtime.feature_relationships
                    (relationship_class_id, origin_feature_class_id, origin_feature_id,
                     destination_feature_class_id, destination_feature_id, established_by)
                VALUES
                    (rc_sub_pnl, fc_sub, sub_id,
                     fc_panel, 'bcde0000-0000-0000-0000-000000000001', 'seed:pnc_devices_network_example'),
                    (rc_sub_pnl, fc_sub, sub_id,
                     fc_panel, 'bcde0000-0000-0000-0000-000000000002', 'seed:pnc_devices_network_example')
                ON CONFLICT (relationship_class_id, origin_feature_id, destination_feature_id) DO NOTHING;
                RAISE NOTICE 'linked PNL-DC + PNL-CTRL under SUBSTATION % -- reachable from the map (substation -> panel -> device).', sub_id;
            END IF;
        END IF;
    END IF;

    RAISE NOTICE 'seed/pnc_devices_network_example.sql applied: 2 panels + 4 P&C devices + 4 containment edges (idempotent). GET /features/STATION_BATTERY, /BATTERY_CHARGER, /CONTROL_IED, /ANNUNCIATOR to see them; check PNL-DC / PNL-CTRL containment.';
END $$;
