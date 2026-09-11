-- seed/protection_curves_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives
-- under /seed on purpose, same reasoning as the other seed files.
-- Phase (protection), Slice B. Two halves:
--
--   1. Settings fields that make protection *capturable* -- relay IDMT
--      settings on PROTECTIVE_RELAY, fuse rating/speed on MV_FUSE, and a
--      sectionalizer's count-to-lockout. ALL nullable (the additive-compile
--      rule: adding a NOT NULL column to a class with rows fails).
--
--   2. The tabulated TCC catalog (migration 0076): the standard IEC/IEEE
--      relay curves as real stored (multiple, time) points, plus one
--      illustrative fuse curve. The points ARE the curve -- the coordination
--      engine (Slice C) interpolates them and never evaluates an IDMT
--      formula. The standard relay curves are generated here by evaluating
--      their defining IEC 60255 / IEEE C37.112 formula ONCE, at seed time,
--      to populate accurate points -- a point-generator for the standards,
--      not runtime parametrics. A real manufacturer fuse curve would be
--      loaded into the same table as measured points (the example below
--      shows the shape).
--
-- A relay's curve_type field stores a curve CODE (e.g. 'IEC_SI') that equals
-- protection_curves.code -- that string is the join the engine follows from
-- a relay feature to its curve's points. DEFINITE_TIME is a valid curve_type
-- with NO stored points: it means "operate after definite_time_s", handled
-- directly by the engine.
--
-- Depends on:
--   * migration 0076                    (protection_curves / _points tables)
--   * seed/protective_relay_example.sql (PROTECTIVE_RELAY)
--   * seed/mv_protection_example.sql    (MV_FUSE)
--   * seed/mv_switching_example.sql     (MV_SECTIONALIZER)
-- Any class not present just seeds no fields for it.
--
-- Apply, then recompile the three classes so the new columns land:
--   task seed-protection-curves
--   task compile CODE=PROTECTIVE_RELAY
--   task compile CODE=MV_FUSE
--   task compile CODE=MV_SECTIONALIZER
-- (the curve tables are plain metadata -- nothing to compile for them.)
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL
-- has been reviewed statically -- a live psql apply + the three compiles +
-- a curve-point spot check are still owed to call it verified.

-- ===========================================================================
-- Part 1 -- domains + settings fields
-- ===========================================================================

INSERT INTO metadata.field_groups (code, name, sort_order) VALUES
    ('protection', 'Protection', 1)
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domains (code, name, domain_type) VALUES
    ('TCC_CURVE', 'Time-current curve', 'coded'),
    ('FUSE_SPEED', 'Fuse speed / element', 'coded')
ON CONFLICT (code) DO NOTHING;

-- TCC_CURVE values MUST equal protection_curves.code for the join to resolve
-- (DEFINITE_TIME is intentionally point-less -- see header).
INSERT INTO metadata.domain_values (domain_id, code, label, sort_order)
SELECT d.id, v.code, v.label, v.sort_order
FROM metadata.domains d
JOIN (VALUES
    ('TCC_CURVE', 'IEC_SI', 'IEC Standard Inverse', 1),
    ('TCC_CURVE', 'IEC_VI', 'IEC Very Inverse', 2),
    ('TCC_CURVE', 'IEC_EI', 'IEC Extremely Inverse', 3),
    ('TCC_CURVE', 'IEEE_MI', 'IEEE Moderately Inverse', 4),
    ('TCC_CURVE', 'IEEE_VI', 'IEEE Very Inverse', 5),
    ('TCC_CURVE', 'IEEE_EI', 'IEEE Extremely Inverse', 6),
    ('TCC_CURVE', 'DEFINITE_TIME', 'Definite time', 7),
    ('FUSE_SPEED', 'K', 'K (fast)', 1),
    ('FUSE_SPEED', 'T', 'T (slow)', 2),
    ('FUSE_SPEED', 'SLOW', 'Slow / time-delay', 3),
    ('FUSE_SPEED', 'FAST', 'Fast', 4)
) AS v(domain_code, code, label, sort_order) ON v.domain_code = d.code
ON CONFLICT (domain_id, code) DO NOTHING;

-- PROTECTIVE_RELAY IDMT settings (all nullable).
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, v.nullable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('curve_type', 'text', 'TCC_CURVE', 'protection', true),
    ('pickup_current_a', 'numeric', NULL, 'protection', true),
    ('time_dial', 'numeric', NULL, 'protection', true),
    ('inst_pickup_a', 'numeric', NULL, 'protection', true),
    ('definite_time_s', 'numeric', NULL, 'protection', true)
) AS v(name, data_type, domain_code, group_code, nullable) ON true
LEFT JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code = 'PROTECTIVE_RELAY'
ON CONFLICT (feature_class_id, name) DO NOTHING;

-- MV_FUSE rating + speed + curve reference (all nullable). curve_ref holds a
-- protection_curves.code (an ampere-quantity fuse curve, e.g.
-- 'FUSE_100T_EXAMPLE') -- the string the coordination engine follows from a
-- fuse feature to its melt/clear points, the fuse analog of the relay's
-- curve_type. Free text (not domain-constrained) so any loaded manufacturer
-- curve resolves without a domain edit; an unknown/blank code simply leaves
-- the fuse's operate time unresolved, degrading gracefully.
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, v.nullable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('fuse_rating_a', 'numeric', NULL, 'protection', true),
    ('fuse_speed', 'text', 'FUSE_SPEED', 'protection', true),
    ('curve_ref', 'text', NULL, 'protection', true)
) AS v(name, data_type, domain_code, group_code, nullable) ON true
LEFT JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code = 'MV_FUSE'
ON CONFLICT (feature_class_id, name) DO NOTHING;

-- MV_SECTIONALIZER count-to-lockout (nullable integer).
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, v.nullable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('count_to_lockout', 'integer', NULL, 'protection', true)
) AS v(name, data_type, domain_code, group_code, nullable) ON true
LEFT JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code = 'MV_SECTIONALIZER'
ON CONFLICT (feature_class_id, name) DO NOTHING;

-- Attach every new field to all of its class's subtypes as NOT required
-- (is_required = false), matching the nullable columns. One set-generating
-- INSERT over the three classes' new field names.
INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, false
FROM metadata.subtypes s
JOIN metadata.fields f ON f.feature_class_id = s.feature_class_id
JOIN metadata.feature_classes fc ON fc.id = s.feature_class_id
WHERE (fc.code = 'PROTECTIVE_RELAY' AND f.name IN ('curve_type', 'pickup_current_a', 'time_dial', 'inst_pickup_a', 'definite_time_s'))
   OR (fc.code = 'MV_FUSE'          AND f.name IN ('fuse_rating_a', 'fuse_speed', 'curve_ref'))
   OR (fc.code = 'MV_SECTIONALIZER' AND f.name IN ('count_to_lockout'))
ON CONFLICT (subtype_id, field_id) DO NOTHING;

-- ===========================================================================
-- Part 2 -- the tabulated curve catalog
-- ===========================================================================

-- Curve headers -- codes match the TCC_CURVE domain values above.
INSERT INTO metadata.protection_curves (code, name, standard, quantity) VALUES
    ('IEC_SI',  'IEC Standard Inverse',     'IEC',  'multiple'),
    ('IEC_VI',  'IEC Very Inverse',         'IEC',  'multiple'),
    ('IEC_EI',  'IEC Extremely Inverse',    'IEC',  'multiple'),
    ('IEEE_MI', 'IEEE Moderately Inverse',  'IEEE', 'multiple'),
    ('IEEE_VI', 'IEEE Very Inverse',        'IEEE', 'multiple'),
    ('IEEE_EI', 'IEEE Extremely Inverse',   'IEEE', 'multiple')
ON CONFLICT (code) DO NOTHING;

-- Points, generated once from the defining formula (unified as
-- t = coeff / (M^exp - 1) + b, with b = 0 for the IEC curves) at
-- time_dial = 1, sampled across the useful multiple range. These are stored
-- rows -- the engine interpolates them, it does not re-run this formula.
INSERT INTO metadata.protection_curve_points (curve_id, band, current, time_s)
SELECT c.id, 'operate', m.mult,
       round((p.coeff / (power(m.mult, p.exp) - 1) + p.b)::numeric, 4)
FROM metadata.protection_curves c
JOIN (VALUES
    ('IEC_SI',  0.14,   0.02, 0.0),
    ('IEC_VI',  13.5,   1.0,  0.0),
    ('IEC_EI',  80.0,   2.0,  0.0),
    ('IEEE_MI', 0.0515, 0.02, 0.1140),
    ('IEEE_VI', 19.61,  2.0,  0.491),
    ('IEEE_EI', 28.2,   2.0,  0.1217)
) AS p(code, coeff, exp, b) ON p.code = c.code
CROSS JOIN (VALUES (1.5),(2.0),(2.5),(3.0),(4.0),(5.0),(6.0),(8.0),(10.0),(15.0),(20.0)) AS m(mult)
ON CONFLICT (curve_id, band, current) DO NOTHING;

-- One illustrative fuse curve (quantity = ampere, two bounding bands) so the
-- fuse path in the engine has real data to exercise. NOT a real manufacturer
-- sheet -- a plausible 100 A expulsion (T) fuse shape; replace with measured
-- points per deployment. minimum_melt is the earlier (lower-time) bound at a
-- given current, total_clear the later one.
INSERT INTO metadata.protection_curves (code, name, standard, quantity) VALUES
    ('FUSE_100T_EXAMPLE', 'Example 100 A T fuse', 'FUSE', 'ampere')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.protection_curve_points (curve_id, band, current, time_s)
SELECT c.id, v.band, v.current, v.time_s
FROM metadata.protection_curves c
JOIN (VALUES
    ('minimum_melt', 200.0,  300.0),
    ('minimum_melt', 300.0,  30.0),
    ('minimum_melt', 500.0,  3.5),
    ('minimum_melt', 1000.0, 0.30),
    ('minimum_melt', 2000.0, 0.045),
    ('minimum_melt', 4000.0, 0.011),
    ('total_clear',  200.0,  600.0),
    ('total_clear',  300.0,  60.0),
    ('total_clear',  500.0,  7.0),
    ('total_clear',  1000.0, 0.60),
    ('total_clear',  2000.0, 0.090),
    ('total_clear',  4000.0, 0.022)
) AS v(band, current, time_s) ON true
WHERE c.code = 'FUSE_100T_EXAMPLE'
ON CONFLICT (curve_id, band, current) DO NOTHING;
