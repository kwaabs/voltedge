-- 0047_add_symbol_element_kind.sql
-- Purpose: extend runtime.diagram_elements.kind (migration 0046) with a new
-- 'symbol' kind -- the non-feature-backed schematic glyph a user stamps from
-- the Phase 24 Slice 2 stencil palette (docs/phase-24-diagram-templates.md).
--
-- 0046 deliberately baked the *other* future kinds (title_block/legend/slot)
-- into its CHECK up front so introducing them needs no migration; 'symbol'
-- was not among them (the symbol library was scoped as a separate content
-- track), so it does need this one. A stamped symbol is pure diagram
-- furniture like a box/text/line -- it points at no feature -- but carries
-- its glyph's icon_ref in `style` (style.iconRef), resolved against
-- metadata.symbology_icons exactly the way a placement's feature icon is,
-- so no new columns are needed, only the kind widening.
--
-- Postgres auto-named 0046's inline CHECK diagram_elements_kind_check; drop
-- and recreate it with 'symbol' added. Existing rows are unaffected (every
-- current value is still permitted).

ALTER TABLE runtime.diagram_elements DROP CONSTRAINT diagram_elements_kind_check;
ALTER TABLE runtime.diagram_elements ADD CONSTRAINT diagram_elements_kind_check
    CHECK (kind IN ('text', 'box', 'line', 'symbol', 'title_block', 'legend', 'slot'));

COMMENT ON COLUMN runtime.diagram_elements.kind IS 'text | box | line | symbol | title_block | legend | slot. text/box/line/symbol are rendered today; title_block/legend/slot are model-ready and wired in later Phase 24 slices. A symbol carries its glyph in style.iconRef (metadata.symbology_icons), backed by no feature.';
