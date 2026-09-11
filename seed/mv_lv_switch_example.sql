-- seed/mv_lv_switch_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives
-- under /seed on purpose, same reasoning as the other seed files.
--
-- Two generic switching classes that replace the single DIST_SWITCH:
--   * MV_SWITCH -- a generic MV switch. Junction, two terminals, connects
--     ONLY to MV features (MV_CONDUCTOR), exactly the topology DIST_SWITCH
--     already had (DIST_SWITCH's own network_rules are MV_CONDUCTOR-only --
--     seed/switching_device_example.sql -- so MV_SWITCH is its direct
--     successor; the "migrate DIST_SWITCH -> MV_SWITCH then drop" step is a
--     separate, destructive migration, NOT this file).
--   * LV_SWITCH -- the LV sibling. Same shape, connects ONLY to LV features
--     (LV_CONDUCTOR).
--
-- Both carry an `is_fused` boolean flag (Phase: generic switches). When true,
-- the switch has a protection element: it carries a fuse curve (curve_ref, an
-- ampere-quantity metadata.protection_curves.code, the same field MV_FUSE uses)
-- and the coordination engine grades it as a protective device
-- (api/internal/powerflow/coordination.go's evalFusedSwitch, which defers to the
-- same fuse logic as MV_FUSE once is_fused is set). An OPEN switch (fused or
-- not) is a trace barrier via the shared device_state = OPEN rule -- a blown
-- fuse is modelled as the switch going OPEN, so no separate "operated" state is
-- needed. The barrier rows are attached below AND covered idempotently by
-- seed/trace_barrier_resync.sql.
--
-- WHAT'S NEW vs. DIST_SWITCH: geometry_optional + is_diagram_placeable (like the
-- MV switching classes -- switchgear a crew may place geographically, on a
-- one-line diagram, or both), and the is_fused / curve_ref / fuse_rating_a
-- protection fields. Otherwise byte-for-byte the DIST_SWITCH inline shape:
-- device_state (CLOSED/OPEN) + condition_rating + T1 line / T2 load terminals +
-- the three network_rules (feeder -> T1, -> T2, and Tn -> feeder so a DOWNSTREAM
-- trace walks through) + an OPEN barrier on every barrier-respecting config.
--
-- Depends on seed/connectivity_example.sql (MV_CONDUCTOR),
-- seed/lv_conductors_example.sql (LV_CONDUCTOR), and
-- seed/trace_configurations_example.sql (the configs the barrier rows attach to
-- -- the barrier INSERT is a no-op if those are absent, then
-- seed/trace_barrier_resync.sql backfills them order-independently).
--
-- Apply with psql, then compile both classes:
--   task seed-mv-lv-switch
--   task compile CODE=MV_SWITCH
--   task compile CODE=LV_SWITCH
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL has
-- been reviewed statically -- a live psql apply + the two `task compile` runs +
-- a downstream trace across an OPEN MV_SWITCH + a coordination run against a
-- fused switch are still owed to call it verified.

INSERT INTO metadata.field_groups (code, name, sort_order) VALUES
    ('electrical', 'Electrical', 1),
    ('maintenance', 'Maintenance', 2),
    ('protection', 'Protection', 1)
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
-- MV_SWITCH -- generic MV switch. Junction, two terminals (T1 line-side, T2
-- load-side). Single 'standard' subtype (like PROTECTIVE_RELAY): the fused vs.
-- non-fused distinction is the is_fused boolean, not a subtype, per the design
-- decision to keep it a flag.
-- ===========================================================================
INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status, is_diagram_container, is_diagram_placeable, geometry_optional)
VALUES ('MV_SWITCH', 'MV Switch', 'point', 4326, 'junction', 'draft', false, true, true)
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, v.code, v.name
FROM metadata.feature_classes fc
JOIN (VALUES (1, 'standard')) AS v(code, name) ON true
WHERE fc.code = 'MV_SWITCH'
ON CONFLICT (feature_class_id, code) DO NOTHING;

INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, v.nullable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('device_state',     'text',    'DEVICE_STATE',     'electrical',  false),
    ('condition_rating', 'text',    'CONDITION_RATING', 'maintenance', false),
    ('is_fused',         'boolean', NULL,               'protection',  true),
    ('fuse_rating_a',    'numeric', NULL,               'protection',  true),
    ('curve_ref',        'text',    NULL,               'protection',  true)
) AS v(name, data_type, domain_code, group_code, nullable) ON true
LEFT JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code = 'MV_SWITCH'
ON CONFLICT (feature_class_id, name) DO NOTHING;

-- Only device_state + condition_rating are required; the protection fields are
-- optional (meaningful only when is_fused is true).
INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, true
FROM metadata.subtypes s
JOIN metadata.fields f ON f.feature_class_id = s.feature_class_id
WHERE s.feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'MV_SWITCH')
  AND f.name IN ('device_state', 'condition_rating')
ON CONFLICT (subtype_id, field_id) DO NOTHING;

INSERT INTO metadata.terminals (feature_class_id, subtype_id, code, role, phase_capable)
SELECT fc.id, NULL, v.code, v.role, v.phase_capable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('T1', 'primary', true),
    ('T2', 'secondary', true)
) AS v(code, role, phase_capable) ON true
WHERE fc.code = 'MV_SWITCH'
ON CONFLICT (feature_class_id, subtype_id, code) DO NOTHING;

-- ===========================================================================
-- LV_SWITCH -- generic LV switch. Same shape as MV_SWITCH, connects to
-- LV_CONDUCTOR instead of MV_CONDUCTOR.
-- ===========================================================================
INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status, is_diagram_container, is_diagram_placeable, geometry_optional)
VALUES ('LV_SWITCH', 'LV Switch', 'point', 4326, 'junction', 'draft', false, true, true)
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, v.code, v.name
FROM metadata.feature_classes fc
JOIN (VALUES (1, 'standard')) AS v(code, name) ON true
WHERE fc.code = 'LV_SWITCH'
ON CONFLICT (feature_class_id, code) DO NOTHING;

INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, v.nullable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('device_state',     'text',    'DEVICE_STATE',     'electrical',  false),
    ('condition_rating', 'text',    'CONDITION_RATING', 'maintenance', false),
    ('is_fused',         'boolean', NULL,               'protection',  true),
    ('fuse_rating_a',    'numeric', NULL,               'protection',  true),
    ('curve_ref',        'text',    NULL,               'protection',  true)
) AS v(name, data_type, domain_code, group_code, nullable) ON true
LEFT JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code = 'LV_SWITCH'
ON CONFLICT (feature_class_id, name) DO NOTHING;

INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, true
FROM metadata.subtypes s
JOIN metadata.fields f ON f.feature_class_id = s.feature_class_id
WHERE s.feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'LV_SWITCH')
  AND f.name IN ('device_state', 'condition_rating')
ON CONFLICT (subtype_id, field_id) DO NOTHING;

INSERT INTO metadata.terminals (feature_class_id, subtype_id, code, role, phase_capable)
SELECT fc.id, NULL, v.code, v.role, v.phase_capable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('T1', 'primary', true),
    ('T2', 'secondary', true)
) AS v(code, role, phase_capable) ON true
WHERE fc.code = 'LV_SWITCH'
ON CONFLICT (feature_class_id, subtype_id, code) DO NOTHING;

-- ===========================================================================
-- network_rules -- the inline three-rule shape per switch, keyed to the
-- switch's own network conductor (MV_SWITCH <-> MV_CONDUCTOR, LV_SWITCH <->
-- LV_CONDUCTOR): feeder into T1, feeder into T2, and T2 -> feeder so the switch
-- is the FROM side of its outgoing leg (a DOWNSTREAM trace walks through). One
-- set-generating INSERT per rule shape across the two (switch, conductor)
-- pairs; guarded WHERE NOT EXISTS, since network_rules has no unique key.
-- ===========================================================================

-- Feeder into each switch's line side (T1).
INSERT INTO metadata.network_rules (name, from_feature_class_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT cond.code || ' -> ' || sw.code || ' T1', cond.id, sw.id, t1.id, NULL, true
FROM (VALUES ('MV_SWITCH', 'MV_CONDUCTOR'), ('LV_SWITCH', 'LV_CONDUCTOR')) AS pair(switch_code, cond_code)
JOIN metadata.feature_classes sw ON sw.code = pair.switch_code
JOIN metadata.feature_classes cond ON cond.code = pair.cond_code
JOIN metadata.terminals t1 ON t1.feature_class_id = sw.id AND t1.code = 'T1'
WHERE NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = cond.id AND to_feature_class_id = sw.id AND to_terminal_id = t1.id
  );

-- Feeder into each switch's load side (T2).
INSERT INTO metadata.network_rules (name, from_feature_class_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT cond.code || ' -> ' || sw.code || ' T2', cond.id, sw.id, t2.id, NULL, true
FROM (VALUES ('MV_SWITCH', 'MV_CONDUCTOR'), ('LV_SWITCH', 'LV_CONDUCTOR')) AS pair(switch_code, cond_code)
JOIN metadata.feature_classes sw ON sw.code = pair.switch_code
JOIN metadata.feature_classes cond ON cond.code = pair.cond_code
JOIN metadata.terminals t2 ON t2.feature_class_id = sw.id AND t2.code = 'T2'
WHERE NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = cond.id AND to_feature_class_id = sw.id AND to_terminal_id = t2.id
  );

-- Each switch's load side onward to more conductor (switch as FROM side).
INSERT INTO metadata.network_rules (name, from_feature_class_id, from_terminal_id, to_feature_class_id, phase_rule, is_valid)
SELECT sw.code || ' T2 -> ' || cond.code, sw.id, t2.id, cond.id, NULL, true
FROM (VALUES ('MV_SWITCH', 'MV_CONDUCTOR'), ('LV_SWITCH', 'LV_CONDUCTOR')) AS pair(switch_code, cond_code)
JOIN metadata.feature_classes sw ON sw.code = pair.switch_code
JOIN metadata.feature_classes cond ON cond.code = pair.cond_code
JOIN metadata.terminals t2 ON t2.feature_class_id = sw.id AND t2.code = 'T2'
WHERE NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = sw.id AND from_terminal_id = t2.id AND to_feature_class_id = cond.id
  );

-- ===========================================================================
-- trace_barrier_rules -- halt at an OPEN switch on every barrier-respecting
-- config. Same set-generating shape the MV switching seed uses; a no-op if no
-- barrier-respecting config is seeded yet (seed/trace_barrier_resync.sql then
-- backfills it order-independently).
-- ===========================================================================
INSERT INTO metadata.trace_barrier_rules (trace_configuration_id, feature_class_id, field_id, halts_on_value_id)
SELECT tc.id, sw.id, f.id, dv.id
FROM metadata.feature_classes sw
JOIN metadata.fields f ON f.feature_class_id = sw.id AND f.name = 'device_state'
JOIN metadata.domain_values dv ON dv.domain_id = f.domain_id AND dv.code = 'OPEN'
JOIN metadata.trace_configurations tc ON tc.stop_at_open_devices = true
WHERE sw.code IN ('MV_SWITCH', 'LV_SWITCH')
  AND NOT EXISTS (
    SELECT 1 FROM metadata.trace_barrier_rules r
    WHERE r.trace_configuration_id = tc.id AND r.feature_class_id = sw.id AND r.field_id = f.id AND r.halts_on_value_id = dv.id
  );
