-- seed/pad_mount_urd.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives under
-- /seed on purpose, same reasoning as the other seed files. Phase 57 -- the
-- PAD_MOUNT_URD kit for the general assembly engine (api/internal/assembly,
-- migration 0093). Models the compact underground-residential (URD) unit: place
-- one pad-mounted DIST_XFMR and get its LV_LINK_BOX feeder pillar wired to the
-- secondary.
--
-- Uses only EXISTING classes -- DIST_XFMR (seed/connectivity_example.sql) as the
-- anchor, LV_LINK_BOX (seed/lv_devices_example.sql, subtype feeder_pillar) as the
-- kit. This seed adds NO class and NO structural relationship (a pad transformer
-- and its pillar are adjacent ground equipment, neither contains nor supports the
-- other -- they are linked purely electrically). It adds one network leg and the
-- template recipe.
--
-- Kit (creation/wiring order):
--   pos 1 LV_LINK_BOX feeder_pillar -- DIST_XFMR.T2 -> LV_LINK_BOX.T1 (leg added
--                     here). wire_to_position 0 = the anchor transformer.
-- A DOWNSTREAM trace walks transformer -> pillar -> (LV feeders off the pillar's
-- outgoing ways); an OPEN pillar (its born-isolated default) halts it until
-- commissioned.
--
-- The anchor is forced to DIST_XFMR subtype 2 (pad_mounted) by the template's
-- anchor_subtype_code -- a URD unit is pad-mounted by definition.
--
-- Apply with psql after the dependency seeds above and migration 0093:
--   task seed-pad-mount-urd
-- No compile step -- pure metadata rows.

-- ---------------------------------------------------------------------------
-- network_rule -- the single internal leg: transformer LV secondary into the
-- feeder pillar's incoming terminal. phase_rule NULL (structural authorization),
-- matched in either direction so one rule suffices.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.network_rules (name, from_feature_class_id, from_terminal_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'DIST_XFMR T2 -> LV_LINK_BOX T1', xf.id, xft2.id, lb.id, lbt1.id, NULL, true
FROM metadata.feature_classes xf
JOIN metadata.feature_classes lb ON lb.code = 'LV_LINK_BOX'
JOIN metadata.terminals xft2 ON xft2.feature_class_id = xf.id AND xft2.code = 'T2'
JOIN metadata.terminals lbt1 ON lbt1.feature_class_id = lb.id AND lbt1.code = 'T1'
WHERE xf.code = 'DIST_XFMR'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules nr
    WHERE nr.from_feature_class_id = xf.id AND nr.from_terminal_id = xft2.id
      AND nr.to_feature_class_id = lb.id AND nr.to_terminal_id = lbt1.id
  );

-- ---------------------------------------------------------------------------
-- The recipe: PAD_MOUNT_URD on the DIST_XFMR anchor, forced to subtype 2
-- (pad_mounted).
-- ---------------------------------------------------------------------------
INSERT INTO metadata.assembly_templates (anchor_feature_class_id, code, name, anchor_subtype_code)
SELECT fc.id, 'PAD_MOUNT_URD', 'Pad-mount transformer + feeder pillar (URD)', 2
FROM metadata.feature_classes fc
WHERE fc.code = 'DIST_XFMR'
ON CONFLICT (anchor_feature_class_id, code) DO NOTHING;

-- Single item: the feeder pillar. No structural link (support_kind NULL); wired to
-- the anchor transformer (wire_to_position 0): DIST_XFMR.T2 -> LV_LINK_BOX.T1.
INSERT INTO metadata.assembly_items
    (template_id, component_feature_class_id, subtype_code, attributes, support_kind, support_parent_position, wire_to_position, own_terminal, upstream_terminal, position, offset_x, offset_y, rotation)
SELECT t.id, cfc.id, v.subtype_code, v.attributes::jsonb, v.support_kind, v.support_parent_position, v.wire_to_position, v.own_terminal, v.upstream_terminal, v.position, v.offset_x, v.offset_y, v.rotation
FROM (VALUES
    (1, 'LV_LINK_BOX', 1, '{"device_state":"OPEN","condition_rating":"GOOD"}', NULL::text, NULL::int, 0, 'T1', 'T2', 0.0, -2.0, 0.0)
) AS v(position, component_code, subtype_code, attributes, support_kind, support_parent_position, wire_to_position, own_terminal, upstream_terminal, offset_x, offset_y, rotation)
JOIN metadata.feature_classes cfc ON cfc.code = v.component_code
JOIN metadata.assembly_templates t ON t.code = 'PAD_MOUNT_URD'
  AND t.anchor_feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'DIST_XFMR')
ON CONFLICT (template_id, position) DO NOTHING;
