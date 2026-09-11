-- 0046_create_diagram_elements.sql
-- Purpose: runtime.diagram_elements -- the non-feature-backed sibling of
-- runtime.diagram_placements (migration 0019). A placement always points at a
-- real feature (a polymorphic feature_class_id/feature_id it merely positions);
-- an element points at nothing -- it is diagram furniture that exists only on
-- the canvas: an annotation box grouping a bay, a free text label, a
-- schematic-only line (a bus tie or trip path with no feature behind it), and
-- later the scaffolding of templates (title blocks, legends, and empty class-
-- tagged slots a user binds to a real feature after applying a template).
--
-- This is the Phase 24 keystone (docs/phase-24-diagram-templates.md, Slice 1):
-- three of the four "template" meanings -- layout skeleton, sheet furniture,
-- grouping -- all need one shared primitive that lives in the diagram's own
-- local x/y space but is not a feature. That primitive is this table. Slice 1
-- implements the text/box/line kinds (delivering Phase 5 open items #3 grouping
-- boxes and #4 schematic-only lines standalone); title_block/legend/slot are
-- model-ready here (in the CHECK, with their columns) but rendered/wired in
-- later slices, so no follow-on migration is needed to introduce them.
--
-- Coordinates are numeric to match diagram_placements exactly (same local,
-- abstract-not-geographic space), and `path` is jsonb for the same reason a
-- placement's is (an ordered [[x,y],...] polyline, not a PostGIS geometry).
-- Heterogeneous, kind-specific styling (stroke, dash, fill, colour, font size)
-- goes in `style` jsonb rather than a wide fan of nullable columns; only the
-- structural geometry (x/y/width/height/rotation/path) and slot binding are
-- typed columns.

CREATE TABLE runtime.diagram_elements (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    diagram_instance_id uuid NOT NULL REFERENCES runtime.diagram_instances(id) ON DELETE CASCADE,
    kind                text NOT NULL CHECK (kind IN ('text', 'box', 'line', 'title_block', 'legend', 'slot')),
    x                   numeric NOT NULL,
    y                   numeric NOT NULL,
    width               numeric,
    height              numeric,
    rotation            numeric NOT NULL DEFAULT 0,
    path                jsonb,
    text                text,
    style               jsonb NOT NULL DEFAULT '{}'::jsonb,
    z_index             integer NOT NULL DEFAULT 0,
    -- Slot expectation (kind = 'slot'): what real feature this placeholder is
    -- meant to be bound to when a template is applied. Stored as the human
    -- class code (not a feature_classes FK) so a saved template stays portable
    -- across environments, same code-not-uuid convention the whole API surface
    -- uses. All null for non-slot kinds. Binding UX arrives in a later slice.
    slot_class          text,
    slot_subtype        integer,
    slot_role           text,
    -- Once a slot is bound: the resolved real feature. Polymorphic and
    -- FK-less, same reasoning as diagram_placements.feature_id.
    bound_class         text,
    bound_id            uuid,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz
);

COMMENT ON TABLE runtime.diagram_elements IS 'Non-feature-backed diagram furniture (Phase 24 keystone): annotation boxes, free text, schematic-only lines, and later title blocks, legends, and template slots. The sibling of diagram_placements -- same local x/y space, but points at no real feature. Cascades away with its diagram_instance.';
COMMENT ON COLUMN runtime.diagram_elements.kind IS 'text | box | line | title_block | legend | slot. Slice 1 renders text/box/line; the other three are model-ready and wired in later Phase 24 slices (no follow-on migration needed to add them).';
COMMENT ON COLUMN runtime.diagram_elements.path IS 'For kind = line: ordered JSON array of [x, y] vertex pairs in the diagram''s local coordinate space, e.g. [[10,20],[30,20]]. Null for non-line kinds; x/y still anchor the element''s label/hit target.';
COMMENT ON COLUMN runtime.diagram_elements.style IS 'Kind-specific rendering hints (stroke, dash, fill, colour, font size), never any real-world semantics. Defaults to an empty object.';
COMMENT ON COLUMN runtime.diagram_elements.z_index IS 'Draw order within the diagram: lower is farther back. Boxes typically sit behind placements, labels in front.';
COMMENT ON COLUMN runtime.diagram_elements.slot_class IS 'For kind = slot: the human class code (e.g. TRANSFORMER) this placeholder expects. Stored as a code, not a feature_classes FK, so a saved template is portable. Null for other kinds.';
COMMENT ON COLUMN runtime.diagram_elements.bound_id IS 'For a bound slot: the resolved real feature id. No FK -- same polymorphic reasoning as diagram_placements.feature_id.';

CREATE INDEX diagram_elements_instance_idx ON runtime.diagram_elements (diagram_instance_id);
