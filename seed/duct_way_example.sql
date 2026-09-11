-- seed/duct_way_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives under
-- /seed on purpose so it's never picked up by the migration runner.
-- Phase 44 Slice 4 (docs/phase-44-cable-management.md): the per-way sub-entity that
-- turns the COARSE duct-capacity model (Slice 3: way_count field minus a flat COUNT
-- of cable_routing rows) into a TRUE per-bore one.
--
--   DUCT_WAY  (geometry_type='none', network_role='none')
--     -- one bore/way inside a CABLE_DUCT. Purely logical: it has no map geometry
--        of its own (it lives inside its parent duct's line) and never joins the
--        electrical graph -- exactly like MV_SWITCHBOARD / MV_SWITCHGEAR_PANEL are
--        geometry-less container children. Its only field is way_number, the bore
--        position 1..N. A duct with way_count = N fans out N of these (the
--        POST /ducts composite endpoint, api/internal/duct), each contained in the
--        duct, so a cable's cable_routing link can target a SPECIFIC way.
--
-- Relationship classes added here (no migration needed -- both kinds already exist):
--   CABLE_DUCT contains DUCT_WAY            (containment, one_to_many) -- the fan-out
--   DUCT_WAY   carries  {MV,LV}_CONDUCTOR   (cable_routing, many_to_many) -- per-way
--                                              routing; a cable occupies a way
--
-- The Slice 3 CABLE_DUCT->conductor cable_routing classes stay (seed/cable_routing_
-- example.sql): a duct with NO ways still answers capacity coarsely. The capacity
-- endpoint (api/internal/crud/capacity.go) uses per-way occupancy when the duct has
-- DUCT_WAY children and falls back to the coarse count when it doesn't.
--
-- NO symbology rule: a geometry_type='none' class is never rendered on the map or a
-- diagram, so the publish/compile path (api/internal/compiler) needs none -- the
-- same as MV_SWITCHBOARD. NO subtypes, NO terminals (network_role='none').
--
-- Depends on seed/cable_management_example.sql (CABLE_DUCT), migration 0082 (the
-- cable_routing kind), seed/connectivity_example.sql (MV_CONDUCTOR) and
-- seed/lv_conductors_example.sql (LV_CONDUCTOR). Apply with psql after those, e.g.:
--   task seed-duct-way
-- then compile:
--   task compile CODE=DUCT_WAY

-- Field group reused from the civil inventory seed (re-asserted so this applies
-- standalone).
INSERT INTO metadata.field_groups (code, name, sort_order) VALUES
    ('general', 'General', 1)
ON CONFLICT (code) DO NOTHING;

-- =========================================================================
-- The DUCT_WAY class -- geometry-less logical child, never traced, never drawn.
-- srid MUST be NULL for geometry_type='none' (feature_classes CHECK, migration
-- 0007). is_diagram_container/placeable both false: it appears nowhere on a map
-- or schematic, only inside its parent duct's feature panel (the Ways list).
-- =========================================================================
INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status, is_diagram_container)
VALUES
    ('DUCT_WAY', 'Duct way', 'none', NULL, 'none', 'draft', false)
ON CONFLICT (code) DO NOTHING;

-- Single field: way_number (the bore position 1..N). NOT NULL -- the fan-out
-- endpoint supplies it for every way it creates; metadata.fields has no class-level
-- default (defaults are per-subtype, and DUCT_WAY has no subtypes).
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, v.nullable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('way_number', 'integer', NULL, 'general', false)
) AS v(name, data_type, domain_code, group_code, nullable) ON true
LEFT JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code = 'DUCT_WAY'
ON CONFLICT (feature_class_id, name) DO NOTHING;

-- =========================================================================
-- Relationship classes.
-- =========================================================================

-- CABLE_DUCT contains DUCT_WAY -- the fan-out link (one duct, many ways). Existing
-- `containment` kind (migration 0010), so no CHECK widen needed.
INSERT INTO metadata.relationship_classes (name, origin_feature_class_id, destination_feature_class_id, kind, cardinality)
SELECT 'CABLE_DUCT contains DUCT_WAY', duct.id, way.id, 'containment', 'one_to_many'
FROM metadata.feature_classes duct
JOIN metadata.feature_classes way ON way.code = 'DUCT_WAY'
WHERE duct.code = 'CABLE_DUCT'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.relationship_classes
    WHERE origin_feature_class_id = duct.id AND destination_feature_class_id = way.id AND kind = 'containment'
  );

-- DUCT_WAY carries {MV_CONDUCTOR, LV_CONDUCTOR} -- per-way routing. A cable
-- occupies a specific way. many_to_many mirrors the Slice 3 duct->conductor
-- classes (a way could, physically, carry more than one small cable; occupancy is
-- "does this way have >= 1 cable"). Existing `cable_routing` kind (migration 0082).
INSERT INTO metadata.relationship_classes (name, origin_feature_class_id, destination_feature_class_id, kind, cardinality)
SELECT 'DUCT_WAY carries ' || cond.code, way.id, cond.id, 'cable_routing', 'many_to_many'
FROM metadata.feature_classes way
JOIN metadata.feature_classes cond ON cond.code IN ('MV_CONDUCTOR', 'LV_CONDUCTOR')
WHERE way.code = 'DUCT_WAY'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.relationship_classes
    WHERE origin_feature_class_id = way.id AND destination_feature_class_id = cond.id AND kind = 'cable_routing'
  );
