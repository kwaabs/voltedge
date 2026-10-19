-- seed/land_spatial_example.sql
-- Purpose: NOT a migration -- apply.sh scans only /migrations; this lives under
-- /seed like the other class seeds. LAND & SPATIAL MANAGEMENT: four polygon
-- classes that record the LAND the network sits on and the vegetation around it,
-- so the spatial engine (api/internal/spatial) can answer "what assets are inside
-- this easement / corridor / parcel?" and "what land covers this asset?" from the
-- geometry itself. None are network participants (network_role 'none') -- they are
-- land/legal/environmental context, not nodes power flows through.
--
--   * LAND_PARCEL     -- a cadastral land unit (the land an easement burdens).
--   * EASEMENT        -- the legal right to keep the utility's assets on the land.
--   * UTILITY_CORRIDOR-- the managed strip the network runs through (a ROW).
--   * VEGETATION_ZONE -- a patch of vegetation, for clearance / trim analysis.
--
-- Apply with psql, then compile each (the fields below become real columns):
--   task seed-land-spatial           (or psql -f seed/land_spatial_example.sql)
--   task compile CODE=LAND_PARCEL
--   task compile CODE=EASEMENT
--   task compile CODE=UTILITY_CORRIDOR
--   task compile CODE=VEGETATION_ZONE
-- Classes seed as 'draft'; publish them from /admin to make them available.

-- ------------------------------------------------------------- field groups --
INSERT INTO metadata.field_groups (code, name, sort_order) VALUES
    ('land', 'Land', 7),
    ('legal', 'Legal', 8),
    ('vegetation', 'Vegetation', 9)
ON CONFLICT (code) DO NOTHING;

-- ----------------------------------------------------------------- domains --
INSERT INTO metadata.domains (code, name, domain_type) VALUES
    ('LAND_USE', 'Land use', 'coded'),
    ('EASEMENT_TYPE', 'Easement type', 'coded'),
    ('EASEMENT_STATUS', 'Easement status', 'coded'),
    ('CORRIDOR_TYPE', 'Corridor type', 'coded'),
    ('VEGETATION_TYPE', 'Vegetation type', 'coded'),
    ('CONDITION_RATING', 'Condition rating', 'coded')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domain_values (domain_id, code, label, sort_order)
SELECT d.id, v.code, v.label, v.sort_order
FROM metadata.domains d
JOIN (VALUES
    ('LAND_USE', 'AGRICULTURAL', 'Agricultural', 1),
    ('LAND_USE', 'RESIDENTIAL',  'Residential',  2),
    ('LAND_USE', 'COMMERCIAL',   'Commercial',   3),
    ('LAND_USE', 'INDUSTRIAL',   'Industrial',   4),
    ('LAND_USE', 'FOREST',       'Forest',       5),
    ('LAND_USE', 'PUBLIC',       'Public / institutional', 6),
    ('LAND_USE', 'VACANT',       'Vacant',       7),
    ('LAND_USE', 'OTHER',        'Other',        8),

    ('EASEMENT_TYPE', 'WAYLEAVE',        'Wayleave',            1),
    ('EASEMENT_TYPE', 'UNDERGROUND',     'Underground cable',   2),
    ('EASEMENT_TYPE', 'OVERHEAD',        'Overhead line',       3),
    ('EASEMENT_TYPE', 'ACCESS',          'Access',              4),
    ('EASEMENT_TYPE', 'SUBSTATION',      'Substation site',     5),
    ('EASEMENT_TYPE', 'GENERAL',         'General',             6),

    ('EASEMENT_STATUS', 'ACTIVE',     'Active',     1),
    ('EASEMENT_STATUS', 'PENDING',    'Pending',    2),
    ('EASEMENT_STATUS', 'EXPIRED',    'Expired',    3),
    ('EASEMENT_STATUS', 'TERMINATED', 'Terminated', 4),

    ('CORRIDOR_TYPE', 'TRANSMISSION',   'Transmission',  1),
    ('CORRIDOR_TYPE', 'DISTRIBUTION',   'Distribution',  2),
    ('CORRIDOR_TYPE', 'MIXED',          'Mixed',         3),
    ('CORRIDOR_TYPE', 'COMMUNICATIONS', 'Communications',4),

    ('VEGETATION_TYPE', 'TREE',   'Tree',   1),
    ('VEGETATION_TYPE', 'FOREST', 'Forest / woodland', 2),
    ('VEGETATION_TYPE', 'HEDGE',  'Hedge',  3),
    ('VEGETATION_TYPE', 'SHRUB',  'Shrub',  4),
    ('VEGETATION_TYPE', 'GRASS',  'Grass',  5),
    ('VEGETATION_TYPE', 'OTHER',  'Other',  6),

    -- CONDITION_RATING is normally seeded upstream; re-asserted so the vegetation
    -- condition field below always has values even applied standalone.
    ('CONDITION_RATING', 'GOOD', 'Good', 1),
    ('CONDITION_RATING', 'FAIR', 'Fair', 2),
    ('CONDITION_RATING', 'POOR', 'Poor', 3)
) AS v(domain_code, code, label, sort_order) ON v.domain_code = d.code
ON CONFLICT (domain_id, code) DO NOTHING;

-- ============================================================================
-- LAND_PARCEL -- a cadastral land unit. polygon footprint, network_role 'none'.
-- ============================================================================
INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status)
VALUES ('LAND_PARCEL', 'Land Parcel', 'polygon', 4326, 'none', 'draft')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, 1, 'standard' FROM metadata.feature_classes fc WHERE fc.code = 'LAND_PARCEL'
ON CONFLICT (feature_class_id, code) DO NOTHING;

INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, v.nullable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('parcel_ref', 'text',    NULL,       'land', false),
    ('owner_name', 'text',    NULL,       'land', true),
    ('land_use',   'text',    'LAND_USE', 'land', true),
    ('area_sqm',   'numeric', NULL,       'land', true)
) AS v(name, data_type, domain_code, group_code, nullable) ON true
LEFT JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code = 'LAND_PARCEL'
ON CONFLICT (feature_class_id, name) DO NOTHING;

INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, true
FROM metadata.subtypes s
JOIN metadata.fields f ON f.feature_class_id = s.feature_class_id
WHERE s.feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'LAND_PARCEL')
  AND f.name = 'parcel_ref'
ON CONFLICT (subtype_id, field_id) DO NOTHING;

-- ============================================================================
-- EASEMENT -- the legal right to keep the network's assets on the land.
-- ============================================================================
INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status)
VALUES ('EASEMENT', 'Easement', 'polygon', 4326, 'none', 'draft')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, 1, 'standard' FROM metadata.feature_classes fc WHERE fc.code = 'EASEMENT'
ON CONFLICT (feature_class_id, code) DO NOTHING;

INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, v.nullable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('easement_ref',    'text', NULL,              'legal', false),
    ('easement_type',   'text', 'EASEMENT_TYPE',   'legal', false),
    ('easement_status', 'text', 'EASEMENT_STATUS', 'legal', true),
    ('grantor',         'text', NULL,              'legal', true),
    ('granted_date',    'date', NULL,              'legal', true),
    ('expiry_date',     'date', NULL,              'legal', true),
    ('consideration',   'text', NULL,              'legal', true)
) AS v(name, data_type, domain_code, group_code, nullable) ON true
LEFT JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code = 'EASEMENT'
ON CONFLICT (feature_class_id, name) DO NOTHING;

INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, true
FROM metadata.subtypes s
JOIN metadata.fields f ON f.feature_class_id = s.feature_class_id
WHERE s.feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'EASEMENT')
  AND f.name IN ('easement_ref', 'easement_type')
ON CONFLICT (subtype_id, field_id) DO NOTHING;

-- ============================================================================
-- UTILITY_CORRIDOR -- the managed strip (right-of-way) the network runs through.
-- ============================================================================
INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status)
VALUES ('UTILITY_CORRIDOR', 'Utility Corridor', 'polygon', 4326, 'none', 'draft')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, 1, 'standard' FROM metadata.feature_classes fc WHERE fc.code = 'UTILITY_CORRIDOR'
ON CONFLICT (feature_class_id, code) DO NOTHING;

INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, v.nullable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('corridor_ref',  'text',    NULL,            'land', true),
    ('corridor_type', 'text',    'CORRIDOR_TYPE', 'land', false),
    ('width_m',       'numeric', NULL,            'land', true),
    ('managed',       'boolean', NULL,            'land', true)
) AS v(name, data_type, domain_code, group_code, nullable) ON true
LEFT JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code = 'UTILITY_CORRIDOR'
ON CONFLICT (feature_class_id, name) DO NOTHING;

INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, true
FROM metadata.subtypes s
JOIN metadata.fields f ON f.feature_class_id = s.feature_class_id
WHERE s.feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'UTILITY_CORRIDOR')
  AND f.name = 'corridor_type'
ON CONFLICT (subtype_id, field_id) DO NOTHING;

-- ============================================================================
-- VEGETATION_ZONE -- a patch of vegetation, for clearance / trim analysis.
-- ============================================================================
INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status)
VALUES ('VEGETATION_ZONE', 'Vegetation Zone', 'polygon', 4326, 'none', 'draft')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, 1, 'standard' FROM metadata.feature_classes fc WHERE fc.code = 'VEGETATION_ZONE'
ON CONFLICT (feature_class_id, code) DO NOTHING;

INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, v.nullable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('veg_type',          'text',    'VEGETATION_TYPE',  'vegetation', false),
    ('species',           'text',    NULL,               'vegetation', true),
    ('height_m',          'numeric', NULL,               'vegetation', true),
    ('condition',         'text',    'CONDITION_RATING', 'vegetation', true),
    ('last_trimmed',      'date',    NULL,               'vegetation', true),
    ('trim_cycle_months', 'integer', NULL,               'vegetation', true)
) AS v(name, data_type, domain_code, group_code, nullable) ON true
LEFT JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code = 'VEGETATION_ZONE'
ON CONFLICT (feature_class_id, name) DO NOTHING;

INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, true
FROM metadata.subtypes s
JOIN metadata.fields f ON f.feature_class_id = s.feature_class_id
WHERE s.feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'VEGETATION_ZONE')
  AND f.name = 'veg_type'
ON CONFLICT (subtype_id, field_id) DO NOTHING;
