-- seed/mv_capacitor_bank_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives under
-- /seed on purpose, same reasoning as the other seed files. Assembly-backlog
-- Tier-2 #6 (part 1 of 2) -- the MV_CAP_BANK feature class. Part 2 is the
-- assembly kit seed/pole_mount_capacitor_bank.sql; the volt-VAR analysis
-- integration lives in api/internal/powerflow.
--
-- MV_CAP_BANK -- a shunt capacitor bank: reactive-power (leading kVAr) support
-- connected line-to-earth to raise voltage and correct power factor. Shaped like
-- MV_ARRESTER (a SHUNT junction: T1 taps the line/primary, T2 is the earth/neutral
-- return, phase_capable = false), because electrically a cap bank is a shunt
-- element too -- it injects VArs at its node, it does not pass real power through.
-- What is new is the reactive RATING and a switch state:
--   rated_kvar       numeric -- three-phase reactive rating (leading), kVAr. The
--                    engine injects this as reactive support at the tapped node.
--   device_state     text    -- CLOSED = in-circuit (injecting), OPEN = switched
--                    out (no injection). Only the switched subtype carries it as
--                    required; a fixed bank is permanently connected.
--   condition_rating text    -- maintenance state, as every field asset carries.
--
-- Subtypes:
--   1 fixed    -- permanently connected; always injecting when energised.
--   2 switched -- a cap switch (or the kit's MV_SWITCH) puts it in/out; the
--                volt-VAR logic reads device_state to decide if it contributes.
--
-- Depends on: seed/connectivity_example.sql (MV_CONDUCTOR), and for the full
-- footprint seed/circuit_membership_example.sql, seed/operational_state_example.sql,
-- seed/snapping_rules_example.sql, seed/ansi_stencils.sql (the ansi-capacitor
-- glyph), seed/cim_mapping_example.sql (cim_profiles), seed/symbology_icons.sql
-- (the diamond map icon). Every JOIN inserts nothing if a dependency is absent,
-- so partial application is graceful.
--
-- Apply, then compile:
--   task seed-mv-capacitor-bank
--   task compile CODE=MV_CAP_BANK
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL has
-- been reviewed statically -- a live psql apply + compile + a volt-VAR run are
-- still owed to call it verified.

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
VALUES ('MV_CAP_BANK', 'MV Capacitor Bank', 'point', 4326, 'junction', 'draft', false, true, true)
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, v.code, v.name
FROM metadata.feature_classes fc
JOIN (VALUES (1, 'fixed'), (2, 'switched')) AS v(code, name) ON true
WHERE fc.code = 'MV_CAP_BANK'
ON CONFLICT (feature_class_id, code) DO NOTHING;

INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, v.nullable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('rated_kvar',       'numeric', NULL,               'electrical',  false),
    ('device_state',     'text',    'DEVICE_STATE',     'electrical',  true),
    ('condition_rating', 'text',    'CONDITION_RATING', 'maintenance', false)
) AS v(name, data_type, domain_code, group_code, nullable) ON true
LEFT JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code = 'MV_CAP_BANK'
ON CONFLICT (feature_class_id, name) DO NOTHING;

-- Required fields differ by subtype: both need a rating + a condition, but only a
-- SWITCHED bank carries a required device_state (a fixed bank is always in). Done
-- as two subtype-specific inserts rather than one class-wide rule.
INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, true
FROM metadata.subtypes s
JOIN metadata.fields f ON f.feature_class_id = s.feature_class_id
WHERE s.feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'MV_CAP_BANK')
  AND (
    f.name IN ('rated_kvar', 'condition_rating')
    OR (f.name = 'device_state' AND s.code = 2)
  )
ON CONFLICT (subtype_id, field_id) DO NOTHING;

-- Shunt terminals: T1 taps the line (primary), T2 is the earth/neutral return
-- (phase_capable = false, like MV_ARRESTER.T2 and DIST_XFMR.T3).
INSERT INTO metadata.terminals (feature_class_id, subtype_id, code, role, phase_capable)
SELECT fc.id, NULL, v.code, v.role, v.phase_capable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('T1', 'primary', true),
    ('T2', 'ground', false)
) AS v(code, role, phase_capable) ON true
WHERE fc.code = 'MV_CAP_BANK'
ON CONFLICT (feature_class_id, subtype_id, code) DO NOTHING;

-- ===========================================================================
-- network_rules -- one rule only: the line taps into T1 (shunt). T2 (earth) is a
-- dead-end, no rule -- identical to MV_ARRESTER. The kit adds an MV_SWITCH T2 ->
-- MV_CAP_BANK T1 leg so a switched bank sits behind its cap switch.
-- ===========================================================================
INSERT INTO metadata.network_rules (name, from_feature_class_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'MV_CONDUCTOR -> MV_CAP_BANK T1', mv.id, cb.id, t1.id, NULL, true
FROM metadata.feature_classes mv
JOIN metadata.feature_classes cb ON cb.code = 'MV_CAP_BANK'
JOIN metadata.terminals t1 ON t1.feature_class_id = cb.id AND t1.code = 'T1'
WHERE mv.code = 'MV_CONDUCTOR'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = mv.id AND to_feature_class_id = cb.id AND to_terminal_id = t1.id
  );

-- ===========================================================================
-- Full footprint -- mirrors the MV_RECLOSER seed for the single class.
-- ===========================================================================

-- 1. Circuit membership: MV network stamp + circuit_id field.
UPDATE metadata.feature_classes SET network = 'MV' WHERE code = 'MV_CAP_BANK';

INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, 'circuit_id', 'uuid', NULL, fg.id, true
FROM metadata.feature_classes fc
JOIN metadata.field_groups fg ON fg.code = 'electrical'
WHERE fc.code = 'MV_CAP_BANK'
ON CONFLICT (feature_class_id, name) DO NOTHING;

-- 2. operational_state lifecycle field.
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, 'operational_state', 'text', d.id, fg.id, true
FROM metadata.feature_classes fc
JOIN metadata.domains d       ON d.code  = 'OPERATIONAL_STATE'
JOIN metadata.field_groups fg ON fg.code = 'operational'
WHERE fc.code = 'MV_CAP_BANK'
ON CONFLICT (feature_class_id, name) DO NOTHING;

-- 3. Snapping: MV_CONDUCTOR -> MV_CAP_BANK (point_feature, either side).
INSERT INTO metadata.snapping_rules (from_feature_class_id, to_feature_class_id, to_terminal_id, target_type_value_id, tolerance_m, priority, is_required)
SELECT frm.id, tgt.id, NULL, tt.id, 0.5, 1, false
FROM metadata.feature_classes frm
CROSS JOIN metadata.feature_classes tgt
JOIN metadata.domain_values tt ON tt.code = 'point_feature'
  AND tt.domain_id = (SELECT id FROM metadata.domains WHERE code = 'SNAP_TARGET_TYPE')
WHERE frm.code = 'MV_CONDUCTOR' AND tgt.code = 'MV_CAP_BANK'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.snapping_rules sr
    WHERE sr.from_feature_class_id = frm.id AND sr.to_feature_class_id = tgt.id
      AND sr.to_terminal_id IS NULL
      AND sr.target_type_value_id = tt.id
  );

-- 4. Map symbology -- default (solid diamond, cyan) + device_state = OPEN
--    override (grey, switched-out reads as inactive).
INSERT INTO metadata.symbology_rules (feature_class_id, render_context, style_def)
SELECT fc.id, 'map', '{"icon_ref": "diamond", "color": "#0891b2"}'::jsonb
FROM metadata.feature_classes fc
WHERE fc.code = 'MV_CAP_BANK'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules
    WHERE feature_class_id = fc.id AND subtype_id IS NULL AND domain_value_id IS NULL AND render_context = 'map'
  );

INSERT INTO metadata.symbology_rules (feature_class_id, domain_value_id, render_context, style_def)
SELECT cb.id, dv.id, 'map', '{"icon_ref": "diamond", "color": "#94a3b8"}'::jsonb
FROM metadata.feature_classes cb
JOIN metadata.fields f ON f.feature_class_id = cb.id AND f.name = 'device_state'
JOIN metadata.domain_values dv ON dv.domain_id = f.domain_id AND dv.code = 'OPEN'
WHERE cb.code = 'MV_CAP_BANK'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules
    WHERE feature_class_id = cb.id AND subtype_id IS NULL AND domain_value_id = dv.id AND render_context = 'map'
  );

-- 5. Diagram symbology -- the ansi-capacitor glyph (seed/ansi_stencils.sql).
INSERT INTO metadata.symbology_rules (feature_class_id, render_context, style_def)
SELECT fc.id, 'diagram', '{"icon_ref": "ansi-capacitor", "color": "#0891b2"}'::jsonb
FROM metadata.feature_classes fc
WHERE fc.code = 'MV_CAP_BANK'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules
    WHERE feature_class_id = fc.id AND subtype_id IS NULL AND domain_value_id IS NULL AND render_context = 'diagram'
  );

-- 6. CIM -- LinearShuntCompensator (the CIM class for a shunt cap bank),
--    conducting_junction; rated_kvar maps to ShuntCompensatorInfo.ratedReactivePower
--    (VAr, so scale kVAr -> VAr by 1e3), device_state to the section on/off.
--    NOTE: current CIM has no ShuntCompensator.nomQ (a pre-CIM16 attribute dropped
--    in favour of susceptance). ratedReactivePower is the real, semantically-exact
--    attribute, but it lives on the AssetInfo class ShuntCompensatorInfo; the fully
--    powerflow-correct field is LinearShuntCompensator.bPerSection, which needs a
--    kVAr -> susceptance conversion (Q / nomU^2) we can't express as a declarative
--    transform -- a follow-up if a solver needs the susceptance directly.
INSERT INTO metadata.cim_class_mappings (feature_class_id, subtype_id, cim_class, cim_kind)
SELECT fc.id, NULL, 'LinearShuntCompensator', 'conducting_junction'
FROM metadata.feature_classes fc
WHERE fc.code = 'MV_CAP_BANK'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.cim_class_mappings m
    WHERE m.feature_class_id = fc.id AND m.subtype_id IS NULL
  );

INSERT INTO metadata.cim_attribute_mappings (field_id, cim_attribute, transform, scale_factor, unit)
SELECT f.id, 'ShuntCompensatorInfo.ratedReactivePower', 'scale', 1000, 'VAr'
FROM metadata.fields f
JOIN metadata.feature_classes fc ON fc.id = f.feature_class_id
WHERE fc.code = 'MV_CAP_BANK' AND f.name = 'rated_kvar'
ON CONFLICT (field_id, cim_attribute) DO NOTHING;
