-- 0052_add_geometry_optional_flag.sql
-- Purpose: geometry_optional lets a point/line/polygon feature class hold
-- features that have NO map geometry -- so one class can carry features that
-- live on the geographic map, on a diagram only, or on both, per feature
-- (Phase 26; the classic case is a fuse that some crews place geographically
-- and others only schematically). Orthogonal to geometry_type (which still
-- decides the draw gesture and the geom column's type) and to
-- is_diagram_placeable (which decides diagram eligibility). Default false =
-- today's behaviour: geom is NOT NULL, every feature must be placed on the map.
-- When true, a recompile relaxes the geom column to nullable (see
-- compiler/ddl.go + compiler.go) and the CRUD create validator stops requiring
-- a geometry. geometry_type = 'none' classes ignore this flag -- they have no
-- geom column at all.

ALTER TABLE metadata.feature_classes
    ADD COLUMN geometry_optional boolean NOT NULL DEFAULT false;

COMMENT ON COLUMN metadata.feature_classes.geometry_optional IS 'True for feature classes whose features may exist without map geometry (Phase 26 optional geometry): a recompile makes the geom column nullable and the create validator stops requiring a geometry, so one class can hold map-only, diagram-only, or both-placed features per feature. Default false keeps geom NOT NULL. Ignored for geometry_type = none classes (no geom column). Orthogonal to is_diagram_placeable and geometry_type.';
