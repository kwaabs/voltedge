-- 0030_seed_territory_boundaries.sql
-- Purpose: Phase 11 Slice E -- seed a starter territory tree WITH boundaries
-- beneath the root 'Utility' (migration 0027 seeded the level catalog and
-- the bare root only). Territories are fixed reference data -- the app has
-- no user-facing authoring UI; the tree and its polygons come from an
-- authoritative administrative-boundary dataset and are loaded here, with
-- the schema, exactly like the level catalog.
--
-- IMPORTANT -- these are ILLUSTRATIVE PLACEHOLDER boundaries, not real
-- administrative areas. They are simple rectangles anchored around the
-- web map's default center (web/src/components/map/voltedge-map.tsx,
-- DEFAULT_CENTER = -0.127, 51.507, central London) purely so a feature
-- drawn near that center actually resolves to a district and the spatial
-- assignment / enforcement path can be exercised end-to-end. Replace them
-- with the authoritative dataset (a later migration overwriting geom, or a
-- fresh seed) before turning territory_enforcement_enabled on in anger.
--
-- The rectangles are deliberately, strictly nested so the tree is
-- geometrically consistent -- each child's boundary falls entirely within
-- its parent's, the same invariant api/internal/territory.validatePlacement
-- would enforce on an app-side write (which doesn't run for a raw SQL seed,
-- so the nesting is authored correctly by hand here):
--
--   Central London Region        lon [-0.20, -0.04]  lat [51.46, 51.56]
--     Westminster District        lon [-0.16, -0.11]  lat [51.48, 51.54]  <- contains default center
--       Whitehall Area            lon [-0.135,-0.12]  lat [51.500,51.510] <- contains default center (deepest)
--     City of London District     lon [-0.10, -0.05]  lat [51.505,51.525]
--
-- A feature at the default center resolves to Whitehall Area (rank 3 area)
-- -- the DEEPEST containing territory -- which satisfies "at least a
-- district" (rank 2). A feature elsewhere in Westminster but outside
-- Whitehall resolves to the district. geom is stored as MultiPolygon
-- (ST_Multi) to match the column type, even though each placeholder is a
-- single ring.

-- Region, under the seeded root utility.
INSERT INTO gis.territories (parent_id, level_id, name, geom)
SELECT
    (SELECT id FROM gis.territories WHERE name = 'Utility' AND parent_id IS NULL),
    (SELECT id FROM gis.territory_levels WHERE code = 'region'),
    'Central London Region',
    ST_Multi(ST_GeomFromText('POLYGON((-0.20 51.46, -0.04 51.46, -0.04 51.56, -0.20 51.56, -0.20 51.46))', 4326));

-- Districts, under the region.
INSERT INTO gis.territories (parent_id, level_id, name, geom)
SELECT
    (SELECT id FROM gis.territories WHERE name = 'Central London Region'),
    (SELECT id FROM gis.territory_levels WHERE code = 'district'),
    'Westminster District',
    ST_Multi(ST_GeomFromText('POLYGON((-0.16 51.48, -0.11 51.48, -0.11 51.54, -0.16 51.54, -0.16 51.48))', 4326));

INSERT INTO gis.territories (parent_id, level_id, name, geom)
SELECT
    (SELECT id FROM gis.territories WHERE name = 'Central London Region'),
    (SELECT id FROM gis.territory_levels WHERE code = 'district'),
    'City of London District',
    ST_Multi(ST_GeomFromText('POLYGON((-0.10 51.505, -0.05 51.505, -0.05 51.525, -0.10 51.525, -0.10 51.505))', 4326));

-- Area, under Westminster District (demonstrates deepest-match below the
-- minimum feature level).
INSERT INTO gis.territories (parent_id, level_id, name, geom)
SELECT
    (SELECT id FROM gis.territories WHERE name = 'Westminster District'),
    (SELECT id FROM gis.territory_levels WHERE code = 'area'),
    'Whitehall Area',
    ST_Multi(ST_GeomFromText('POLYGON((-0.135 51.500, -0.12 51.500, -0.12 51.510, -0.135 51.510, -0.135 51.500))', 4326));
