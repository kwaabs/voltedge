-- seed/symbology_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this
-- lives under /seed on purpose, same reasoning as the other seed
-- files. A mandatory default symbology_rules row per existing feature
-- class (docs/metadata-core.md: every feature class MUST have one rule
-- with subtype_id and domain_value_id both null before publish), plus
-- one real subtype/domain_value-scoped override -- DIST_SWITCH's
-- device_state=OPEN gets a genuinely different icon_ref (a hollow
-- triangle instead of a solid one), not just a different color, to
-- actually exercise the shape-vs-modifier distinction and the
-- most-specific-wins resolution order, not just the trivial always-
-- matches-everything default case.
--
-- icon_ref values here (circle/square/triangle/triangle-outline/diamond)
-- key into web/src/lib/symbology-icons.ts's small hand-authored SVG
-- registry, not a real sprite/asset pipeline (docs/open-items.md's own
-- framing for that gap). Point classes get a shape; MV_CONDUCTOR (a
-- line) and SUBSTATION (a polygon) only ever get color/width or
-- fill_color/stroke_color -- there's no icon_ref concept for a
-- geometry that isn't a discrete point.
--
-- Depends on seed/service_point_example.sql and
-- seed/switching_device_example.sql having already been applied
-- (reuses their feature classes and, for DIST_SWITCH, its DEVICE_STATE
-- domain). Apply with psql after those. Nothing to compile --
-- symbology_rules rows have no physical footprint, resolved entirely
-- client-side (see web/src/lib/symbology.ts).

INSERT INTO metadata.symbology_rules (feature_class_id, render_context, style_def)
SELECT fc.id, 'map', '{"icon_ref": "circle", "color": "#92400e"}'::jsonb
FROM metadata.feature_classes fc
WHERE fc.code = 'POLE'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules
    WHERE feature_class_id = fc.id AND subtype_id IS NULL AND domain_value_id IS NULL AND render_context = 'map'
  );

INSERT INTO metadata.symbology_rules (feature_class_id, render_context, style_def)
SELECT fc.id, 'map', '{"icon_ref": "square", "color": "#2563eb"}'::jsonb
FROM metadata.feature_classes fc
WHERE fc.code = 'DIST_XFMR'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules
    WHERE feature_class_id = fc.id AND subtype_id IS NULL AND domain_value_id IS NULL AND render_context = 'map'
  );

INSERT INTO metadata.symbology_rules (feature_class_id, render_context, style_def)
SELECT fc.id, 'map', '{"icon_ref": "diamond", "color": "#7c3aed"}'::jsonb
FROM metadata.feature_classes fc
WHERE fc.code = 'SERVICE_POINT'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules
    WHERE feature_class_id = fc.id AND subtype_id IS NULL AND domain_value_id IS NULL AND render_context = 'map'
  );

INSERT INTO metadata.symbology_rules (feature_class_id, render_context, style_def)
SELECT fc.id, 'map', '{"color": "#3f8fa6", "width": 3}'::jsonb
FROM metadata.feature_classes fc
WHERE fc.code = 'MV_CONDUCTOR'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules
    WHERE feature_class_id = fc.id AND subtype_id IS NULL AND domain_value_id IS NULL AND render_context = 'map'
  );

INSERT INTO metadata.symbology_rules (feature_class_id, render_context, style_def)
SELECT fc.id, 'map', '{"fill_color": "#64748b", "stroke_color": "#64748b"}'::jsonb
FROM metadata.feature_classes fc
WHERE fc.code = 'SUBSTATION'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules
    WHERE feature_class_id = fc.id AND subtype_id IS NULL AND domain_value_id IS NULL AND render_context = 'map'
  );

-- DIST_SWITCH: mandatory default (covers CLOSED, and anything else)...

INSERT INTO metadata.symbology_rules (feature_class_id, render_context, style_def)
SELECT fc.id, 'map', '{"icon_ref": "triangle", "color": "#16a34a"}'::jsonb
FROM metadata.feature_classes fc
WHERE fc.code = 'DIST_SWITCH'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules
    WHERE feature_class_id = fc.id AND subtype_id IS NULL AND domain_value_id IS NULL AND render_context = 'map'
  );

-- ...and a domain_value-scoped override for device_state = OPEN -- a
-- real shape change (hollow vs. solid triangle), the same example
-- docs/metadata-core.md's own "Shape vs. modifier" table uses.

INSERT INTO metadata.symbology_rules (feature_class_id, domain_value_id, render_context, style_def)
SELECT sw.id, dv.id, 'map', '{"icon_ref": "triangle-outline", "color": "#dc2626"}'::jsonb
FROM metadata.feature_classes sw
JOIN metadata.fields f ON f.feature_class_id = sw.id AND f.name = 'device_state'
JOIN metadata.domain_values dv ON dv.domain_id = f.domain_id AND dv.code = 'OPEN'
WHERE sw.code = 'DIST_SWITCH'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules
    WHERE feature_class_id = sw.id AND subtype_id IS NULL AND domain_value_id = dv.id AND render_context = 'map'
  );
