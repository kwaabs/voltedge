-- 0048_create_diagram_stencils.sql
-- Purpose: metadata.diagram_stencils -- the palette catalog behind the Phase
-- 24 Slice 2 symbol stencil library (docs/phase-24-diagram-templates.md). A
-- stencil is one entry in the "drop a schematic symbol" palette on the diagram
-- canvas: an ANSI/IEEE glyph (ground, junction node, CT/VT, delta/wye winding,
-- flow arrow, ...) a user stamps as a non-feature `symbol` element (kind added
-- in migration 0047).
--
-- The SVG artwork itself is NOT stored here -- it lives in
-- metadata.symbology_icons (migration 0023), the one place this project keeps
-- icon markup, resolved client-side by iconDataUri. icon_ref is a FK into that
-- table, so a stencil's glyph is the *same* asset a feature symbology rule can
-- reference (style_def.icon_ref = 'ansi-relay'): authoring the ANSI set once,
-- in symbology_icons, makes it reusable by BOTH the stencil palette and
-- feature diagram symbology (the "both" scope chosen for Slice 2). This table
-- only marks which of those icons appear in the palette and how they group.
--
-- Not seeded by a migration: apply.sh scans /migrations only, and the ANSI
-- content is hand-authored the same way seed/symbology_icons.sql already is --
-- see seed/ansi_stencils.sql, which fills both symbology_icons (the markup)
-- and this catalog (the palette entries). An empty catalog just means an empty
-- palette, never a broken canvas.

CREATE TABLE metadata.diagram_stencils (
    icon_ref   text PRIMARY KEY REFERENCES metadata.symbology_icons(icon_ref) ON DELETE CASCADE,
    label      text NOT NULL,
    category   text NOT NULL DEFAULT 'general',
    sort_order integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE metadata.diagram_stencils IS 'Palette catalog for the diagram symbol stencil library (Phase 24 Slice 2). Each row marks a metadata.symbology_icons glyph as stampable onto a diagram as a non-feature symbol element, with a label and grouping. The SVG artwork is shared with feature symbology rules (same icon_ref), never duplicated here.';
COMMENT ON COLUMN metadata.diagram_stencils.icon_ref IS 'FK into metadata.symbology_icons -- the shared SVG asset. ON DELETE CASCADE: dropping the artwork drops its palette entry too.';
COMMENT ON COLUMN metadata.diagram_stencils.category IS 'Palette grouping heading (e.g. grounding, instrument, transformers). Purely for organising the picker; no semantics.';
COMMENT ON COLUMN metadata.diagram_stencils.sort_order IS 'Order within (and across) categories in the palette; lower first, icon_ref as the stable tiebreak.';
