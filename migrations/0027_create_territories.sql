-- 0027_create_territories.sql
-- Purpose: Phase 11 Slice A -- introduce a hierarchical territory model.
-- Every utility operates over a nested set of administrative areas
-- (utility -> region -> district -> area -> plot, by default), and every
-- geographically-placed feature must ultimately belong to at least a
-- district. This migration lays only the two catalog/data tables; the
-- physical-table `territory_id` column, the spatial auto-assignment, and
-- the write-time enforcement come in later slices (compiler + CRUD).
--
-- Two tables, both in the `gis` schema (migrations/0015) -- they carry
-- real polygon geometry and sit alongside the physical feature tables the
-- containment query will run against, so they belong with the geographic
-- data, not in metadata (definitional) or runtime (generic instance data).
--
--   gis.territory_levels -- the ORDERED, admin-configurable catalog of
--     levels. Not hardcoded five-deep in Go: the levels are data, exactly
--     like feature classes / domains are data, so the hierarchy can be
--     renamed or re-depthed without a migration or a redeploy. `rank`
--     orders them (0 = top / utility). Exactly one level is flagged
--     `is_min_feature_level` -- the shallowest level a geographic feature
--     is required to reach ("at least a district"). A partial unique index
--     enforces at-most-one; the seed guarantees exactly-one.
--
--   gis.territories -- the tree itself, a self-referential adjacency list
--     (one table, not one-table-per-level, so depth stays configurable).
--     `parent_id` is null only at the root. `geom` is the authored
--     boundary the spatial containment query uses; nullable so a territory
--     can exist in the tree before its boundary is drawn (the root utility
--     seeds with none). MultiPolygon, not Polygon: real administrative
--     boundaries routinely have exclaves/islands, and ST_Contains handles
--     a multipolygon against a point identically.
--
-- The structural invariants that can't be expressed as a single-row check
-- -- a child's level must be exactly one rank below its parent's, a
-- child's boundary must fall within its parent's -- are validated in Go
-- (api/internal/territory), matching this repo's standing convention of
-- validating cross-row/cross-table references in the app, not with
-- Postgres triggers (see the *_by columns, feature_connectivity, etc.).

CREATE TABLE gis.territory_levels (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code                 text NOT NULL UNIQUE,
    name                 text NOT NULL,
    rank                 integer NOT NULL UNIQUE CHECK (rank >= 0),
    is_min_feature_level boolean NOT NULL DEFAULT false,
    created_at           timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE gis.territory_levels IS 'The ordered, admin-configurable catalog of territory levels (utility, region, district, ... by default). Levels are data, not a hardcoded enum, so the hierarchy''s names and depth can change without a migration. Ordered by rank (0 = top).';
COMMENT ON COLUMN gis.territory_levels.code IS 'Stable machine identifier for the level, e.g. utility, region, district, area, plot.';
COMMENT ON COLUMN gis.territory_levels.rank IS 'Depth of the level, 0 = top/root (utility). Unique -- each level occupies exactly one depth. A territory''s parent must sit exactly one rank above it (enforced in api/internal/territory, not here).';
COMMENT ON COLUMN gis.territory_levels.is_min_feature_level IS 'True for the single shallowest level a geographically-placed feature is required to belong to ("at least a district"). A partial unique index enforces at-most-one true; the seed guarantees exactly one. Feature write-time enforcement (later slice) rejects any geographic feature whose containing territory is shallower than this level.';

-- At most one level may be flagged the minimum feature level.
CREATE UNIQUE INDEX territory_levels_one_min_feature_level
    ON gis.territory_levels ((is_min_feature_level))
    WHERE is_min_feature_level;

CREATE TABLE gis.territories (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    parent_id  uuid REFERENCES gis.territories(id),
    level_id   uuid NOT NULL REFERENCES gis.territory_levels(id),
    name       text NOT NULL,
    geom       geometry(MultiPolygon, 4326),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE gis.territories IS 'The territory tree -- a self-referential adjacency list (one table, arbitrary depth). Each row is one administrative area at some level; parent_id is null only at the root. geom is the authored boundary used for point-in-polygon feature assignment (nullable until drawn).';
COMMENT ON COLUMN gis.territories.parent_id IS 'The containing territory, or null at the root. A child''s level must be exactly one rank below its parent''s, and (when both have geometry) its boundary must fall within the parent''s -- both validated in api/internal/territory.';
COMMENT ON COLUMN gis.territories.level_id IS 'Which level (gis.territory_levels) this territory sits at.';
COMMENT ON COLUMN gis.territories.geom IS 'Authored boundary, MultiPolygon in SRID 4326. Nullable so a territory can exist in the tree before its boundary is drawn. The feature-assignment query (later slice) only ever matches territories that have a boundary.';

CREATE INDEX territories_geom_idx ON gis.territories USING GIST (geom);
CREATE INDEX territories_parent_idx ON gis.territories (parent_id);
CREATE INDEX territories_level_idx ON gis.territories (level_id);

-- Seed the default level catalog. This is a starting point an admin can
-- rename/extend, not a fixed contract -- but it encodes the requirement
-- that a geographic feature must reach at least a district (rank 2).
INSERT INTO gis.territory_levels (code, name, rank, is_min_feature_level) VALUES
    ('utility',  'Utility',  0, false),
    ('region',   'Region',   1, false),
    ('district', 'District', 2, true),
    ('area',     'Area',     3, false),
    ('plot',     'Plot',     4, false);

-- Seed a single root territory for the operating utility. Single-tenant
-- today (see docs/phase-10-auth.md): one root, no boundary yet.
INSERT INTO gis.territories (parent_id, level_id, name)
SELECT NULL, id, 'Utility'
FROM gis.territory_levels
WHERE code = 'utility';
