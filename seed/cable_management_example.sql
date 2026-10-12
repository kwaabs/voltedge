-- seed/cable_management_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives under
-- /seed on purpose so it's never picked up by the migration runner.
-- Phase 44 Slice 1 (docs/phase-44-cable-management.md): the CIVIL cable-management
-- inventory -- three network_role='none' classes that record where cable physically
-- lives, WITHOUT ever joining the electrical graph (they are inventory + a parallel
-- civil graph, exactly like POLE/TOWER support furniture -- seed/tower_example.sql):
--
--   CABLE_TRENCH   (line)    -- the excavation a cable/duct is buried in
--   CABLE_DUCT     (line)    -- the conduit a cable is pulled through; carries
--                               way_count = the coarse spare-way capacity number
--   CABLE_CHAMBER  (polygon) -- the manhole / jointing pit a joint sits in
--
-- These carry the same lean field spine TOWER uses (CONDITION_RATING + install_date
-- + last_inspected_at from the shared domains) plus a couple of class-specific coded
-- fields. NO terminals (network_role='none', so no network_rules), NO relationship
-- classes here (the cable_routing coupling + chamber->joint containment come in
-- Slices 2-3), NO engine touch. Pure inventory.
--
-- Apply with psql after the base migrations (0001-0015+), e.g.:
--   docker compose exec -T gitbash psql "postgres://postgres:voltedge_dev@postgres:5432/postgres?sslmode=disable" -f /workspace/seed/cable_management_example.sql
-- or: task seed-cable-management
-- then compile each:
--   task compile CODE=CABLE_TRENCH   (and CABLE_DUCT, CABLE_CHAMBER)

INSERT INTO metadata.field_groups (code, name, sort_order) VALUES
    ('general', 'General', 1),
    ('maintenance', 'Maintenance', 2),
    ('asset', 'Asset', 3)
ON CONFLICT (code) DO NOTHING;

-- Class-specific coded domains. CONDITION_RATING is shared with POLE/TOWER and
-- re-asserted (ON CONFLICT DO NOTHING) so this seed applies standalone.
INSERT INTO metadata.domains (code, name, domain_type) VALUES
    ('TRENCH_SURFACE', 'Trench surface',   'coded'),
    ('DUCT_MATERIAL',  'Duct material',    'coded'),
    ('CHAMBER_COVER',  'Chamber cover',    'coded'),
    ('CONDITION_RATING', 'Condition rating', 'coded')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domain_values (domain_id, code, label, sort_order)
SELECT d.id, v.code, v.label, v.sort_order
FROM metadata.domains d
JOIN (VALUES
    ('TRENCH_SURFACE', 'ROAD',    'Road',    1),
    ('TRENCH_SURFACE', 'FOOTWAY', 'Footway', 2),
    ('TRENCH_SURFACE', 'VERGE',   'Verge',   3),
    ('TRENCH_SURFACE', 'UNMADE',  'Unmade',  4),
    ('DUCT_MATERIAL', 'PVC',          'PVC',          1),
    ('DUCT_MATERIAL', 'HDPE',         'HDPE',         2),
    ('DUCT_MATERIAL', 'STEEL',        'Steel',        3),
    ('DUCT_MATERIAL', 'EARTHENWARE',  'Earthenware',  4),
    ('CHAMBER_COVER', 'LIGHT',  'Light duty',  1),
    ('CHAMBER_COVER', 'MEDIUM', 'Medium duty', 2),
    ('CHAMBER_COVER', 'HEAVY',  'Heavy duty',  3),
    ('CONDITION_RATING', 'GOOD', 'Good', 1),
    ('CONDITION_RATING', 'FAIR', 'Fair', 2),
    ('CONDITION_RATING', 'POOR', 'Poor', 3)
) AS v(domain_code, code, label, sort_order) ON v.domain_code = d.code
ON CONFLICT (domain_id, code) DO NOTHING;

-- =========================================================================
-- Feature classes -- all network_role='none' (civil inventory, never traced),
-- none is a diagram container. Geometry: trench+duct are lines, chamber is a
-- polygon (a manhole footprint), geometry_optional on the chamber so one can be
-- dropped without a surveyed outline.
-- =========================================================================
INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status, is_diagram_container)
VALUES
    ('CABLE_TRENCH',  'Cable trench',  'line',    4326, 'none', 'draft', false),
    ('CABLE_DUCT',    'Cable duct',    'line',    4326, 'none', 'draft', false)
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status, is_diagram_container, geometry_optional)
VALUES
    ('CABLE_CHAMBER', 'Cable chamber', 'polygon', 4326, 'none', 'draft', false, true)
ON CONFLICT (code) DO NOTHING;

-- Chamber subtypes -- the housing typology (metadata.subtypes.code is an INTEGER
-- key stored on the feature row: 1 manhole, 2 footway box, 3 joint bay, 4 jointing
-- pit). Trench/duct have no subtypes (their
-- variability is carried by the surface_type / duct_material coded fields).
INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, v.code, v.name
FROM metadata.feature_classes fc
JOIN (VALUES
    (1, 'Manhole'),
    (2, 'Footway box'),
    (3, 'Joint bay'),
    (4, 'Jointing pit')
) AS v(code, name) ON true
WHERE fc.code = 'CABLE_CHAMBER'
ON CONFLICT (feature_class_id, code) DO NOTHING;

-- =========================================================================
-- Fields.
-- =========================================================================

-- CABLE_TRENCH: surface_type (coded) + depth/width (mm) + shared maintenance spine.
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, v.nullable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('surface_type',      'text',    'TRENCH_SURFACE',   'general',     false),
    ('depth_mm',          'integer', NULL,               'general',     true),
    ('width_mm',          'integer', NULL,               'general',     true),
    ('condition_rating',  'text',    'CONDITION_RATING', 'maintenance', false),
    ('install_date',      'date',    NULL,               'asset',       true),
    ('last_inspected_at', 'date',    NULL,               'maintenance', true)
) AS v(name, data_type, domain_code, group_code, nullable) ON true
LEFT JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code = 'CABLE_TRENCH'
ON CONFLICT (feature_class_id, name) DO NOTHING;

-- CABLE_DUCT: duct_material (coded) + way_count (the coarse capacity number,
-- NOT NULL -- supplied on create; metadata.fields has no class-level default,
-- so there's no server-side default 1, the create form requires a value) +
-- bore_diameter_mm + shared maintenance spine.
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, v.nullable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('duct_material',     'text',    'DUCT_MATERIAL',    'general',     false),
    ('way_count',         'integer', NULL,               'general',     false),
    ('bore_diameter_mm',  'integer', NULL,               'general',     true),
    ('condition_rating',  'text',    'CONDITION_RATING', 'maintenance', false),
    ('install_date',      'date',    NULL,               'asset',       true),
    ('last_inspected_at', 'date',    NULL,               'maintenance', true)
) AS v(name, data_type, domain_code, group_code, nullable) ON true
LEFT JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code = 'CABLE_DUCT'
ON CONFLICT (feature_class_id, name) DO NOTHING;

-- CABLE_CHAMBER: access_cover_type (coded) + shared maintenance spine.
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, v.nullable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('access_cover_type', 'text', 'CHAMBER_COVER',    'general',     false),
    ('condition_rating',  'text', 'CONDITION_RATING', 'maintenance', false),
    ('install_date',      'date', NULL,               'asset',       true),
    ('last_inspected_at', 'date', NULL,               'maintenance', true)
) AS v(name, data_type, domain_code, group_code, nullable) ON true
LEFT JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code = 'CABLE_CHAMBER'
ON CONFLICT (feature_class_id, name) DO NOTHING;

-- =========================================================================
-- Subtype field rules -- only CABLE_CHAMBER has subtypes; require the coded
-- identity fields on every chamber subtype (mirrors TOWER's subtype rules).
-- =========================================================================
INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, true
FROM metadata.subtypes s
JOIN metadata.fields f ON f.feature_class_id = s.feature_class_id
WHERE s.feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'CABLE_CHAMBER')
  AND f.name IN ('access_cover_type', 'condition_rating')
ON CONFLICT (subtype_id, field_id) DO NOTHING;
