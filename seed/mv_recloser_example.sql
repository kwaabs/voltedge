-- seed/mv_recloser_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives under
-- /seed on purpose, same reasoning as the other seed files.
--
-- MV_RECLOSER -- a self-contained MV auto-recloser, promoted from what used to be
-- DIST_SWITCH's attribute-less `automatic_recloser` subtype into a first-class
-- feature class with REAL recloser attributes. Unlike the generic MV_SWITCH (a
-- switch whose only protection concept is a single fuse curve via is_fused), a
-- recloser is an active, multi-shot protective device: it trips on a fault,
-- auto-recloses a set number of times over programmed dead-times, and locks out
-- (goes OPEN) if the fault persists -- following a fast/slow curve sequence
-- (fuse-saving). It grades in the coordination engine like a breaker/fuse, off
-- its SLOW curve (api/internal/powerflow/coordination.go's evalRecloser).
--
-- Topology + shape are identical to MV_SWITCH (junction, T1 line / T2 load, MV
-- only, geometry_optional + diagram_placeable, OPEN = trace barrier). What's
-- different is the protection field set:
--   reclose_attempts   integer -- operations to lockout (e.g. 4)
--   fast_curve_ref     text    -- protection_curves.code, the fast/instantaneous curve
--   slow_curve_ref     text    -- protection_curves.code, the delayed/coordinating curve
--   reclose_interval_s numeric -- dead time between shots (representative), seconds
--   reset_time_s       numeric -- time to reset the operation counter, seconds
--
-- This file ships the class AND its full cross-cutting footprint in one go (the
-- MV_SWITCH split its base + parity across two files only because the base
-- shipped first; a brand-new class has no such history): circuit membership,
-- operational lifecycle, snapping, protection_trip, map + diagram symbology, CIM.
-- The recloser reuses the exact diagram glyph (ansi-load-break) and CIM class
-- (Recloser) that DIST_SWITCH's automatic_recloser subtype override used, so the
-- schematic + export identity carries over unchanged.
--
-- Depends on: seed/connectivity_example.sql (MV_CONDUCTOR), and for the FULL
-- footprint also seed/circuit_membership_example.sql (network col migration 0064),
-- seed/operational_state_example.sql (OPERATIONAL_STATE domain + 'operational'
-- group), seed/snapping_rules_example.sql (SNAP_TARGET_TYPE), seed/mv_relay_example.sql
-- (PROTECTIVE_RELAY + migration 0075 kind CHECK), seed/protection_curves_example.sql
-- (the curves fast/slow_curve_ref point at), seed/ansi_stencils.sql
-- (ansi-load-break glyph), seed/cim_mapping_example.sql (cim_profiles), and
-- seed/trace_configurations_example.sql (barrier configs). Every JOIN finds
-- nothing (inserts nothing) if a dependency is absent, so partial application is
-- graceful; seed/trace_barrier_resync.sql backfills any barrier missed by order.
--
-- Apply, then compile:
--   task seed-mv-recloser
--   task compile CODE=MV_RECLOSER
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL has
-- been reviewed statically -- a live psql apply + `task compile CODE=MV_RECLOSER`
-- + a coordination run against a recloser are still owed to call it verified.

INSERT INTO metadata.field_groups (code, name, sort_order) VALUES
    ('electrical', 'Electrical', 1),
    ('maintenance', 'Maintenance', 2),
    ('protection', 'Protection', 1)
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domains (code, name, domain_type) VALUES
    ('DEVICE_STATE', 'Device state', 'coded'),
    ('CONDITION_RATING', 'Condition rating', 'coded'),
    -- Shared with PROTECTIVE_RELAY (seed/protective_relay_example.sql). Asserted
    -- here too so a self-contained recloser apply binds device_number to a
    -- populated domain regardless of seed order.
    ('ANSI_DEVICE_NUMBER', 'ANSI/IEEE device function number', 'coded')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domain_values (domain_id, code, label, sort_order)
SELECT d.id, v.code, v.label, v.sort_order
FROM metadata.domains d
JOIN (VALUES
    ('DEVICE_STATE', 'CLOSED', 'Closed', 1),
    ('DEVICE_STATE', 'OPEN', 'Open', 2),
    ('CONDITION_RATING', 'GOOD', 'Good', 1),
    ('CONDITION_RATING', 'FAIR', 'Fair', 2),
    ('CONDITION_RATING', 'POOR', 'Poor', 3),
    -- ANSI/IEEE device function numbers (mirror of the PROTECTIVE_RELAY seed;
    -- ON CONFLICT DO NOTHING keeps the two in sync without clobbering).
    ('ANSI_DEVICE_NUMBER', '27', '27 - Undervoltage', 1),
    ('ANSI_DEVICE_NUMBER', '59', '59 - Overvoltage', 2),
    ('ANSI_DEVICE_NUMBER', '50_51', '50/51 - Phase overcurrent', 3),
    ('ANSI_DEVICE_NUMBER', '50_51N', '50/51N - Ground overcurrent', 4),
    ('ANSI_DEVICE_NUMBER', '86', '86 - Lockout', 5),
    ('ANSI_DEVICE_NUMBER', '87', '87 - Differential', 6),
    ('ANSI_DEVICE_NUMBER', '25', '25 - Synchronism check', 7),
    ('ANSI_DEVICE_NUMBER', '32', '32 - Directional power', 8),
    ('ANSI_DEVICE_NUMBER', '37', '37 - Undercurrent / underpower', 9),
    ('ANSI_DEVICE_NUMBER', '46', '46 - Negative-sequence / phase unbalance', 10),
    ('ANSI_DEVICE_NUMBER', '49', '49 - Thermal overload', 11),
    ('ANSI_DEVICE_NUMBER', '64', '64 - Earth-fault / restricted earth-fault', 12),
    ('ANSI_DEVICE_NUMBER', '67', '67 - Directional phase overcurrent', 13),
    ('ANSI_DEVICE_NUMBER', '67N', '67N - Directional ground overcurrent', 14),
    ('ANSI_DEVICE_NUMBER', '79', '79 - Auto-reclose', 15),
    ('ANSI_DEVICE_NUMBER', '81', '81 - Over/under frequency', 16)
) AS v(domain_code, code, label, sort_order) ON v.domain_code = d.code
ON CONFLICT (domain_id, code) DO NOTHING;

-- ===========================================================================
-- Class + subtype + fields + terminals.
-- ===========================================================================
INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status, is_diagram_container, is_diagram_placeable, geometry_optional)
VALUES ('MV_RECLOSER', 'MV Recloser', 'point', 4326, 'junction', 'draft', false, true, true)
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, v.code, v.name
FROM metadata.feature_classes fc
JOIN (VALUES (1, 'standard')) AS v(code, name) ON true
WHERE fc.code = 'MV_RECLOSER'
ON CONFLICT (feature_class_id, code) DO NOTHING;

INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, v.nullable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('device_state',       'text',    'DEVICE_STATE',       'electrical',  false),
    ('condition_rating',   'text',    'CONDITION_RATING',   'maintenance', false),
    -- The recloser's PRIMARY ANSI function (coded, like PROTECTIVE_RELAY's). A
    -- self-contained recloser is typically 50/51 + 79; optional (nullable) so a
    -- recloser can be modelled before its settings are known.
    ('device_number',      'text',    'ANSI_DEVICE_NUMBER', 'protection',  true),
    ('reclose_attempts',   'integer', NULL,                 'protection',  true),
    ('fast_curve_ref',     'text',    NULL,               'protection',  true),
    ('slow_curve_ref',     'text',    NULL,               'protection',  true),
    ('reclose_interval_s', 'numeric', NULL,               'protection',  true),
    ('reset_time_s',       'numeric', NULL,               'protection',  true)
) AS v(name, data_type, domain_code, group_code, nullable) ON true
LEFT JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code = 'MV_RECLOSER'
ON CONFLICT (feature_class_id, name) DO NOTHING;

-- Only device_state + condition_rating are required; the recloser-programming
-- fields are optional (a recloser can be modelled before its settings are known).
INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, true
FROM metadata.subtypes s
JOIN metadata.fields f ON f.feature_class_id = s.feature_class_id
WHERE s.feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'MV_RECLOSER')
  AND f.name IN ('device_state', 'condition_rating')
ON CONFLICT (subtype_id, field_id) DO NOTHING;

INSERT INTO metadata.terminals (feature_class_id, subtype_id, code, role, phase_capable)
SELECT fc.id, NULL, v.code, v.role, v.phase_capable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('T1', 'primary', true),
    ('T2', 'secondary', true)
) AS v(code, role, phase_capable) ON true
WHERE fc.code = 'MV_RECLOSER'
ON CONFLICT (feature_class_id, subtype_id, code) DO NOTHING;

-- ===========================================================================
-- network_rules -- inline three-rule shape (MV_CONDUCTOR <-> MV_RECLOSER):
-- feeder into T1, feeder into T2, and T2 -> feeder so a DOWNSTREAM trace walks
-- through. Guarded WHERE NOT EXISTS (network_rules has no unique key).
-- ===========================================================================
INSERT INTO metadata.network_rules (name, from_feature_class_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'MV_CONDUCTOR -> MV_RECLOSER T1', cond.id, sw.id, t1.id, NULL, true
FROM metadata.feature_classes sw
JOIN metadata.feature_classes cond ON cond.code = 'MV_CONDUCTOR'
JOIN metadata.terminals t1 ON t1.feature_class_id = sw.id AND t1.code = 'T1'
WHERE sw.code = 'MV_RECLOSER'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = cond.id AND to_feature_class_id = sw.id AND to_terminal_id = t1.id
  );

INSERT INTO metadata.network_rules (name, from_feature_class_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'MV_CONDUCTOR -> MV_RECLOSER T2', cond.id, sw.id, t2.id, NULL, true
FROM metadata.feature_classes sw
JOIN metadata.feature_classes cond ON cond.code = 'MV_CONDUCTOR'
JOIN metadata.terminals t2 ON t2.feature_class_id = sw.id AND t2.code = 'T2'
WHERE sw.code = 'MV_RECLOSER'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = cond.id AND to_feature_class_id = sw.id AND to_terminal_id = t2.id
  );

INSERT INTO metadata.network_rules (name, from_feature_class_id, from_terminal_id, to_feature_class_id, phase_rule, is_valid)
SELECT 'MV_RECLOSER T2 -> MV_CONDUCTOR', sw.id, t2.id, cond.id, NULL, true
FROM metadata.feature_classes sw
JOIN metadata.feature_classes cond ON cond.code = 'MV_CONDUCTOR'
JOIN metadata.terminals t2 ON t2.feature_class_id = sw.id AND t2.code = 'T2'
WHERE sw.code = 'MV_RECLOSER'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = sw.id AND from_terminal_id = t2.id AND to_feature_class_id = cond.id
  );

-- ===========================================================================
-- trace_barrier_rules -- lockout is modelled as OPEN, so an OPEN recloser halts
-- a barrier-respecting trace. seed/trace_barrier_resync.sql also covers this.
-- ===========================================================================
INSERT INTO metadata.trace_barrier_rules (trace_configuration_id, feature_class_id, field_id, halts_on_value_id)
SELECT tc.id, sw.id, f.id, dv.id
FROM metadata.feature_classes sw
JOIN metadata.fields f ON f.feature_class_id = sw.id AND f.name = 'device_state'
JOIN metadata.domain_values dv ON dv.domain_id = f.domain_id AND dv.code = 'OPEN'
JOIN metadata.trace_configurations tc ON tc.stop_at_open_devices = true
WHERE sw.code = 'MV_RECLOSER'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.trace_barrier_rules r
    WHERE r.trace_configuration_id = tc.id AND r.feature_class_id = sw.id AND r.field_id = f.id AND r.halts_on_value_id = dv.id
  );

-- ===========================================================================
-- Full footprint -- mirrors the MV_SWITCH parity seed for the single class.
-- ===========================================================================

-- 1. Circuit membership: MV network stamp + circuit_id field.
UPDATE metadata.feature_classes SET network = 'MV' WHERE code = 'MV_RECLOSER';

INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, 'circuit_id', 'uuid', NULL, fg.id, true
FROM metadata.feature_classes fc
JOIN metadata.field_groups fg ON fg.code = 'electrical'
WHERE fc.code = 'MV_RECLOSER'
ON CONFLICT (feature_class_id, name) DO NOTHING;

-- 2. operational_state lifecycle field.
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, 'operational_state', 'text', d.id, fg.id, true
FROM metadata.feature_classes fc
JOIN metadata.domains d       ON d.code  = 'OPERATIONAL_STATE'
JOIN metadata.field_groups fg ON fg.code = 'operational'
WHERE fc.code = 'MV_RECLOSER'
ON CONFLICT (feature_class_id, name) DO NOTHING;

-- 3. Snapping: MV_CONDUCTOR -> MV_RECLOSER (point_feature, either side).
INSERT INTO metadata.snapping_rules (from_feature_class_id, to_feature_class_id, to_terminal_id, target_type_value_id, tolerance_m, priority, is_required)
SELECT frm.id, tgt.id, NULL, tt.id, 0.5, 1, false
FROM metadata.feature_classes frm
CROSS JOIN metadata.feature_classes tgt
JOIN metadata.domain_values tt ON tt.code = 'point_feature'
  AND tt.domain_id = (SELECT id FROM metadata.domains WHERE code = 'SNAP_TARGET_TYPE')
WHERE frm.code = 'MV_CONDUCTOR' AND tgt.code = 'MV_RECLOSER'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.snapping_rules sr
    WHERE sr.from_feature_class_id = frm.id AND sr.to_feature_class_id = tgt.id
      AND sr.to_terminal_id IS NULL
      AND sr.target_type_value_id = tt.id
  );

-- 4. protection_trip: PROTECTIVE_RELAY trips MV_RECLOSER (a recloser may be
--    relay-controlled as well as self-contained).
INSERT INTO metadata.relationship_classes (name, origin_feature_class_id, destination_feature_class_id, kind, cardinality)
SELECT 'PROTECTIVE_RELAY trips ' || dev.code, relay.id, dev.id, 'protection_trip', 'many_to_many'
FROM metadata.feature_classes relay
JOIN metadata.feature_classes dev ON dev.code = 'MV_RECLOSER'
WHERE relay.code = 'PROTECTIVE_RELAY'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.relationship_classes
    WHERE origin_feature_class_id = relay.id AND destination_feature_class_id = dev.id AND kind = 'protection_trip'
  );

-- 5. Map symbology -- default (solid triangle, amber to read distinct from a
--    plain switch) + device_state = OPEN override (hollow triangle, red).
INSERT INTO metadata.symbology_rules (feature_class_id, render_context, style_def)
SELECT fc.id, 'map', '{"icon_ref": "triangle", "color": "#d97706"}'::jsonb
FROM metadata.feature_classes fc
WHERE fc.code = 'MV_RECLOSER'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules
    WHERE feature_class_id = fc.id AND subtype_id IS NULL AND domain_value_id IS NULL AND render_context = 'map'
  );

INSERT INTO metadata.symbology_rules (feature_class_id, domain_value_id, render_context, style_def)
SELECT sw.id, dv.id, 'map', '{"icon_ref": "triangle-outline", "color": "#dc2626"}'::jsonb
FROM metadata.feature_classes sw
JOIN metadata.fields f ON f.feature_class_id = sw.id AND f.name = 'device_state'
JOIN metadata.domain_values dv ON dv.domain_id = f.domain_id AND dv.code = 'OPEN'
WHERE sw.code = 'MV_RECLOSER'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules
    WHERE feature_class_id = sw.id AND subtype_id IS NULL AND domain_value_id = dv.id AND render_context = 'map'
  );

-- 6. Diagram symbology -- ansi-load-break (the recloser reads as a load-break
--    device, exactly the glyph DIST_SWITCH's automatic_recloser subtype used).
INSERT INTO metadata.symbology_rules (feature_class_id, render_context, style_def)
SELECT fc.id, 'diagram', '{"icon_ref": "ansi-load-break", "color": "#d97706"}'::jsonb
FROM metadata.feature_classes fc
WHERE fc.code = 'MV_RECLOSER'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules
    WHERE feature_class_id = fc.id AND subtype_id IS NULL AND domain_value_id IS NULL AND render_context = 'diagram'
  );

-- 7. CIM -- Recloser (a ProtectedSwitch), conducting_junction, matching the CIM
--    class DIST_SWITCH's recloser subtype override mapped to; + Switch.normalOpen.
INSERT INTO metadata.cim_class_mappings (feature_class_id, subtype_id, cim_class, cim_kind)
SELECT fc.id, NULL, 'Recloser', 'conducting_junction'
FROM metadata.feature_classes fc
WHERE fc.code = 'MV_RECLOSER'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.cim_class_mappings m
    WHERE m.feature_class_id = fc.id AND m.subtype_id IS NULL
  );

INSERT INTO metadata.cim_attribute_mappings (field_id, cim_attribute, transform, value_map, unit)
SELECT f.id, 'Switch.normalOpen', 'boolean', '{"OPEN":true,"CLOSED":false}'::jsonb, NULL
FROM metadata.fields f
JOIN metadata.feature_classes fc ON fc.id = f.feature_class_id
WHERE fc.code = 'MV_RECLOSER' AND f.name = 'device_state'
ON CONFLICT (field_id, cim_attribute) DO NOTHING;
