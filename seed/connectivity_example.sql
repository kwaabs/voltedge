-- seed/connectivity_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives
-- under /seed on purpose, same reasoning as seed/pole_example.sql.
-- Demo metadata for MV_CONDUCTOR (edge) and DIST_XFMR (junction), the
-- network-participant sample configurations from
-- docs/metadata-core.md's "Sample configurations" section, minimal to
-- what's needed to exercise api/internal/connectivity end to end: a
-- valid rule into terminal T1, a disabled rule into T3 (rule exists but
-- is_valid = false), and T2 deliberately left with no rule row at all --
-- the three outcomes the zero-trust matcher has to tell apart.
--
-- Apply with psql after migrations 0001-0016 have run, then compile
-- both classes:
--   task seed-connectivity   (or the psql -f invocation directly)
--   task compile CODE=MV_CONDUCTOR
--   task compile CODE=DIST_XFMR

INSERT INTO metadata.field_groups (code, name, sort_order) VALUES
    ('electrical', 'Electrical', 1),
    ('maintenance', 'Maintenance', 2)
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domains (code, name, domain_type) VALUES
    ('VOLTAGE_CLASS', 'Voltage class', 'coded'),
    ('CONDITION_RATING', 'Condition rating', 'coded')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domain_values (domain_id, code, label, sort_order)
SELECT d.id, v.code, v.label, v.sort_order
FROM metadata.domains d
JOIN (VALUES
    ('VOLTAGE_CLASS', '11KV', '11 kV', 1),
    ('VOLTAGE_CLASS', '33KV', '33 kV', 2),
    ('CONDITION_RATING', 'GOOD', 'Good', 1),
    ('CONDITION_RATING', 'FAIR', 'Fair', 2),
    ('CONDITION_RATING', 'POOR', 'Poor', 3)
) AS v(domain_code, code, label, sort_order) ON v.domain_code = d.code
ON CONFLICT (domain_id, code) DO NOTHING;

-- MV_CONDUCTOR -- edge, no terminals, never itself the "to" side of a
-- network_rules row per docs/metadata-core.md.

INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status, is_diagram_container)
VALUES ('MV_CONDUCTOR', 'MV Conductor', 'line', 4326, 'edge', 'draft', false)
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, v.code, v.name
FROM metadata.feature_classes fc
JOIN (VALUES (1, 'overhead_mv_conductor'), (2, 'underground_mv_conductor')) AS v(code, name) ON true
WHERE fc.code = 'MV_CONDUCTOR'
ON CONFLICT (feature_class_id, code) DO NOTHING;

INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, v.nullable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('voltage_class', 'text', 'VOLTAGE_CLASS', 'electrical', false),
    ('condition_rating', 'text', 'CONDITION_RATING', 'maintenance', false)
) AS v(name, data_type, domain_code, group_code, nullable) ON true
LEFT JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code = 'MV_CONDUCTOR'
ON CONFLICT (feature_class_id, name) DO NOTHING;

INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, true
FROM metadata.subtypes s
JOIN metadata.fields f ON f.feature_class_id = s.feature_class_id
WHERE s.feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'MV_CONDUCTOR')
  AND f.name IN ('voltage_class', 'condition_rating')
ON CONFLICT (subtype_id, field_id) DO NOTHING;

-- DIST_XFMR -- junction, with T1 (primary), T2 (secondary), T3 (ground)
-- terminals, per docs/metadata-core.md.

INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status, is_diagram_container)
VALUES ('DIST_XFMR', 'Distribution Transformer', 'point', 4326, 'junction', 'draft', false)
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, v.code, v.name
FROM metadata.feature_classes fc
JOIN (VALUES (1, 'pole_mounted'), (2, 'pad_mounted'), (3, 'underground_vault')) AS v(code, name) ON true
WHERE fc.code = 'DIST_XFMR'
ON CONFLICT (feature_class_id, code) DO NOTHING;

INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, v.nullable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('primary_voltage', 'text', 'VOLTAGE_CLASS', 'electrical', false),
    ('condition_rating', 'text', 'CONDITION_RATING', 'maintenance', false)
) AS v(name, data_type, domain_code, group_code, nullable) ON true
LEFT JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code = 'DIST_XFMR'
ON CONFLICT (feature_class_id, name) DO NOTHING;

INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, true
FROM metadata.subtypes s
JOIN metadata.fields f ON f.feature_class_id = s.feature_class_id
WHERE s.feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'DIST_XFMR')
  AND f.name IN ('primary_voltage', 'condition_rating')
ON CONFLICT (subtype_id, field_id) DO NOTHING;

INSERT INTO metadata.terminals (feature_class_id, subtype_id, code, role, phase_capable)
SELECT fc.id, NULL, v.code, v.role, v.phase_capable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('T1', 'primary', true),
    ('T2', 'secondary', true),
    ('T3', 'ground', false)
) AS v(code, role, phase_capable) ON true
WHERE fc.code = 'DIST_XFMR'
ON CONFLICT (feature_class_id, subtype_id, code) DO NOTHING;

-- network_rules -- no unique constraint on this table (a rule set is
-- meant to be authored deliberately, not upserted by code), so this
-- guards idempotency with WHERE NOT EXISTS instead of ON CONFLICT.
--
-- T1 gets a real, valid rule. T3 gets a rule that exists but is
-- disabled (is_valid = false) -- a connection there should be rejected
-- for a different, more specific reason than T2, which gets no rule row
-- at all and falls on the plain zero-trust default.

INSERT INTO metadata.network_rules (name, from_feature_class_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'MV_CONDUCTOR -> DIST_XFMR T1', mv.id, xfmr.id, t1.id,
       'from.voltage_class = to.primary_voltage', true
FROM metadata.feature_classes mv, metadata.feature_classes xfmr
JOIN metadata.terminals t1 ON t1.feature_class_id = xfmr.id AND t1.code = 'T1'
WHERE mv.code = 'MV_CONDUCTOR' AND xfmr.code = 'DIST_XFMR'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = mv.id AND to_feature_class_id = xfmr.id AND to_terminal_id = t1.id
  );

INSERT INTO metadata.network_rules (name, from_feature_class_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'MV_CONDUCTOR -> DIST_XFMR T3 (retired)', mv.id, xfmr.id, t3.id,
       'from.voltage_class = to.primary_voltage', false
FROM metadata.feature_classes mv, metadata.feature_classes xfmr
JOIN metadata.terminals t3 ON t3.feature_class_id = xfmr.id AND t3.code = 'T3'
WHERE mv.code = 'MV_CONDUCTOR' AND xfmr.code = 'DIST_XFMR'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = mv.id AND to_feature_class_id = xfmr.id AND to_terminal_id = t3.id
  );
