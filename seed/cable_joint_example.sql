-- seed/cable_joint_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives under
-- /seed on purpose, same reasoning as the other seed files.
-- Phase 44 Slice 2 (docs/phase-44-cable-management.md): the ELECTRICAL axis of
-- cable management -- the cable JOINT as a first-class network junction, one per
-- network (the MV_SWITCH / LV_SWITCH split precedent, seed/mv_lv_switch_example.sql):
--
--   MV_CABLE_JOINT / LV_CABLE_JOINT -- point, network_role='junction', a
--   zero-impedance pass-through node. Splicing a cable run AT a joint turns one
--   implicit edge into two REAL traced sections with the joint as the assetized
--   splice between them. Because the trace / section / isolation / powerflow
--   engines already treat a junction as a zero-Z pass-through, NOTHING in any
--   engine changes -- the joint just appears as another node on the walk.
--
-- Subtypes (the splice typology): straight (in-line), branch (a TEE -- a third
-- terminal T3), transition (e.g. PILC->XLPE, or a cable->OH pothead), termination
-- (a sealing end; still junction so a trace lands on it -- it just uses T1).
--
-- Terminals: T1 (primary) + T2 (secondary) are GENERIC (subtype_id null -- they
-- apply to every subtype); T3 (other) is scoped to the `branch` subtype ONLY, so
-- only a tee joint offers a third leg. This mixes generic + subtype-scoped
-- terminals on one class, which resolves per-CODE (metadata/load.go ResolveTerminal:
-- a subtype-specific terminal wins for its own code, else the generic one is used)
-- -- so a branch joint sees T1/T2 (generic) AND T3 (branch), a straight joint sees
-- just T1/T2.
--
-- network_rules mirror the switch's inline shape, keyed to the joint's own network
-- conductor (MV_CABLE_JOINT <-> MV_CONDUCTOR, LV_CABLE_JOINT <-> LV_CONDUCTOR):
-- conductor into T1, conductor into T2, T2 -> conductor (so a DOWNSTREAM trace
-- walks through). For a branch joint, two extra rules add the tee: conductor into
-- T3 and T3 -> conductor.
--
-- Containment: CABLE_CHAMBER contains {MV_CABLE_JOINT, LV_CABLE_JOINT} via the
-- EXISTING `containment` relationship kind (no migration -- the chamber is the box
-- the joint sits in), the exact analogue of METER_CHAMBER contains {METER,LV_BUSBAR}.
--
-- Depends on seed/connectivity_example.sql (MV_CONDUCTOR + CONDITION_RATING),
-- seed/lv_conductors_example.sql (LV_CONDUCTOR), and seed/cable_management_example.sql
-- (CABLE_CHAMBER, Slice 1). Roles used are primary/secondary/other only, so this
-- needs NO terminal-role migration (0070's 'tertiary' is deliberately avoided --
-- a tee tap is not a transformer winding).
--
-- Apply with psql, then compile both classes:
--   task seed-cable-joint
--   task compile CODE=MV_CABLE_JOINT
--   task compile CODE=LV_CABLE_JOINT
-- Symbology (the mandatory map default) is in seed/cable_joint_symbology.sql --
--   task seed-cable-joint-symbology
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL is
-- reviewed statically -- a live psql apply + the two compiles + a DOWNSTREAM trace
-- through a joint + a split-at-joint are still owed to call it verified.

INSERT INTO metadata.field_groups (code, name, sort_order) VALUES
    ('general', 'General', 1),
    ('maintenance', 'Maintenance', 2),
    ('asset', 'Asset', 3)
ON CONFLICT (code) DO NOTHING;

-- INSULATION_TYPE is new (the joint's cable-insulation family). CONDITION_RATING
-- is shared and re-asserted so this seed stands alone.
INSERT INTO metadata.domains (code, name, domain_type) VALUES
    ('INSULATION_TYPE', 'Insulation type', 'coded'),
    ('CONDITION_RATING', 'Condition rating', 'coded')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domain_values (domain_id, code, label, sort_order)
SELECT d.id, v.code, v.label, v.sort_order
FROM metadata.domains d
JOIN (VALUES
    ('INSULATION_TYPE', 'XLPE',        'XLPE',            1),
    ('INSULATION_TYPE', 'EPR',         'EPR',             2),
    ('INSULATION_TYPE', 'PILC',        'PILC (paper)',    3),
    ('INSULATION_TYPE', 'HEAT_SHRINK', 'Heat-shrink',     4),
    ('CONDITION_RATING', 'GOOD', 'Good', 1),
    ('CONDITION_RATING', 'FAIR', 'Fair', 2),
    ('CONDITION_RATING', 'POOR', 'Poor', 3)
) AS v(domain_code, code, label, sort_order) ON v.domain_code = d.code
ON CONFLICT (domain_id, code) DO NOTHING;

-- ===========================================================================
-- Feature classes -- point, network_role='junction' (a real pass-through node
-- on the electrical graph, unlike the Slice 1 civil none-role classes). Not a
-- diagram container, not diagram-placeable in this slice (map only). Geometry
-- required -- a joint has a physical location (typically inside a chamber).
-- ===========================================================================
INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status, is_diagram_container)
VALUES
    ('MV_CABLE_JOINT', 'MV cable joint', 'point', 4326, 'junction', 'draft', false),
    ('LV_CABLE_JOINT', 'LV cable joint', 'point', 4326, 'junction', 'draft', false)
ON CONFLICT (code) DO NOTHING;

-- Splice-typology subtypes, both classes.
INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, v.code, v.name
FROM metadata.feature_classes fc
JOIN (VALUES
    ('straight',    'Straight'),
    ('branch',      'Branch (tee)'),
    ('transition',  'Transition'),
    ('termination', 'Termination')
) AS v(code, name) ON true
WHERE fc.code IN ('MV_CABLE_JOINT', 'LV_CABLE_JOINT')
ON CONFLICT (feature_class_id, code) DO NOTHING;

-- Fields -- the lean joint spine.
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, v.nullable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('insulation_type',   'text', 'INSULATION_TYPE',  'general',     false),
    ('condition_rating',  'text', 'CONDITION_RATING', 'maintenance', false),
    ('install_date',      'date', NULL,               'asset',       true),
    ('last_inspected_at', 'date', NULL,               'maintenance', true)
) AS v(name, data_type, domain_code, group_code, nullable) ON true
LEFT JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code IN ('MV_CABLE_JOINT', 'LV_CABLE_JOINT')
ON CONFLICT (feature_class_id, name) DO NOTHING;

-- Require insulation_type + condition_rating on every subtype of both classes.
INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, true
FROM metadata.subtypes s
JOIN metadata.fields f ON f.feature_class_id = s.feature_class_id
WHERE s.feature_class_id IN (
        SELECT id FROM metadata.feature_classes WHERE code IN ('MV_CABLE_JOINT', 'LV_CABLE_JOINT')
      )
  AND f.name IN ('insulation_type', 'condition_rating')
ON CONFLICT (subtype_id, field_id) DO NOTHING;

-- ===========================================================================
-- Terminals. T1/T2 generic (subtype_id null -- every subtype); T3 branch-only.
-- ===========================================================================

-- Generic T1 + T2 on both classes (subtype_id null).
INSERT INTO metadata.terminals (feature_class_id, subtype_id, code, role, phase_capable)
SELECT fc.id, NULL, v.code, v.role, v.phase_capable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('T1', 'primary',   true),
    ('T2', 'secondary', true)
) AS v(code, role, phase_capable) ON true
WHERE fc.code IN ('MV_CABLE_JOINT', 'LV_CABLE_JOINT')
ON CONFLICT (feature_class_id, subtype_id, code) DO NOTHING;

-- T3 on the `branch` subtype only (the tee's third leg). role 'other' -- a tap,
-- not a winding, so no 0070 'tertiary' dependency.
INSERT INTO metadata.terminals (feature_class_id, subtype_id, code, role, phase_capable)
SELECT s.feature_class_id, s.id, 'T3', 'other', true
FROM metadata.subtypes s
JOIN metadata.feature_classes fc ON fc.id = s.feature_class_id
WHERE fc.code IN ('MV_CABLE_JOINT', 'LV_CABLE_JOINT') AND s.code = 'branch'
ON CONFLICT (feature_class_id, subtype_id, code) DO NOTHING;

-- ===========================================================================
-- network_rules -- the inline feed-through shape, keyed to the joint's own
-- network conductor (MV_CABLE_JOINT <-> MV_CONDUCTOR, LV_CABLE_JOINT <->
-- LV_CONDUCTOR). Base T1/T2 rules for every joint; two extra T3 rules add the
-- tee for a branch joint. Guarded WHERE NOT EXISTS (no unique key on
-- network_rules). phase_rule left NULL -- a splice is not a phase-matched hop.
-- ===========================================================================

-- Conductor into the joint's T1.
INSERT INTO metadata.network_rules (name, from_feature_class_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT cond.code || ' -> ' || j.code || ' T1', cond.id, j.id, t1.id, NULL, true
FROM (VALUES ('MV_CABLE_JOINT', 'MV_CONDUCTOR'), ('LV_CABLE_JOINT', 'LV_CONDUCTOR')) AS pair(joint_code, cond_code)
JOIN metadata.feature_classes j ON j.code = pair.joint_code
JOIN metadata.feature_classes cond ON cond.code = pair.cond_code
JOIN metadata.terminals t1 ON t1.feature_class_id = j.id AND t1.code = 'T1'
WHERE NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = cond.id AND to_feature_class_id = j.id AND to_terminal_id = t1.id
  );

-- Conductor into the joint's T2.
INSERT INTO metadata.network_rules (name, from_feature_class_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT cond.code || ' -> ' || j.code || ' T2', cond.id, j.id, t2.id, NULL, true
FROM (VALUES ('MV_CABLE_JOINT', 'MV_CONDUCTOR'), ('LV_CABLE_JOINT', 'LV_CONDUCTOR')) AS pair(joint_code, cond_code)
JOIN metadata.feature_classes j ON j.code = pair.joint_code
JOIN metadata.feature_classes cond ON cond.code = pair.cond_code
JOIN metadata.terminals t2 ON t2.feature_class_id = j.id AND t2.code = 'T2'
WHERE NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = cond.id AND to_feature_class_id = j.id AND to_terminal_id = t2.id
  );

-- Joint T2 onward to more conductor (joint as the FROM side, so DOWNSTREAM walks through).
INSERT INTO metadata.network_rules (name, from_feature_class_id, from_terminal_id, to_feature_class_id, phase_rule, is_valid)
SELECT j.code || ' T2 -> ' || cond.code, j.id, t2.id, cond.id, NULL, true
FROM (VALUES ('MV_CABLE_JOINT', 'MV_CONDUCTOR'), ('LV_CABLE_JOINT', 'LV_CONDUCTOR')) AS pair(joint_code, cond_code)
JOIN metadata.feature_classes j ON j.code = pair.joint_code
JOIN metadata.feature_classes cond ON cond.code = pair.cond_code
JOIN metadata.terminals t2 ON t2.feature_class_id = j.id AND t2.code = 'T2'
WHERE NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = j.id AND from_terminal_id = t2.id AND to_feature_class_id = cond.id
  );

-- Branch tee: conductor into T3 (branch joints only -- the T3 join finds no row
-- for non-branch subtypes).
INSERT INTO metadata.network_rules (name, from_feature_class_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT cond.code || ' -> ' || j.code || ' T3', cond.id, j.id, t3.id, NULL, true
FROM (VALUES ('MV_CABLE_JOINT', 'MV_CONDUCTOR'), ('LV_CABLE_JOINT', 'LV_CONDUCTOR')) AS pair(joint_code, cond_code)
JOIN metadata.feature_classes j ON j.code = pair.joint_code
JOIN metadata.feature_classes cond ON cond.code = pair.cond_code
JOIN metadata.terminals t3 ON t3.feature_class_id = j.id AND t3.code = 'T3'
WHERE NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = cond.id AND to_feature_class_id = j.id AND to_terminal_id = t3.id
  );

-- Branch tee: joint T3 onward to more conductor (the second downstream leg).
INSERT INTO metadata.network_rules (name, from_feature_class_id, from_terminal_id, to_feature_class_id, phase_rule, is_valid)
SELECT j.code || ' T3 -> ' || cond.code, j.id, t3.id, cond.id, NULL, true
FROM (VALUES ('MV_CABLE_JOINT', 'MV_CONDUCTOR'), ('LV_CABLE_JOINT', 'LV_CONDUCTOR')) AS pair(joint_code, cond_code)
JOIN metadata.feature_classes j ON j.code = pair.joint_code
JOIN metadata.feature_classes cond ON cond.code = pair.cond_code
JOIN metadata.terminals t3 ON t3.feature_class_id = j.id AND t3.code = 'T3'
WHERE NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = j.id AND from_terminal_id = t3.id AND to_feature_class_id = cond.id
  );

-- ===========================================================================
-- Containment -- CABLE_CHAMBER contains {MV_CABLE_JOINT, LV_CABLE_JOINT} via the
-- existing `containment` kind (no migration). one_to_many: a chamber holds many
-- joints. Guarded on (origin, destination, kind). No-op if seed/cable_management_
-- example.sql (CABLE_CHAMBER) hasn't been applied yet.
-- ===========================================================================
INSERT INTO metadata.relationship_classes (name, origin_feature_class_id, destination_feature_class_id, kind, cardinality)
SELECT 'CABLE_CHAMBER contains ' || j.code, ch.id, j.id, 'containment', 'one_to_many'
FROM metadata.feature_classes ch
JOIN metadata.feature_classes j ON j.code IN ('MV_CABLE_JOINT', 'LV_CABLE_JOINT')
WHERE ch.code = 'CABLE_CHAMBER'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.relationship_classes
    WHERE origin_feature_class_id = ch.id AND destination_feature_class_id = j.id AND kind = 'containment'
  );
