-- seed/lv_unmetered_ct_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives
-- under /seed on purpose, same reasoning as the other seed files.
-- Phase 9, Slice 5 (unmetered LV + CT-metering nuances). Closes the last
-- two service-delivery scenarios not covered by Slices 1-3:
--   * STREET_LIGHT -- an unmetered LV leaf (scenario 8). Fed straight off
--     an LV run, no cutout/meter in between, since street lighting is an
--     unmetered utility load.
--   * CT-metering detail (scenario 5, commercial 3-phase CT) -- NOT a new
--     feature class. Per docs/phase-9's "Decisions made", CTs/VTs are an
--     *attribute of the meter*, not features of their own. So this adds a
--     `ct_ratio` field to the existing METER, required only for the
--     `ct_metered` subtype.
--
-- TWO PATTERNS THIS SLICE EXERCISES FOR THE FIRST TIME (worth naming):
--
--   1. A phase-gated device->leaf rule. `LV_CONDUCTOR -> STREET_LIGHT T1`
--      opts into the Slice 0 gate (phase_rule = 'phases_intersect'): a
--      light wired to L1 must be fed by a conductor carrying L1. Slice 1
--      only exercised the gate edge-to-edge; the gate itself is
--      terminal-agnostic (api/internal/connectivity/phase.go reads each
--      endpoint's `phase` field regardless of terminals), so this is the
--      same mechanism reaching a single-terminal load.
--
--   2. A per-subtype required field. `ct_ratio` is physically NULLABLE
--      (whole-current meters leave it unset) but is made required for the
--      `ct_metered` subtype ONLY, via a single subtype_field_rules row
--      against that subtype's id. Every earlier slice required a field for
--      *all* of a class's subtypes; this is the first field required for
--      one subtype and optional for another -- exactly what
--      subtype_field_rules exists for.
--
-- SAFE ALTER: `ct_ratio` is added to METER, which Slices 3 may already
-- have compiled. Because the column is nullable, re-running
-- `task compile CODE=METER` takes the ALTER TABLE ADD COLUMN path and is
-- safe on a populated table (unlike a NOT NULL add) -- no reset needed.
--
-- Depends on seed/lv_metering_example.sql (METER) and
-- seed/lv_conductors_example.sql (LV_CONDUCTOR) having been applied. The
-- PHASE / CONDITION_RATING domains + field_groups are re-asserted
-- idempotently so this seed is self-sufficient.
--
-- Apply with psql, then compile the new class and re-compile METER so the
-- new nullable column reaches its physical table:
--   task seed-lv-unmetered-ct   (or the psql -f invocation directly)
--   task compile CODE=STREET_LIGHT
--   task compile CODE=METER
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL
-- has been reviewed statically -- a live psql apply + the two `task
-- compile` runs + a phase-mismatch connect check on the street-light rule
-- are still owed to call it verified.

INSERT INTO metadata.field_groups (code, name, sort_order) VALUES
    ('electrical', 'Electrical', 1),
    ('maintenance', 'Maintenance', 2)
ON CONFLICT (code) DO NOTHING;

-- PHASE + CONDITION_RATING -- re-asserted idempotently (seeded upstream).
INSERT INTO metadata.domains (code, name, domain_type) VALUES
    ('PHASE', 'Phase', 'coded'),
    ('CONDITION_RATING', 'Condition rating', 'coded'),
    ('LUMINAIRE_TYPE', 'Luminaire type', 'coded'),
    ('CT_RATIO', 'CT ratio', 'coded')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domain_values (domain_id, code, label, sort_order)
SELECT d.id, v.code, v.label, v.sort_order
FROM metadata.domains d
JOIN (VALUES
    ('PHASE', 'L1',      'L1',                     1),
    ('PHASE', 'L2',      'L2',                     2),
    ('PHASE', 'L3',      'L3',                     3),
    ('PHASE', 'N',       'Neutral',                4),
    ('PHASE', 'L1N',     'L1 + N (1-phase)',       5),
    ('PHASE', 'L2N',     'L2 + N (1-phase)',       6),
    ('PHASE', 'L3N',     'L3 + N (1-phase)',       7),
    ('PHASE', 'L1L2L3N', 'L1 L2 L3 + N (3-phase)', 8),
    ('CONDITION_RATING', 'GOOD', 'Good', 1),
    ('CONDITION_RATING', 'FAIR', 'Fair', 2),
    ('CONDITION_RATING', 'POOR', 'Poor', 3),
    ('LUMINAIRE_TYPE', 'LED', 'LED', 1),
    ('LUMINAIRE_TYPE', 'SON', 'High-pressure sodium (SON)', 2),
    ('LUMINAIRE_TYPE', 'SOX', 'Low-pressure sodium (SOX)', 3),
    ('LUMINAIRE_TYPE', 'MH',  'Metal halide', 4),
    ('CT_RATIO', '100_5',  '100/5 A',  1),
    ('CT_RATIO', '200_5',  '200/5 A',  2),
    ('CT_RATIO', '400_5',  '400/5 A',  3),
    ('CT_RATIO', '800_5',  '800/5 A',  4),
    ('CT_RATIO', '1600_5', '1600/5 A', 5)
) AS v(domain_code, code, label, sort_order) ON v.domain_code = d.code
ON CONFLICT (domain_id, code) DO NOTHING;

-- ------------------------------------------------------- STREET_LIGHT --
-- Unmetered LV leaf: junction with a single terminal (T1), like
-- SERVICE_POINT, but no meter is ever wired ahead of it. Subtypes are the
-- mounting variant (physical), phase/luminaire/condition are the fields.

INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status, is_diagram_container)
VALUES ('STREET_LIGHT', 'Street Light', 'point', 4326, 'junction', 'draft', false)
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, v.code, v.name
FROM metadata.feature_classes fc
JOIN (VALUES (1, 'column_mounted'), (2, 'wall_mounted')) AS v(code, name) ON true
WHERE fc.code = 'STREET_LIGHT'
ON CONFLICT (feature_class_id, code) DO NOTHING;

INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, v.nullable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('phase',            'text', 'PHASE',            'electrical',  false),
    ('luminaire_type',   'text', 'LUMINAIRE_TYPE',   'electrical',  false),
    ('condition_rating', 'text', 'CONDITION_RATING', 'maintenance', false)
) AS v(name, data_type, domain_code, group_code, nullable) ON true
LEFT JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code = 'STREET_LIGHT'
ON CONFLICT (feature_class_id, name) DO NOTHING;

INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, true
FROM metadata.subtypes s
JOIN metadata.fields f ON f.feature_class_id = s.feature_class_id
WHERE s.feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'STREET_LIGHT')
  AND f.name IN ('phase', 'luminaire_type', 'condition_rating')
ON CONFLICT (subtype_id, field_id) DO NOTHING;

INSERT INTO metadata.terminals (feature_class_id, subtype_id, code, role, phase_capable)
SELECT fc.id, NULL, 'T1', 'primary', true
FROM metadata.feature_classes fc
WHERE fc.code = 'STREET_LIGHT'
ON CONFLICT (feature_class_id, subtype_id, code) DO NOTHING;

-- Unmetered feed straight off an LV run, phase-gated (see pattern 1 in the
-- header): the light's phase must intersect the feeding conductor's.
INSERT INTO metadata.network_rules (name, from_feature_class_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'LV_CONDUCTOR -> STREET_LIGHT T1 (phase-matched)', lv.id, sl.id, t1.id, 'phases_intersect', true
FROM metadata.feature_classes lv CROSS JOIN metadata.feature_classes sl
JOIN metadata.terminals t1 ON t1.feature_class_id = sl.id AND t1.code = 'T1'
WHERE lv.code = 'LV_CONDUCTOR' AND sl.code = 'STREET_LIGHT'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = lv.id AND to_feature_class_id = sl.id AND to_terminal_id = t1.id
  );

-- ------------------------------------------------- CT-metering nuance --
-- A ct_ratio field on the EXISTING METER (Slice 3), NOT a new class. The
-- field is nullable at the schema level; the subtype_field_rules row below
-- makes it required for the ct_metered subtype only (pattern 2 in the
-- header). whole_current meters leave it NULL and stay valid.

INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, 'ct_ratio', 'text', d.id, fg.id, true
FROM metadata.feature_classes fc
JOIN metadata.domains d ON d.code = 'CT_RATIO'
JOIN metadata.field_groups fg ON fg.code = 'electrical'
WHERE fc.code = 'METER'
ON CONFLICT (feature_class_id, name) DO NOTHING;

-- Required for the ct_metered subtype only -- a CT-metered service has no
-- meaning without its CT ratio, whereas a whole_current meter never has one.
INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, true
FROM metadata.subtypes s
JOIN metadata.fields f ON f.feature_class_id = s.feature_class_id
WHERE s.feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'METER')
  AND s.code = '2'          -- the ct_metered subtype (code '2', per the Slice 3 seed)
  AND f.name = 'ct_ratio'
ON CONFLICT (subtype_id, field_id) DO NOTHING;
