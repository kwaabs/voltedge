-- seed/mv_metering_symbology.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives under
-- /seed on purpose, same as seed/symbology_example.sql, mv_symbology_example.sql
-- and diagram_symbology.sql. Phase 43 (MV metering) symbology: give the THREE
-- new metering classes both a mandatory 'map' default rule and, for the
-- diagram-placeable ones, a 'diagram' ANSI glyph so the METERING_SCHEMATIC
-- (Slice 2) reads as real symbols instead of generic shapes.
--
-- Scope is exactly the Phase 43 classes -- MV_METERING_POINT, MV_METER,
-- MV_METERING_CUBICLE. MV_CT and MV_VT already have full map+diagram symbology
-- from seed/mv_symbology_example.sql (Phase 27 Slice 5: MV_CT circle/ansi-ct,
-- MV_VT square/ansi-vt, both instrument cyan), so they are NOT touched here.
--
-- TWO reasons this file exists:
--   1. Publish gate. docs/metadata-core.md: every feature class MUST have one
--      symbology_rules row with subtype_id AND domain_value_id both null (a
--      'map' default) before it can be published. The three Phase 43 classes
--      have none yet.
--   2. The metering schematic. The diagram canvas prefers a 'diagram' rule and
--      falls back to 'map' (web/src/components/map/diagram-canvas.tsx:
--      resolveSymbology(...,'diagram') ?? resolveSymbology(...,'map')). Without
--      a 'diagram' rule the meter drawn on a METERING_SCHEMATIC falls back to
--      its generic map shape; with one it reads as the ANSI meter glyph.
--
-- Reuses EXISTING glyphs only (no new symbology_icons): ansi-meter, ansi-terminal
-- are seeded by seed/diagram_symbology.sql + seed/ansi_stencils.sql. Map shapes
-- key into web/src/lib/symbology-icons.ts (circle/square/triangle/diamond), same
-- as seed/symbology_example.sql.
--
-- Colours -- a coherent scheme matching the existing palette:
--   * instrument cyan (#0891b2) for the metering DEVICE (MV_METER) -- the same
--     cyan METER/CT/VT use. Diamond map shape, distinct from MV_CT's circle and
--     MV_VT's square (all three instrument cyan) so they stay tellable apart.
--   * customer violet (#7c3aed) for MV_METERING_POINT -- the SAME identity
--     SERVICE_POINT carries (both are the customer delivery point, MV vs LV).
--   * container slate (#475569) for MV_METERING_CUBICLE -- an enclosure, not a
--     device; NO diagram glyph (it OWNS the schematic, it is not placed on one),
--     exactly like METER_CHAMBER stays on its map shape.
--
-- Class -> (map shape, diagram glyph):
--   MV_METERING_POINT   -> (diamond violet, ansi-terminal violet)
--   MV_METER            -> (diamond cyan,   ansi-meter cyan)
--   MV_METERING_CUBICLE -> (square slate,   -- no diagram rule)
--
-- Depends on seed/mv_metering_example.sql (MV_METERING_POINT, MV_METER),
-- seed/mv_metering_cubicle_example.sql (MV_METERING_CUBICLE), and
-- seed/ansi_stencils.sql + seed/diagram_symbology.sql (the ansi-meter /
-- ansi-terminal glyphs). Apply with psql after those. Nothing to compile --
-- symbology_rules rows are resolved entirely client-side (web/src/lib/symbology.ts).

-- =========================================================================
-- 1. Mandatory 'map' default rules (subtype_id + domain_value_id both null).
-- =========================================================================

-- MV_METERING_POINT -- the MV customer delivery point; violet diamond, the same
-- map identity SERVICE_POINT carries.
INSERT INTO metadata.symbology_rules (feature_class_id, render_context, style_def)
SELECT fc.id, 'map', '{"icon_ref": "diamond", "color": "#7c3aed"}'::jsonb
FROM metadata.feature_classes fc
WHERE fc.code = 'MV_METERING_POINT'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules
    WHERE feature_class_id = fc.id AND subtype_id IS NULL AND domain_value_id IS NULL AND render_context = 'map'
  );

-- MV_METER -- instrument cyan diamond (distinct from MV_CT circle / MV_VT square).
INSERT INTO metadata.symbology_rules (feature_class_id, render_context, style_def)
SELECT fc.id, 'map', '{"icon_ref": "diamond", "color": "#0891b2"}'::jsonb
FROM metadata.feature_classes fc
WHERE fc.code = 'MV_METER'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules
    WHERE feature_class_id = fc.id AND subtype_id IS NULL AND domain_value_id IS NULL AND render_context = 'map'
  );

-- MV_METERING_CUBICLE -- container slate square (no diagram glyph; it owns the
-- schematic, it is not placed on one -- the METER_CHAMBER treatment).
INSERT INTO metadata.symbology_rules (feature_class_id, render_context, style_def)
SELECT fc.id, 'map', '{"icon_ref": "square", "color": "#475569"}'::jsonb
FROM metadata.feature_classes fc
WHERE fc.code = 'MV_METERING_CUBICLE'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules
    WHERE feature_class_id = fc.id AND subtype_id IS NULL AND domain_value_id IS NULL AND render_context = 'map'
  );

-- =========================================================================
-- 2. 'diagram' ANSI glyph rules (device classes only; the cubicle owns the
--    schematic, it is not drawn on one).
-- =========================================================================

-- MV_METERING_POINT -- reads as a terminal (the delivery point), violet.
INSERT INTO metadata.symbology_rules (feature_class_id, render_context, style_def)
SELECT fc.id, 'diagram', '{"icon_ref": "ansi-terminal", "color": "#7c3aed"}'::jsonb
FROM metadata.feature_classes fc
WHERE fc.code = 'MV_METERING_POINT'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules
    WHERE feature_class_id = fc.id AND subtype_id IS NULL AND domain_value_id IS NULL AND render_context = 'diagram'
  );

-- MV_METER -- meter-dial glyph, instrument cyan (same as LV METER's diagram rule).
INSERT INTO metadata.symbology_rules (feature_class_id, render_context, style_def)
SELECT fc.id, 'diagram', '{"icon_ref": "ansi-meter", "color": "#0891b2"}'::jsonb
FROM metadata.feature_classes fc
WHERE fc.code = 'MV_METER'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules
    WHERE feature_class_id = fc.id AND subtype_id IS NULL AND domain_value_id IS NULL AND render_context = 'diagram'
  );
