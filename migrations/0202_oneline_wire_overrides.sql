-- Operations-owned WIRE routing for the LIVE operations one-line.
--
-- The node counterpart (ops.oneline_overrides, migration 0200) nudges where a feature
-- is DRAWN; this stores the hand-routed BEND VERTICES of a wire between two nodes, so an
-- operator can shape the schematic (avoid crossings, align runs) and have it survive the
-- live recompute.
--
-- Only the INTERMEDIATE vertices are stored -- the renderer always composes the path as
-- [live start node, ...these vertices, live end node], so the endpoints keep following
-- the nodes (and the node overrides) while the bends stick. wire_key is a STABLE key the
-- client derives from the wire: a real conductor's feature_id, else "link:<sorted node
-- id pair>" for a plain link (both stable across the deterministic layout recompute).
--
-- Operations-owned display state (operations:read / operations:write), NOT a GIS diagram
-- -- it never touches feature geometry. Dropping it loses only hand-routing.
CREATE TABLE IF NOT EXISTS ops.oneline_wire_overrides (
    wire_key   text PRIMARY KEY,
    vertices   jsonb NOT NULL,
    updated_by uuid,
    updated_at timestamptz NOT NULL DEFAULT now()
);
