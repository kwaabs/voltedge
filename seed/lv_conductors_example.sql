-- seed/lv_conductors_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives
-- under /seed on purpose, same reasoning as the other seed files.
-- Phase 9, Slice 1 (the LV conductor). A single edge class, LV_CONDUCTOR,
-- that carries low-voltage power from a distribution transformer's
-- secondary to the service points it feeds -- the "short service drop off
-- the transformer's LV bus" that seed/service_point_example.sql explicitly
-- deferred ("Revisit if an LV conductor class is ever added").
--
-- THE SETTLED CONSTRUCTION/ROLE CONVENTION (decided once, for MV and LV
-- alike -- see docs/phase-9-lv-service-delivery.md "Decisions made"):
--   * Construction (overhead vs. underground) is ALWAYS the SUBTYPE.
--     LV_CONDUCTOR mirrors MV_CONDUCTOR exactly:
--     overhead_lv_conductor / underground_lv_conductor.
--   * Functional role (main vs. service), where a class has one, is
--     ALWAYS a FIELD -- here the coded `lv_role` field (LV_ROLE domain).
-- This reverses an earlier abandoned draft that split LV into two
-- classes (LV_MAIN / LV_SERVICE) keyed on role, and a briefly-considered
-- role-as-subtype variant. Both are gone. One class, construction as
-- subtype, role as field. Nothing about symbology is lost by this choice:
-- symbology_rules (migrations/0012) key off subtype_id AND/OR
-- domain_value_id (most-specific-wins), so an overhead-main vs.
-- underground-service render distinction is still fully expressible
-- against (subtype x lv_role) without construction ever being a field.
--
-- DESTRUCTIVE REPOINT (intentional): once LV_CONDUCTOR exists, a service
-- point is fed *through* it, not by a bare terminal-to-terminal hop off
-- the transformer. So the direct `DIST_XFMR T2 -> SERVICE_POINT T1` rule
-- that seed/service_point_example.sql seeded as a simplification is
-- RETIRED here (is_valid = false, guarded so it only flips a currently
-- valid row). The realistic path becomes
-- DIST_XFMR.T2 -> LV_CONDUCTOR -> SERVICE_POINT.T1.
--
-- Depends on seed/service_point_example.sql (DIST_XFMR, SERVICE_POINT and
-- their terminals) and seed/phase_model_example.sql (the PHASE domain)
-- having been applied. The PHASE domain + 'electrical' field_group are
-- re-asserted idempotently below so this seed is self-sufficient if
-- phase_model happens to be applied after it.
--
-- Apply with psql, then compile the new class:
--   task seed-lv-conductors   (or the psql -f invocation directly)
--   task compile CODE=LV_CONDUCTOR
--
-- Static-review note: as with every artifact in this repo right now, the
-- shell here can't spawn (bash.exe EPERM), so this SQL has been reviewed
-- statically -- a live psql apply + `task compile CODE=LV_CONDUCTOR` + a
-- connect/isolation-trace check are still owed before it's verified.

INSERT INTO metadata.field_groups (code, name, sort_order) VALUES
    ('electrical', 'Electrical', 1)
ON CONFLICT (code) DO NOTHING;

-- PHASE domain -- normally seeded by seed/phase_model_example.sql;
-- re-asserted idempotently so LV_CONDUCTOR's `phase` field always has a
-- domain to point at regardless of seed order.
INSERT INTO metadata.domains (code, name, domain_type) VALUES
    ('PHASE', 'Phase', 'coded')
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
    ('PHASE', 'L1L2L3N', 'L1 L2 L3 + N (3-phase)', 8)
) AS v(domain_code, code, label, sort_order) ON v.domain_code = d.code
ON CONFLICT (domain_id, code) DO NOTHING;

-- LV_ROLE -- the functional role field's domain. main = the shared LV
-- feeder run leaving the transformer; service = the drop tee-ing off it to
-- an individual supply. A field, NOT a subtype (see the convention note in
-- the header) -- construction is what the subtype models.
INSERT INTO metadata.domains (code, name, domain_type) VALUES
    ('LV_ROLE', 'LV conductor role', 'coded')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domain_values (domain_id, code, label, sort_order)
SELECT d.id, v.code, v.label, v.sort_order
FROM metadata.domains d
JOIN (VALUES
    ('LV_ROLE', 'main',    'Main',    1),
    ('LV_ROLE', 'service', 'Service', 2)
) AS v(domain_code, code, label, sort_order) ON v.domain_code = d.code
ON CONFLICT (domain_id, code) DO NOTHING;

-- material_specs -- real LV cable catalog entries, so LV_CONDUCTOR's
-- conductor_spec is a lookup into engineering numbers (resistance /
-- reactance / ampacity for the future voltage-drop + loading calcs), not
-- an opaque label. Same pattern MV_CONDUCTOR's conductor_spec already
-- uses (migrations/0021). category 'cable' since these are insulated LV
-- cables, not bare overhead line. Thermal columns (migrations/0022) are
-- left NULL -- an IEEE 738 ampacity calc on LV cable is out of scope here.
INSERT INTO metadata.material_specs (code, name, category, resistance_ohm_per_km, reactance_ohm_per_km, ampacity_amps, diameter_mm) VALUES
    ('ABC_95',        'ABC 95mm² (aerial bundled)',       'cable', 0.320, 0.080, 240, 14.0),
    ('WAVEFORM_185',  'Waveform 185mm² (LV underground)', 'cable', 0.164, 0.070, 313, 30.0),
    ('CONCENTRIC_25', 'Concentric 25mm² (service)',       'cable', 0.727, 0.080, 100, 12.0)
ON CONFLICT (code) DO NOTHING;

-- LV_CABLE_SPEC -- the coded domain LV_CONDUCTOR.conductor_spec keys off.
-- Each value is material-backed: material_spec_id links it to the
-- catalog row above (value code = material_specs.code by convention), so
-- selecting "ABC 95mm²" on a conductor resolves straight to its numbers.
INSERT INTO metadata.domains (code, name, domain_type) VALUES
    ('LV_CABLE_SPEC', 'LV cable spec', 'coded')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domain_values (domain_id, code, label, sort_order, material_spec_id)
SELECT d.id, v.code, v.label, v.sort_order, ms.id
FROM metadata.domains d
JOIN (VALUES
    ('ABC_95',        'ABC 95mm² (aerial bundled)',       1),
    ('WAVEFORM_185',  'Waveform 185mm² (LV underground)', 2),
    ('CONCENTRIC_25', 'Concentric 25mm² (service)',       3)
) AS v(code, label, sort_order) ON true
LEFT JOIN metadata.material_specs ms ON ms.code = v.code
WHERE d.code = 'LV_CABLE_SPEC'
ON CONFLICT (domain_id, code) DO NOTHING;

-- LV_CONDUCTOR -- edge, no terminals, never itself the "to" side of a
-- network_rules row (same shape as MV_CONDUCTOR). Two subtypes: overhead
-- vs. underground construction -- mirroring MV_CONDUCTOR exactly.

INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status, is_diagram_container)
VALUES ('LV_CONDUCTOR', 'LV Conductor', 'line', 4326, 'edge', 'draft', false)
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, v.code, v.name
FROM metadata.feature_classes fc
JOIN (VALUES (1, 'overhead_lv_conductor'), (2, 'underground_lv_conductor')) AS v(code, name) ON true
WHERE fc.code = 'LV_CONDUCTOR'
ON CONFLICT (feature_class_id, code) DO NOTHING;

-- Three coded fields, all required per subtype below. phase is required
-- here (unlike the nullable MV_CONDUCTOR.phase added in Slice 0) because
-- LV_CONDUCTOR is a brand-new class with no existing rows -- the compiler
-- takes the CREATE-TABLE branch, so a NOT NULL column is free, and a LV
-- run with no phase is meaningless for the phase-matched connectivity rule
-- below.
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, v.nullable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('phase',          'text', 'PHASE',         'electrical', false),
    ('conductor_spec', 'text', 'LV_CABLE_SPEC', 'electrical', false),
    ('lv_role',        'text', 'LV_ROLE',       'electrical', false)
) AS v(name, data_type, domain_code, group_code, nullable) ON true
LEFT JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code = 'LV_CONDUCTOR'
ON CONFLICT (feature_class_id, name) DO NOTHING;

INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, true
FROM metadata.subtypes s
JOIN metadata.fields f ON f.feature_class_id = s.feature_class_id
WHERE s.feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'LV_CONDUCTOR')
  AND f.name IN ('phase', 'conductor_spec', 'lv_role')
ON CONFLICT (subtype_id, field_id) DO NOTHING;

-- network_rules -- no unique constraint (a rule set is authored
-- deliberately, not upserted), so idempotency is guarded with
-- WHERE NOT EXISTS, same as the other seeds.

-- (1) The transformer's secondary feeds the LV run. from_terminal_id set
-- (DIST_XFMR.T2), LV_CONDUCTOR is a plain edge so no to_terminal_id --
-- same shape as the existing DIST_XFMR T2 -> MV_CONDUCTOR rule.
INSERT INTO metadata.network_rules (name, from_feature_class_id, from_terminal_id, to_feature_class_id, phase_rule, is_valid)
SELECT 'DIST_XFMR T2 -> LV_CONDUCTOR', xf.id, t2.id, lv.id, NULL, true
FROM metadata.feature_classes xf CROSS JOIN metadata.feature_classes lv
JOIN metadata.terminals t2 ON t2.feature_class_id = xf.id AND t2.code = 'T2'
WHERE xf.code = 'DIST_XFMR' AND lv.code = 'LV_CONDUCTOR'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = xf.id AND from_terminal_id = t2.id AND to_feature_class_id = lv.id
  );

-- (2) LV run to LV run -- a main splicing to a service drop, or two
-- mains joined. Both sides are plain edges (from/to_terminal_id NULL).
-- This is the first rule to opt into the Slice 0 phase gate:
-- phase_rule = 'phases_intersect' means the connect matcher requires both
-- endpoints' `phase` values to share a live line conductor (L1/L2/L3),
-- so an L1N service can't silently splice onto an L2N main. The NOT
-- EXISTS guard pins from_terminal_id IS NULL AND to_terminal_id IS NULL
-- so it matches this edge-to-edge rule specifically.
INSERT INTO metadata.network_rules (name, from_feature_class_id, to_feature_class_id, phase_rule, is_valid)
SELECT 'LV_CONDUCTOR -> LV_CONDUCTOR (phase-matched)', lv.id, lv.id, 'phases_intersect', true
FROM metadata.feature_classes lv
WHERE lv.code = 'LV_CONDUCTOR'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = lv.id AND to_feature_class_id = lv.id
      AND from_terminal_id IS NULL AND to_terminal_id IS NULL
  );

-- (3) The LV run terminates at a service point's single terminal. This is
-- the realistic replacement for the retired direct transformer->service
-- hop (see (4)).
INSERT INTO metadata.network_rules (name, from_feature_class_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'LV_CONDUCTOR -> SERVICE_POINT T1', lv.id, sp.id, t1.id, NULL, true
FROM metadata.feature_classes lv CROSS JOIN metadata.feature_classes sp
JOIN metadata.terminals t1 ON t1.feature_class_id = sp.id AND t1.code = 'T1'
WHERE lv.code = 'LV_CONDUCTOR' AND sp.code = 'SERVICE_POINT'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = lv.id AND to_feature_class_id = sp.id AND to_terminal_id = t1.id
  );

-- (4) DESTRUCTIVE REPOINT -- retire the direct DIST_XFMR T2 ->
-- SERVICE_POINT T1 rule seed/service_point_example.sql seeded as a
-- stand-in. Now that a real LV_CONDUCTOR exists, a service point is fed
-- through it, not straight off the transformer terminal. Guarded on
-- is_valid = true so re-running is a no-op once flipped, and so it only
-- ever demotes a live rule (never resurrects one an operator disabled for
-- another reason).
UPDATE metadata.network_rules nr
SET is_valid = false
FROM metadata.feature_classes xf, metadata.terminals t2,
     metadata.feature_classes sp, metadata.terminals t1
WHERE xf.code = 'DIST_XFMR' AND t2.feature_class_id = xf.id AND t2.code = 'T2'
  AND sp.code = 'SERVICE_POINT' AND t1.feature_class_id = sp.id AND t1.code = 'T1'
  AND nr.from_feature_class_id = xf.id AND nr.from_terminal_id = t2.id
  AND nr.to_feature_class_id = sp.id AND nr.to_terminal_id = t1.id
  AND nr.is_valid = true;
