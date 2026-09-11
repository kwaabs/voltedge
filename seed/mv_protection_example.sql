-- seed/mv_protection_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives
-- under /seed on purpose, same reasoning as the other seed files.
-- Phase 27, Slice 2 (MV protection). Two classes:
--   * MV_FUSE     -- an inline MV fuse / drop-out cutout. Two terminals
--     (line/load), exactly the inline DIST_SWITCH shape; a blown or pulled
--     fuse is device_state = OPEN and acts as a trace barrier, so an
--     isolation trace stops at it just like an open breaker.
--   * MV_ARRESTER -- a surge / lightning arrester. A SHUNT device, not an
--     inline one: T1 taps the line, T2 is the earth connection (there is
--     no earth feature class, so T2 carries no network_rule -- it is a
--     documented dead-end to ground, phase_capable = false, mirroring
--     DIST_XFMR's T3 ground terminal). It is always in-circuit -- it has
--     no device_state and is never a trace barrier.
--
-- Both classes are geometry_optional = true / is_diagram_placeable = true,
-- the Phase 27 map-or-diagram-or-both model (see the header of
-- seed/mv_switching_example.sql for why both flags are set explicitly).
--
-- Depends on seed/connectivity_example.sql (MV_CONDUCTOR) having been
-- applied, and on seed/trace_configurations_example.sql for the configs the
-- MV_FUSE barrier rows attach to (the barrier INSERT is a no-op if absent).
--
-- Apply with psql, then compile both classes:
--   task seed-mv-protection   (or the psql -f invocation directly)
--   task compile CODE=MV_FUSE
--   task compile CODE=MV_ARRESTER
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL
-- has been reviewed statically -- a live psql apply + the two `task compile`
-- runs + an isolation-trace check are still owed to call it verified.

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

-- ---------------------------------------------------------------------------
-- MV_FUSE -- junction, two terminals (T1 line-side, T2 load-side). Inline on
-- the feeder like DIST_SWITCH; device_state OPEN models a blown or pulled
-- fuse and is a trace barrier. Subtypes name the interrupting technology.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status, is_diagram_container, is_diagram_placeable, geometry_optional)
VALUES ('MV_FUSE', 'MV Fuse', 'point', 4326, 'junction', 'draft', false, true, true)
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, v.code, v.name
FROM metadata.feature_classes fc
JOIN (VALUES (1, 'expulsion_fuse'), (2, 'current_limiting_fuse')) AS v(code, name) ON true
WHERE fc.code = 'MV_FUSE'
ON CONFLICT (feature_class_id, code) DO NOTHING;

INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, v.nullable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('device_state', 'text', 'DEVICE_STATE', 'electrical', false),
    ('condition_rating', 'text', 'CONDITION_RATING', 'maintenance', false)
) AS v(name, data_type, domain_code, group_code, nullable) ON true
LEFT JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code = 'MV_FUSE'
ON CONFLICT (feature_class_id, name) DO NOTHING;

INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, true
FROM metadata.subtypes s
JOIN metadata.fields f ON f.feature_class_id = s.feature_class_id
WHERE s.feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'MV_FUSE')
  AND f.name IN ('device_state', 'condition_rating')
ON CONFLICT (subtype_id, field_id) DO NOTHING;

INSERT INTO metadata.terminals (feature_class_id, subtype_id, code, role, phase_capable)
SELECT fc.id, NULL, v.code, v.role, v.phase_capable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('T1', 'primary', true),
    ('T2', 'secondary', true)
) AS v(code, role, phase_capable) ON true
WHERE fc.code = 'MV_FUSE'
ON CONFLICT (feature_class_id, subtype_id, code) DO NOTHING;

-- ---------------------------------------------------------------------------
-- MV_ARRESTER -- junction, shunt. T1 taps the line (primary), T2 is the
-- earth connection (ground, phase_capable = false -- like DIST_XFMR.T3).
-- Always in-circuit: NO device_state field, NO barrier. Subtypes name the
-- arrester technology (gapless metal-oxide vs. older gapped silicon-carbide).
-- ---------------------------------------------------------------------------
INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status, is_diagram_container, is_diagram_placeable, geometry_optional)
VALUES ('MV_ARRESTER', 'MV Surge Arrester', 'point', 4326, 'junction', 'draft', false, true, true)
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, v.code, v.name
FROM metadata.feature_classes fc
JOIN (VALUES (1, 'gapless_mov'), (2, 'gapped_sic')) AS v(code, name) ON true
WHERE fc.code = 'MV_ARRESTER'
ON CONFLICT (feature_class_id, code) DO NOTHING;

-- condition_rating only -- an arrester carries no switch state. (Rated /
-- MCOV voltage would be a natural next field but there's no domain for it
-- yet, and inventing one here would be guessing at a deployment's rating
-- set; left for a later slice rather than seeded speculatively.)
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, v.nullable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('condition_rating', 'text', 'CONDITION_RATING', 'maintenance', false)
) AS v(name, data_type, domain_code, group_code, nullable) ON true
LEFT JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code = 'MV_ARRESTER'
ON CONFLICT (feature_class_id, name) DO NOTHING;

INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, true
FROM metadata.subtypes s
JOIN metadata.fields f ON f.feature_class_id = s.feature_class_id
WHERE s.feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'MV_ARRESTER')
  AND f.name IN ('condition_rating')
ON CONFLICT (subtype_id, field_id) DO NOTHING;

INSERT INTO metadata.terminals (feature_class_id, subtype_id, code, role, phase_capable)
SELECT fc.id, NULL, v.code, v.role, v.phase_capable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('T1', 'primary', true),
    ('T2', 'ground', false)
) AS v(code, role, phase_capable) ON true
WHERE fc.code = 'MV_ARRESTER'
ON CONFLICT (feature_class_id, subtype_id, code) DO NOTHING;

-- ---------------------------------------------------------------------------
-- network_rules
-- ---------------------------------------------------------------------------

-- MV_FUSE -- the inline three-rule shape (feeder into T1, into T2, and
-- T2 -> conductor so a DOWNSTREAM trace walks through), same as the Slice 1
-- switching devices.
INSERT INTO metadata.network_rules (name, from_feature_class_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'MV_CONDUCTOR -> MV_FUSE T1', mv.id, fu.id, t1.id, NULL, true
FROM metadata.feature_classes mv CROSS JOIN metadata.feature_classes fu
JOIN metadata.terminals t1 ON t1.feature_class_id = fu.id AND t1.code = 'T1'
WHERE mv.code = 'MV_CONDUCTOR' AND fu.code = 'MV_FUSE'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = mv.id AND to_feature_class_id = fu.id AND to_terminal_id = t1.id
  );

INSERT INTO metadata.network_rules (name, from_feature_class_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'MV_CONDUCTOR -> MV_FUSE T2', mv.id, fu.id, t2.id, NULL, true
FROM metadata.feature_classes mv CROSS JOIN metadata.feature_classes fu
JOIN metadata.terminals t2 ON t2.feature_class_id = fu.id AND t2.code = 'T2'
WHERE mv.code = 'MV_CONDUCTOR' AND fu.code = 'MV_FUSE'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = mv.id AND to_feature_class_id = fu.id AND to_terminal_id = t2.id
  );

INSERT INTO metadata.network_rules (name, from_feature_class_id, from_terminal_id, to_feature_class_id, phase_rule, is_valid)
SELECT 'MV_FUSE T2 -> MV_CONDUCTOR', fu.id, t2.id, mv.id, NULL, true
FROM metadata.feature_classes mv CROSS JOIN metadata.feature_classes fu
JOIN metadata.terminals t2 ON t2.feature_class_id = fu.id AND t2.code = 'T2'
WHERE mv.code = 'MV_CONDUCTOR' AND fu.code = 'MV_FUSE'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = fu.id AND from_terminal_id = t2.id AND to_feature_class_id = mv.id
  );

-- MV_ARRESTER -- one rule only: the line taps into T1. T2 (ground) has no
-- feature to connect to, so no rule; it is a shunt dead-end, not a hop a
-- trace continues through.
INSERT INTO metadata.network_rules (name, from_feature_class_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'MV_CONDUCTOR -> MV_ARRESTER T1', mv.id, ar.id, t1.id, NULL, true
FROM metadata.feature_classes mv CROSS JOIN metadata.feature_classes ar
JOIN metadata.terminals t1 ON t1.feature_class_id = ar.id AND t1.code = 'T1'
WHERE mv.code = 'MV_CONDUCTOR' AND ar.code = 'MV_ARRESTER'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = mv.id AND to_feature_class_id = ar.id AND to_terminal_id = t1.id
  );

-- ---------------------------------------------------------------------------
-- trace_barrier_rules -- MV_FUSE only (the arrester has no device_state).
-- Halt at an OPEN (blown/pulled) fuse on every barrier-respecting config,
-- same stop_at_open_devices = true keying as Slice 1.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.trace_barrier_rules (trace_configuration_id, feature_class_id, field_id, halts_on_value_id)
SELECT tc.id, dev.id, f.id, dv.id
FROM metadata.feature_classes dev
JOIN metadata.fields f ON f.feature_class_id = dev.id AND f.name = 'device_state'
JOIN metadata.domain_values dv ON dv.domain_id = f.domain_id AND dv.code = 'OPEN'
JOIN metadata.trace_configurations tc ON tc.stop_at_open_devices = true
WHERE dev.code = 'MV_FUSE'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.trace_barrier_rules
    WHERE trace_configuration_id = tc.id AND feature_class_id = dev.id AND field_id = f.id AND halts_on_value_id = dv.id
  );
