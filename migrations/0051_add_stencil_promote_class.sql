-- 0051_add_stencil_promote_class.sql
-- Purpose: promote_class turns a diagram stencil from pure decoration into a
-- seed for a real feature (Phase 25). A symbol element stamped from a stencil
-- is non-feature furniture (Phase 24 Slice 2) -- it draws a glyph but has no
-- code/attributes/geometry and never appears in trace or connectivity.
-- Double-clicking such a symbol now offers to PROMOTE it: create a real
-- geometry_type = 'none' feature of promote_class at the symbol's spot, place
-- it there, and replace the glyph with that feature's placement. NULL means the
-- stencil stays decoration only (ground studs, north arrows, device-number
-- circles -- symbols that aren't network devices), so it isn't promotable.
--
-- A feature-class *code*, deliberately NOT an FK to metadata.feature_classes --
-- the same "code, not FK" choice diagram_elements.slot_class already makes
-- (migration 0046). It's resolved at promote time against the live metadata; an
-- unknown or later-removed class just makes that one stencil un-promotable,
-- which is a no-op on the palette, never a broken canvas or a failed migration.
--
-- Not seeded here (apply.sh scans /migrations only) -- the ansi-relay ->
-- PROTECTIVE_RELAY mapping is authored idempotently in seed/ansi_stencils.sql,
-- beside the glyph content it depends on.

ALTER TABLE metadata.diagram_stencils
    ADD COLUMN promote_class text;

COMMENT ON COLUMN metadata.diagram_stencils.promote_class IS 'Feature class code (a code, not an FK) a stamped symbol of this stencil can be promoted into: create a real geometry_type = none feature of this class at the symbol''s position and swap the glyph for its placement (Phase 25). NULL = the stencil is decoration only, not promotable.';
