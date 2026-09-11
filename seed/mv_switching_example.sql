-- seed/mv_switching_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives
-- under /seed on purpose, same reasoning as the other seed files.
-- Phase 27, Slice 1 (MV switching devices). Three junction classes that
-- sit inline on the MV network exactly the way DIST_SWITCH already does
-- (seed/switching_device_example.sql) -- so this pass needs no new engine
-- mechanism, just new metadata over the existing device_state barrier and
-- the DOWNSTREAM/UPSTREAM/target-class traces:
--   * MV_BREAKER       -- the substation / feeder circuit breaker. Two
--     terminals (line/load), interrupts fault current. OPEN = barrier.
--   * MV_ISOLATOR      -- the off-load isolator / disconnector. Two
--     terminals, provides a visible isolating gap. OPEN = barrier.
--   * MV_SECTIONALIZER -- the auto-sectionalizer that counts fault
--     passages and opens on a dead line to isolate a faulted section.
--     Two terminals. OPEN = barrier.
-- (The recloser already lives on DIST_SWITCH as its automatic_recloser
-- subtype -- seed/switching_device_example.sql -- so it is deliberately
-- NOT duplicated here.)
--
-- WHAT'S NEW vs. the LV/MV device seeds this copies:
--   geometry_optional = true and is_diagram_placeable = true on every
--   class. These are substation/feeder switchgear that a crew may place
--   geographically (a pole-mounted feeder breaker) OR only on a one-line
--   diagram (a bay breaker inside a yard) OR both. geometry_optional (see
--   migrations/0052) lets an individual feature exist with NULL geom, and
--   the compiler emits `geom ... ` WITHOUT NOT NULL on first CREATE TABLE
--   when the flag is set (api/internal/compiler/ddl.go). is_diagram_placeable
--   (migrations/0032) must be set explicitly here: 0032's backfill only
--   flipped rows that existed when it ran, so a class inserted afterwards
--   takes the column DEFAULT false unless we say otherwise (the same reason
--   seed/ansi_stencils.sql had to flip it for PROTECTIVE_RELAY).
--
-- Both new knobs aside, the shape is byte-for-byte the DIST_SWITCH pattern:
-- device_state (CLOSED/OPEN) field + T1 line / T2 load terminals + the
-- three network_rules (MV_CONDUCTOR -> T1, -> T2, and Tn -> MV_CONDUCTOR
-- so a DOWNSTREAM trace can walk through the device) + an OPEN barrier row
-- on every barrier-respecting trace config.
--
-- Depends on seed/connectivity_example.sql (MV_CONDUCTOR) having been
-- applied, and on seed/trace_configurations_example.sql for the trace
-- configs the barrier rows attach to (the barrier INSERT is a no-op if
-- those configs are absent).
--
-- Apply with psql, then compile all three classes:
--   task seed-mv-switching   (or the psql -f invocation directly)
--   task compile CODE=MV_BREAKER
--   task compile CODE=MV_ISOLATOR
--   task compile CODE=MV_SECTIONALIZER
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL
-- has been reviewed statically -- a live psql apply + the three `task
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

-- ---------------------------------------------------------------------------
-- MV_BREAKER -- junction, two terminals (T1 line-side, T2 load-side).
-- Interrupts fault current; the workhorse protective switch of a substation
-- bay or a feeder head. Subtypes name the interrupting medium (not acted on
-- by the engine yet, present so a later slice keys off a real field).
-- ---------------------------------------------------------------------------
INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status, is_diagram_container, is_diagram_placeable, geometry_optional)
VALUES ('MV_BREAKER', 'MV Breaker', 'point', 4326, 'junction', 'draft', false, true, true)
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, v.code, v.name
FROM metadata.feature_classes fc
JOIN (VALUES (1, 'vacuum_breaker'), (2, 'sf6_breaker')) AS v(code, name) ON true
WHERE fc.code = 'MV_BREAKER'
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
WHERE fc.code = 'MV_BREAKER'
ON CONFLICT (feature_class_id, name) DO NOTHING;

INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, true
FROM metadata.subtypes s
JOIN metadata.fields f ON f.feature_class_id = s.feature_class_id
WHERE s.feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'MV_BREAKER')
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
WHERE fc.code = 'MV_BREAKER'
ON CONFLICT (feature_class_id, subtype_id, code) DO NOTHING;

-- ---------------------------------------------------------------------------
-- MV_ISOLATOR -- junction, two terminals. An off-load disconnector: opened
-- only when de-energised, provides a visible isolating gap for safe working.
-- Same inline topological shape as the breaker. Subtypes name the physical
-- break arrangement.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status, is_diagram_container, is_diagram_placeable, geometry_optional)
VALUES ('MV_ISOLATOR', 'MV Isolator', 'point', 4326, 'junction', 'draft', false, true, true)
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, v.code, v.name
FROM metadata.feature_classes fc
JOIN (VALUES (1, 'single_break'), (2, 'double_break')) AS v(code, name) ON true
WHERE fc.code = 'MV_ISOLATOR'
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
WHERE fc.code = 'MV_ISOLATOR'
ON CONFLICT (feature_class_id, name) DO NOTHING;

INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, true
FROM metadata.subtypes s
JOIN metadata.fields f ON f.feature_class_id = s.feature_class_id
WHERE s.feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'MV_ISOLATOR')
  AND f.name IN ('device_state', 'condition_rating')
ON CONFLICT (subtype_id, field_id) DO NOTHING;

INSERT INTO metadata.terminals (feature_class_id, subtype_id, code, role, phase_capable)
SELECT fc.id, NULL, v.code, v.role, v.phase_capable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('T1', 'primary', true),
    ('T2', 'secondary', true)
) AS v(code, role, phase_capable) ON true
WHERE fc.code = 'MV_ISOLATOR'
ON CONFLICT (feature_class_id, subtype_id, code) DO NOTHING;

-- ---------------------------------------------------------------------------
-- MV_SECTIONALIZER -- junction, two terminals. Counts fault-current
-- passages while an upstream recloser/breaker is clearing, then opens
-- during a dead interval to isolate a faulted section. Topologically inline
-- like the others. Subtypes name the counting mechanism.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status, is_diagram_container, is_diagram_placeable, geometry_optional)
VALUES ('MV_SECTIONALIZER', 'MV Sectionalizer', 'point', 4326, 'junction', 'draft', false, true, true)
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, v.code, v.name
FROM metadata.feature_classes fc
JOIN (VALUES (1, 'hydraulic'), (2, 'electronic')) AS v(code, name) ON true
WHERE fc.code = 'MV_SECTIONALIZER'
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
WHERE fc.code = 'MV_SECTIONALIZER'
ON CONFLICT (feature_class_id, name) DO NOTHING;

INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, true
FROM metadata.subtypes s
JOIN metadata.fields f ON f.feature_class_id = s.feature_class_id
WHERE s.feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'MV_SECTIONALIZER')
  AND f.name IN ('device_state', 'condition_rating')
ON CONFLICT (subtype_id, field_id) DO NOTHING;

INSERT INTO metadata.terminals (feature_class_id, subtype_id, code, role, phase_capable)
SELECT fc.id, NULL, v.code, v.role, v.phase_capable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('T1', 'primary', true),
    ('T2', 'secondary', true)
) AS v(code, role, phase_capable) ON true
WHERE fc.code = 'MV_SECTIONALIZER'
ON CONFLICT (feature_class_id, subtype_id, code) DO NOTHING;

-- ---------------------------------------------------------------------------
-- network_rules -- for each device, the three-rule DIST_SWITCH shape:
--   MV_CONDUCTOR -> device T1   (feeder into the line side)
--   MV_CONDUCTOR -> device T2   (feeder into the load side)
--   device T2    -> MV_CONDUCTOR (device is the FROM side of its outgoing
--                                 leg, so a DOWNSTREAM trace walks through)
-- All phase_rule NULL: device hops, not the phase-matched edge splice.
-- One set-generating INSERT per rule shape across the three device classes
-- rather than nine near-identical statements; guarded WHERE NOT EXISTS,
-- since network_rules has no unique key.
-- ---------------------------------------------------------------------------

-- Feeder into each device's line side (T1).
INSERT INTO metadata.network_rules (name, from_feature_class_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'MV_CONDUCTOR -> ' || dev.code || ' T1', mv.id, dev.id, t1.id, NULL, true
FROM metadata.feature_classes mv
JOIN metadata.feature_classes dev ON dev.code IN ('MV_BREAKER', 'MV_ISOLATOR', 'MV_SECTIONALIZER')
JOIN metadata.terminals t1 ON t1.feature_class_id = dev.id AND t1.code = 'T1'
WHERE mv.code = 'MV_CONDUCTOR'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = mv.id AND to_feature_class_id = dev.id AND to_terminal_id = t1.id
  );

-- Feeder into each device's load side (T2).
INSERT INTO metadata.network_rules (name, from_feature_class_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'MV_CONDUCTOR -> ' || dev.code || ' T2', mv.id, dev.id, t2.id, NULL, true
FROM metadata.feature_classes mv
JOIN metadata.feature_classes dev ON dev.code IN ('MV_BREAKER', 'MV_ISOLATOR', 'MV_SECTIONALIZER')
JOIN metadata.terminals t2 ON t2.feature_class_id = dev.id AND t2.code = 'T2'
WHERE mv.code = 'MV_CONDUCTOR'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = mv.id AND to_feature_class_id = dev.id AND to_terminal_id = t2.id
  );

-- Each device's load side onward to more conductor (device as FROM side).
INSERT INTO metadata.network_rules (name, from_feature_class_id, from_terminal_id, to_feature_class_id, phase_rule, is_valid)
SELECT dev.code || ' T2 -> MV_CONDUCTOR', dev.id, t2.id, mv.id, NULL, true
FROM metadata.feature_classes mv
JOIN metadata.feature_classes dev ON dev.code IN ('MV_BREAKER', 'MV_ISOLATOR', 'MV_SECTIONALIZER')
JOIN metadata.terminals t2 ON t2.feature_class_id = dev.id AND t2.code = 'T2'
WHERE mv.code = 'MV_CONDUCTOR'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = dev.id AND from_terminal_id = t2.id AND to_feature_class_id = mv.id
  );

-- ---------------------------------------------------------------------------
-- trace_barrier_rules -- halt an isolation trace at an OPEN breaker /
-- isolator / sectionalizer, exactly like the DIST_SWITCH barrier. Attached
-- to every barrier-respecting config (stop_at_open_devices = true) rather
-- than a hardcoded config list, so the two target-class traces
-- (TRACE_TO_TRANSFORMER / LV_NETWORK_TRACE) that also respect barriers are
-- covered without naming them -- a fault upstream of an open MV device
-- should not be reported as reaching the transformer through it. One
-- set-generating INSERT over the 3 devices x N barrier configs; a no-op if
-- no barrier-respecting config is seeded yet.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.trace_barrier_rules (trace_configuration_id, feature_class_id, field_id, halts_on_value_id)
SELECT tc.id, dev.id, f.id, dv.id
FROM metadata.feature_classes dev
JOIN metadata.fields f ON f.feature_class_id = dev.id AND f.name = 'device_state'
JOIN metadata.domain_values dv ON dv.domain_id = f.domain_id AND dv.code = 'OPEN'
JOIN metadata.trace_configurations tc ON tc.stop_at_open_devices = true
WHERE dev.code IN ('MV_BREAKER', 'MV_ISOLATOR', 'MV_SECTIONALIZER')
  AND NOT EXISTS (
    SELECT 1 FROM metadata.trace_barrier_rules
    WHERE trace_configuration_id = tc.id AND feature_class_id = dev.id AND field_id = f.id AND halts_on_value_id = dv.id
  );
