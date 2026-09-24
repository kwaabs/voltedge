-- seed/pole_mount_voltage_regulator.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives under
-- /seed on purpose, same reasoning as the other seed files. Assembly-backlog
-- Tier-2 #7 (part 2 of 2) -- the POLE_MOUNT_REGULATOR kit for the general
-- assembly engine (api/internal/assembly, migration 0093). Part 1 is the class
-- seed/mv_voltage_regulator_example.sql; the tap-optimization analysis lives in
-- api/internal/powerflow (resolveRegulator + forwardSweep reg case + tapopt.go).
--
-- Models a pole-mounted MV step voltage regulator as an ASSEMBLY: place one POLE
-- and get its pole-top kit -- the regulator plus a surge arrester shunt-tapping
-- EACH side (source + load) -- spatially supported by the pole and internally
-- wired. Same BRANCH shape as POLE_MOUNT_RECLOSER (a series device with an
-- arrester on each bushing).
--
-- Uses only EXISTING classes -- POLE (seed/pole_example.sql), MV_VREG
-- (seed/mv_voltage_regulator_example.sql, this seed's part 1), MV_ARRESTER
-- (seed/mv_protection_example.sql). Adds NO new Go: the general engine already
-- drives branch kits off per-item structural-parent + wire-to-position, so a new
-- kit is pure metadata -- the structural support classes, two internal shunt
-- legs, and the template recipe.
--
-- Kit (creation/wiring order):
--   pos 1 MV_VREG      the regulator -- fed EXTERNALLY (MV line -> T1, the rule
--                      MV_CONDUCTOR -> MV_VREG T1 ships with the regulator class);
--                      no internal wire. Born CLOSED at nominal tap 0 (in service,
--                      no boost) -- the tap-optimization study then finds the tap
--                      that best holds the downstream feeder in band.
--   pos 2 MV_ARRESTER  source-side surge tap -- MV_VREG.T1 -> MV_ARRESTER.T1
--                      (leg added here; shunt on the source node).
--   pos 3 MV_ARRESTER  load-side surge tap -- MV_VREG.T2 -> MV_ARRESTER.T1
--                      (leg added here; shunt on the load node).
-- Every item is spatial_support-held by the pole (one_to_many). A DOWNSTREAM
-- trace walks (MV line) -> regulator -> (load feeder); the regulator is a series
-- pass-through so the walk continues through it, and the forward sweep applies
-- its tap ratio to the voltage. The arresters are shunt dead-ends (T2 to earth),
-- so they never carry the trace onward.
--
-- Apply with psql after the dependency seeds above, migration 0093, and BOTH
-- part 1 (seed/mv_voltage_regulator_example.sql) and MV_ARRESTER:
--   task seed-pole-mount-voltage-regulator
-- No compile step -- pure metadata rows; the component classes are compiled by
-- their own seeds (MV_VREG via `task compile CODE=MV_VREG`).
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL has
-- been reviewed statically -- a live psql apply + placing the kit via the
-- Assemblies menu + a tap-optimization run against it are still owed to verify.

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
JOIN metadata.feature_classes child ON child.code IN ('MV_VREG', 'MV_ARRESTER')
WHERE pole.code = 'POLE'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.relationship_classes rc
    WHERE rc.origin_feature_class_id = pole.id AND rc.destination_feature_class_id = child.id AND rc.kind = 'spatial_support'
  );

-- ---------------------------------------------------------------------------
-- network_rules -- the internal shunt legs the kit wires along. phase_rule NULL:
-- an internal factory leg is authorized structurally, not by voltage-class match,
-- exactly as the recloser / pole-transformer / packaged-substation internal legs.
-- EstablishConnection matches a rule in EITHER direction, so one rule per leg
-- suffices; both terminal joins reference an already-joined class (all-explicit
-- joins), sidestepping the comma-then-JOIN "invalid reference to FROM-clause
-- entry" gotcha.
-- ---------------------------------------------------------------------------

-- leg 1: source-side arrester taps the regulator SOURCE node (T1). Arrester T1 to
-- regulator T1 -- same electrical node as the incoming MV feed. Arrester T2
-- (ground) is a dead-end to earth, so it carries no rule.
INSERT INTO metadata.network_rules (name, from_feature_class_id, from_terminal_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'MV_VREG T1 -> MV_ARRESTER T1', rg.id, rgt1.id, ar.id, art1.id, NULL, true
FROM metadata.feature_classes rg
JOIN metadata.feature_classes ar ON ar.code = 'MV_ARRESTER'
JOIN metadata.terminals rgt1 ON rgt1.feature_class_id = rg.id AND rgt1.code = 'T1'
JOIN metadata.terminals art1 ON art1.feature_class_id = ar.id AND art1.code = 'T1'
WHERE rg.code = 'MV_VREG'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules nr
    WHERE nr.from_feature_class_id = rg.id AND nr.from_terminal_id = rgt1.id
      AND nr.to_feature_class_id = ar.id AND nr.to_terminal_id = art1.id
  );

-- leg 2: load-side arrester taps the regulator LOAD node (T2). Arrester T1 to
-- regulator T2. Same shunt shape as leg 1, on the downstream bushing.
INSERT INTO metadata.network_rules (name, from_feature_class_id, from_terminal_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'MV_VREG T2 -> MV_ARRESTER T1', rg.id, rgt2.id, ar.id, art1.id, NULL, true
FROM metadata.feature_classes rg
JOIN metadata.feature_classes ar ON ar.code = 'MV_ARRESTER'
JOIN metadata.terminals rgt2 ON rgt2.feature_class_id = rg.id AND rgt2.code = 'T2'
JOIN metadata.terminals art1 ON art1.feature_class_id = ar.id AND art1.code = 'T1'
WHERE rg.code = 'MV_VREG'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules nr
    WHERE nr.from_feature_class_id = rg.id AND nr.from_terminal_id = rgt2.id
      AND nr.to_feature_class_id = ar.id AND nr.to_terminal_id = art1.id
  );

-- ---------------------------------------------------------------------------
-- The recipe: POLE_MOUNT_REGULATOR on the POLE anchor. anchor_subtype_code NULL
-- -- the placing crew picks the pole material, same as POLE_MOUNT_STD /
-- POLE_MOUNT_RECLOSER.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.assembly_templates (anchor_feature_class_id, code, name, anchor_subtype_code)
SELECT fc.id, 'POLE_MOUNT_REGULATOR', 'Pole-mounted voltage regulator', NULL
FROM metadata.feature_classes fc
WHERE fc.code = 'POLE'
ON CONFLICT (anchor_feature_class_id, code) DO NOTHING;

-- Items. support_parent_position 0 = the anchor pole (spatial_support on every
-- item). wire_to_position 0 = anchor, >=1 = an earlier item; NULL = externally
-- fed. own_terminal is on THIS item; upstream_terminal is on the wire_to target.
-- The regulator is born three_phase (subtype 2), CLOSED, at nominal tap 0 with a
-- classic 0.625%/step regulator so the tap study has real authority to explore.
INSERT INTO metadata.assembly_items
    (template_id, component_feature_class_id, subtype_code, attributes, support_kind, support_parent_position, wire_to_position, own_terminal, upstream_terminal, position, offset_x, offset_y, rotation)
SELECT t.id, cfc.id, v.subtype_code, v.attributes::jsonb, v.support_kind, v.support_parent_position, v.wire_to_position, v.own_terminal, v.upstream_terminal, v.position, v.offset_x, v.offset_y, v.rotation
FROM (VALUES
    (1, 'MV_VREG',     2, '{"regulator_step_pct":0.625,"regulator_tap_position":0,"device_state":"CLOSED","condition_rating":"GOOD"}', 'spatial_support', 0, NULL, NULL, NULL,  0.0,  0.0, 0.0),
    (2, 'MV_ARRESTER', 1, '{"condition_rating":"GOOD"}',                                                                              'spatial_support', 0, 1,    'T1', 'T1', -1.5,  1.5, 0.0),
    (3, 'MV_ARRESTER', 1, '{"condition_rating":"GOOD"}',                                                                              'spatial_support', 0, 1,    'T1', 'T2',  1.5,  1.5, 0.0)
) AS v(position, component_code, subtype_code, attributes, support_kind, support_parent_position, wire_to_position, own_terminal, upstream_terminal, offset_x, offset_y, rotation)
JOIN metadata.feature_classes cfc ON cfc.code = v.component_code
JOIN metadata.assembly_templates t ON t.code = 'POLE_MOUNT_REGULATOR'
  AND t.anchor_feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'POLE')
ON CONFLICT (template_id, position) DO NOTHING;
