-- seed/substation_yard_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives
-- under /seed on purpose, same reasoning as seed/pole_example.sql.
-- Demo metadata for SUBSTATION_YARD -- a polygon feature class, the
-- first one in this repo. Every other seeded feature class so far is
-- point (POLE, DIST_XFMR) or line (MV_CONDUCTOR); this exists so the
-- map's draw tool (docs/phase-3-create-feature.md) has all three
-- geometry types to draw and test against.
--
-- Apply with psql after migrations 0001-0016 have run, e.g.:
--   task seed-substation-yard
-- then compile it:
--   task compile CODE=SUBSTATION_YARD

INSERT INTO metadata.field_groups (code, name, sort_order) VALUES
    ('site', 'Site', 1)
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domains (code, name, domain_type) VALUES
    ('YARD_ENCLOSURE', 'Yard enclosure', 'coded'),
    ('CONDITION_RATING', 'Condition rating', 'coded')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domain_values (domain_id, code, label, sort_order)
SELECT d.id, v.code, v.label, v.sort_order
FROM metadata.domains d
JOIN (VALUES
    ('YARD_ENCLOSURE', 'FENCED', 'Fenced', 1),
    ('YARD_ENCLOSURE', 'WALLED', 'Walled', 2),
    ('YARD_ENCLOSURE', 'OPEN', 'Open', 3),
    ('CONDITION_RATING', 'GOOD', 'Good', 1),
    ('CONDITION_RATING', 'FAIR', 'Fair', 2),
    ('CONDITION_RATING', 'POOR', 'Poor', 3)
) AS v(domain_code, code, label, sort_order) ON v.domain_code = d.code
ON CONFLICT (domain_id, code) DO NOTHING;

-- network_role 'none' -- a substation yard is a site boundary, not a
-- network participant; nothing here connects through terminals the way
-- DIST_XFMR does.
INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status, is_diagram_container)
VALUES ('SUBSTATION_YARD', 'Substation Yard', 'polygon', 4326, 'none', 'draft', false)
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, v.code, v.name
FROM metadata.feature_classes fc
JOIN (VALUES (1, 'distribution_substation'), (2, 'transmission_substation')) AS v(code, name) ON true
WHERE fc.code = 'SUBSTATION_YARD'
ON CONFLICT (feature_class_id, code) DO NOTHING;

INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, v.nullable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('enclosure_type', 'text', 'YARD_ENCLOSURE', 'site', false),
    ('condition_rating', 'text', 'CONDITION_RATING', 'site', false)
) AS v(name, data_type, domain_code, group_code, nullable) ON true
LEFT JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code = 'SUBSTATION_YARD'
ON CONFLICT (feature_class_id, name) DO NOTHING;

INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, true
FROM metadata.subtypes s
JOIN metadata.fields f ON f.feature_class_id = s.feature_class_id
WHERE s.feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'SUBSTATION_YARD')
  AND f.name IN ('enclosure_type', 'condition_rating')
ON CONFLICT (subtype_id, field_id) DO NOTHING;
