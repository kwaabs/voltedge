-- seed/mv_measuring_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives
-- under /seed on purpose, same reasoning as the other seed files.
-- Phase 27, Slice 3 (MV measuring / instrument transformers). Two classes:
--   * MV_CT -- a current transformer. Measures line current; conceptually
--     in series but NON-interrupting, so it is modelled as a single-terminal
--     tap on the line (T1), an annotation the trace does not walk through --
--     the same attach-and-dead-end shape MV_ARRESTER uses -- rather than an
--     inline T1/T2 node that would inflate the graph with a pass-through the
--     current never actually stops at. NOT a trace barrier.
--   * MV_VT -- a voltage / potential transformer. A SHUNT device line-to-
--     ground, topologically identical to the arrester: T1 taps the line, T2
--     is the earth connection (ground, phase_capable = false), no barrier.
--
-- WHY THESE ARE THEIR OWN CLASSES (and LV CT/VT were not). docs/phase-9's
-- "Decisions made" folds LV service-metering CT/VT into a `ct_ratio`
-- ATTRIBUTE on METER, because there they're an internal detail of a service
-- meter. On the MV side an instrument transformer is standalone yard/bay
-- plant -- a metering-class or protection-class CT on a feeder, a bus VT --
-- with its own location, condition and ratio, so it earns a feature class.
-- This does not contradict the LV decision; it is a different device at a
-- different level. (User-locked: distinct class per device, CT/VT in scope.)
--
-- Reuses the existing CT_RATIO domain (seed/lv_unmetered_ct_example.sql) for
-- MV_CT, and adds a new VT_RATIO domain (mirroring the two VOLTAGE_CLASS
-- levels 11 kV / 33 kV) for MV_VT. Both classes are
-- geometry_optional = true / is_diagram_placeable = true (Phase 27
-- map-or-diagram-or-both model; see seed/mv_switching_example.sql header).
--
-- Depends on seed/connectivity_example.sql (MV_CONDUCTOR). The CT_RATIO /
-- CONDITION_RATING domains + field_groups are re-asserted idempotently so
-- this seed is self-sufficient regardless of whether the LV CT seed ran.
--
-- Apply with psql, then compile both classes:
--   task seed-mv-measuring   (or the psql -f invocation directly)
--   task compile CODE=MV_CT
--   task compile CODE=MV_VT
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL
-- has been reviewed statically -- a live psql apply + the two `task compile`
-- runs are still owed to call it verified.

INSERT INTO metadata.field_groups (code, name, sort_order) VALUES
    ('electrical', 'Electrical', 1),
    ('maintenance', 'Maintenance', 2)
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domains (code, name, domain_type) VALUES
    ('CONDITION_RATING', 'Condition rating', 'coded'),
    ('CT_RATIO', 'CT ratio', 'coded'),
    ('VT_RATIO', 'VT ratio', 'coded')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domain_values (domain_id, code, label, sort_order)
SELECT d.id, v.code, v.label, v.sort_order
FROM metadata.domains d
JOIN (VALUES
    ('CONDITION_RATING', 'GOOD', 'Good', 1),
    ('CONDITION_RATING', 'FAIR', 'Fair', 2),
    ('CONDITION_RATING', 'POOR', 'Poor', 3),
    -- CT_RATIO re-asserted (seeded by the LV CT slice); harmless if present.
    ('CT_RATIO', '100_5',  '100/5 A',  1),
    ('CT_RATIO', '200_5',  '200/5 A',  2),
    ('CT_RATIO', '400_5',  '400/5 A',  3),
    ('CT_RATIO', '800_5',  '800/5 A',  4),
    ('CT_RATIO', '1600_5', '1600/5 A', 5),
    -- VT_RATIO -- new this slice. Secondary is the usual 110 V; the two
    -- primaries mirror the VOLTAGE_CLASS domain (11 kV / 33 kV).
    ('VT_RATIO', '11KV_110', '11 kV / 110 V', 1),
    ('VT_RATIO', '33KV_110', '33 kV / 110 V', 2)
) AS v(domain_code, code, label, sort_order) ON v.domain_code = d.code
ON CONFLICT (domain_id, code) DO NOTHING;

-- ---------------------------------------------------------------------------
-- MV_CT -- junction, single terminal T1 (a tap on the line). Non-interrupting
-- measuring device: attached to the conductor, never walked through. Fields
-- are the CT ratio (reused CT_RATIO domain) and condition. Subtypes name the
-- core class (metering-accuracy vs. protection-class core).
-- ---------------------------------------------------------------------------
INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status, is_diagram_container, is_diagram_placeable, geometry_optional)
VALUES ('MV_CT', 'MV Current Transformer', 'point', 4326, 'junction', 'draft', false, true, true)
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, v.code, v.name
FROM metadata.feature_classes fc
JOIN (VALUES (1, 'metering_ct'), (2, 'protection_ct')) AS v(code, name) ON true
WHERE fc.code = 'MV_CT'
ON CONFLICT (feature_class_id, code) DO NOTHING;

INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, v.nullable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('ct_ratio', 'text', 'CT_RATIO', 'electrical', false),
    ('condition_rating', 'text', 'CONDITION_RATING', 'maintenance', false)
) AS v(name, data_type, domain_code, group_code, nullable) ON true
LEFT JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code = 'MV_CT'
ON CONFLICT (feature_class_id, name) DO NOTHING;

INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, true
FROM metadata.subtypes s
JOIN metadata.fields f ON f.feature_class_id = s.feature_class_id
WHERE s.feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'MV_CT')
  AND f.name IN ('ct_ratio', 'condition_rating')
ON CONFLICT (subtype_id, field_id) DO NOTHING;

INSERT INTO metadata.terminals (feature_class_id, subtype_id, code, role, phase_capable)
SELECT fc.id, NULL, 'T1', 'primary', true
FROM metadata.feature_classes fc
WHERE fc.code = 'MV_CT'
ON CONFLICT (feature_class_id, subtype_id, code) DO NOTHING;

-- ---------------------------------------------------------------------------
-- MV_VT -- junction, shunt line-to-ground (T1 line / T2 ground), same shape
-- as MV_ARRESTER. Measuring only, no barrier. Fields are the VT ratio (new
-- VT_RATIO domain) and condition. Subtypes: inductive VT vs. capacitive
-- (CVT).
-- ---------------------------------------------------------------------------
INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status, is_diagram_container, is_diagram_placeable, geometry_optional)
VALUES ('MV_VT', 'MV Voltage Transformer', 'point', 4326, 'junction', 'draft', false, true, true)
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, v.code, v.name
FROM metadata.feature_classes fc
JOIN (VALUES (1, 'inductive_vt'), (2, 'capacitive_vt')) AS v(code, name) ON true
WHERE fc.code = 'MV_VT'
ON CONFLICT (feature_class_id, code) DO NOTHING;

INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, v.nullable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('vt_ratio', 'text', 'VT_RATIO', 'electrical', false),
    ('condition_rating', 'text', 'CONDITION_RATING', 'maintenance', false)
) AS v(name, data_type, domain_code, group_code, nullable) ON true
LEFT JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code = 'MV_VT'
ON CONFLICT (feature_class_id, name) DO NOTHING;

INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, true
FROM metadata.subtypes s
JOIN metadata.fields f ON f.feature_class_id = s.feature_class_id
WHERE s.feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'MV_VT')
  AND f.name IN ('vt_ratio', 'condition_rating')
ON CONFLICT (subtype_id, field_id) DO NOTHING;

INSERT INTO metadata.terminals (feature_class_id, subtype_id, code, role, phase_capable)
SELECT fc.id, NULL, v.code, v.role, v.phase_capable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('T1', 'primary', true),
    ('T2', 'ground', false)
) AS v(code, role, phase_capable) ON true
WHERE fc.code = 'MV_VT'
ON CONFLICT (feature_class_id, subtype_id, code) DO NOTHING;

-- ---------------------------------------------------------------------------
-- network_rules -- each measuring device is a single-connection tap on the
-- line (T1). No outgoing rule (neither is walked through), no ground rule for
-- the VT's T2 (no earth feature class) -- exactly the MV_ARRESTER pattern.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.network_rules (name, from_feature_class_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'MV_CONDUCTOR -> ' || dev.code || ' T1', mv.id, dev.id, t1.id, NULL, true
FROM metadata.feature_classes mv
JOIN metadata.feature_classes dev ON dev.code IN ('MV_CT', 'MV_VT')
JOIN metadata.terminals t1 ON t1.feature_class_id = dev.id AND t1.code = 'T1'
WHERE mv.code = 'MV_CONDUCTOR'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = mv.id AND to_feature_class_id = dev.id AND to_terminal_id = t1.id
  );

-- No trace_barrier_rules: neither MV_CT nor MV_VT carries device_state.
