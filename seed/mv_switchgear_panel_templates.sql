-- seed/mv_switchgear_panel_templates.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives under
-- /seed on purpose, same reasoning as the other seed files. Phase 42 Slice 2:
-- the per-role device recipes behind the composite "place one indoor MV
-- switchgear panel and get its whole standard device set" endpoint
-- (api/internal/panel). Depends on migration 0072 (the panel_templates /
-- panel_template_items tables) and on seed/mv_switchgear_panel_example.sql
-- (Slice 1: MV_SWITCHGEAR_PANEL + its BAY_ROLE-backed panel_role field) plus the
-- device class seeds (mv_switching / mv_measuring / protective_relay / the
-- Slice 1 MV_EARTH_SWITCH) having been applied.
--
-- A template is keyed by (panel class, role). The role codes are the shared
-- BAY_ROLE values (INCOMER / FEEDER / TRANSFORMER / BUS_SECTION / BUS_COUPLER,
-- seed/bay_example.sql) that MV_SWITCHGEAR_PANEL's panel_role field points at.
-- One item = one device the recipe fans out, with the default coded attribute
-- values its required fields need so the created feature passes the same
-- create-time validation a manual create does -- the crew edits the real values
-- (actual CT ratio, relay function, condition) afterward.
--
-- Strawman device set (the agreed Phase 42 lists):
--   INCOMER = 1 breaker / 2 isolators / 1 earth switch / 1 CT / 1 VT / 1 relay
--   FEEDER / TRANSFORMER / BUS_SECTION / BUS_COUPLER = same minus the VT
--     (feeders read the shared bus VT, so no dedicated VT per feeder cubicle).
--
-- offset_x / offset_y position each device in the panel's column on the board
-- mimic (Phase 42 Slice 3 draws them; this slice only stores and returns them).
-- The main column runs top-to-bottom at x=0; the VT and relay sit off to the
-- side at x=60. Values are cosmetic placeholders, not electrical data.
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL has
-- been reviewed statically -- a live psql apply + a build-a-panel call through
-- POST /panels are still owed to call it verified.

-- ---------------------------------------------------------------------------
-- Templates: one row per role, for the MV_SWITCHGEAR_PANEL class. Idempotent on
-- the (panel class, role) unique key.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.panel_templates (panel_feature_class_id, role_value_id, name)
SELECT pfc.id, rv.id, v.name
FROM metadata.feature_classes pfc
JOIN (VALUES
    ('INCOMER',     'Incomer panel'),
    ('FEEDER',      'Feeder panel'),
    ('TRANSFORMER', 'Transformer feeder panel'),
    ('BUS_SECTION', 'Bus section panel'),
    ('BUS_COUPLER', 'Bus coupler panel')
) AS v(role_code, name) ON true
JOIN metadata.domains rd ON rd.code = 'BAY_ROLE'
JOIN metadata.domain_values rv ON rv.domain_id = rd.id AND rv.code = v.role_code
WHERE pfc.code = 'MV_SWITCHGEAR_PANEL'
ON CONFLICT (panel_feature_class_id, role_value_id) DO NOTHING;

-- ---------------------------------------------------------------------------
-- Items: one row per device per role. The (role_code, device_code, subtype,
-- position, offset_x, offset_y) tuples below are joined to their template and
-- device class by code; the default attributes are built per device_code by the
-- CASE, resolving the CT/VT/relay coded placeholders from the first value of
-- their domain so this seed never hardcodes a value code that might not exist.
-- device_state defaults to OPEN (a freshly placed switch is open until
-- commissioned) and condition_rating to GOOD -- both known DEVICE_STATE /
-- CONDITION_RATING values (seed/mv_switching_example.sql). Idempotent on the
-- (template, position) unique key.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.panel_template_items
    (template_id, device_feature_class_id, subtype_code, attributes, position, offset_x, offset_y, rotation)
SELECT pt.id,
       dfc.id,
       v.subtype_code,
       CASE v.device_code
           WHEN 'MV_CT' THEN jsonb_build_object(
               'ct_ratio', (SELECT dv.code FROM metadata.domain_values dv
                            JOIN metadata.domains d ON d.id = dv.domain_id
                            WHERE d.code = 'CT_RATIO' ORDER BY dv.sort_order LIMIT 1),
               'condition_rating', 'GOOD')
           WHEN 'MV_VT' THEN jsonb_build_object(
               'vt_ratio', (SELECT dv.code FROM metadata.domain_values dv
                            JOIN metadata.domains d ON d.id = dv.domain_id
                            WHERE d.code = 'VT_RATIO' ORDER BY dv.sort_order LIMIT 1),
               'condition_rating', 'GOOD')
           WHEN 'PROTECTIVE_RELAY' THEN jsonb_build_object(
               'device_number', (SELECT dv.code FROM metadata.domain_values dv
                                 JOIN metadata.domains d ON d.id = dv.domain_id
                                 WHERE d.code = 'ANSI_DEVICE_NUMBER' ORDER BY dv.sort_order LIMIT 1))
           ELSE jsonb_build_object('device_state', 'OPEN', 'condition_rating', 'GOOD')
       END,
       v.position, v.offset_x, v.offset_y, 0
FROM metadata.feature_classes pfc
JOIN (VALUES
    -- INCOMER -- full set including the dedicated VT.
    ('INCOMER',     'MV_ISOLATOR',      1, 1,  0.0,   0.0),
    ('INCOMER',     'MV_BREAKER',       1, 2,  0.0,  40.0),
    ('INCOMER',     'MV_CT',            2, 3,  0.0,  80.0),
    ('INCOMER',     'MV_ISOLATOR',      1, 4,  0.0, 120.0),
    ('INCOMER',     'MV_EARTH_SWITCH',  1, 5,  0.0, 160.0),
    ('INCOMER',     'MV_VT',            1, 6, 60.0,  80.0),
    ('INCOMER',     'PROTECTIVE_RELAY', 1, 7, 60.0,  40.0),
    -- FEEDER -- same spine, no dedicated VT.
    ('FEEDER',      'MV_ISOLATOR',      1, 1,  0.0,   0.0),
    ('FEEDER',      'MV_BREAKER',       1, 2,  0.0,  40.0),
    ('FEEDER',      'MV_CT',            2, 3,  0.0,  80.0),
    ('FEEDER',      'MV_ISOLATOR',      1, 4,  0.0, 120.0),
    ('FEEDER',      'MV_EARTH_SWITCH',  1, 5,  0.0, 160.0),
    ('FEEDER',      'PROTECTIVE_RELAY', 1, 6, 60.0,  40.0),
    -- TRANSFORMER feeder -- identical to a feeder.
    ('TRANSFORMER', 'MV_ISOLATOR',      1, 1,  0.0,   0.0),
    ('TRANSFORMER', 'MV_BREAKER',       1, 2,  0.0,  40.0),
    ('TRANSFORMER', 'MV_CT',            2, 3,  0.0,  80.0),
    ('TRANSFORMER', 'MV_ISOLATOR',      1, 4,  0.0, 120.0),
    ('TRANSFORMER', 'MV_EARTH_SWITCH',  1, 5,  0.0, 160.0),
    ('TRANSFORMER', 'PROTECTIVE_RELAY', 1, 6, 60.0,  40.0),
    -- BUS_SECTION -- same as a feeder.
    ('BUS_SECTION', 'MV_ISOLATOR',      1, 1,  0.0,   0.0),
    ('BUS_SECTION', 'MV_BREAKER',       1, 2,  0.0,  40.0),
    ('BUS_SECTION', 'MV_CT',            2, 3,  0.0,  80.0),
    ('BUS_SECTION', 'MV_ISOLATOR',      1, 4,  0.0, 120.0),
    ('BUS_SECTION', 'MV_EARTH_SWITCH',  1, 5,  0.0, 160.0),
    ('BUS_SECTION', 'PROTECTIVE_RELAY', 1, 6, 60.0,  40.0),
    -- BUS_COUPLER -- same as a feeder.
    ('BUS_COUPLER', 'MV_ISOLATOR',      1, 1,  0.0,   0.0),
    ('BUS_COUPLER', 'MV_BREAKER',       1, 2,  0.0,  40.0),
    ('BUS_COUPLER', 'MV_CT',            2, 3,  0.0,  80.0),
    ('BUS_COUPLER', 'MV_ISOLATOR',      1, 4,  0.0, 120.0),
    ('BUS_COUPLER', 'MV_EARTH_SWITCH',  1, 5,  0.0, 160.0),
    ('BUS_COUPLER', 'PROTECTIVE_RELAY', 1, 6, 60.0,  40.0)
) AS v(role_code, device_code, subtype_code, position, offset_x, offset_y) ON true
JOIN metadata.domains rd ON rd.code = 'BAY_ROLE'
JOIN metadata.domain_values rv ON rv.domain_id = rd.id AND rv.code = v.role_code
JOIN metadata.panel_templates pt ON pt.panel_feature_class_id = pfc.id AND pt.role_value_id = rv.id
JOIN metadata.feature_classes dfc ON dfc.code = v.device_code
WHERE pfc.code = 'MV_SWITCHGEAR_PANEL'
ON CONFLICT (template_id, position) DO NOTHING;
