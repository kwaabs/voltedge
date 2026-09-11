-- seed/control_building_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives under
-- /seed on purpose, same reasoning as seed/substation_yard_example.sql.
--
-- Phase 47 Slice 1 (docs/phase-47-substation-site-model.md) -- the OPTIONAL
-- control room / building of a substation, recorded as an inventoried asset.
--
-- Slice 1 originally also re-homed the control panels INTO the building (a
-- CONTROL_BUILDING contains SUBSTATION_PANEL edge, kept alongside SUBSTATION
-- contains SUBSTATION_PANEL as a dual legal parent). The Phase 47 minimal-model
-- correction dropped that: a panel's sole parent is SUBSTATION, and the building
-- is an optional map polygon that is NOT a containment parent. This keeps the
-- model single-parent throughout. See migration 0086, which removes the edge on
-- databases that already ran the old form of this seed.
--
-- Introduced in Slice 1 (built before the Slice 2 rename SUBSTATION_YARD ->
-- SUBSTATION, so it carried no rename risk); Slice 2 has since renamed the
-- reference here to SUBSTATION, along with every other seed and migration 0085.
--
-- The building is a POLYGON with OPTIONAL geometry (Phase 26): you can draw its
-- footprint inside the fence, or just record that a building exists with no
-- geometry. network_role 'container' is retained (the class is a site facility,
-- and the role is harmless with no containment edges), network NULL (a building
-- is not an electrical network participant -- same as the yard). No required
-- fields -- a building may be recorded with only a footprint.
--
-- Apply with psql, then compile (a polygon class compiles a nullable geom column
-- for optional geometry, plus territory_id, plus the five fields):
--   task seed-control-building
--   task compile CODE=CONTROL_BUILDING
--
-- Depends on: seed/substation_yard_example.sql (SUBSTATION + CONDITION_RATING
-- domain) and seed/substation_panel_example.sql (SUBSTATION_PANEL). Any class not
-- yet seeded is simply skipped by the JOINs, so this file is order-independent.

INSERT INTO metadata.field_groups (code, name, sort_order) VALUES
    ('building', 'Building', 1)
ON CONFLICT (code) DO NOTHING;

-- CONDITION_RATING is shared (re-asserted defensively for order-independence, the
-- same pattern the yard/bay seeds use). BUILDING_CONSTRUCTION is new.
INSERT INTO metadata.domains (code, name, domain_type) VALUES
    ('CONDITION_RATING', 'Condition rating', 'coded'),
    ('BUILDING_CONSTRUCTION', 'Building construction', 'coded')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domain_values (domain_id, code, label, sort_order)
SELECT d.id, v.code, v.label, v.sort_order
FROM metadata.domains d
JOIN (VALUES
    ('CONDITION_RATING', 'GOOD', 'Good', 1),
    ('CONDITION_RATING', 'FAIR', 'Fair', 2),
    ('CONDITION_RATING', 'POOR', 'Poor', 3),
    ('BUILDING_CONSTRUCTION', 'BRICK', 'Brick / masonry', 1),
    ('BUILDING_CONSTRUCTION', 'CONCRETE', 'Reinforced concrete', 2),
    ('BUILDING_CONSTRUCTION', 'PREFAB', 'Prefabricated', 3),
    ('BUILDING_CONSTRUCTION', 'CONTAINERIZED', 'Containerized / packaged', 4),
    ('BUILDING_CONSTRUCTION', 'STEEL_CLAD', 'Steel-clad', 5)
) AS v(domain_code, code, label, sort_order) ON v.domain_code = d.code
ON CONFLICT (domain_id, code) DO NOTHING;

-- CONTROL_BUILDING -- polygon, OPTIONAL geometry (geometry_optional=true),
-- network_role 'container' (holds panels), network NULL. is_diagram_container and
-- is_diagram_placeable both false: a building is a map footprint, not a one-line
-- element (a building schematic is a later nicety, out of scope this slice).
INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status, is_diagram_container, is_diagram_placeable, geometry_optional)
VALUES ('CONTROL_BUILDING', 'Control Building', 'polygon', 4326, 'container', 'draft', false, false, true)
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, 1, 'standard'
FROM metadata.feature_classes fc
WHERE fc.code = 'CONTROL_BUILDING'
ON CONFLICT (feature_class_id, code) DO NOTHING;

-- All fields nullable -- a building may be recorded with just a footprint.
-- num_floors is the user's headline example; floor_area_sqm rounds out the
-- physical record; construction_type + condition_rating are the coded facets.
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, v.nullable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('building_ref',      'text',    NULL,                    'building', true),
    ('num_floors',        'integer', NULL,                    'building', true),
    ('construction_type', 'text',    'BUILDING_CONSTRUCTION', 'building', true),
    ('floor_area_sqm',    'numeric', NULL,                    'building', true),
    ('condition_rating',  'text',    'CONDITION_RATING',      'building', true)
) AS v(name, data_type, domain_code, group_code, nullable) ON true
LEFT JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code = 'CONTROL_BUILDING'
ON CONFLICT (feature_class_id, name) DO NOTHING;

-- ===========================================================================
-- Containment. Both containment / one_to_many, metadata-only (no instances --
-- those are API-established), guarded on (origin, destination, kind).
-- ===========================================================================

-- SUBSTATION contains CONTROL_BUILDING (a substation has at most a few buildings).
INSERT INTO metadata.relationship_classes (name, origin_feature_class_id, destination_feature_class_id, kind, cardinality)
SELECT 'SUBSTATION contains CONTROL_BUILDING', yard.id, bldg.id, 'containment', 'one_to_many'
FROM metadata.feature_classes yard, metadata.feature_classes bldg
WHERE yard.code = 'SUBSTATION' AND bldg.code = 'CONTROL_BUILDING'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.relationship_classes rc
    WHERE rc.origin_feature_class_id = yard.id
      AND rc.destination_feature_class_id = bldg.id
      AND rc.kind = 'containment'
  );

-- NOTE: the CONTROL_BUILDING contains SUBSTATION_PANEL edge is intentionally NOT
-- seeded (Phase 47 minimal-model correction). A panel's sole parent is SUBSTATION
-- (substation_panel_containment_example.sql); the building is not a containment
-- parent. Migration 0086 removes this edge from databases that seeded the old form.

-- ===========================================================================
-- Map symbology -- mandatory default (subtype+domain null) to clear the publish
-- gate (docs/metadata-core.md). Polygon fill/stroke, slate, matching the yard's
-- own polygon rule (symbology_example.sql) but lighter fill so a building reads
-- as a distinct block inside the yard. Resolved client-side, no compile.
-- ===========================================================================
INSERT INTO metadata.symbology_rules (feature_class_id, render_context, style_def)
SELECT fc.id, 'map', '{"fill_color": "#cbd5e1", "stroke_color": "#475569"}'::jsonb
FROM metadata.feature_classes fc
WHERE fc.code = 'CONTROL_BUILDING'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules
    WHERE feature_class_id = fc.id AND subtype_id IS NULL AND domain_value_id IS NULL AND render_context = 'map'
  );
