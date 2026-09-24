-- seed/pole_mount_recloser.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives under
-- /seed on purpose, same reasoning as the other seed files. Assembly-backlog
-- Tier-2 #4 -- the POLE_MOUNT_RECLOSER kit for the general assembly engine
-- (api/internal/assembly, migration 0093). Models a pole-mounted MV auto-recloser
-- as an ASSEMBLY: place one POLE and get its pole-top kit, spatially supported by
-- the pole and internally wired.
--
-- Uses only EXISTING classes -- POLE (seed/pole_example.sql), MV_RECLOSER
-- (seed/mv_recloser_example.sql), MV_ARRESTER (seed/mv_protection_example.sql).
-- This seed adds NO class and NO new Go: the general engine already drives
-- star/chain/branch off per-item structural-parent + wire-to-position, so a new
-- kit is pure metadata -- the two structural relationship classes, two internal
-- shunt legs, and the template recipe.
--
-- Why the recloser is self-contained (no controller/CT/VT child features): the
-- MV_RECLOSER class already folds the recloser's controller programming
-- (reclose_attempts / fast+slow_curve_ref / reclose_interval_s / reset_time_s)
-- into its own attributes and grades in the coordination engine as one device
-- (see seed/mv_recloser_example.sql). So the kit's job is the pole-top HARDWARE
-- around it: the recloser itself plus a surge arrester shunt-tapping EACH side
-- (source + load), which is how pole reclosers are protected in the field. Two
-- arresters wiring back to the one recloser at different terminals = a BRANCH,
-- exactly what POLE_MOUNT_STD exercises (arrester + LV board both off the xfmr).
--
-- Kit (creation/wiring order):
--   pos 1 MV_RECLOSER  the recloser  -- fed EXTERNALLY (MV line -> T1, the rule
--                      MV_CONDUCTOR -> MV_RECLOSER T1 already ships with the
--                      recloser class); no internal wire. Born OPEN (lockout /
--                      not-yet-commissioned), its trace-barrier default.
--   pos 2 MV_ARRESTER  line-side surge tap -- MV_RECLOSER.T1 -> MV_ARRESTER.T1
--                      (leg added here; shunt on the source node).
--   pos 3 MV_ARRESTER  load-side surge tap -- MV_RECLOSER.T2 -> MV_ARRESTER.T1
--                      (leg added here; shunt on the load node).
-- Every item is spatial_support-held by the pole (one_to_many). A DOWNSTREAM
-- trace walks (MV line) -> recloser -> (load feeder), and an OPEN recloser
-- (device_state OPEN, its born-isolated default = lockout) halts that walk until
-- it is commissioned CLOSED. The arresters are shunt dead-ends (T2 to earth), so
-- they never carry the trace onward.
--
-- Apply with psql after the dependency seeds above and migration 0093:
--   task seed-pole-mount-recloser
-- No compile step -- pure metadata rows; the component classes are compiled by
-- their own seeds.
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL has
-- been reviewed statically -- a live psql apply + placing the kit via the
-- Assemblies menu are still owed to call it verified.

-- ---------------------------------------------------------------------------
-- Structural relationship classes -- the pole SUPPORTS each kit component
-- (kind 'spatial_support', already in the 0010 CHECK). one_to_many: one pole
-- carries many mounted items, each mounted item sits on exactly one pole.
-- POLE -> MV_ARRESTER already ships from seed/overhead_pole_transformer.sql;
-- re-added idempotently so this seed stands alone.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.relationship_classes (name, origin_feature_class_id, destination_feature_class_id, kind, cardinality)
SELECT 'POLE supports ' || child.code, pole.id, child.id, 'spatial_support', 'one_to_many'
FROM metadata.feature_classes pole
JOIN metadata.feature_classes child ON child.code IN ('MV_RECLOSER', 'MV_ARRESTER')
WHERE pole.code = 'POLE'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.relationship_classes rc
    WHERE rc.origin_feature_class_id = pole.id AND rc.destination_feature_class_id = child.id AND rc.kind = 'spatial_support'
  );

-- ---------------------------------------------------------------------------
-- network_rules -- the internal shunt legs the kit wires along. phase_rule NULL:
-- an internal factory leg is authorized structurally, not by voltage-class match,
-- exactly as the pole-transformer and packaged-substation internal legs are.
-- EstablishConnection matches a rule in EITHER direction, so one rule per leg
-- suffices; both terminal joins reference an already-joined class (all-explicit
-- joins), sidestepping the comma-then-JOIN "invalid reference to FROM-clause
-- entry" gotcha the Phase-57 two-terminal seeds hit.
-- ---------------------------------------------------------------------------

-- leg 1: line-side arrester taps the recloser SOURCE node (T1). Arrester T1 to
-- recloser T1 -- same electrical node as the incoming MV feed. Arrester T2
-- (ground) is a dead-end to earth, so it carries no rule.
INSERT INTO metadata.network_rules (name, from_feature_class_id, from_terminal_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'MV_RECLOSER T1 -> MV_ARRESTER T1', rc.id, rct1.id, ar.id, art1.id, NULL, true
FROM metadata.feature_classes rc
JOIN metadata.feature_classes ar ON ar.code = 'MV_ARRESTER'
JOIN metadata.terminals rct1 ON rct1.feature_class_id = rc.id AND rct1.code = 'T1'
JOIN metadata.terminals art1 ON art1.feature_class_id = ar.id AND art1.code = 'T1'
WHERE rc.code = 'MV_RECLOSER'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules nr
    WHERE nr.from_feature_class_id = rc.id AND nr.from_terminal_id = rct1.id
      AND nr.to_feature_class_id = ar.id AND nr.to_terminal_id = art1.id
  );

-- leg 2: load-side arrester taps the recloser LOAD node (T2). Arrester T1 to
-- recloser T2. Same shunt shape as leg 1, on the downstream bushing.
INSERT INTO metadata.network_rules (name, from_feature_class_id, from_terminal_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'MV_RECLOSER T2 -> MV_ARRESTER T1', rc.id, rct2.id, ar.id, art1.id, NULL, true
FROM metadata.feature_classes rc
JOIN metadata.feature_classes ar ON ar.code = 'MV_ARRESTER'
JOIN metadata.terminals rct2 ON rct2.feature_class_id = rc.id AND rct2.code = 'T2'
JOIN metadata.terminals art1 ON art1.feature_class_id = ar.id AND art1.code = 'T1'
WHERE rc.code = 'MV_RECLOSER'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules nr
    WHERE nr.from_feature_class_id = rc.id AND nr.from_terminal_id = rct2.id
      AND nr.to_feature_class_id = ar.id AND nr.to_terminal_id = art1.id
  );

-- ---------------------------------------------------------------------------
-- The recipe: POLE_MOUNT_RECLOSER on the POLE anchor. anchor_subtype_code NULL --
-- the placing crew picks the pole material (wood/concrete/steel), same as
-- POLE_MOUNT_STD.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.assembly_templates (anchor_feature_class_id, code, name, anchor_subtype_code)
SELECT fc.id, 'POLE_MOUNT_RECLOSER', 'Pole-mounted recloser', NULL
FROM metadata.feature_classes fc
WHERE fc.code = 'POLE'
ON CONFLICT (anchor_feature_class_id, code) DO NOTHING;

-- Items. support_parent_position 0 = the anchor pole (spatial_support on every
-- item). wire_to_position 0 = anchor, >=1 = an earlier item; NULL = externally
-- fed. own_terminal is on THIS item; upstream_terminal is on the wire_to target.
INSERT INTO metadata.assembly_items
    (template_id, component_feature_class_id, subtype_code, attributes, support_kind, support_parent_position, wire_to_position, own_terminal, upstream_terminal, position, offset_x, offset_y, rotation)
SELECT t.id, cfc.id, v.subtype_code, v.attributes::jsonb, v.support_kind, v.support_parent_position, v.wire_to_position, v.own_terminal, v.upstream_terminal, v.position, v.offset_x, v.offset_y, v.rotation
FROM (VALUES
    (1, 'MV_RECLOSER', 1, '{"device_state":"OPEN","condition_rating":"GOOD"}', 'spatial_support', 0, NULL, NULL, NULL,  0.0,  0.0, 0.0),
    (2, 'MV_ARRESTER', 1, '{"condition_rating":"GOOD"}',                       'spatial_support', 0, 1,    'T1', 'T1', -1.5,  1.5, 0.0),
    (3, 'MV_ARRESTER', 1, '{"condition_rating":"GOOD"}',                       'spatial_support', 0, 1,    'T1', 'T2',  1.5,  1.5, 0.0)
) AS v(position, component_code, subtype_code, attributes, support_kind, support_parent_position, wire_to_position, own_terminal, upstream_terminal, offset_x, offset_y, rotation)
JOIN metadata.feature_classes cfc ON cfc.code = v.component_code
JOIN metadata.assembly_templates t ON t.code = 'POLE_MOUNT_RECLOSER'
  AND t.anchor_feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'POLE')
ON CONFLICT (template_id, position) DO NOTHING;
