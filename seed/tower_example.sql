-- seed/tower_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives
-- under /seed on purpose so it's never picked up by the migration runner.
-- Demo metadata for the TOWER feature class: a transmission SUPPORT STRUCTURE,
-- sibling of POLE (seed/pole_example.sql) but leaner -- it carries only SOME of
-- POLE's fields (material, height class, install date, condition + inspection)
-- and DROPS pole_use, since a tower is inherently a transmission structure. Its
-- material values are tower-specific (steel lattice / tubular steel / concrete),
-- distinct from POLE_MATERIAL (wood/concrete/steel). Like POLE it is a point with
-- network_role 'none' (a support, not electrically-connected apparatus) and is not
-- a diagram container. Height class and condition rating REUSE POLE's / the shared
-- domains (POLE_HEIGHT_CLASS, CONDITION_RATING) -- those are re-asserted here with
-- ON CONFLICT DO NOTHING so this seed applies standalone, before or after the pole.
--
-- Apply with psql after migrations 0001-0015 have run, e.g.:
--   docker compose exec -T gitbash psql "postgres://postgres:voltedge_dev@postgres:5432/postgres?sslmode=disable" -f /workspace/seed/tower_example.sql
-- or: task seed-tower
-- then compile it:
--   cd api && go run ./cmd/compile TOWER
-- or: task compile CODE=TOWER

INSERT INTO metadata.field_groups (code, name, sort_order) VALUES
    ('general', 'General', 1),
    ('maintenance', 'Maintenance', 2),
    ('asset', 'Asset', 3)
ON CONFLICT (code) DO NOTHING;

-- TOWER_MATERIAL is tower-specific (deliberately different from POLE_MATERIAL).
-- POLE_HEIGHT_CLASS and CONDITION_RATING are shared with POLE and re-asserted so
-- this seed works even when the pole seed hasn't been applied.
INSERT INTO metadata.domains (code, name, domain_type) VALUES
    ('TOWER_MATERIAL', 'Tower material', 'coded'),
    ('POLE_HEIGHT_CLASS', 'Pole height class', 'coded'),
    ('CONDITION_RATING', 'Condition rating', 'coded')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domain_values (domain_id, code, label, sort_order)
SELECT d.id, v.code, v.label, v.sort_order
FROM metadata.domains d
JOIN (VALUES
    ('TOWER_MATERIAL', 'STEEL_LATTICE', 'Steel lattice', 1),
    ('TOWER_MATERIAL', 'TUBULAR_STEEL', 'Tubular steel', 2),
    ('TOWER_MATERIAL', 'CONCRETE',      'Concrete',      3),
    ('POLE_HEIGHT_CLASS', 'CLASS_1', 'Class 1', 1),
    ('POLE_HEIGHT_CLASS', 'CLASS_2', 'Class 2', 2),
    ('POLE_HEIGHT_CLASS', 'CLASS_3', 'Class 3', 3),
    ('CONDITION_RATING', 'GOOD', 'Good', 1),
    ('CONDITION_RATING', 'FAIR', 'Fair', 2),
    ('CONDITION_RATING', 'POOR', 'Poor', 3)
) AS v(domain_code, code, label, sort_order) ON v.domain_code = d.code
ON CONFLICT (domain_id, code) DO NOTHING;

INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status, is_diagram_container)
VALUES ('TOWER', 'Tower', 'point', 4326, 'none', 'draft', false)
ON CONFLICT (code) DO NOTHING;

-- Structure-type subtypes (the transmission-tower typology), replacing POLE's
-- material-based wood/concrete/steel subtypes.
INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, v.code, v.name
FROM metadata.feature_classes fc
JOIN (VALUES
    (1, 'lattice'),
    (2, 'tubular'),
    (3, 'guyed'),
    (4, 'monopole')
) AS v(code, name) ON true
WHERE fc.code = 'TOWER'
ON CONFLICT (feature_class_id, code) DO NOTHING;

-- Some, not all, of POLE's fields: tower_material + height_class + install_date +
-- condition_rating + last_inspected_at. pole_use is intentionally omitted.
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, v.nullable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('tower_material',    'text', 'TOWER_MATERIAL',    'general',     false),
    ('height_class',      'text', 'POLE_HEIGHT_CLASS', 'general',     false),
    ('install_date',      'date', NULL,                'asset',       true),
    ('condition_rating',  'text', 'CONDITION_RATING',  'maintenance', false),
    ('last_inspected_at', 'date', NULL,                'maintenance', true)
) AS v(name, data_type, domain_code, group_code, nullable) ON true
LEFT JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code = 'TOWER'
ON CONFLICT (feature_class_id, name) DO NOTHING;

INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, true
FROM metadata.subtypes s
JOIN metadata.fields f ON f.feature_class_id = s.feature_class_id
WHERE s.feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'TOWER')
  AND f.name IN ('tower_material', 'height_class', 'condition_rating')
ON CONFLICT (subtype_id, field_id) DO NOTHING;
