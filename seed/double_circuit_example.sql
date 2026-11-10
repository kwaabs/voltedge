-- seed/double_circuit_example.sql
-- Purpose: NOT a migration -- lives under /seed, applied with psql (task
-- seed-double-circuit), same reasoning as every other seed file.
--
-- TEST FIXTURE for the double-circuit / tower-elevation proof-of-concept. Three
-- small structure+circuit networks, deliberately focused on what the concept
-- needs -- STRUCTURES carrying TWO CIRCUITS -- not full electrically-traced
-- feeders (no sources/switches/transformers/connectivity; that apparatus adds
-- risk and isn't what this proves). Each conductor is one feature tagged with a
-- circuit_ref, and every structure is linked to the conductors it carries via a
-- spatial_support relationship (the same TOWER/POLE supports MV_CONDUCTOR classes
-- Phase 35 uses for a real double circuit).
--
--   NET 1  — 33 kV DOUBLE CIRCUIT on TOWERS (4 lattice towers)
--            FDR-33A and FDR-33B share the SAME path (coincident geometry) → the
--            map draws one line; the structures each carry 2 circuits.
--   NET 2  — 11 kV DOUBLE CIRCUIT on MIXED poles + towers (P,T,P,T,P)
--            FDR-11A / FDR-11B, coincident, every structure carries both.
--   NET 3  — LV, TWO PARALLEL lines on POLES (4 poles)
--            LV-FDR-A / LV-FDR-B, slightly offset (genuinely parallel), both on
--            the shared poles.
--   NET 4  — 33 kV QUAD CIRCUIT on TOWERS (3 lattice towers): TWO circuits on the
--            LEFT, TWO on the RIGHT. Here each tower→conductor spatial_support link
--            CARRIES AN ARRANGEMENT HINT in its attributes jsonb (migration 0190):
--            {"side":"L|R","level":1|2}. The elevation reads it and draws the true
--            2-left/2-right layout instead of inferring a symmetric stack. NET 1–3
--            set no hint on purpose, so they exercise the inferred fallback; NET 4
--            exercises the recorded-arrangement path. All four circuits are 33 kV
--            (same symbology colour) — told apart by circuit_ref + position, not hue.
--   NET 5  — MIXED-ORIENTATION POLE (2 poles): TWO 11 kV circuits arranged VERTICAL
--            (left + right, level 1) OVER one LV circuit arranged HORIZONTAL (centre,
--            level 2) — the canonical MV-over-LV distribution pole. Each link records
--            {"side","level","orientation"}; the elevation honours orientation
--            per-circuit (MV vertical, LV horizontal on the same structure).
--
-- circuit_ref is set DIRECTLY here (it is nullable text). In production it is
-- DERIVED from a conductor's circuit_id (see seed/circuit_ref_example.sql); for a
-- throwaway test fixture a hand-set tag is the simplest way to give the two runs
-- distinct circuit identities the elevation/map can read. Re-running the
-- circuit_ref derive (seed/circuit_ref_derived.sql) would blank these (no
-- circuit_id) -- fine, they are disposable test features.
--
-- PREREQUISITES (classes seeded AND compiled): TOWER (seed/tower_example.sql),
-- POLE (seed/pole_example.sql), MV_CONDUCTOR (seed/connectivity_example.sql),
-- LV_CONDUCTOR (seed/lv_conductors_example.sql), the circuit_ref field on both
-- conductor classes (seed/circuit_ref_example.sql, then recompile), and the
-- TOWER/POLE supports MV_CONDUCTOR relationship classes
-- (seed/tower_relationship_example.sql, seed/relationship_example.sql). The DO
-- block RAISEs an actionable message naming any missing piece. Fully idempotent
-- (fixed uuids + ON CONFLICT).
--
-- Static-review note: shell can't spawn here (bash EPERM), so this is reviewed
-- statically -- a live psql apply + a look on the map are still owed to call it
-- verified.

DO $$
DECLARE
    fc_tower uuid; fc_pole uuid; fc_mv uuid; fc_lv uuid;
    st_tower_lattice integer; st_pole_conc integer; st_mv_oh integer; st_lv_oh integer;
    rc_tower_mv uuid; rc_pole_mv uuid; rc_pole_lv uuid;
BEGIN
    -- ---- feature classes ----
    SELECT id INTO fc_tower FROM metadata.feature_classes WHERE code = 'TOWER';
    IF fc_tower IS NULL THEN RAISE EXCEPTION 'missing feature_class TOWER -- apply seed/tower_example.sql then: task compile CODE=TOWER'; END IF;
    SELECT id INTO fc_pole FROM metadata.feature_classes WHERE code = 'POLE';
    IF fc_pole IS NULL THEN RAISE EXCEPTION 'missing feature_class POLE -- apply seed/pole_example.sql then: task compile CODE=POLE'; END IF;
    SELECT id INTO fc_mv FROM metadata.feature_classes WHERE code = 'MV_CONDUCTOR';
    IF fc_mv IS NULL THEN RAISE EXCEPTION 'missing feature_class MV_CONDUCTOR -- apply seed/connectivity_example.sql then: task compile CODE=MV_CONDUCTOR'; END IF;
    SELECT id INTO fc_lv FROM metadata.feature_classes WHERE code = 'LV_CONDUCTOR';
    IF fc_lv IS NULL THEN RAISE EXCEPTION 'missing feature_class LV_CONDUCTOR -- apply seed/lv_conductors_example.sql then: task compile CODE=LV_CONDUCTOR'; END IF;

    -- ---- compiled physical tables ----
    IF to_regclass('gis.tower') IS NULL THEN RAISE EXCEPTION 'table gis.tower does not exist -- run: task compile CODE=TOWER'; END IF;
    IF to_regclass('gis.pole') IS NULL THEN RAISE EXCEPTION 'table gis.pole does not exist -- run: task compile CODE=POLE'; END IF;
    IF to_regclass('gis.mv_conductor') IS NULL THEN RAISE EXCEPTION 'table gis.mv_conductor does not exist -- run: task compile CODE=MV_CONDUCTOR'; END IF;
    IF to_regclass('gis.lv_conductor') IS NULL THEN RAISE EXCEPTION 'table gis.lv_conductor does not exist -- run: task compile CODE=LV_CONDUCTOR'; END IF;

    -- ---- circuit_ref columns (the identity the fixture sets) ----
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema='gis' AND table_name='mv_conductor' AND column_name='circuit_ref') THEN
        RAISE EXCEPTION 'gis.mv_conductor is missing column circuit_ref -- apply seed/circuit_ref_example.sql then: task compile CODE=MV_CONDUCTOR'; END IF;
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema='gis' AND table_name='lv_conductor' AND column_name='circuit_ref') THEN
        RAISE EXCEPTION 'gis.lv_conductor is missing column circuit_ref -- apply seed/circuit_ref_example.sql then: task compile CODE=LV_CONDUCTOR'; END IF;

    -- ---- subtype integer codes ----
    SELECT code INTO st_tower_lattice FROM metadata.subtypes WHERE feature_class_id = fc_tower AND name = 'lattice';
    IF st_tower_lattice IS NULL THEN RAISE EXCEPTION 'TOWER subtype ''lattice'' not found -- re-apply seed/tower_example.sql'; END IF;
    SELECT code INTO st_pole_conc FROM metadata.subtypes WHERE feature_class_id = fc_pole AND name = 'concrete';
    IF st_pole_conc IS NULL THEN RAISE EXCEPTION 'POLE subtype ''concrete'' not found -- re-apply seed/pole_example.sql'; END IF;
    SELECT code INTO st_mv_oh FROM metadata.subtypes WHERE feature_class_id = fc_mv AND name = 'overhead_mv_conductor';
    IF st_mv_oh IS NULL THEN RAISE EXCEPTION 'MV_CONDUCTOR subtype ''overhead_mv_conductor'' not found -- re-apply seed/connectivity_example.sql'; END IF;
    SELECT code INTO st_lv_oh FROM metadata.subtypes WHERE feature_class_id = fc_lv AND name = 'overhead_lv_conductor';
    IF st_lv_oh IS NULL THEN RAISE EXCEPTION 'LV_CONDUCTOR subtype ''overhead_lv_conductor'' not found -- re-apply seed/lv_conductors_example.sql'; END IF;

    -- ---- relationship classes (create POLE supports LV_CONDUCTOR if absent) ----
    INSERT INTO metadata.relationship_classes (name, origin_feature_class_id, destination_feature_class_id, kind, cardinality)
    SELECT 'POLE supports LV_CONDUCTOR', fc_pole, fc_lv, 'spatial_support', 'many_to_many'
    WHERE NOT EXISTS (
        SELECT 1 FROM metadata.relationship_classes
        WHERE origin_feature_class_id = fc_pole AND destination_feature_class_id = fc_lv AND kind = 'spatial_support');

    SELECT id INTO rc_tower_mv FROM metadata.relationship_classes
     WHERE origin_feature_class_id = fc_tower AND destination_feature_class_id = fc_mv AND kind = 'spatial_support';
    IF rc_tower_mv IS NULL THEN RAISE EXCEPTION 'relationship class "TOWER supports MV_CONDUCTOR" missing -- apply seed/tower_relationship_example.sql'; END IF;
    SELECT id INTO rc_pole_mv FROM metadata.relationship_classes
     WHERE origin_feature_class_id = fc_pole AND destination_feature_class_id = fc_mv AND kind = 'spatial_support';
    IF rc_pole_mv IS NULL THEN RAISE EXCEPTION 'relationship class "POLE supports MV_CONDUCTOR" missing -- apply seed/relationship_example.sql'; END IF;
    SELECT id INTO rc_pole_lv FROM metadata.relationship_classes
     WHERE origin_feature_class_id = fc_pole AND destination_feature_class_id = fc_lv AND kind = 'spatial_support';

    -- =====================================================================
    -- NET 1 — 33 kV DOUBLE CIRCUIT on 4 lattice towers (y = 5.620)
    -- =====================================================================
    INSERT INTO gis.tower (id, name, subtype, status, geom, tower_material, height_class, condition_rating) VALUES
        ('dc330000-0000-0000-0000-000000000001', 'TWR-33-1', st_tower_lattice, 'published', ST_SetSRID(ST_MakePoint(-0.2200,5.6200),4326), 'STEEL_LATTICE', 'CLASS_3', 'GOOD'),
        ('dc330000-0000-0000-0000-000000000002', 'TWR-33-2', st_tower_lattice, 'published', ST_SetSRID(ST_MakePoint(-0.2180,5.6200),4326), 'STEEL_LATTICE', 'CLASS_3', 'GOOD'),
        ('dc330000-0000-0000-0000-000000000003', 'TWR-33-3', st_tower_lattice, 'published', ST_SetSRID(ST_MakePoint(-0.2160,5.6200),4326), 'STEEL_LATTICE', 'CLASS_3', 'GOOD'),
        ('dc330000-0000-0000-0000-000000000004', 'TWR-33-4', st_tower_lattice, 'published', ST_SetSRID(ST_MakePoint(-0.2140,5.6200),4326), 'STEEL_LATTICE', 'CLASS_3', 'GOOD')
    ON CONFLICT (id) DO NOTHING;

    -- Two 33 kV circuits on the SAME path (coincident) → one drawn line.
    INSERT INTO gis.mv_conductor (id, name, subtype, status, geom, voltage_class, condition_rating, conductor_spec, circuit_ref) VALUES
        ('dc330000-0000-0000-0000-0000000000a1', 'FDR-33A', st_mv_oh, 'published',
         ST_SetSRID(ST_GeomFromText('LINESTRING(-0.2200 5.6200, -0.2180 5.6200, -0.2160 5.6200, -0.2140 5.6200)'),4326),
         '33KV', 'GOOD', 'ACSR_100', 'FDR-33A'),
        ('dc330000-0000-0000-0000-0000000000a2', 'FDR-33B', st_mv_oh, 'published',
         ST_SetSRID(ST_GeomFromText('LINESTRING(-0.2200 5.6200, -0.2180 5.6200, -0.2160 5.6200, -0.2140 5.6200)'),4326),
         '33KV', 'GOOD', 'ACSR_100', 'FDR-33B')
    ON CONFLICT (id) DO NOTHING;

    -- Each tower supports BOTH 33 kV circuits.
    INSERT INTO runtime.feature_relationships (relationship_class_id, origin_feature_class_id, origin_feature_id, destination_feature_class_id, destination_feature_id, established_by)
    SELECT rc_tower_mv, fc_tower, t.id, fc_mv, c.id, 'seed:double_circuit'
    FROM (VALUES
        ('dc330000-0000-0000-0000-000000000001'::uuid), ('dc330000-0000-0000-0000-000000000002'::uuid),
        ('dc330000-0000-0000-0000-000000000003'::uuid), ('dc330000-0000-0000-0000-000000000004'::uuid)) AS t(id)
    CROSS JOIN (VALUES
        ('dc330000-0000-0000-0000-0000000000a1'::uuid), ('dc330000-0000-0000-0000-0000000000a2'::uuid)) AS c(id)
    ON CONFLICT (relationship_class_id, origin_feature_id, destination_feature_id) DO NOTHING;

    -- =====================================================================
    -- NET 2 — 11 kV DOUBLE CIRCUIT on MIXED poles + towers (y = 5.610)
    --   01 pole, 02 tower, 03 pole, 04 tower, 05 pole
    -- =====================================================================
    INSERT INTO gis.pole (id, name, subtype, status, geom, pole_material, pole_use, height_class, condition_rating) VALUES
        ('dc110000-0000-0000-0000-000000000001', 'P-11-1', st_pole_conc, 'published', ST_SetSRID(ST_MakePoint(-0.2200,5.6100),4326), 'CONCRETE', 'DISTRIBUTION', 'CLASS_2', 'GOOD'),
        ('dc110000-0000-0000-0000-000000000003', 'P-11-3', st_pole_conc, 'published', ST_SetSRID(ST_MakePoint(-0.2160,5.6100),4326), 'CONCRETE', 'DISTRIBUTION', 'CLASS_2', 'GOOD'),
        ('dc110000-0000-0000-0000-000000000005', 'P-11-5', st_pole_conc, 'published', ST_SetSRID(ST_MakePoint(-0.2120,5.6100),4326), 'CONCRETE', 'DISTRIBUTION', 'CLASS_2', 'GOOD')
    ON CONFLICT (id) DO NOTHING;

    INSERT INTO gis.tower (id, name, subtype, status, geom, tower_material, height_class, condition_rating) VALUES
        ('dc110000-0000-0000-0000-000000000002', 'TWR-11-2', st_tower_lattice, 'published', ST_SetSRID(ST_MakePoint(-0.2180,5.6100),4326), 'STEEL_LATTICE', 'CLASS_3', 'GOOD'),
        ('dc110000-0000-0000-0000-000000000004', 'TWR-11-4', st_tower_lattice, 'published', ST_SetSRID(ST_MakePoint(-0.2140,5.6100),4326), 'STEEL_LATTICE', 'CLASS_3', 'GOOD')
    ON CONFLICT (id) DO NOTHING;

    INSERT INTO gis.mv_conductor (id, name, subtype, status, geom, voltage_class, condition_rating, conductor_spec, circuit_ref) VALUES
        ('dc110000-0000-0000-0000-0000000000a1', 'FDR-11A', st_mv_oh, 'published',
         ST_SetSRID(ST_GeomFromText('LINESTRING(-0.2200 5.6100, -0.2180 5.6100, -0.2160 5.6100, -0.2140 5.6100, -0.2120 5.6100)'),4326),
         '11KV', 'GOOD', 'ACSR_100', 'FDR-11A'),
        ('dc110000-0000-0000-0000-0000000000a2', 'FDR-11B', st_mv_oh, 'published',
         ST_SetSRID(ST_GeomFromText('LINESTRING(-0.2200 5.6100, -0.2180 5.6100, -0.2160 5.6100, -0.2140 5.6100, -0.2120 5.6100)'),4326),
         '11KV', 'GOOD', 'ACSR_100', 'FDR-11B')
    ON CONFLICT (id) DO NOTHING;

    -- poles support both circuits (POLE class), towers support both (TOWER class)
    INSERT INTO runtime.feature_relationships (relationship_class_id, origin_feature_class_id, origin_feature_id, destination_feature_class_id, destination_feature_id, established_by)
    SELECT rc_pole_mv, fc_pole, p.id, fc_mv, c.id, 'seed:double_circuit'
    FROM (VALUES ('dc110000-0000-0000-0000-000000000001'::uuid), ('dc110000-0000-0000-0000-000000000003'::uuid), ('dc110000-0000-0000-0000-000000000005'::uuid)) AS p(id)
    CROSS JOIN (VALUES ('dc110000-0000-0000-0000-0000000000a1'::uuid), ('dc110000-0000-0000-0000-0000000000a2'::uuid)) AS c(id)
    ON CONFLICT (relationship_class_id, origin_feature_id, destination_feature_id) DO NOTHING;

    INSERT INTO runtime.feature_relationships (relationship_class_id, origin_feature_class_id, origin_feature_id, destination_feature_class_id, destination_feature_id, established_by)
    SELECT rc_tower_mv, fc_tower, t.id, fc_mv, c.id, 'seed:double_circuit'
    FROM (VALUES ('dc110000-0000-0000-0000-000000000002'::uuid), ('dc110000-0000-0000-0000-000000000004'::uuid)) AS t(id)
    CROSS JOIN (VALUES ('dc110000-0000-0000-0000-0000000000a1'::uuid), ('dc110000-0000-0000-0000-0000000000a2'::uuid)) AS c(id)
    ON CONFLICT (relationship_class_id, origin_feature_id, destination_feature_id) DO NOTHING;

    -- =====================================================================
    -- NET 3 — LV, TWO PARALLEL lines on 4 poles (y = 5.600)
    --   A at y=5.60000, B offset to y=5.60004 (~4 m) -- genuinely parallel.
    -- =====================================================================
    INSERT INTO gis.pole (id, name, subtype, status, geom, pole_material, pole_use, height_class, condition_rating) VALUES
        ('dc0b0000-0000-0000-0000-000000000001', 'P-LV-1', st_pole_conc, 'published', ST_SetSRID(ST_MakePoint(-0.2200,5.6000),4326), 'CONCRETE', 'DISTRIBUTION', 'CLASS_1', 'GOOD'),
        ('dc0b0000-0000-0000-0000-000000000002', 'P-LV-2', st_pole_conc, 'published', ST_SetSRID(ST_MakePoint(-0.2180,5.6000),4326), 'CONCRETE', 'DISTRIBUTION', 'CLASS_1', 'GOOD'),
        ('dc0b0000-0000-0000-0000-000000000003', 'P-LV-3', st_pole_conc, 'published', ST_SetSRID(ST_MakePoint(-0.2160,5.6000),4326), 'CONCRETE', 'DISTRIBUTION', 'CLASS_1', 'GOOD'),
        ('dc0b0000-0000-0000-0000-000000000004', 'P-LV-4', st_pole_conc, 'published', ST_SetSRID(ST_MakePoint(-0.2140,5.6000),4326), 'CONCRETE', 'DISTRIBUTION', 'CLASS_1', 'GOOD')
    ON CONFLICT (id) DO NOTHING;

    INSERT INTO gis.lv_conductor (id, name, subtype, status, geom, phase, conductor_spec, lv_role, circuit_ref) VALUES
        ('dc0b0000-0000-0000-0000-0000000000a1', 'LV-FDR-A', st_lv_oh, 'published',
         ST_SetSRID(ST_GeomFromText('LINESTRING(-0.2200 5.60000, -0.2180 5.60000, -0.2160 5.60000, -0.2140 5.60000)'),4326),
         'L1L2L3N', 'WAVEFORM_185', 'main', 'LV-FDR-A'),
        ('dc0b0000-0000-0000-0000-0000000000a2', 'LV-FDR-B', st_lv_oh, 'published',
         ST_SetSRID(ST_GeomFromText('LINESTRING(-0.2200 5.60004, -0.2180 5.60004, -0.2160 5.60004, -0.2140 5.60004)'),4326),
         'L1L2L3N', 'WAVEFORM_185', 'main', 'LV-FDR-B')
    ON CONFLICT (id) DO NOTHING;

    INSERT INTO runtime.feature_relationships (relationship_class_id, origin_feature_class_id, origin_feature_id, destination_feature_class_id, destination_feature_id, established_by)
    SELECT rc_pole_lv, fc_pole, p.id, fc_lv, c.id, 'seed:double_circuit'
    FROM (VALUES
        ('dc0b0000-0000-0000-0000-000000000001'::uuid), ('dc0b0000-0000-0000-0000-000000000002'::uuid),
        ('dc0b0000-0000-0000-0000-000000000003'::uuid), ('dc0b0000-0000-0000-0000-000000000004'::uuid)) AS p(id)
    CROSS JOIN (VALUES
        ('dc0b0000-0000-0000-0000-0000000000a1'::uuid), ('dc0b0000-0000-0000-0000-0000000000a2'::uuid)) AS c(id)
    ON CONFLICT (relationship_class_id, origin_feature_id, destination_feature_id) DO NOTHING;

    -- =====================================================================
    -- NET 4 — 33 kV QUAD CIRCUIT on 3 lattice towers (y = 5.630)
    --   2 circuits LEFT (levels 1,2), 2 circuits RIGHT (levels 1,2). The
    --   arrangement is RECORDED on each support link's attributes (mig 0190),
    --   so the elevation draws the true 2-left/2-right layout.
    -- =====================================================================
    INSERT INTO gis.tower (id, name, subtype, status, geom, tower_material, height_class, condition_rating) VALUES
        ('dcf40000-0000-0000-0000-000000000001', 'TWR-Q-1', st_tower_lattice, 'published', ST_SetSRID(ST_MakePoint(-0.2200,5.6300),4326), 'STEEL_LATTICE', 'CLASS_3', 'GOOD'),
        ('dcf40000-0000-0000-0000-000000000002', 'TWR-Q-2', st_tower_lattice, 'published', ST_SetSRID(ST_MakePoint(-0.2180,5.6300),4326), 'STEEL_LATTICE', 'CLASS_3', 'GOOD'),
        ('dcf40000-0000-0000-0000-000000000003', 'TWR-Q-3', st_tower_lattice, 'published', ST_SetSRID(ST_MakePoint(-0.2160,5.6300),4326), 'STEEL_LATTICE', 'CLASS_3', 'GOOD')
    ON CONFLICT (id) DO NOTHING;

    -- Four 33 kV circuits on the SAME path (coincident) → one drawn line; the
    -- four are distinguished by circuit_ref and by their recorded side/level.
    INSERT INTO gis.mv_conductor (id, name, subtype, status, geom, voltage_class, condition_rating, conductor_spec, circuit_ref) VALUES
        ('dcf40000-0000-0000-0000-0000000000a1', 'FDR-Q-L1', st_mv_oh, 'published',
         ST_SetSRID(ST_GeomFromText('LINESTRING(-0.2200 5.6300, -0.2180 5.6300, -0.2160 5.6300)'),4326),
         '33KV', 'GOOD', 'ACSR_100', 'FDR-Q-L1'),
        ('dcf40000-0000-0000-0000-0000000000a2', 'FDR-Q-L2', st_mv_oh, 'published',
         ST_SetSRID(ST_GeomFromText('LINESTRING(-0.2200 5.6300, -0.2180 5.6300, -0.2160 5.6300)'),4326),
         '33KV', 'GOOD', 'ACSR_100', 'FDR-Q-L2'),
        ('dcf40000-0000-0000-0000-0000000000a3', 'FDR-Q-R1', st_mv_oh, 'published',
         ST_SetSRID(ST_GeomFromText('LINESTRING(-0.2200 5.6300, -0.2180 5.6300, -0.2160 5.6300)'),4326),
         '33KV', 'GOOD', 'ACSR_100', 'FDR-Q-R1'),
        ('dcf40000-0000-0000-0000-0000000000a4', 'FDR-Q-R2', st_mv_oh, 'published',
         ST_SetSRID(ST_GeomFromText('LINESTRING(-0.2200 5.6300, -0.2180 5.6300, -0.2160 5.6300)'),4326),
         '33KV', 'GOOD', 'ACSR_100', 'FDR-Q-R2')
    ON CONFLICT (id) DO NOTHING;

    -- Each tower supports all four circuits; the per-link attributes record WHERE
    -- each circuit hangs (side + crossarm level) — same for a circuit on every
    -- tower, varying per circuit, hence a VALUES list of (conductor, attributes).
    INSERT INTO runtime.feature_relationships (relationship_class_id, origin_feature_class_id, origin_feature_id, destination_feature_class_id, destination_feature_id, established_by, attributes)
    SELECT rc_tower_mv, fc_tower, t.id, fc_mv, c.id, 'seed:quad_circuit', c.attrs::jsonb
    FROM (VALUES
        ('dcf40000-0000-0000-0000-000000000001'::uuid), ('dcf40000-0000-0000-0000-000000000002'::uuid),
        ('dcf40000-0000-0000-0000-000000000003'::uuid)) AS t(id)
    CROSS JOIN (VALUES
        ('dcf40000-0000-0000-0000-0000000000a1'::uuid, '{"side":"L","level":1}'),
        ('dcf40000-0000-0000-0000-0000000000a2'::uuid, '{"side":"L","level":2}'),
        ('dcf40000-0000-0000-0000-0000000000a3'::uuid, '{"side":"R","level":1}'),
        ('dcf40000-0000-0000-0000-0000000000a4'::uuid, '{"side":"R","level":2}')) AS c(id, attrs)
    ON CONFLICT (relationship_class_id, origin_feature_id, destination_feature_id) DO NOTHING;

    -- =====================================================================
    -- NET 5 — MIXED-ORIENTATION POLE (2 poles, y = 5.640)
    --   2 × 11 kV circuits VERTICAL (L & R, level 1) OVER 1 × LV HORIZONTAL
    --   (centre, level 2). orientation is recorded PER circuit, so the
    --   elevation draws MV vertical and LV horizontal on the same structure.
    -- =====================================================================
    INSERT INTO gis.pole (id, name, subtype, status, geom, pole_material, pole_use, height_class, condition_rating) VALUES
        ('dcf50000-0000-0000-0000-000000000001', 'P-U-1', st_pole_conc, 'published', ST_SetSRID(ST_MakePoint(-0.2200,5.6400),4326), 'CONCRETE', 'DISTRIBUTION', 'CLASS_2', 'GOOD'),
        ('dcf50000-0000-0000-0000-000000000002', 'P-U-2', st_pole_conc, 'published', ST_SetSRID(ST_MakePoint(-0.2180,5.6400),4326), 'CONCRETE', 'DISTRIBUTION', 'CLASS_2', 'GOOD')
    ON CONFLICT (id) DO NOTHING;

    -- Two 11 kV circuits (the MV, carried vertical) on the shared path.
    INSERT INTO gis.mv_conductor (id, name, subtype, status, geom, voltage_class, condition_rating, conductor_spec, circuit_ref) VALUES
        ('dcf50000-0000-0000-0000-0000000000a1', 'FDR-U-MA', st_mv_oh, 'published',
         ST_SetSRID(ST_GeomFromText('LINESTRING(-0.2200 5.6400, -0.2180 5.6400)'),4326),
         '11KV', 'GOOD', 'ACSR_100', 'FDR-U-MA'),
        ('dcf50000-0000-0000-0000-0000000000a2', 'FDR-U-MB', st_mv_oh, 'published',
         ST_SetSRID(ST_GeomFromText('LINESTRING(-0.2200 5.6400, -0.2180 5.6400)'),4326),
         '11KV', 'GOOD', 'ACSR_100', 'FDR-U-MB')
    ON CONFLICT (id) DO NOTHING;

    -- One LV circuit (the underbuild, carried horizontal).
    INSERT INTO gis.lv_conductor (id, name, subtype, status, geom, phase, conductor_spec, lv_role, circuit_ref) VALUES
        ('dcf50000-0000-0000-0000-0000000000b1', 'LV-U-A', st_lv_oh, 'published',
         ST_SetSRID(ST_GeomFromText('LINESTRING(-0.2200 5.63996, -0.2180 5.63996)'),4326),
         'L1L2L3N', 'WAVEFORM_185', 'main', 'LV-U-A')
    ON CONFLICT (id) DO NOTHING;

    -- MV links: both poles carry both 11 kV circuits, recorded as VERTICAL at level 1,
    -- one on the left and one on the right.
    INSERT INTO runtime.feature_relationships (relationship_class_id, origin_feature_class_id, origin_feature_id, destination_feature_class_id, destination_feature_id, established_by, attributes)
    SELECT rc_pole_mv, fc_pole, p.id, fc_mv, c.id, 'seed:mixed_pole', c.attrs::jsonb
    FROM (VALUES ('dcf50000-0000-0000-0000-000000000001'::uuid), ('dcf50000-0000-0000-0000-000000000002'::uuid)) AS p(id)
    CROSS JOIN (VALUES
        ('dcf50000-0000-0000-0000-0000000000a1'::uuid, '{"side":"L","level":1,"orientation":"vertical"}'),
        ('dcf50000-0000-0000-0000-0000000000a2'::uuid, '{"side":"R","level":1,"orientation":"vertical"}')) AS c(id, attrs)
    ON CONFLICT (relationship_class_id, origin_feature_id, destination_feature_id) DO NOTHING;

    -- LV link: both poles carry the LV, recorded as HORIZONTAL at level 2 (centre),
    -- i.e. the underbuild below the MV.
    INSERT INTO runtime.feature_relationships (relationship_class_id, origin_feature_class_id, origin_feature_id, destination_feature_class_id, destination_feature_id, established_by, attributes)
    SELECT rc_pole_lv, fc_pole, p.id, fc_lv, c.id, 'seed:mixed_pole', c.attrs::jsonb
    FROM (VALUES ('dcf50000-0000-0000-0000-000000000001'::uuid), ('dcf50000-0000-0000-0000-000000000002'::uuid)) AS p(id)
    CROSS JOIN (VALUES
        ('dcf50000-0000-0000-0000-0000000000b1'::uuid, '{"side":"C","level":2,"orientation":"horizontal"}')) AS c(id, attrs)
    ON CONFLICT (relationship_class_id, origin_feature_id, destination_feature_id) DO NOTHING;

    RAISE NOTICE 'double_circuit_example.sql applied: 5 test networks — 33kV(4 towers+2 ckts), 11kV(5 mixed+2 ckts), LV(4 poles+2 parallel), 33kV QUAD(3 towers+4 ckts, 2L/2R), MIXED pole(2 poles, MV vertical over LV horizontal). Idempotent.';
END $$;
