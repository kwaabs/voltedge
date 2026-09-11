-- 0054_add_diagram_placement_label_hidden.sql
-- Purpose: let a single feature's label be hidden on a diagram without
-- affecting its geometry or anyone else's view. `label_hidden` is a per-
-- placement flag toggled from the canvas (a small control beside each
-- feature); the renderer omits the name text when it's true. Defaults to
-- false so every placement written before this column existed keeps showing
-- its label, and NOT NULL so the renderer never has to reason about a
-- three-valued flag. Deliberately orthogonal to x/y/rotation/path: it is
-- updated on its own (PATCH /placements/{id}), never through the move/rotate
-- upsert, so dragging a feature never disturbs its label visibility.

ALTER TABLE runtime.diagram_placements
    ADD COLUMN label_hidden boolean NOT NULL DEFAULT false;

COMMENT ON COLUMN runtime.diagram_placements.label_hidden IS 'When true, the canvas does not draw this placement''s name label (per-feature, toggled from the diagram). Default false. Independent of geometry: updated via PATCH /diagrams/{id}/placements/{id}, not the place/move upsert.';
