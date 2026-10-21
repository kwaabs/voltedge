-- seed/packed_station_assembly.sql
-- Purpose: NOT a migration -- lives under /seed (apply.sh only scans /migrations),
-- same convention as the other seed files. Defines the NESTED "packed station"
-- assembly on the existing PACKAGED_SUBSTATION class, driven by the GENERAL
-- assembly engine (api/internal/assembly, metadata.assembly_templates /
-- metadata.assembly_items). A packed station is a point-placed enclosure that
-- fans out its whole pre-wired kit in one step.
--
-- TWO configs, each offered as a FIXED (subtype 1 permanent) and a MOBILE
-- (subtype 2 relocatable) variant -- four template rows in all:
--   A. RMU + distribution transformer + LV pillar   (PACKED_RMU_*)
--   B. distribution transformer + LV pillar          (PACKED_XFMR_*)
--
-- NESTED, not flat: the RMU and the LV pillar are REAL features that contain
-- their own internals, built inline in one flat template using the assembly
-- engine's ability to contain an item under an EARLIER item
-- (support_parent_position) and wire an item to an earlier item
-- (wire_to_position). So the containment tree is:
--   PACKAGED_SUBSTATION (the one point with map geometry)
--   |- RMU                         (config A only; contained by the station)
--   |  |- MV_BUSBAR                (the RMU's internal bus; ways converge on it)
--   |  |- MV_SWITCH x3             (2 ring ways + 1 transformer tee; each T2 -> bus)
--   |- DIST_XFMR                   (contained by the station; HV from the tee, LV to the pillar)
--   |- LV_PILLAR                   (contained by the station)
--      |- LV_BUSBAR               (fed from the transformer LV)
--      |- LV_FUSE x4              (the outgoing LV ways)
-- Only the station carries geometry + territory; every kit feature is created
-- geometry-less (geometry_optional) and travels with the station, so moving the
-- one point moves the whole unit -- which is exactly what a mobile unit needs.
--
-- Coexistence: PACKAGED_SUBSTATION also has the older CHAIN recipe (COMPACT) on
-- the SEPARATE packagedsub engine (seed/packaged_substation_templates.sql). That
-- is a different table set with its own unique keys -- no conflict. The four
-- configs here surface in the general Assemblies menu automatically
-- (assembly/options.go: the only gate is "a template row exists").
--
-- Depends on: seed/packaged_substation_example.sql (the class + permanent/mobile
-- subtypes), seed/rmu_example.sql (RMU + its containment legs + MV_SWITCH T2 ->
-- MV_BUSBAR), seed/connectivity_example.sql (DIST_XFMR), seed/mv_lv_switch_example
-- .sql (MV_SWITCH), seed/mv_busbar_example.sql (MV_BUSBAR), seed/lv_pillar_example
-- .sql (LV_PILLAR/LV_FUSE + their legs), seed/lv_metering_example.sql (LV_BUSBAR),
-- and migration 0093 (assembly_templates/_items). All of those must be applied
-- and the component classes compiled. This seed needs NO recompile -- it only adds
-- metadata rows (relationship classes, one network rule, templates + items).
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL is
-- reviewed statically -- a live psql apply + a build-a-unit call through
-- POST /assemblies are still owed to call it verified.

-- ---------------------------------------------------------------------------
-- 1. Containment relationship classes the nested tree needs that don't yet
--    exist. PACKAGED_SUBSTATION already contains DIST_XFMR (and MV_SWITCH,
--    LV_BUSBAR) from its own seed; RMU already contains MV_BUSBAR/MV_SWITCH and
--    LV_PILLAR already contains LV_BUSBAR/LV_FUSE from theirs. The missing legs
--    are the station owning the RMU and the LV pillar as whole sub-units.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.relationship_classes (name, origin_feature_class_id, destination_feature_class_id, kind, cardinality)
SELECT 'PACKAGED_SUBSTATION contains ' || child.code, pkg.id, child.id, 'containment', 'one_to_many'
FROM metadata.feature_classes pkg
JOIN metadata.feature_classes child ON child.code IN ('RMU', 'LV_PILLAR')
WHERE pkg.code = 'PACKAGED_SUBSTATION'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.relationship_classes rc
    WHERE rc.origin_feature_class_id = pkg.id AND rc.destination_feature_class_id = child.id AND rc.kind = 'containment'
  );

-- ---------------------------------------------------------------------------
-- 2. The ONE internal network leg that doesn't yet exist: the RMU transformer
--    TEE way feeds the transformer from its cable side (T1), not its bus side
--    (T2, which goes to the internal bus). Every other leg already ships:
--      MV_SWITCH.T2 -> MV_BUSBAR      (rmu_example.sql, the ways -> bus)
--      DIST_XFMR.T2 -> LV_BUSBAR.T1   (packaged_substation_example.sql)
--      LV_BUSBAR.T2 -> LV_FUSE.T1     (lv_pillar_example.sql)
--    phase_rule NULL -- an internal factory leg is authorized structurally, like
--    the others. Establishment matches a rule in either direction, so one suffices.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.network_rules (name, from_feature_class_id, from_terminal_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'MV_SWITCH T1 -> DIST_XFMR T1', sw.id, swt1.id, xf.id, xft1.id, NULL, true
FROM metadata.feature_classes sw
JOIN metadata.feature_classes xf ON xf.code = 'DIST_XFMR'
JOIN metadata.terminals swt1 ON swt1.feature_class_id = sw.id AND swt1.code = 'T1'
JOIN metadata.terminals xft1 ON xft1.feature_class_id = xf.id AND xft1.code = 'T1'
WHERE sw.code = 'MV_SWITCH'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules nr
    WHERE nr.from_feature_class_id = sw.id AND nr.from_terminal_id = swt1.id
      AND nr.to_feature_class_id = xf.id AND nr.to_terminal_id = xft1.id
  );

-- ---------------------------------------------------------------------------
-- 3. The four template rows (config x tenure). anchor_subtype_code pins the
--    enclosure subtype (1 permanent / 2 mobile) so each is a distinct menu entry.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.assembly_templates (anchor_feature_class_id, code, name, anchor_subtype_code)
SELECT fc.id, v.code, v.name, v.anchor_subtype_code
FROM metadata.feature_classes fc
JOIN (VALUES
    ('PACKED_RMU_FIXED',   'Packed station — RMU + transformer + pillar (fixed)',  1),
    ('PACKED_RMU_MOBILE',  'Packed station — RMU + transformer + pillar (mobile)', 2),
    ('PACKED_XFMR_FIXED',  'Packed station — transformer + pillar (fixed)',        1),
    ('PACKED_XFMR_MOBILE', 'Packed station — transformer + pillar (mobile)',       2)
) AS v(code, name, anchor_subtype_code) ON true
WHERE fc.code = 'PACKAGED_SUBSTATION'
ON CONFLICT (anchor_feature_class_id, code) DO NOTHING;

-- ---------------------------------------------------------------------------
-- 4. Config A items (RMU + transformer + pillar). Inserted into BOTH the fixed
--    and mobile templates (they share the same kit; only the enclosure subtype
--    differs). position = creation + wiring order; support_parent_position 0 =
--    the station anchor, >=1 = an earlier item; wire_to_position 0 = anchor,
--    >=1 = an earlier item, NULL = externally fed; own/upstream are the two ends
--    of the leg; offsets are cosmetic single-line layout. Component attributes are
--    the required-field minimums (the crew edits real values afterward).
--      ways -> bus:   own T2, upstream NULL (the bus is a terminal-less common node)
--      tee -> xfmr:   xfmr own T1 <- tee T1  (the new leg above)
--      xfmr -> board: board own T1 <- xfmr T2
--      board -> fuse: fuse own T1 <- board T2
-- ---------------------------------------------------------------------------
INSERT INTO metadata.assembly_items
    (template_id, component_feature_class_id, subtype_code, attributes, support_kind, support_parent_position, wire_to_position, own_terminal, upstream_terminal, position, offset_x, offset_y, rotation)
SELECT t.id, cfc.id, v.subtype_code, v.attributes::jsonb, v.support_kind, v.support_parent_position, v.wire_to_position, v.own_terminal, v.upstream_terminal, v.position, v.offset_x, v.offset_y, v.rotation
FROM (VALUES
    --  pos  component     subtype     attributes(jsonb)                                                      support_kind   parent  wire_to    own         upstream    off_x   off_y   rot
    ( 1, 'RMU',       1,         '{"rmu_config":"2W_1T"}',                                               'containment', 0,      NULL::int, NULL::text, NULL::text,   0.0,   0.0, 0.0),
    ( 2, 'MV_BUSBAR', NULL::int, '{}',                                                                   'containment', 1,      NULL::int, NULL::text, NULL::text,   0.0,  40.0, 0.0),
    ( 3, 'MV_SWITCH', 1,         '{"device_state":"OPEN","condition_rating":"GOOD","is_fused":false}',   'containment', 1,      2,         'T2',       NULL::text, -40.0,  20.0, 0.0),
    ( 4, 'MV_SWITCH', 1,         '{"device_state":"OPEN","condition_rating":"GOOD","is_fused":false}',   'containment', 1,      2,         'T2',       NULL::text,  40.0,  20.0, 0.0),
    ( 5, 'MV_SWITCH', 1,         '{"device_state":"OPEN","condition_rating":"GOOD","is_fused":true}',    'containment', 1,      2,         'T2',       NULL::text,   0.0,  70.0, 0.0),
    ( 6, 'DIST_XFMR', 2,         '{"primary_voltage":"11KV","condition_rating":"GOOD"}',                 'containment', 0,      5,         'T1',       'T1',         0.0, 110.0, 0.0),
    ( 7, 'LV_PILLAR', 1,         '{}',                                                                   'containment', 0,      NULL::int, NULL::text, NULL::text,   0.0, 170.0, 0.0),
    ( 8, 'LV_BUSBAR', 1,         '{"phase":"L1L2L3N"}',                                                  'containment', 7,      6,         'T1',       'T2',         0.0, 210.0, 0.0),
    ( 9, 'LV_FUSE',   1,         '{"device_state":"OPEN","condition_rating":"GOOD","fuse_rating":"100A"}', 'containment', 7,    8,         'T1',       'T2',       -60.0, 250.0, 0.0),
    (10, 'LV_FUSE',   1,         '{"device_state":"OPEN","condition_rating":"GOOD","fuse_rating":"100A"}', 'containment', 7,    8,         'T1',       'T2',       -20.0, 250.0, 0.0),
    (11, 'LV_FUSE',   1,         '{"device_state":"OPEN","condition_rating":"GOOD","fuse_rating":"100A"}', 'containment', 7,    8,         'T1',       'T2',        20.0, 250.0, 0.0),
    (12, 'LV_FUSE',   1,         '{"device_state":"OPEN","condition_rating":"GOOD","fuse_rating":"100A"}', 'containment', 7,    8,         'T1',       'T2',        60.0, 250.0, 0.0)
) AS v(position, component_code, subtype_code, attributes, support_kind, support_parent_position, wire_to_position, own_terminal, upstream_terminal, offset_x, offset_y, rotation)
JOIN metadata.feature_classes cfc ON cfc.code = v.component_code
JOIN metadata.assembly_templates t
  ON t.code IN ('PACKED_RMU_FIXED', 'PACKED_RMU_MOBILE')
  AND t.anchor_feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'PACKAGED_SUBSTATION')
ON CONFLICT (template_id, position) DO NOTHING;

-- ---------------------------------------------------------------------------
-- 5. Config B items (transformer + pillar, no RMU). The transformer's HV is
--    externally fed (wire_to NULL) -- the crew connects the incoming MV at
--    install. Same board + fuses as config A.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.assembly_items
    (template_id, component_feature_class_id, subtype_code, attributes, support_kind, support_parent_position, wire_to_position, own_terminal, upstream_terminal, position, offset_x, offset_y, rotation)
SELECT t.id, cfc.id, v.subtype_code, v.attributes::jsonb, v.support_kind, v.support_parent_position, v.wire_to_position, v.own_terminal, v.upstream_terminal, v.position, v.offset_x, v.offset_y, v.rotation
FROM (VALUES
    ( 1, 'DIST_XFMR', 2,         '{"primary_voltage":"11KV","condition_rating":"GOOD"}',                 'containment', 0,      NULL::int, NULL::text, NULL::text,   0.0,   0.0, 0.0),
    ( 2, 'LV_PILLAR', 1,         '{}',                                                                   'containment', 0,      NULL::int, NULL::text, NULL::text,   0.0,  80.0, 0.0),
    ( 3, 'LV_BUSBAR', 1,         '{"phase":"L1L2L3N"}',                                                  'containment', 2,      1,         'T1',       'T2',         0.0, 120.0, 0.0),
    ( 4, 'LV_FUSE',   1,         '{"device_state":"OPEN","condition_rating":"GOOD","fuse_rating":"100A"}', 'containment', 2,    3,         'T1',       'T2',       -60.0, 160.0, 0.0),
    ( 5, 'LV_FUSE',   1,         '{"device_state":"OPEN","condition_rating":"GOOD","fuse_rating":"100A"}', 'containment', 2,    3,         'T1',       'T2',       -20.0, 160.0, 0.0),
    ( 6, 'LV_FUSE',   1,         '{"device_state":"OPEN","condition_rating":"GOOD","fuse_rating":"100A"}', 'containment', 2,    3,         'T1',       'T2',        20.0, 160.0, 0.0),
    ( 7, 'LV_FUSE',   1,         '{"device_state":"OPEN","condition_rating":"GOOD","fuse_rating":"100A"}', 'containment', 2,    3,         'T1',       'T2',        60.0, 160.0, 0.0)
) AS v(position, component_code, subtype_code, attributes, support_kind, support_parent_position, wire_to_position, own_terminal, upstream_terminal, offset_x, offset_y, rotation)
JOIN metadata.feature_classes cfc ON cfc.code = v.component_code
JOIN metadata.assembly_templates t
  ON t.code IN ('PACKED_XFMR_FIXED', 'PACKED_XFMR_MOBILE')
  AND t.anchor_feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'PACKAGED_SUBSTATION')
ON CONFLICT (template_id, position) DO NOTHING;

DO $$
BEGIN
    RAISE NOTICE 'seed/packed_station_assembly.sql applied: + PACKAGED_SUBSTATION contains RMU/LV_PILLAR, + MV_SWITCH T1 -> DIST_XFMR T1 rule, + 4 nested packed-station assembly templates (PACKED_RMU_FIXED/MOBILE, PACKED_XFMR_FIXED/MOBILE). No recompile needed. Place via the Assemblies menu -> Packaged Substation.';
END $$;
