-- seed/structure_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives
-- under /seed on purpose, same reasoning as seed/substation_yard_example.sql.
-- Metadata for STRUCTURE -- a generic polygon building/structure class used to
-- record WHERE a service point or meter is physically hosted (the premises).
-- Deliberately generic: any building or structure, permanent or temporary,
-- whose attributes are descriptive (use, storeys, basement) rather than
-- electrical -- network_role 'none', it is not a network participant, it is the
-- premises a SERVICE_POINT / METER lives in.
--
-- Apply with psql after the metering seeds (SERVICE_POINT + METER must exist for
-- the containment pairings to attach; the JOINs skip any class not yet seeded):
--   task seed-structure
-- then compile it (the fields below become real columns):
--   task compile CODE=STRUCTURE

-- ------------------------------------------------------------- field group --
INSERT INTO metadata.field_groups (code, name, sort_order) VALUES
    ('building', 'Building', 6)
ON CONFLICT (code) DO NOTHING;

-- ----------------------------------------------------------------- domain --
-- STRUCTURE_USE is the premises classification. Coded so it stays a controlled
-- list (report/filter by use), open-ended by design -- add values as new use
-- types appear; OTHER is the catch-all so a structure is never un-classifiable.
INSERT INTO metadata.domains (code, name, domain_type) VALUES
    ('STRUCTURE_USE', 'Structure use', 'coded')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domain_values (domain_id, code, label, sort_order)
SELECT d.id, v.code, v.label, v.sort_order
FROM metadata.domains d
JOIN (VALUES
    ('STRUCTURE_USE', 'RESIDENCE',     'Residence',     1),
    ('STRUCTURE_USE', 'COMMERCIAL',    'Commercial',    2),
    ('STRUCTURE_USE', 'INDUSTRIAL',    'Industrial',    3),
    ('STRUCTURE_USE', 'KIOSK',         'Kiosk',         4),
    ('STRUCTURE_USE', 'HOSPITAL',      'Hospital',      5),
    ('STRUCTURE_USE', 'INSTITUTIONAL', 'Institutional', 6),
    ('STRUCTURE_USE', 'EDUCATIONAL',   'Educational',   7),
    ('STRUCTURE_USE', 'RELIGIOUS',     'Religious',     8),
    ('STRUCTURE_USE', 'MIXED_USE',     'Mixed use',     9),
    ('STRUCTURE_USE', 'OTHER',         'Other',         10)
) AS v(domain_code, code, label, sort_order) ON v.domain_code = d.code
ON CONFLICT (domain_id, code) DO NOTHING;

-- --------------------------------------------------------------- the class --
-- geometry 'polygon' (the building footprint), srid 4326. network_role 'none'
-- -- a structure is a premises boundary, not a network node; nothing connects
-- through it. Not a diagram container/placeable (default false) -- it lives on
-- the map, not on a schematic.
INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status)
VALUES ('STRUCTURE', 'Structure', 'polygon', 4326, 'none', 'draft')
ON CONFLICT (code) DO NOTHING;

-- Subtypes name the permanence axis the user asked for (permanent vs temporary
-- building). The functional classification is the use_type FIELD, not a subtype.
INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, v.code, v.name
FROM metadata.feature_classes fc
JOIN (VALUES (1, 'permanent'), (2, 'temporary')) AS v(code, name) ON true
WHERE fc.code = 'STRUCTURE'
ON CONFLICT (feature_class_id, code) DO NOTHING;

-- use_type is the required classification (coded STRUCTURE_USE). storeys
-- (integer) and has_basement (boolean) are descriptive and optional -- not
-- every structure records them, and "could be anything" per the brief.
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, v.nullable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('use_type',     'text',    'STRUCTURE_USE', 'building', false),
    ('storeys',      'integer', NULL,            'building', true),
    ('has_basement', 'boolean', NULL,            'building', true)
) AS v(name, data_type, domain_code, group_code, nullable) ON true
LEFT JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code = 'STRUCTURE'
ON CONFLICT (feature_class_id, name) DO NOTHING;

-- use_type required on both subtypes (permanent + temporary).
INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, true
FROM metadata.subtypes s
JOIN metadata.fields f ON f.feature_class_id = s.feature_class_id
WHERE s.feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'STRUCTURE')
  AND f.name = 'use_type'
ON CONFLICT (subtype_id, field_id) DO NOTHING;

-- ============================================================================
-- CONTAINMENT relationship_classes -- legal pairings only (no instances),
-- guarded on (origin, destination, kind) via NOT EXISTS. A class not yet seeded
-- is skipped by the JOIN. This is what makes "this meter / service point is
-- hosted in that building" an establishable relationship. one_to_many: a
-- structure hosts many service points / meters, each hosted in one structure.
-- Adding STRUCTURE as a parent doesn't disturb SERVICE_POINT / METER's existing
-- parents (a device instance still picks exactly one parent).
-- ============================================================================

-- STRUCTURE contains SERVICE_POINT (one_to_many).
INSERT INTO metadata.relationship_classes (name, origin_feature_class_id, destination_feature_class_id, kind, cardinality)
SELECT 'STRUCTURE contains SERVICE_POINT', st.id, sp.id, 'containment', 'one_to_many'
FROM metadata.feature_classes st, metadata.feature_classes sp
WHERE st.code = 'STRUCTURE' AND sp.code = 'SERVICE_POINT'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.relationship_classes
    WHERE origin_feature_class_id = st.id AND destination_feature_class_id = sp.id AND kind = 'containment'
  );

-- STRUCTURE contains METER (one_to_many). For a meter hosted directly on the
-- premises with no chamber (e.g. a wall-mounted meter). A meter inside a chamber
-- is contained by the chamber instead (METER_CHAMBER contains METER, seeded in
-- lv_metering_example.sql); this pairing is the chamber-less case.
INSERT INTO metadata.relationship_classes (name, origin_feature_class_id, destination_feature_class_id, kind, cardinality)
SELECT 'STRUCTURE contains METER', st.id, m.id, 'containment', 'one_to_many'
FROM metadata.feature_classes st, metadata.feature_classes m
WHERE st.code = 'STRUCTURE' AND m.code = 'METER'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.relationship_classes
    WHERE origin_feature_class_id = st.id AND destination_feature_class_id = m.id AND kind = 'containment'
  );

-- STRUCTURE contains METER_CHAMBER (one_to_many). The primary hosting case: a
-- building holds one or more meter chambers, and each chamber in turn contains
-- its busbar + meters (METER_CHAMBER contains ..., lv_metering_example.sql). This
-- is what makes the containment suggestion appear when a meter chamber is placed
-- inside a structure. A structure can host many chambers; a chamber sits in one.
INSERT INTO metadata.relationship_classes (name, origin_feature_class_id, destination_feature_class_id, kind, cardinality)
SELECT 'STRUCTURE contains METER_CHAMBER', st.id, mc.id, 'containment', 'one_to_many'
FROM metadata.feature_classes st, metadata.feature_classes mc
WHERE st.code = 'STRUCTURE' AND mc.code = 'METER_CHAMBER'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.relationship_classes
    WHERE origin_feature_class_id = st.id AND destination_feature_class_id = mc.id AND kind = 'containment'
  );
