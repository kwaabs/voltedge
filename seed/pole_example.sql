-- seed/pole_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives
-- under /seed on purpose so it's never picked up by the migration runner.
-- Demo metadata for the POLE feature class exactly as documented in
-- docs/metadata-core.md's "Sample configurations" section, for exercising
-- the schema compiler (api/internal/compiler) end to end.
--
-- Apply with psql after migrations 0001-0015 have run, e.g.:
--   docker compose exec -T gitbash psql "postgres://postgres:voltedge_dev@postgres:5432/postgres?sslmode=disable" -f /workspace/seed/pole_example.sql
-- or: make seed-pole
-- then compile it:
--   cd api && go run ./cmd/compile POLE
-- or: make compile CODE=POLE

INSERT INTO metadata.field_groups (code, name, sort_order) VALUES
    ('general', 'General', 1),
    ('maintenance', 'Maintenance', 2),
    ('asset', 'Asset', 3)
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domains (code, name, domain_type) VALUES
    ('POLE_MATERIAL', 'Pole material', 'coded'),
    ('POLE_USE', 'Pole use', 'coded'),
    ('POLE_HEIGHT_CLASS', 'Pole height class', 'coded'),
    ('CONDITION_RATING', 'Condition rating', 'coded')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domain_values (domain_id, code, label, sort_order)
SELECT d.id, v.code, v.label, v.sort_order
FROM metadata.domains d
JOIN (VALUES
    ('POLE_MATERIAL', 'WOOD', 'Wood', 1),
    ('POLE_MATERIAL', 'CONCRETE', 'Concrete', 2),
    ('POLE_MATERIAL', 'STEEL', 'Steel', 3),
    ('POLE_USE', 'DISTRIBUTION', 'Distribution', 1),
    ('POLE_USE', 'TRANSMISSION', 'Transmission', 2),
    ('POLE_USE', 'SERVICE', 'Service', 3),
    ('POLE_HEIGHT_CLASS', 'CLASS_1', 'Class 1 (9 m)', 1),
    ('POLE_HEIGHT_CLASS', 'CLASS_2', 'Class 2 (12 m)', 2),
    ('POLE_HEIGHT_CLASS', 'CLASS_3', 'Class 3 (15 m)', 3),
    ('CONDITION_RATING', 'GOOD', 'Good', 1),
    ('CONDITION_RATING', 'FAIR', 'Fair', 2),
    ('CONDITION_RATING', 'POOR', 'Poor', 3)
) AS v(domain_code, code, label, sort_order) ON v.domain_code = d.code
ON CONFLICT (domain_id, code) DO NOTHING;

INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status, is_diagram_container)
VALUES ('POLE', 'Pole', 'point', 4326, 'none', 'draft', false)
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, v.code, v.name
FROM metadata.feature_classes fc
JOIN (VALUES (1, 'wood'), (2, 'concrete'), (3, 'steel')) AS v(code, name) ON true
WHERE fc.code = 'POLE'
ON CONFLICT (feature_class_id, code) DO NOTHING;

INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, v.nullable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('pole_material', 'text', 'POLE_MATERIAL', 'general', false),
    ('pole_use', 'text', 'POLE_USE', 'general', false),
    ('height_class', 'text', 'POLE_HEIGHT_CLASS', 'general', false),
    ('install_date', 'date', NULL, 'asset', true),
    ('condition_rating', 'text', 'CONDITION_RATING', 'maintenance', false),
    ('last_inspected_at', 'date', NULL, 'maintenance', true)
) AS v(name, data_type, domain_code, group_code, nullable) ON true
LEFT JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code = 'POLE'
ON CONFLICT (feature_class_id, name) DO NOTHING;

INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, true
FROM metadata.subtypes s
JOIN metadata.fields f ON f.feature_class_id = s.feature_class_id
WHERE s.feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'POLE')
  AND f.name IN ('pole_material', 'pole_use', 'height_class', 'condition_rating')
ON CONFLICT (subtype_id, field_id) DO NOTHING;
