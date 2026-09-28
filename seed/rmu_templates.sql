-- seed/rmu_templates.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives under
-- /seed on purpose, same reasoning as the other seed files. Phase RMU Slice 2:
-- the per-configuration way recipes behind the composite "place one Ring Main
-- Unit and get its whole assembly of ways + internal bus" endpoint
-- (api/internal/rmu). Depends on migration 0073 (the rmu_templates /
-- rmu_template_items tables), on seed/rmu_example.sql (Slice 1: the RMU class +
-- the RMU_CONFIG-backed rmu_config field), on seed/mv_lv_switch_example.sql
-- (MV_SWITCH -- each switched way is a plain MV_SWITCH) and on
-- seed/mv_busbar_example.sql (MV_BUSBAR -- the internal bus each template names)
-- having been applied.
--
-- A template is keyed by (RMU class, config). The config codes are the
-- RMU_CONFIG values (2W_1T, 2W_2T, seed/rmu_example.sql) that RMU's rmu_config
-- field points at. The parent row also names the ONE internal bus class to
-- create (MV_BUSBAR -- the RMU's internal bus is a plain MV bus) -- every config
-- has exactly one bus. One item = one
-- switched way the recipe fans out, with the default coded attribute values its
-- required fields need so the created way passes the same create-time validation
-- a manual create does; the crew edits the real values (actual state, condition)
-- afterward. bus_terminal names the way terminal wired to the bus (T2).
--
-- The way sets:
--   2W_1T = 2 ring ways + 1 transformer (tee) way    (the common unit)
--   2W_2T = 2 ring ways + 2 transformer (tee) ways    (a double-tee unit)
--
-- offset_x / offset_y position each way on the future RMU mimic (Slice 3 draws
-- them; this slice only stores and returns them). The two ring ways sit at the
-- ends, tee ways hang below the bus. Values are cosmetic placeholders, not
-- electrical data. device_state defaults to OPEN (a freshly placed way is open
-- until commissioned) and condition_rating to GOOD -- both known DEVICE_STATE /
-- CONDITION_RATING values (seed/rmu_example.sql).
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL has
-- been reviewed statically -- a live psql apply + a build-an-RMU call through
-- POST /rmus are still owed to call it verified.

-- ---------------------------------------------------------------------------
-- Templates: one row per config, for the RMU class, naming MV_BUSBAR as the
-- internal bus. Idempotent on the (RMU class, config) unique key.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.rmu_templates (rmu_feature_class_id, config_value_id, busbar_feature_class_id, name)
SELECT rfc.id, cv.id, bus.id, v.name
FROM metadata.feature_classes rfc
JOIN (VALUES
    ('2W_1T', '2 ring + 1 transformer RMU'),
    ('2W_2T', '2 ring + 2 transformer RMU')
) AS v(config_code, name) ON true
JOIN metadata.domains cd ON cd.code = 'RMU_CONFIG'
JOIN metadata.domain_values cv ON cv.domain_id = cd.id AND cv.code = v.config_code
JOIN metadata.feature_classes bus ON bus.code = 'MV_BUSBAR'
WHERE rfc.code = 'RMU'
ON CONFLICT (rmu_feature_class_id, config_value_id) DO NOTHING;

-- ---------------------------------------------------------------------------
-- Items: one row per way per config. The (config_code, kind, position,
-- offset_x, offset_y) tuples below are joined to their template by config code;
-- the way class is always MV_SWITCH, subtype_code always 1 (MV_SWITCH's single
-- 'standard' subtype), its bus-side terminal always T2, and its default
-- attributes are device_state=OPEN + condition_rating=GOOD + is_fused. The
-- ring-vs-transformer distinction is carried by is_fused: a tee (transformer)
-- way is a fused switch protecting the distribution transformer (is_fused=true),
-- a ring way is not (is_fused=false). kind below is that flag (0 = ring,
-- 1 = transformer).
--
-- Cable side (migration 0123): every way also names the EXTERNAL cable conductor
-- the fan-out creates on its cable terminal (T1) -- promoting the bare, unwired
-- T1 into a real MV_CONDUCTOR feature. cable_feature_class_id = MV_CONDUCTOR,
-- cable_subtype_code = 2 (underground -- RMU ways are cabled, not overhead),
-- cable_terminal = 'T1', and cable_attributes carries MV_CONDUCTOR's two required
-- fields (voltage_class + condition_rating, seed/connectivity_example.sql) so the
-- geometry-less create passes validation. This requires MV_CONDUCTOR to be
-- geometry_optional AND recompiled first (seed/mv_conductor_diagram_promote.sql
-- + `task compile CODE=MV_CONDUCTOR`) so gis.mv_conductor.geom drops NOT NULL.
--
-- Idempotent on the (template, position) unique key, and DO UPDATE on the cable
-- columns so a re-apply over rows seeded before 0123 promotes them to carry the
-- cable (the non-cable columns are already correct, so only the cable set moves).
-- ---------------------------------------------------------------------------
INSERT INTO metadata.rmu_template_items
    (template_id, way_feature_class_id, subtype_code, attributes, bus_terminal, position, offset_x, offset_y, rotation,
     cable_feature_class_id, cable_subtype_code, cable_attributes, cable_terminal)
SELECT pt.id,
       way.id,
       1,
       jsonb_build_object('device_state', 'OPEN', 'condition_rating', 'GOOD',
                          'is_fused', (v.kind = 1)),
       'T2',
       v.position, v.offset_x, v.offset_y, 0,
       cond.id,
       2,
       jsonb_build_object('voltage_class', '11KV', 'condition_rating', 'GOOD'),
       'T1'
FROM metadata.feature_classes rfc
JOIN (VALUES
    -- 2W_1T -- two ring ways at the ends, one tee way below the bus.
    ('2W_1T', 0, 1,   0.0,   0.0),   -- ring way (incoming)
    ('2W_1T', 0, 2, 120.0,   0.0),   -- ring way (outgoing)
    ('2W_1T', 1, 3,  60.0,  60.0),   -- transformer (tee) way
    -- 2W_2T -- two ring ways at the ends, two tee ways below the bus.
    ('2W_2T', 0, 1,   0.0,   0.0),   -- ring way (incoming)
    ('2W_2T', 0, 2, 180.0,   0.0),   -- ring way (outgoing)
    ('2W_2T', 1, 3,  60.0,  60.0),   -- transformer (tee) way 1
    ('2W_2T', 1, 4, 120.0,  60.0)    -- transformer (tee) way 2
) AS v(config_code, kind, position, offset_x, offset_y) ON true
JOIN metadata.domains cd ON cd.code = 'RMU_CONFIG'
JOIN metadata.domain_values cv ON cv.domain_id = cd.id AND cv.code = v.config_code
JOIN metadata.rmu_templates pt ON pt.rmu_feature_class_id = rfc.id AND pt.config_value_id = cv.id
JOIN metadata.feature_classes way ON way.code = 'MV_SWITCH'
JOIN metadata.feature_classes cond ON cond.code = 'MV_CONDUCTOR'
WHERE rfc.code = 'RMU'
ON CONFLICT (template_id, position) DO UPDATE SET
    cable_feature_class_id = EXCLUDED.cable_feature_class_id,
    cable_subtype_code     = EXCLUDED.cable_subtype_code,
    cable_attributes       = EXCLUDED.cable_attributes,
    cable_terminal         = EXCLUDED.cable_terminal;
