-- seed/switching_device_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this
-- lives under /seed on purpose, same reasoning as the other seed files.
-- Phase 4's trace engine needs a real feature class whose state a
-- trace can stop at -- migrations/0009 and 0014's own comments already
-- name the illustrative example precisely: "on feature_class
-- DIST_SWITCH, if field device_state equals domain value OPEN, stop."
-- This file builds exactly that: a junction feature class with a
-- DEVICE_STATE-domained field and two terminals, so an existing
-- MV_CONDUCTOR can connect to either side of it the same way it
-- already connects to DIST_XFMR (seed/connectivity_example.sql).
--
-- Depends on seed/connectivity_example.sql having already been applied
-- (reuses its MV_CONDUCTOR feature class as the from-side of this
-- file's own network_rules).
--
-- Apply with psql after seed/connectivity_example.sql, then compile:
--   task compile CODE=DIST_SWITCH

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

-- DIST_SWITCH -- junction, two terminals (T1 line-side, T2 load-side),
-- so it can sit inline on a conductor run the same way DIST_XFMR does.
-- Two subtypes: a plain manually-operated switch, and a recloser
-- (auto-restores after a transient fault) -- the distinction isn't
-- acted on anywhere yet (nothing in this pass models auto-restoration),
-- it's here so a later slice has a real field to key that off instead
-- of needing a schema change first.

INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status, is_diagram_container)
VALUES ('DIST_SWITCH', 'Distribution Switch', 'point', 4326, 'junction', 'draft', false)
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, v.code, v.name
FROM metadata.feature_classes fc
JOIN (VALUES (1, 'manual_switch'), (2, 'automatic_recloser')) AS v(code, name) ON true
WHERE fc.code = 'DIST_SWITCH'
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
WHERE fc.code = 'DIST_SWITCH'
ON CONFLICT (feature_class_id, name) DO NOTHING;

INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, true
FROM metadata.subtypes s
JOIN metadata.fields f ON f.feature_class_id = s.feature_class_id
WHERE s.feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'DIST_SWITCH')
  AND f.name IN ('device_state', 'condition_rating')
ON CONFLICT (subtype_id, field_id) DO NOTHING;

-- role is constrained to primary/secondary/ground/other
-- (migrations/0009) -- "line_side"/"load_side" isn't one of those, so
-- T1/T2 map onto primary/secondary the same way DIST_XFMR's terminals
-- already do, even though "primary winding" isn't quite the right
-- mental model for a switch; the column's real job here is just
-- distinguishing the two sides, which primary/secondary does fine.
INSERT INTO metadata.terminals (feature_class_id, subtype_id, code, role, phase_capable)
SELECT fc.id, NULL, v.code, v.role, v.phase_capable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('T1', 'primary', true),
    ('T2', 'secondary', true)
) AS v(code, role, phase_capable) ON true
WHERE fc.code = 'DIST_SWITCH'
ON CONFLICT (feature_class_id, subtype_id, code) DO NOTHING;

-- network_rules -- lets a plain MV_CONDUCTOR (no terminals of its own)
-- connect to either side of the switch, same shape as
-- seed/connectivity_example.sql's MV_CONDUCTOR -> DIST_XFMR.T1 rule.
-- No phase_rule text -- nothing evaluates phase_rule anywhere yet (see
-- docs/phase-2-connectivity.md), and this pass deliberately stubs
-- respect_phase in the trace engine too (see docs/phase-4-trace-engine.md),
-- so writing an expression nothing reads would just be noise.
--
-- T1 (line side) and T2 (load side) both legally accept an incoming
-- conductor -- but api/internal/trace's DOWNSTREAM/UPSTREAM directions
-- walk feature_connectivity's from/to columns literally (no separate
-- "flow direction" concept exists yet), so which side actually gets
-- *established* as a connection's "from" vs. "to" is what determines
-- whether a downstream trace can walk through the switch at all. A
-- fourth rule below (DIST_SWITCH T2 -> MV_CONDUCTOR) makes the switch
-- itself the "from" side for its outgoing leg -- see
-- docs/phase-4-trace-engine.md's "A gap this slice had to close" for
-- the full story of why that's needed, not just this rule existing.

INSERT INTO metadata.network_rules (name, from_feature_class_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'MV_CONDUCTOR -> DIST_SWITCH T1', mv.id, sw.id, t1.id, NULL, true
FROM metadata.feature_classes mv, metadata.feature_classes sw
JOIN metadata.terminals t1 ON t1.feature_class_id = sw.id AND t1.code = 'T1'
WHERE mv.code = 'MV_CONDUCTOR' AND sw.code = 'DIST_SWITCH'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = mv.id AND to_feature_class_id = sw.id AND to_terminal_id = t1.id
  );

INSERT INTO metadata.network_rules (name, from_feature_class_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'MV_CONDUCTOR -> DIST_SWITCH T2', mv.id, sw.id, t2.id, NULL, true
FROM metadata.feature_classes mv, metadata.feature_classes sw
JOIN metadata.terminals t2 ON t2.feature_class_id = sw.id AND t2.code = 'T2'
WHERE mv.code = 'MV_CONDUCTOR' AND sw.code = 'DIST_SWITCH'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = mv.id AND to_feature_class_id = sw.id AND to_terminal_id = t2.id
  );

-- The switch's load-side terminal as the FROM side, a plain conductor
-- as TO -- this is the rule the seeded topology actually establishes
-- against for the switch's outgoing leg, so a DOWNSTREAM trace starting
-- upstream of the switch can walk through it to whatever's beyond.

INSERT INTO metadata.network_rules (name, from_feature_class_id, from_terminal_id, to_feature_class_id, phase_rule, is_valid)
SELECT 'DIST_SWITCH T2 -> MV_CONDUCTOR', sw.id, t2.id, mv.id, NULL, true
FROM metadata.feature_classes mv, metadata.feature_classes sw
JOIN metadata.terminals t2 ON t2.feature_class_id = sw.id AND t2.code = 'T2'
WHERE mv.code = 'MV_CONDUCTOR' AND sw.code = 'DIST_SWITCH'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = sw.id AND from_terminal_id = t2.id AND to_feature_class_id = mv.id
  );
