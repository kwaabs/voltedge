-- 0033_add_diagram_placement_path.sql
-- Purpose: a line feature placed on a diagram (Phase 12 map-pick placement,
-- option B2) is drawn by hand as a polyline, and its vertices are stored
-- here. `path` is an ordered JSON array of [x, y] pairs in the diagram's own
-- local coordinate space (abstract, not geographic -- so jsonb, not a PostGIS
-- geometry). Null for point placements, which keep using x/y/rotation. When
-- present it is what the canvas renders; when absent a line placement falls
-- back to the connectivity-derived elbow (or a marker at x/y), so every
-- placement written before this column existed is unaffected.

ALTER TABLE runtime.diagram_placements
    ADD COLUMN path jsonb;

COMMENT ON COLUMN runtime.diagram_placements.path IS 'For a drawn line placement (Phase 12): ordered JSON array of [x, y] vertex pairs in the diagram''s local coordinate space, e.g. [[10,20],[30,20],[30,40]]. Null for point placements. x/y remain the node''s anchor (label/hit position); rotation is meaningful only for points.';
