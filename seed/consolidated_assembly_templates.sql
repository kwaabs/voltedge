-- seed/consolidated_assembly_templates.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations; lives under /seed
-- like the other seeds. Re-expresses the RMU (star) and packaged-substation
-- (chain) recipes as rows of the GENERIC assembly engine (metadata.assembly_
-- templates / _items, migration 0093), so every "place a point + fan out its
-- contained kit" placement runs through one engine. Replaces seed/rmu_templates
-- .sql and seed/packaged_substation_templates.sql (retired in the consolidation).
--
-- Why these map cleanly: migration 0093 was built to generalise RMU (star, 0073)
-- and packagedsub (chain, 0092). assembly_items expresses both via
-- (support_kind, support_parent_position) for the structural link and
-- (wire_to_position, own_terminal, upstream_terminal) for the electrical leg --
-- own_terminal is THIS item's terminal, upstream_terminal is the TARGET's. The
-- generic fan-out writes a connection From={target,upstream_terminal} ->
-- To={this,own_terminal}; connectivity.matchRuleEitherDirection normalises the
-- direction, so a leg stores identically to the old engines regardless of which
-- end we name first (this is what lets the RMU cable -- conductor as the FROM/
-- upstream side -- be modelled as an ordinary wired item).
--
-- anchor_attributes (migration 0194) carries the config field the old engines
-- stored on the placed unit (package_config / rmu_config). The generic engine
-- merges it UNDER the placing user's attributes, so the recipe supplies the
-- required config without the user re-entering it (and without double-encoding).
--
-- Depends on: migration 0093 (assembly_templates/_items), 0194 (anchor_attributes),
-- 0192 (assembly_ref on every feature table -- the fan-out stamps the anchor), and
-- the component classes already seeded + compiled: PACKAGED_SUBSTATION, RMU,
-- MV_SWITCH, DIST_XFMR, LV_BUSBAR, MV_BUSBAR, MV_CONDUCTOR. Apply after those;
-- re-apply is idempotent. No compile for the templates themselves (pure metadata).

-- ---------------------------------------------------------------------------
-- Component prerequisites: geometry_optional on the geometry-less components
-- (each internal component / external cable is created WITHOUT map geometry).
-- Carried over from the retired engine seeds; BACKWARD-COMPATIBLE (only relaxes).
-- NEEDS a recompile of each to take effect: `task compile CODE=DIST_XFMR`,
-- `task compile CODE=MV_CONDUCTOR`. (LV_BUSBAR / MV_SWITCH / MV_BUSBAR are already
-- relaxed by their own seeds.)
-- ---------------------------------------------------------------------------
UPDATE metadata.feature_classes SET geometry_optional = true
WHERE code = 'DIST_XFMR' AND geometry_optional IS DISTINCT FROM true;
UPDATE metadata.feature_classes SET geometry_optional = true
WHERE code = 'MV_CONDUCTOR' AND geometry_optional IS DISTINCT FROM true;

-- ===========================================================================
-- Packaged substation -- COMPACT chain (MV_SWITCH -> DIST_XFMR -> LV_BUSBAR).
-- Two templates (permanent / mobile) via anchor_subtype_code, mirroring the old
-- (config x tenure-subtype) option cross-product and the PACKED_* convention.
-- ===========================================================================
INSERT INTO metadata.assembly_templates
    (anchor_feature_class_id, code, name, anchor_subtype_code, anchor_attributes)
SELECT afc.id, v.code, v.name, v.subtype, jsonb_build_object('package_config', 'COMPACT')
FROM metadata.feature_classes afc
JOIN (VALUES
    ('COMPACT_PERMANENT', 'Packaged substation — compact (permanent)', 1),
    ('COMPACT_MOBILE',    'Packaged substation — compact (mobile)',    2)
) AS v(code, name, subtype) ON true
WHERE afc.code = 'PACKAGED_SUBSTATION'
ON CONFLICT (anchor_feature_class_id, code) DO NOTHING;

INSERT INTO metadata.assembly_items
    (template_id, component_feature_class_id, subtype_code, attributes,
     support_kind, support_parent_position, wire_to_position, own_terminal, upstream_terminal,
     position, offset_x, offset_y, rotation)
SELECT at.id, comp.id, v.subtype_code,
       CASE v.component_code
         WHEN 'MV_SWITCH' THEN jsonb_build_object('device_state', 'OPEN', 'condition_rating', 'GOOD', 'is_fused', false)
         WHEN 'DIST_XFMR' THEN jsonb_build_object('primary_voltage', '11KV', 'condition_rating', 'GOOD')
         WHEN 'LV_BUSBAR' THEN jsonb_build_object('phase', 'L1L2L3N')
         ELSE '{}'::jsonb
       END,
       'containment', 0, v.wire_to, v.own_terminal, v.upstream_terminal,
       v.position, v.offset_x, v.offset_y, 0
FROM metadata.assembly_templates at
JOIN metadata.feature_classes afc ON afc.id = at.anchor_feature_class_id AND afc.code = 'PACKAGED_SUBSTATION'
JOIN (VALUES
    -- component,    subtype, wire_to,  own,        upstream,    pos, off_x, off_y
    ('MV_SWITCH', 1, NULL::int, NULL::text, NULL::text, 1, 0.0,   0.0),
    ('DIST_XFMR', 2, 1,         'T1',       'T2',       2, 0.0,  80.0),
    ('LV_BUSBAR', 1, 2,         'T1',       'T2',       3, 0.0, 160.0)
) AS v(component_code, subtype_code, wire_to, own_terminal, upstream_terminal, position, offset_x, offset_y) ON true
JOIN metadata.feature_classes comp ON comp.code = v.component_code
WHERE at.code IN ('COMPACT_PERMANENT', 'COMPACT_MOBILE')
ON CONFLICT (template_id, position) DO NOTHING;

-- ===========================================================================
-- RMU -- star: one shared MV_BUSBAR + N switched ways, each way wired to the bus
-- (way.T2) and carrying an external MV_CONDUCTOR cable on its T1. anchor_subtype
-- is left to the placing user (RMU had no tenure cross-product). is_fused marks a
-- transformer (tee) way vs a ring way. Cables are NOT contained (external feeder).
-- ===========================================================================
INSERT INTO metadata.assembly_templates
    (anchor_feature_class_id, code, name, anchor_subtype_code, anchor_attributes)
SELECT afc.id, v.code, v.name, NULL, jsonb_build_object('rmu_config', v.code)
FROM metadata.feature_classes afc
JOIN (VALUES
    ('2W_1T', 'Ring main unit — 2 ring + 1 transformer'),
    ('2W_2T', 'Ring main unit — 2 ring + 2 transformer')
) AS v(code, name) ON true
WHERE afc.code = 'RMU'
ON CONFLICT (anchor_feature_class_id, code) DO NOTHING;

-- Items per config. component / is_fused drive the attributes CASE; support_kind
-- NULL + support_parent NULL = an unlinked (external cable) item. wire_to points
-- at an earlier position: ways -> the bus (pos 1); cables -> their way.
INSERT INTO metadata.assembly_items
    (template_id, component_feature_class_id, subtype_code, attributes,
     support_kind, support_parent_position, wire_to_position, own_terminal, upstream_terminal,
     position, offset_x, offset_y, rotation)
SELECT at.id, comp.id, v.subtype_code,
       CASE v.component_code
         WHEN 'MV_SWITCH'    THEN jsonb_build_object('device_state', 'OPEN', 'condition_rating', 'GOOD', 'is_fused', v.is_fused)
         WHEN 'MV_CONDUCTOR' THEN jsonb_build_object('voltage_class', '11KV', 'condition_rating', 'GOOD')
         ELSE '{}'::jsonb
       END,
       v.support_kind, v.support_parent, v.wire_to, v.own_terminal, v.upstream_terminal,
       v.position, v.offset_x, v.offset_y, 0
FROM metadata.assembly_templates at
JOIN metadata.feature_classes afc ON afc.id = at.anchor_feature_class_id AND afc.code = 'RMU'
JOIN (VALUES
    -- config,  component,     subtype,  is_fused, support_kind,    s_parent, wire_to, own,        upstream,    pos, off_x,  off_y
    ('2W_1T', 'MV_BUSBAR',    NULL::int, false, 'containment',      0,        NULL::int, NULL::text, NULL::text, 1,  60.0, -40.0),
    ('2W_1T', 'MV_SWITCH',    1,         false, 'containment',      0,        1,         'T2',       NULL,       2,   0.0,   0.0),
    ('2W_1T', 'MV_SWITCH',    1,         false, 'containment',      0,        1,         'T2',       NULL,       3, 120.0,   0.0),
    ('2W_1T', 'MV_SWITCH',    1,         true,  'containment',      0,        1,         'T2',       NULL,       4,  60.0,  60.0),
    ('2W_1T', 'MV_CONDUCTOR', 2,         false, NULL,               NULL,     2,         NULL,       'T1',       5,   0.0, -90.0),
    ('2W_1T', 'MV_CONDUCTOR', 2,         false, NULL,               NULL,     3,         NULL,       'T1',       6, 120.0, -90.0),
    ('2W_1T', 'MV_CONDUCTOR', 2,         false, NULL,               NULL,     4,         NULL,       'T1',       7,  60.0, 110.0),
    ('2W_2T', 'MV_BUSBAR',    NULL,      false, 'containment',      0,        NULL,      NULL,       NULL,       1,  90.0, -40.0),
    ('2W_2T', 'MV_SWITCH',    1,         false, 'containment',      0,        1,         'T2',       NULL,       2,   0.0,   0.0),
    ('2W_2T', 'MV_SWITCH',    1,         false, 'containment',      0,        1,         'T2',       NULL,       3, 180.0,   0.0),
    ('2W_2T', 'MV_SWITCH',    1,         true,  'containment',      0,        1,         'T2',       NULL,       4,  60.0,  60.0),
    ('2W_2T', 'MV_SWITCH',    1,         true,  'containment',      0,        1,         'T2',       NULL,       5, 120.0,  60.0),
    ('2W_2T', 'MV_CONDUCTOR', 2,         false, NULL,               NULL,     2,         NULL,       'T1',       6,   0.0, -90.0),
    ('2W_2T', 'MV_CONDUCTOR', 2,         false, NULL,               NULL,     3,         NULL,       'T1',       7, 180.0, -90.0),
    ('2W_2T', 'MV_CONDUCTOR', 2,         false, NULL,               NULL,     4,         NULL,       'T1',       8,  60.0, 110.0),
    ('2W_2T', 'MV_CONDUCTOR', 2,         false, NULL,               NULL,     5,         NULL,       'T1',       9, 120.0, 110.0)
) AS v(config_code, component_code, subtype_code, is_fused, support_kind, support_parent, wire_to, own_terminal, upstream_terminal, position, offset_x, offset_y) ON true
JOIN metadata.feature_classes comp ON comp.code = v.component_code
WHERE at.code = v.config_code
ON CONFLICT (template_id, position) DO NOTHING;
