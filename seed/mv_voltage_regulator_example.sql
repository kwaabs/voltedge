-- seed/mv_voltage_regulator_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives under
-- /seed on purpose, same reasoning as the other seed files. Assembly-backlog
-- Tier-2 #7 (part 1 of 2) -- the MV_VREG feature class. Part 2 is the assembly
-- kit seed/pole_mount_voltage_regulator.sql; the tap-optimization analysis
-- integration lives in api/internal/powerflow (resolveRegulator + forwardSweep
-- reg case + POST /analysis/tap-optimization).
--
-- MV_VREG -- a step voltage regulator: a SERIES autotransformer that boosts or
-- bucks the line voltage by a small percentage per tap step, holding the
-- downstream voltage inside band as load and source voltage vary. Shaped like
-- MV_RECLOSER / MV_SWITCH (a SERIES junction: T1 is the source side, T2 the load
-- side, both phase_capable = true), because a regulator sits IN the line and the
-- current passes through it -- unlike a shunt cap bank which taps off to earth.
-- What is new is the tap control, which the forward sweep turns into a voltage
-- ratio on the SAME voltage base (a regulator does not change voltage class):
--   regulator_step_pct     numeric -- voltage change per tap step, percent (a
--                          classic ANSI regulator is 0.625%/step, ±16 steps =
--                          ±10%). Presence of this field is what marks the node
--                          as a regulator to the engine.
--   regulator_tap_position numeric -- the live tap, signed (negative = buck,
--                          positive = boost); 0 = nominal (no boost). The applied
--                          ratio is 1 + tap * step/100.
--   device_state           text    -- CLOSED = in regulation, OPEN = bypassed
--                          (the bypass switch is closed, regulator out of the
--                          loop; the engine passes voltage straight through).
--   condition_rating       text    -- maintenance state, as every field asset.
--
-- Subtypes:
--   1 single_phase -- one 1-phase regulator (three make a bank, wired via the kit).
--   2 three_phase  -- a gang-operated 3-phase regulator in one tank.
--
-- Depends on: seed/connectivity_example.sql (MV_CONDUCTOR), and for the full
-- footprint seed/circuit_membership_example.sql, seed/operational_state_example.sql,
-- seed/snapping_rules_example.sql, seed/ansi_stencils.sql (the ansi-transformer-2w
-- glyph -- a regulator is drawn as an autotransformer), seed/cim_mapping_example.sql
-- (cim_profiles), seed/symbology_icons.sql (the square map icon). Every JOIN
-- inserts nothing if a dependency is absent, so partial application is graceful.
--
-- Apply, then compile:
--   task seed-mv-voltage-regulator
--   task compile CODE=MV_VREG
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL has
-- been reviewed statically -- a live psql apply + compile + a tap-optimization
-- run are still owed to call it verified.

INSERT INTO metadata.field_groups (code, name, sort_order) VALUES
    ('electrical', 'Electrical', 1),
    ('maintenance', 'Maintenance', 2)
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domains (code, name, domain_type) VALUES
    ('DEVICE_STATE', 'Device state', 'coded'),
    ('CONDITION_RATING', 'Condition rating', 'coded')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domain_values (domain_id, code, label, sort_order)
SELECT d.id, v.code, v.label, v.sort_order
FROM metadata.domains d
JOIN (VALUES
    ('DEVICE_STATE', 'CLOSED', 'Closed', 1),
    ('DEVICE_STATE', 'OPEN', 'Open', 2),
    ('CONDITION_RATING', 'GOOD', 'Good', 1),
    ('CONDITION_RATING', 'FAIR', 'Fair', 2),
    ('CONDITION_RATING', 'POOR', 'Poor', 3)
) AS v(domain_code, code, label, sort_order) ON v.domain_code = d.code
ON CONFLICT (domain_id, code) DO NOTHING;

-- ===========================================================================
-- Class + subtypes + fields + terminals.
-- ===========================================================================
INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status, is_diagram_container, is_diagram_placeable, geometry_optional)
VALUES ('MV_VREG', 'MV Voltage Regulator', 'point', 4326, 'junction', 'draft', false, true, true)
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, v.code, v.name
FROM metadata.feature_classes fc
JOIN (VALUES (1, 'single_phase'), (2, 'three_phase')) AS v(code, name) ON true
WHERE fc.code = 'MV_VREG'
ON CONFLICT (feature_class_id, code) DO NOTHING;

INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, v.nullable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('regulator_step_pct',     'numeric', NULL,               'electrical',  false),
    ('regulator_tap_position', 'numeric', NULL,               'electrical',  false),
    ('device_state',           'text',    'DEVICE_STATE',     'electrical',  false),
    ('condition_rating',       'text',    'CONDITION_RATING', 'maintenance', false)
) AS v(name, data_type, domain_code, group_code, nullable) ON true
LEFT JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code = 'MV_VREG'
ON CONFLICT (feature_class_id, name) DO NOTHING;

-- All four fields required on both subtypes: a regulator must know its step, its
-- live tap, whether it is in service, and its condition. (A single- vs three-
-- phase regulator differs only in how the kit wires it, not in its own fields.)
INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, true
FROM metadata.subtypes s
JOIN metadata.fields f ON f.feature_class_id = s.feature_class_id
WHERE s.feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'MV_VREG')
  AND f.name IN ('regulator_step_pct', 'regulator_tap_position', 'device_state', 'condition_rating')
ON CONFLICT (subtype_id, field_id) DO NOTHING;

-- Series terminals: T1 source side, T2 load side, both phase_capable (current
-- passes through) -- identical to MV_RECLOSER / MV_SWITCH.
INSERT INTO metadata.terminals (feature_class_id, subtype_id, code, role, phase_capable)
SELECT fc.id, NULL, v.code, v.role, v.phase_capable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('T1', 'source', true),
    ('T2', 'load', true)
) AS v(code, role, phase_capable) ON true
WHERE fc.code = 'MV_VREG'
ON CONFLICT (feature_class_id, subtype_id, code) DO NOTHING;

-- ===========================================================================
-- network_rules -- inline three-rule series shape (MV_CONDUCTOR <-> MV_VREG):
-- feeder into T1, feeder into T2, and T2 -> feeder so a DOWNSTREAM trace walks
-- through. Guarded WHERE NOT EXISTS (network_rules has no unique key). Identical
-- to the MV_RECLOSER seed's shape -- a regulator is a series device.
-- ===========================================================================
INSERT INTO metadata.network_rules (name, from_feature_class_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'MV_CONDUCTOR -> MV_VREG T1', cond.id, rg.id, t1.id, NULL, true
FROM metadata.feature_classes rg
JOIN metadata.feature_classes cond ON cond.code = 'MV_CONDUCTOR'
JOIN metadata.terminals t1 ON t1.feature_class_id = rg.id AND t1.code = 'T1'
WHERE rg.code = 'MV_VREG'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = cond.id AND to_feature_class_id = rg.id AND to_terminal_id = t1.id
  );

INSERT INTO metadata.network_rules (name, from_feature_class_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'MV_CONDUCTOR -> MV_VREG T2', cond.id, rg.id, t2.id, NULL, true
FROM metadata.feature_classes rg
JOIN metadata.feature_classes cond ON cond.code = 'MV_CONDUCTOR'
JOIN metadata.terminals t2 ON t2.feature_class_id = rg.id AND t2.code = 'T2'
WHERE rg.code = 'MV_VREG'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = cond.id AND to_feature_class_id = rg.id AND to_terminal_id = t2.id
  );

INSERT INTO metadata.network_rules (name, from_feature_class_id, from_terminal_id, to_feature_class_id, phase_rule, is_valid)
SELECT 'MV_VREG T2 -> MV_CONDUCTOR', rg.id, t2.id, cond.id, NULL, true
FROM metadata.feature_classes rg
JOIN metadata.feature_classes cond ON cond.code = 'MV_CONDUCTOR'
JOIN metadata.terminals t2 ON t2.feature_class_id = rg.id AND t2.code = 'T2'
WHERE rg.code = 'MV_VREG'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = rg.id AND from_terminal_id = t2.id AND to_feature_class_id = cond.id
  );

-- ===========================================================================
-- Full footprint -- mirrors the MV_RECLOSER / MV_CAP_BANK seeds.
-- ===========================================================================

-- 1. Circuit membership: MV network stamp + circuit_id field.
UPDATE metadata.feature_classes SET network = 'MV' WHERE code = 'MV_VREG';

INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, 'circuit_id', 'uuid', NULL, fg.id, true
FROM metadata.feature_classes fc
JOIN metadata.field_groups fg ON fg.code = 'electrical'
WHERE fc.code = 'MV_VREG'
ON CONFLICT (feature_class_id, name) DO NOTHING;

-- 2. operational_state lifecycle field.
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, 'operational_state', 'text', d.id, fg.id, true
FROM metadata.feature_classes fc
JOIN metadata.domains d       ON d.code  = 'OPERATIONAL_STATE'
JOIN metadata.field_groups fg ON fg.code = 'operational'
WHERE fc.code = 'MV_VREG'
ON CONFLICT (feature_class_id, name) DO NOTHING;

-- 3. Snapping: MV_CONDUCTOR -> MV_VREG (point_feature, either side).
INSERT INTO metadata.snapping_rules (from_feature_class_id, to_feature_class_id, to_terminal_id, target_type_value_id, tolerance_m, priority, is_required)
SELECT frm.id, tgt.id, NULL, tt.id, 0.5, 1, false
FROM metadata.feature_classes frm
CROSS JOIN metadata.feature_classes tgt
JOIN metadata.domain_values tt ON tt.code = 'point_feature'
  AND tt.domain_id = (SELECT id FROM metadata.domains WHERE code = 'SNAP_TARGET_TYPE')
WHERE frm.code = 'MV_CONDUCTOR' AND tgt.code = 'MV_VREG'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.snapping_rules sr
    WHERE sr.from_feature_class_id = frm.id AND sr.to_feature_class_id = tgt.id
      AND sr.to_terminal_id IS NULL
      AND sr.target_type_value_id = tt.id
  );

-- 4. Map symbology -- default (solid square, violet) + device_state = OPEN
--    override (grey, bypassed reads as inactive).
INSERT INTO metadata.symbology_rules (feature_class_id, render_context, style_def)
SELECT fc.id, 'map', '{"icon_ref": "square", "color": "#7c3aed"}'::jsonb
FROM metadata.feature_classes fc
WHERE fc.code = 'MV_VREG'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules
    WHERE feature_class_id = fc.id AND subtype_id IS NULL AND domain_value_id IS NULL AND render_context = 'map'
  );

INSERT INTO metadata.symbology_rules (feature_class_id, domain_value_id, render_context, style_def)
SELECT rg.id, dv.id, 'map', '{"icon_ref": "square", "color": "#94a3b8"}'::jsonb
FROM metadata.feature_classes rg
JOIN metadata.fields f ON f.feature_class_id = rg.id AND f.name = 'device_state'
JOIN metadata.domain_values dv ON dv.domain_id = f.domain_id AND dv.code = 'OPEN'
WHERE rg.code = 'MV_VREG'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules
    WHERE feature_class_id = rg.id AND subtype_id IS NULL AND domain_value_id = dv.id AND render_context = 'map'
  );

-- 5. Diagram symbology -- the ansi-transformer-2w glyph (a regulator is drawn as
--    an autotransformer; seed/ansi_stencils.sql).
INSERT INTO metadata.symbology_rules (feature_class_id, render_context, style_def)
SELECT fc.id, 'diagram', '{"icon_ref": "ansi-transformer-2w", "color": "#7c3aed"}'::jsonb
FROM metadata.feature_classes fc
WHERE fc.code = 'MV_VREG'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules
    WHERE feature_class_id = fc.id AND subtype_id IS NULL AND domain_value_id IS NULL AND render_context = 'diagram'
  );

-- 6. CIM -- RatioTapChanger sits on a PowerTransformer in CIM; the closest single
--    conducting-equipment class for a standalone line regulator is PowerTransformer
--    with a tap changer. Map the class to PowerTransformer and the step to
--    RatioTapChanger.stepVoltageIncrement (percent, no scale).
INSERT INTO metadata.cim_class_mappings (feature_class_id, subtype_id, cim_class, cim_kind)
SELECT fc.id, NULL, 'PowerTransformer', 'conducting_junction'
FROM metadata.feature_classes fc
WHERE fc.code = 'MV_VREG'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.cim_class_mappings m
    WHERE m.feature_class_id = fc.id AND m.subtype_id IS NULL
  );

INSERT INTO metadata.cim_attribute_mappings (field_id, cim_attribute, transform, unit)
SELECT f.id, 'RatioTapChanger.stepVoltageIncrement', 'direct', 'percent'
FROM metadata.fields f
JOIN metadata.feature_classes fc ON fc.id = f.feature_class_id
WHERE fc.code = 'MV_VREG' AND f.name = 'regulator_step_pct'
ON CONFLICT (field_id, cim_attribute) DO NOTHING;

INSERT INTO metadata.cim_attribute_mappings (field_id, cim_attribute, transform, unit)
SELECT f.id, 'RatioTapChanger.step', 'direct', NULL
FROM metadata.fields f
JOIN metadata.feature_classes fc ON fc.id = f.feature_class_id
WHERE fc.code = 'MV_VREG' AND f.name = 'regulator_tap_position'
ON CONFLICT (field_id, cim_attribute) DO NOTHING;
