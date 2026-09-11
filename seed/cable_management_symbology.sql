-- seed/cable_management_symbology.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives under
-- /seed on purpose, same as seed/symbology_example.sql / mv_metering_symbology.sql.
-- Phase 44 Slice 1 (docs/phase-44-cable-management.md): give the three CIVIL
-- cable-management classes their mandatory 'map' default symbology rule.
--
-- Why this file exists: the publish gate (docs/metadata-core.md) requires every
-- feature class to have one symbology_rules row with subtype_id AND domain_value_id
-- both null (a 'map' default) before it can leave draft. The three Phase 44 Slice 1
-- classes have none yet. There is NO 'diagram' rule here -- these are civil map
-- inventory, never placed on a schematic (network_role='none').
--
-- Geometry dictates the style_def shape (seed/symbology_example.sql): a line gets
-- {color,width}, a polygon gets {fill_color,stroke_color}, a point gets
-- {icon_ref,color}. So:
--   CABLE_TRENCH  (line)    -> {color, width}          amber, the "civil dig" colour
--   CABLE_DUCT    (line)    -> {color, width}          slate, a thinner conduit line
--   CABLE_CHAMBER (polygon) -> {fill_color,stroke_color} stone, a manhole footprint
--
-- Colours chosen to read as CIVIL (earth/utility tones -- amber/slate/stone),
-- deliberately distinct from the electrical palette (MV teal / instrument cyan /
-- customer violet) so the civil layer is tellable apart from the network at a glance.
--
-- Depends on seed/cable_management_example.sql (the three classes). Apply with psql
-- after it, e.g. task seed-cable-management-symbology. Nothing to compile --
-- symbology_rules rows resolve entirely client-side (web/src/lib/symbology.ts).

-- CABLE_TRENCH -- the excavation; amber line, wider (the dig footprint).
INSERT INTO metadata.symbology_rules (feature_class_id, render_context, style_def)
SELECT fc.id, 'map', '{"color": "#b45309", "width": 4}'::jsonb
FROM metadata.feature_classes fc
WHERE fc.code = 'CABLE_TRENCH'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules
    WHERE feature_class_id = fc.id AND subtype_id IS NULL AND domain_value_id IS NULL AND render_context = 'map'
  );

-- CABLE_DUCT -- the conduit; slate line, thinner (rides inside the trench).
INSERT INTO metadata.symbology_rules (feature_class_id, render_context, style_def)
SELECT fc.id, 'map', '{"color": "#475569", "width": 2}'::jsonb
FROM metadata.feature_classes fc
WHERE fc.code = 'CABLE_DUCT'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules
    WHERE feature_class_id = fc.id AND subtype_id IS NULL AND domain_value_id IS NULL AND render_context = 'map'
  );

-- CABLE_CHAMBER -- the manhole footprint; stone fill + stroke.
INSERT INTO metadata.symbology_rules (feature_class_id, render_context, style_def)
SELECT fc.id, 'map', '{"fill_color": "#78716c", "stroke_color": "#57534e"}'::jsonb
FROM metadata.feature_classes fc
WHERE fc.code = 'CABLE_CHAMBER'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules
    WHERE feature_class_id = fc.id AND subtype_id IS NULL AND domain_value_id IS NULL AND render_context = 'map'
  );
