-- 0032_add_diagram_placeable_flag.sql
-- Purpose: is_diagram_placeable marks feature classes whose features may be
-- placed as nodes on a one-line/schematic diagram (Phase 12 map-pick
-- placement). A diagram is an electrical schematic, so only network features
-- belong on it -- not structural features or containers. Seeded from
-- network_role: edge/junction are the network participants (conductors,
-- transformers, switches, busbars, cutouts, service points); container/none
-- are not. Admin-overridable per class thereafter, exactly like
-- is_diagram_container.

ALTER TABLE metadata.feature_classes
    ADD COLUMN is_diagram_placeable boolean NOT NULL DEFAULT false;

COMMENT ON COLUMN metadata.feature_classes.is_diagram_placeable IS 'True for feature classes whose features may be placed as nodes on a diagram (Phase 12 map-pick placement). A diagram is an electrical schematic, so this is the electrical/network set -- seeded from network_role in (edge, junction), admin-overridable. Distinct from is_diagram_container (who hosts a diagram) and from geometry_type (which decides the draw gesture: point = click, line = drawn polyline).';

UPDATE metadata.feature_classes
   SET is_diagram_placeable = true
 WHERE network_role IN ('edge', 'junction');
