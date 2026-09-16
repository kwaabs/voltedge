-- seed/overhead_pole_transformer.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives under
-- /seed on purpose, same reasoning as the other seed files. Phase 57 -- the
-- POLE_MOUNT_STD kit for the general assembly engine (api/internal/assembly,
-- migration 0093). Models a pole-mounted OVERHEAD distribution transformer as an
-- ASSEMBLY: place one POLE and get its pole-top kit, spatially supported by the
-- pole and internally wired.
--
-- Uses only EXISTING classes -- POLE (seed/pole_example.sql), MV_FUSE / MV_ARRESTER
-- (seed/mv_protection_example.sql), DIST_XFMR (seed/connectivity_example.sql),
-- LV_BUSBAR (seed/lv_metering_example.sql). This seed adds NO class; it adds the
-- structural relationship classes, the two new internal network legs, and the
-- template recipe.
--
-- Kit (creation/wiring order):
--   pos 1 MV_FUSE     drop-out cutout -- fed EXTERNALLY (MV line -> T1, already
--                     permitted); no internal wire.
--   pos 2 DIST_XFMR   pole-mounted    -- MV_FUSE.T2 -> DIST_XFMR.T1 (leg added here).
--   pos 3 MV_ARRESTER shunt tap       -- DIST_XFMR.T1 -> MV_ARRESTER.T1 (leg added).
--   pos 4 LV_BUSBAR   LV board        -- DIST_XFMR.T2 -> LV_BUSBAR.T1 (leg already
--                     ships from seed/packaged_substation_example.sql; idempotent).
-- Every item is spatial_support-held by the pole (one_to_many). So a DOWNSTREAM
-- trace walks (MV line) -> cutout -> transformer -> LV board, and an OPEN cutout
-- (device_state OPEN, its born-isolated default) halts that walk until commissioned.
--
-- Apply with psql after the dependency seeds above and migration 0093:
--   task seed-overhead-pole-transformer
-- No compile step -- pure metadata rows; the component classes are compiled by
-- their own seeds.

-- ---------------------------------------------------------------------------
-- Structural relationship classes -- the pole SUPPORTS each kit component
-- (kind 'spatial_support', already in the 0010 CHECK). one_to_many: one pole
-- carries many mounted items, and a given transformer/cutout/arrester/board sits
-- on exactly one pole (unlike POLE->MV_CONDUCTOR which is many_to_many because a
-- span is shared between poles).
-- ---------------------------------------------------------------------------
INSERT INTO metadata.relationship_classes (name, origin_feature_class_id, destination_feature_class_id, kind, cardinality)
SELECT 'POLE supports ' || child.code, pole.id, child.id, 'spatial_support', 'one_to_many'
FROM metadata.feature_classes pole
JOIN metadata.feature_classes child ON child.code IN ('MV_FUSE', 'DIST_XFMR', 'MV_ARRESTER', 'LV_BUSBAR')
WHERE pole.code = 'POLE'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.relationship_classes rc
    WHERE rc.origin_feature_class_id = pole.id AND rc.destination_feature_class_id = child.id AND rc.kind = 'spatial_support'
  );

-- ---------------------------------------------------------------------------
-- network_rules -- the internal legs the kit wires along. phase_rule NULL: an
-- internal factory leg is authorized structurally, not by voltage-class match,
-- exactly as the packaged-substation and RMU internal legs are. EstablishConnection
-- matches a rule in EITHER direction, so one rule per leg suffices.
-- ---------------------------------------------------------------------------

-- leg 1: cutout load side into the transformer HV primary.
INSERT INTO metadata.network_rules (name, from_feature_class_id, from_terminal_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'MV_FUSE T2 -> DIST_XFMR T1', fu.id, fut2.id, xf.id, xft1.id, NULL, true
FROM metadata.feature_classes fu
JOIN metadata.feature_classes xf ON xf.code = 'DIST_XFMR'
JOIN metadata.terminals fut2 ON fut2.feature_class_id = fu.id AND fut2.code = 'T2'
JOIN metadata.terminals xft1 ON xft1.feature_class_id = xf.id AND xft1.code = 'T1'
WHERE fu.code = 'MV_FUSE'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules nr
    WHERE nr.from_feature_class_id = fu.id AND nr.from_terminal_id = fut2.id
      AND nr.to_feature_class_id = xf.id AND nr.to_terminal_id = xft1.id
  );

-- leg 2: the surge arrester taps the transformer primary node (shunt). Arrester T1
-- to transformer T1 -- same electrical node as the cutout feed. T2 (ground) is a
-- dead-end to earth, so it carries no rule.
INSERT INTO metadata.network_rules (name, from_feature_class_id, from_terminal_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'DIST_XFMR T1 -> MV_ARRESTER T1', xf.id, xft1.id, ar.id, art1.id, NULL, true
FROM metadata.feature_classes xf
JOIN metadata.feature_classes ar ON ar.code = 'MV_ARRESTER'
JOIN metadata.terminals xft1 ON xft1.feature_class_id = xf.id AND xft1.code = 'T1'
JOIN metadata.terminals art1 ON art1.feature_class_id = ar.id AND art1.code = 'T1'
WHERE xf.code = 'DIST_XFMR'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules nr
    WHERE nr.from_feature_class_id = xf.id AND nr.from_terminal_id = xft1.id
      AND nr.to_feature_class_id = ar.id AND nr.to_terminal_id = art1.id
  );

-- leg 3: transformer LV secondary into the LV board incoming terminal. Already
-- shipped by seed/packaged_substation_example.sql; re-added idempotently so this
-- seed stands alone if that one was never applied.
INSERT INTO metadata.network_rules (name, from_feature_class_id, from_terminal_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'DIST_XFMR T2 -> LV_BUSBAR T1', xf.id, xft2.id, b.id, bt1.id, NULL, true
FROM metadata.feature_classes xf
JOIN metadata.feature_classes b ON b.code = 'LV_BUSBAR'
JOIN metadata.terminals xft2 ON xft2.feature_class_id = xf.id AND xft2.code = 'T2'
JOIN metadata.terminals bt1 ON bt1.feature_class_id = b.id AND bt1.code = 'T1'
WHERE xf.code = 'DIST_XFMR'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules nr
    WHERE nr.from_feature_class_id = xf.id AND nr.from_terminal_id = xft2.id
      AND nr.to_feature_class_id = b.id AND nr.to_terminal_id = bt1.id
  );

-- ---------------------------------------------------------------------------
-- The recipe: POLE_MOUNT_STD on the POLE anchor. anchor_subtype_code NULL -- the
-- placing crew picks the pole material (wood/concrete/steel).
-- ---------------------------------------------------------------------------
INSERT INTO metadata.assembly_templates (anchor_feature_class_id, code, name, anchor_subtype_code)
SELECT fc.id, 'POLE_MOUNT_STD', 'Pole-mounted transformer', NULL
FROM metadata.feature_classes fc
WHERE fc.code = 'POLE'
ON CONFLICT (anchor_feature_class_id, code) DO NOTHING;

-- Items. support_parent_position 0 = the anchor pole (spatial_support on every
-- item). wire_to_position 0 = anchor, >=1 = an earlier item; NULL = externally fed.
-- own_terminal is on THIS item; upstream_terminal is on the wire_to target.
INSERT INTO metadata.assembly_items
    (template_id, component_feature_class_id, subtype_code, attributes, support_kind, support_parent_position, wire_to_position, own_terminal, upstream_terminal, position, offset_x, offset_y, rotation)
SELECT t.id, cfc.id, v.subtype_code, v.attributes::jsonb, v.support_kind, v.support_parent_position, v.wire_to_position, v.own_terminal, v.upstream_terminal, v.position, v.offset_x, v.offset_y, v.rotation
FROM (VALUES
    (1, 'MV_FUSE',     1, '{"device_state":"OPEN","condition_rating":"GOOD"}',    'spatial_support', 0, NULL, NULL, NULL, 0.0,  2.0, 0.0),
    (2, 'DIST_XFMR',   1, '{"primary_voltage":"11KV","condition_rating":"GOOD"}', 'spatial_support', 0, 1,    'T1', 'T2', 0.0,  0.0, 0.0),
    (3, 'MV_ARRESTER', 1, '{"condition_rating":"GOOD"}',                          'spatial_support', 0, 2,    'T1', 'T1', 1.5,  1.0, 0.0),
    (4, 'LV_BUSBAR',   1, '{"phase":"L1L2L3N"}',                                  'spatial_support', 0, 2,    'T1', 'T2', 0.0, -2.0, 0.0)
) AS v(position, component_code, subtype_code, attributes, support_kind, support_parent_position, wire_to_position, own_terminal, upstream_terminal, offset_x, offset_y, rotation)
JOIN metadata.feature_classes cfc ON cfc.code = v.component_code
JOIN metadata.assembly_templates t ON t.code = 'POLE_MOUNT_STD'
  AND t.anchor_feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'POLE')
ON CONFLICT (template_id, position) DO NOTHING;
