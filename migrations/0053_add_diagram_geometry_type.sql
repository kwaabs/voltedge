-- 0053_add_diagram_geometry_type.sql
-- Purpose: diagram_geometry_type lets a feature class be drawn as a
-- different shape ON A DIAGRAM than it is on the geographic map. The
-- motivating case is a busbar: geometry_type = 'point' (a junction node
-- geographically -- its "bar" is the fan-out of terminal T2, not a line),
-- but on a one-line/schematic it should read as a spanning bar. Setting
-- diagram_geometry_type = 'line' makes the diagram canvas arm line-drawing
-- and render it as a wire, while the map keeps it a point.
--
-- NULL (the default) means "inherit geometry_type" -- exactly today's
-- behaviour for every existing class, so this is purely additive. It only
-- affects the DIAGRAM surface (runtime.diagram_placements already stores
-- its own independent x/y + path, migration 0033); the geographic geom
-- column, the compiler, and the map are untouched. Orthogonal to
-- is_diagram_placeable (diagram eligibility) and geometry_optional.
--
-- Only 'point' and 'line' are meaningful diagram shapes; a diagram has no
-- polygon or 'none' placement, so the CHECK excludes them.

ALTER TABLE metadata.feature_classes
    ADD COLUMN diagram_geometry_type text
    CHECK (diagram_geometry_type IN ('point', 'line'));

COMMENT ON COLUMN metadata.feature_classes.diagram_geometry_type IS 'Overrides how a feature of this class is drawn on a DIAGRAM (one-line/schematic), independent of geometry_type (the geographic map shape). ''line'' makes the diagram canvas draw it as a wire/bar (the busbar case: point junction on the map, spanning bar on the one-line); ''point'' forces a point placement. NULL = inherit geometry_type (default; today''s behaviour). Only affects the diagram surface -- diagram_placements carry their own x/y + path (migration 0033). Orthogonal to is_diagram_placeable and geometry_optional.';
