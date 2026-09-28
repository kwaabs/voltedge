-- seed/lv_pillar_templates.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives under
-- /seed on purpose, same reasoning as seed/pad_mount_urd.sql, which this file
-- follows. Phase 9 Slice 7 -- the LV feeder-pillar kits for the generic assembly
-- engine (api/internal/assembly, migration 0093). Places one LV_PILLAR and gets
-- its whole internals: one internal LV_BUSBAR plus N fused outgoing LV_FUSE ways,
-- all contained in the pillar and each way wired to the bus. The LV sibling of
-- seed/rmu_templates.sql (which fans an RMU out to MV_SWITCH ways on an MV_BUSBAR).
--
-- Uses only the classes seed/lv_pillar_example.sql adds (LV_PILLAR, LV_FUSE) plus
-- the existing LV_BUSBAR. Two sizes, each a config the operator picks at
-- placement (POST /assemblies Config, surfaced by ListAssemblyOptions):
--   WAYS_4 -- 4-way pillar: 1x LV_BUSBAR + 4x LV_FUSE
--   WAYS_6 -- 6-way pillar: 1x LV_BUSBAR + 6x LV_FUSE
--
-- Kit shape (per config), position order:
--   pos 1        LV_BUSBAR -- the internal bar. CONTAINED by the pillar (anchor).
--                Fed EXTERNALLY (wire_to_position NULL): its T1 incoming is wired
--                to the incoming LV main by the operator after placement, exactly
--                as an RMU way's external T1 is. attributes phase = L1L2L3N (a
--                3-phase distribution board; phase is LV_BUSBAR's required field).
--   pos 2..N+1   LV_FUSE   -- each fused way. CONTAINED by the pillar, and WIRED
--                to the bus (wire_to_position 1): own_terminal T1 (fuse bus side)
--                -> upstream_terminal T2 (the bus's fan-out bar), matching the
--                'LV_BUSBAR T2 -> LV_FUSE T1' rule. Born OPEN (device_state OPEN),
--                so each feeder is commissioned way-by-way, like the RMU/URD.
--
-- The anchor is forced to LV_PILLAR subtype 1 (standard) by anchor_subtype_code.
--
-- Apply with psql after migration 0093 and seed/lv_pillar_example.sql:
--   task seed-lv-pillar-templates
-- No compile step -- pure metadata rows.

-- ===========================================================================
-- WAYS_4 -- 4-way feeder pillar.
-- ===========================================================================
INSERT INTO metadata.assembly_templates (anchor_feature_class_id, code, name, anchor_subtype_code)
SELECT fc.id, 'WAYS_4', 'Feeder pillar — 4-way', 1
FROM metadata.feature_classes fc
WHERE fc.code = 'LV_PILLAR'
ON CONFLICT (anchor_feature_class_id, code) DO NOTHING;

INSERT INTO metadata.assembly_items
    (template_id, component_feature_class_id, subtype_code, attributes, support_kind, support_parent_position, wire_to_position, own_terminal, upstream_terminal, position, offset_x, offset_y, rotation)
SELECT t.id, cfc.id, v.subtype_code, v.attributes::jsonb, v.support_kind, v.support_parent_position, v.wire_to_position, v.own_terminal, v.upstream_terminal, v.position, v.offset_x, v.offset_y, v.rotation
FROM (VALUES
    (1, 'LV_BUSBAR', 1, '{"phase":"L1L2L3N"}',                                             'containment', NULL::int, NULL::int, NULL::text, NULL::text, 0.0,  0.0, 0.0),
    (2, 'LV_FUSE',   1, '{"device_state":"OPEN","condition_rating":"GOOD","fuse_rating":"100A"}', 'containment', NULL::int, 1,         'T1',        'T2',        -3.0, -2.0, 0.0),
    (3, 'LV_FUSE',   1, '{"device_state":"OPEN","condition_rating":"GOOD","fuse_rating":"100A"}', 'containment', NULL::int, 1,         'T1',        'T2',        -1.0, -2.0, 0.0),
    (4, 'LV_FUSE',   1, '{"device_state":"OPEN","condition_rating":"GOOD","fuse_rating":"100A"}', 'containment', NULL::int, 1,         'T1',        'T2',         1.0, -2.0, 0.0),
    (5, 'LV_FUSE',   1, '{"device_state":"OPEN","condition_rating":"GOOD","fuse_rating":"100A"}', 'containment', NULL::int, 1,         'T1',        'T2',         3.0, -2.0, 0.0)
) AS v(position, component_code, subtype_code, attributes, support_kind, support_parent_position, wire_to_position, own_terminal, upstream_terminal, offset_x, offset_y, rotation)
JOIN metadata.feature_classes cfc ON cfc.code = v.component_code
JOIN metadata.assembly_templates t ON t.code = 'WAYS_4'
  AND t.anchor_feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'LV_PILLAR')
ON CONFLICT (template_id, position) DO NOTHING;

-- ===========================================================================
-- WAYS_6 -- 6-way feeder pillar.
-- ===========================================================================
INSERT INTO metadata.assembly_templates (anchor_feature_class_id, code, name, anchor_subtype_code)
SELECT fc.id, 'WAYS_6', 'Feeder pillar — 6-way', 1
FROM metadata.feature_classes fc
WHERE fc.code = 'LV_PILLAR'
ON CONFLICT (anchor_feature_class_id, code) DO NOTHING;

INSERT INTO metadata.assembly_items
    (template_id, component_feature_class_id, subtype_code, attributes, support_kind, support_parent_position, wire_to_position, own_terminal, upstream_terminal, position, offset_x, offset_y, rotation)
SELECT t.id, cfc.id, v.subtype_code, v.attributes::jsonb, v.support_kind, v.support_parent_position, v.wire_to_position, v.own_terminal, v.upstream_terminal, v.position, v.offset_x, v.offset_y, v.rotation
FROM (VALUES
    (1, 'LV_BUSBAR', 1, '{"phase":"L1L2L3N"}',                                             'containment', NULL::int, NULL::int, NULL::text, NULL::text, 0.0,  0.0, 0.0),
    (2, 'LV_FUSE',   1, '{"device_state":"OPEN","condition_rating":"GOOD","fuse_rating":"100A"}', 'containment', NULL::int, 1,         'T1',        'T2',        -5.0, -2.0, 0.0),
    (3, 'LV_FUSE',   1, '{"device_state":"OPEN","condition_rating":"GOOD","fuse_rating":"100A"}', 'containment', NULL::int, 1,         'T1',        'T2',        -3.0, -2.0, 0.0),
    (4, 'LV_FUSE',   1, '{"device_state":"OPEN","condition_rating":"GOOD","fuse_rating":"100A"}', 'containment', NULL::int, 1,         'T1',        'T2',        -1.0, -2.0, 0.0),
    (5, 'LV_FUSE',   1, '{"device_state":"OPEN","condition_rating":"GOOD","fuse_rating":"100A"}', 'containment', NULL::int, 1,         'T1',        'T2',         1.0, -2.0, 0.0),
    (6, 'LV_FUSE',   1, '{"device_state":"OPEN","condition_rating":"GOOD","fuse_rating":"100A"}', 'containment', NULL::int, 1,         'T1',        'T2',         3.0, -2.0, 0.0),
    (7, 'LV_FUSE',   1, '{"device_state":"OPEN","condition_rating":"GOOD","fuse_rating":"100A"}', 'containment', NULL::int, 1,         'T1',        'T2',         5.0, -2.0, 0.0)
) AS v(position, component_code, subtype_code, attributes, support_kind, support_parent_position, wire_to_position, own_terminal, upstream_terminal, offset_x, offset_y, rotation)
JOIN metadata.feature_classes cfc ON cfc.code = v.component_code
JOIN metadata.assembly_templates t ON t.code = 'WAYS_6'
  AND t.anchor_feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'LV_PILLAR')
ON CONFLICT (template_id, position) DO NOTHING;
