-- seed/lv_devices_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives
-- under /seed on purpose, same reasoning as the other seed files.
-- Phase 9, Slice 2 (LV devices). Two junction classes that sit inline on
-- the LV network the same way DIST_SWITCH sits inline on the MV network
-- (seed/switching_device_example.sql) -- so this pass needs no new engine
-- mechanism, just new metadata over the existing device_state barrier and
-- the LOOP_CHECK/isolation traces:
--   * SERVICE_CUTOUT -- the intake service fuse. Two terminals (line/load),
--     so it can be spliced into the LV run just ahead of a service point.
--   * LV_LINK_BOX    -- the feeder pillar / LV sectionalising cabinet.
--     A multi-way node: one incoming way plus three switchable outgoing
--     ways, so a normally-open way models the standard LV meshed-network
--     "normally open point" that loop detection has to reason about.
--
-- Both carry a device_state field (CLOSED/OPEN) and are wired as OPEN
-- barriers on the two barrier-respecting trace configs, exactly like
-- DIST_SWITCH -- so an isolation trace stops at an open cutout or an open
-- link-box way.
--
-- HONEST LIMITATION -- the barrier is FEATURE-level, not per-way. A
-- trace_barrier_rules row halts on the *feature's* device_state, so
-- "way T3 of this link box is open" can't be expressed as a per-terminal
-- barrier here. The way that models a normally-open point is instead
-- expressed by the *absence* of a seeded connection on that way (an open
-- way simply isn't wired), and whole-device isolation by the feature-level
-- device_state = OPEN barrier below. Per-way NOP state is a later slice's
-- concern (it needs a per-terminal state model the schema doesn't have
-- yet); noting it here so the limitation is on the record, not silent.
--
-- Depends on seed/lv_conductors_example.sql (LV_CONDUCTOR) and
-- seed/service_point_example.sql (SERVICE_POINT) having been applied, and
-- on seed/trace_configurations_example.sql for the DOWNSTREAM_TRACE /
-- UPSTREAM_TRACE configs the barrier rows attach to (the barrier INSERT is
-- a no-op if those configs are absent).
--
-- Apply with psql, then compile both classes:
--   task seed-lv-devices   (or the psql -f invocation directly)
--   task compile CODE=SERVICE_CUTOUT
--   task compile CODE=LV_LINK_BOX
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL
-- has been reviewed statically -- a live psql apply + the two `task
-- compile` runs + an isolation-trace check are still owed to call it
-- verified.

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

-- SERVICE_CUTOUT -- junction, two terminals (T1 line-side, T2 load-side),
-- so it sits inline on the LV run just ahead of a service point, the same
-- inline shape DIST_SWITCH uses on MV. Subtypes distinguish the physical
-- kind: a plain fuse carrier vs. a switch-fuse (a fused switch, operable
-- as a disconnector) -- not acted on by the engine yet, present so a later
-- slice keys off a real field.

INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status, is_diagram_container)
VALUES ('SERVICE_CUTOUT', 'Service Cutout', 'point', 4326, 'junction', 'draft', false)
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, v.code, v.name
FROM metadata.feature_classes fc
JOIN (VALUES (1, 'fuse_carrier'), (2, 'switch_fuse')) AS v(code, name) ON true
WHERE fc.code = 'SERVICE_CUTOUT'
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
WHERE fc.code = 'SERVICE_CUTOUT'
ON CONFLICT (feature_class_id, name) DO NOTHING;

INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, true
FROM metadata.subtypes s
JOIN metadata.fields f ON f.feature_class_id = s.feature_class_id
WHERE s.feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'SERVICE_CUTOUT')
  AND f.name IN ('device_state', 'condition_rating')
ON CONFLICT (subtype_id, field_id) DO NOTHING;

-- role is constrained to primary/secondary/ground/other (migrations/0009);
-- T1/T2 map onto primary/secondary the same way DIST_SWITCH's do -- the
-- column's real job is just distinguishing the line and load sides.
INSERT INTO metadata.terminals (feature_class_id, subtype_id, code, role, phase_capable)
SELECT fc.id, NULL, v.code, v.role, v.phase_capable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('T1', 'primary', true),
    ('T2', 'secondary', true)
) AS v(code, role, phase_capable) ON true
WHERE fc.code = 'SERVICE_CUTOUT'
ON CONFLICT (feature_class_id, subtype_id, code) DO NOTHING;

-- LV_LINK_BOX -- junction, four terminals: T1 the incoming way (primary),
-- T2/T3/T4 the switchable outgoing ways ('other', since there's no
-- line/load asymmetry among peers). A multi-way node -- the feeder pillar
-- that fans an LV main out to several streets, or the sectionalising
-- cabinet whose normally-open way ties two feeders. Subtypes name the two
-- physical kinds.

INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status, is_diagram_container)
VALUES ('LV_LINK_BOX', 'LV Link Box', 'point', 4326, 'junction', 'draft', false)
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, v.code, v.name
FROM metadata.feature_classes fc
JOIN (VALUES (1, 'feeder_pillar'), (2, 'link_box')) AS v(code, name) ON true
WHERE fc.code = 'LV_LINK_BOX'
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
WHERE fc.code = 'LV_LINK_BOX'
ON CONFLICT (feature_class_id, name) DO NOTHING;

INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, true
FROM metadata.subtypes s
JOIN metadata.fields f ON f.feature_class_id = s.feature_class_id
WHERE s.feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'LV_LINK_BOX')
  AND f.name IN ('device_state', 'condition_rating')
ON CONFLICT (subtype_id, field_id) DO NOTHING;

INSERT INTO metadata.terminals (feature_class_id, subtype_id, code, role, phase_capable)
SELECT fc.id, NULL, v.code, v.role, v.phase_capable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('T1', 'primary', true),
    ('T2', 'other', true),
    ('T3', 'other', true),
    ('T4', 'other', true)
) AS v(code, role, phase_capable) ON true
WHERE fc.code = 'LV_LINK_BOX'
ON CONFLICT (feature_class_id, subtype_id, code) DO NOTHING;

-- network_rules -- guarded with WHERE NOT EXISTS, same as the other seeds.
-- All phase_rule NULL: these are device hops, not the phase-matched
-- edge-to-edge splice the LV_CONDUCTOR -> LV_CONDUCTOR rule gates.

-- LV run into the cutout's line side.
INSERT INTO metadata.network_rules (name, from_feature_class_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'LV_CONDUCTOR -> SERVICE_CUTOUT T1', lv.id, sc.id, t1.id, NULL, true
FROM metadata.feature_classes lv CROSS JOIN metadata.feature_classes sc
JOIN metadata.terminals t1 ON t1.feature_class_id = sc.id AND t1.code = 'T1'
WHERE lv.code = 'LV_CONDUCTOR' AND sc.code = 'SERVICE_CUTOUT'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = lv.id AND to_feature_class_id = sc.id AND to_terminal_id = t1.id
  );

-- Cutout's load side onward -- either to more LV conductor, or straight
-- to the service point it protects.
INSERT INTO metadata.network_rules (name, from_feature_class_id, from_terminal_id, to_feature_class_id, phase_rule, is_valid)
SELECT 'SERVICE_CUTOUT T2 -> LV_CONDUCTOR', sc.id, t2.id, lv.id, NULL, true
FROM metadata.feature_classes lv CROSS JOIN metadata.feature_classes sc
JOIN metadata.terminals t2 ON t2.feature_class_id = sc.id AND t2.code = 'T2'
WHERE lv.code = 'LV_CONDUCTOR' AND sc.code = 'SERVICE_CUTOUT'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = sc.id AND from_terminal_id = t2.id AND to_feature_class_id = lv.id
  );

INSERT INTO metadata.network_rules (name, from_feature_class_id, from_terminal_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'SERVICE_CUTOUT T2 -> SERVICE_POINT T1', sc.id, t2.id, sp.id, t1.id, NULL, true
FROM metadata.feature_classes sc CROSS JOIN metadata.feature_classes sp
JOIN metadata.terminals t2 ON t2.feature_class_id = sc.id AND t2.code = 'T2'
JOIN metadata.terminals t1 ON t1.feature_class_id = sp.id AND t1.code = 'T1'
WHERE sc.code = 'SERVICE_CUTOUT' AND sp.code = 'SERVICE_POINT'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = sc.id AND from_terminal_id = t2.id AND to_feature_class_id = sp.id AND to_terminal_id = t1.id
  );

-- LV run into the link box's incoming way (T1).
INSERT INTO metadata.network_rules (name, from_feature_class_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'LV_CONDUCTOR -> LV_LINK_BOX T1', lv.id, lb.id, t1.id, NULL, true
FROM metadata.feature_classes lv CROSS JOIN metadata.feature_classes lb
JOIN metadata.terminals t1 ON t1.feature_class_id = lb.id AND t1.code = 'T1'
WHERE lv.code = 'LV_CONDUCTOR' AND lb.code = 'LV_LINK_BOX'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = lv.id AND to_feature_class_id = lb.id AND to_terminal_id = t1.id
  );

-- Each of the link box's outgoing ways (T2/T3/T4) can feed onward LV
-- conductor -- one set-generating INSERT rather than three near-identical
-- ones. An outgoing way left unwired IS the normally-open point (see the
-- header's honest-limitation note).
INSERT INTO metadata.network_rules (name, from_feature_class_id, from_terminal_id, to_feature_class_id, phase_rule, is_valid)
SELECT 'LV_LINK_BOX ' || t.code || ' -> LV_CONDUCTOR', lb.id, t.id, lv.id, NULL, true
FROM metadata.feature_classes lb CROSS JOIN metadata.feature_classes lv
JOIN metadata.terminals t ON t.feature_class_id = lb.id AND t.code IN ('T2', 'T3', 'T4')
WHERE lb.code = 'LV_LINK_BOX' AND lv.code = 'LV_CONDUCTOR'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = lb.id AND from_terminal_id = t.id AND to_feature_class_id = lv.id
  );

-- trace_barrier_rules -- halt an isolation trace at an OPEN cutout or link
-- box, exactly like the DIST_SWITCH barrier
-- (seed/trace_configurations_example.sql). Attached only to the two
-- configs that respect barriers (DOWNSTREAM_TRACE / UPSTREAM_TRACE, both
-- stop_at_open_devices = true) -- attaching to CONNECTED/SHORTEST_PATH
-- would be inert. One set-generating INSERT over the 2 devices x 2 configs
-- (4 rows); a no-op if the trace configs aren't seeded yet.
INSERT INTO metadata.trace_barrier_rules (trace_configuration_id, feature_class_id, field_id, halts_on_value_id)
SELECT tc.id, dev.id, f.id, dv.id
FROM metadata.feature_classes dev
JOIN metadata.fields f ON f.feature_class_id = dev.id AND f.name = 'device_state'
JOIN metadata.domain_values dv ON dv.domain_id = f.domain_id AND dv.code = 'OPEN'
JOIN metadata.trace_configurations tc ON tc.code IN ('DOWNSTREAM_TRACE', 'UPSTREAM_TRACE')
WHERE dev.code IN ('SERVICE_CUTOUT', 'LV_LINK_BOX')
  AND NOT EXISTS (
    SELECT 1 FROM metadata.trace_barrier_rules
    WHERE trace_configuration_id = tc.id AND feature_class_id = dev.id AND field_id = f.id AND halts_on_value_id = dv.id
  );
