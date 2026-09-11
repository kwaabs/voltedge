-- seed/diagram_symbology.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives under
-- /seed on purpose, same as seed/symbology_icons.sql and seed/ansi_stencils.sql.
-- Phase 25 Slice 3 (docs/phase-25-stencil-promotion.md): give each *point*
-- feature class a render_context = 'diagram' symbology rule that points at an
-- ANSI/IEEE schematic glyph, so a placement drawn on a diagram reads as the
-- electric symbol (transformer, disconnect, fuse, ...) instead of the generic
-- map shape (circle/square/triangle/diamond). This is exactly the mechanism the
-- one existing diagram rule already uses -- PROTECTIVE_RELAY -> ansi-relay
-- (seed/protective_relay_example.sql + seed/ansi_stencils.sql) -- just extended
-- to the real point classes. The diagram canvas prefers a 'diagram' rule and
-- falls back to the 'map' rule (web/src/components/map/diagram-canvas.tsx:
-- resolveSymbology(...,'diagram') ?? resolveSymbology(...,'map')), so before this
-- file every point placement fell back to its geographic shape. Adding a rule
-- here flips only the *diagram* drawing -- the map is untouched, and lines
-- (MV/LV conductor) are left exactly as they are.
--
-- Two parts, both idempotent:
--   1. metadata.symbology_icons -- two NEW glyphs (ansi-meter, ansi-street-light)
--      for the classes with no fitting ANSI symbol. Same asset table + {{COLOR}}
--      tint convention as seed/ansi_stencils.sql. Not added to
--      metadata.diagram_stencils -- these are feature symbology, not palette
--      stencils, same as the original five shapes in seed/symbology_icons.sql.
--   2. metadata.symbology_rules -- one render_context = 'diagram' rule per point
--      class (plus two subtype-scoped overrides), each guarded by NOT EXISTS so
--      re-applying never clobbers a hand-edited rule.
--
-- Colours: each class keeps its map colour where it has one (DIST_XFMR blue,
-- DIST_SWITCH green, SERVICE_POINT violet -- see seed/symbology_example.sql), so
-- the symbol changes shape without changing a class's colour identity; classes
-- with no map rule get a coherent scheme (protection orange, instrument cyan,
-- node slate, street-light amber).
--
-- Class -> glyph pairing:
--   DIST_XFMR                         -> ansi-transformer-2w   (#2563eb)
--   DIST_SWITCH (default)             -> ansi-disconnect       (#16a34a)
--   DIST_SWITCH / automatic_recloser  -> ansi-load-break       (#16a34a)
--   SERVICE_CUTOUT                    -> ansi-fuse             (#ea580c)
--   SERVICE_POINT                     -> ansi-terminal         (#7c3aed)
--   LV_LINK_BOX                       -> ansi-node             (#334155)
--   LV_BUSBAR                         -> ansi-terminal         (#334155)
--   METER (default)                   -> ansi-meter    [new]   (#0891b2)
--   METER / ct_metered                -> ansi-ct              (#0891b2)
--   STREET_LIGHT                      -> ansi-street-light [new] (#ca8a04)
-- POLE (structural, not diagram-placeable) and METER_CHAMBER (container role)
-- are intentionally left on their map shape -- no schematic device symbol fits.
--
-- Apply with psql after migrations 0001-0051, after the point-class seeds
-- (seed/connectivity_example.sql, switching_device_example.sql,
-- service_point_example.sql, lv_devices_example.sql, lv_metering_example.sql,
-- lv_unmetered_ct_example.sql) and after seed/ansi_stencils.sql (this file
-- reuses its glyphs). Nothing to compile -- symbology_rules rows are resolved
-- entirely client-side.

-- 1. New glyphs -- upright (rotation 0 points "up"), explicit width/height (the
--    Chromium intrinsic-size bug, see migrations/0023), {{COLOR}} tintable.

INSERT INTO metadata.symbology_icons (icon_ref, svg) VALUES
  ('ansi-meter', '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><g fill="none" stroke="{{COLOR}}" stroke-width="1.6" stroke-linecap="round"><circle cx="12" cy="12" r="8"/><line x1="12" y1="12" x2="16" y2="8"/><circle cx="12" cy="12" r="1.1" fill="{{COLOR}}" stroke="none"/></g></svg>'),
  ('ansi-street-light', '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><g fill="none" stroke="{{COLOR}}" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><line x1="7" y1="21" x2="7" y2="5"/><line x1="7" y1="5" x2="14" y2="5"/><polygon points="11,6 17,6 15.5,9.5 12.5,9.5" fill="{{COLOR}}"/><line x1="12.5" y1="11" x2="11.8" y2="13"/><line x1="14" y1="11" x2="14" y2="13.3"/><line x1="15.5" y1="11" x2="16.2" y2="13"/></g></svg>')
ON CONFLICT (icon_ref) DO NOTHING;

-- 2. Diagram-context rules, one per point class.

-- DIST_XFMR -- two-winding transformer, keeps its map blue.
INSERT INTO metadata.symbology_rules (feature_class_id, render_context, style_def)
SELECT fc.id, 'diagram', '{"icon_ref": "ansi-transformer-2w", "color": "#2563eb"}'::jsonb
FROM metadata.feature_classes fc
WHERE fc.code = 'DIST_XFMR'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules
    WHERE feature_class_id = fc.id AND subtype_id IS NULL AND domain_value_id IS NULL AND render_context = 'diagram'
  );

-- DIST_SWITCH default -- disconnector, keeps its map green.
INSERT INTO metadata.symbology_rules (feature_class_id, render_context, style_def)
SELECT fc.id, 'diagram', '{"icon_ref": "ansi-disconnect", "color": "#16a34a"}'::jsonb
FROM metadata.feature_classes fc
WHERE fc.code = 'DIST_SWITCH'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules
    WHERE feature_class_id = fc.id AND subtype_id IS NULL AND domain_value_id IS NULL AND render_context = 'diagram'
  );

-- DIST_SWITCH / automatic_recloser -- subtype-scoped override to a load-break
-- switch (a recloser reads as a load-break device); most-specific-wins means
-- this beats the default disconnect for that subtype only.
INSERT INTO metadata.symbology_rules (feature_class_id, subtype_id, render_context, style_def)
SELECT fc.id, st.id, 'diagram', '{"icon_ref": "ansi-load-break", "color": "#16a34a"}'::jsonb
FROM metadata.feature_classes fc
JOIN metadata.subtypes st ON st.feature_class_id = fc.id AND st.name = 'automatic_recloser'
WHERE fc.code = 'DIST_SWITCH'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules sr
    WHERE sr.feature_class_id = fc.id AND sr.subtype_id = st.id AND sr.domain_value_id IS NULL AND sr.render_context = 'diagram'
  );

-- SERVICE_CUTOUT -- a cutout is a fuse; protection orange.
INSERT INTO metadata.symbology_rules (feature_class_id, render_context, style_def)
SELECT fc.id, 'diagram', '{"icon_ref": "ansi-fuse", "color": "#ea580c"}'::jsonb
FROM metadata.feature_classes fc
WHERE fc.code = 'SERVICE_CUTOUT'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules
    WHERE feature_class_id = fc.id AND subtype_id IS NULL AND domain_value_id IS NULL AND render_context = 'diagram'
  );

-- SERVICE_POINT -- a service connection reads as a terminal; keeps its map violet.
INSERT INTO metadata.symbology_rules (feature_class_id, render_context, style_def)
SELECT fc.id, 'diagram', '{"icon_ref": "ansi-terminal", "color": "#7c3aed"}'::jsonb
FROM metadata.feature_classes fc
WHERE fc.code = 'SERVICE_POINT'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules
    WHERE feature_class_id = fc.id AND subtype_id IS NULL AND domain_value_id IS NULL AND render_context = 'diagram'
  );

-- LV_LINK_BOX -- a link box is a junction node; slate.
INSERT INTO metadata.symbology_rules (feature_class_id, render_context, style_def)
SELECT fc.id, 'diagram', '{"icon_ref": "ansi-node", "color": "#334155"}'::jsonb
FROM metadata.feature_classes fc
WHERE fc.code = 'LV_LINK_BOX'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules
    WHERE feature_class_id = fc.id AND subtype_id IS NULL AND domain_value_id IS NULL AND render_context = 'diagram'
  );

-- LV_BUSBAR -- a busbar tie point reads as a terminal; slate.
INSERT INTO metadata.symbology_rules (feature_class_id, render_context, style_def)
SELECT fc.id, 'diagram', '{"icon_ref": "ansi-terminal", "color": "#334155"}'::jsonb
FROM metadata.feature_classes fc
WHERE fc.code = 'LV_BUSBAR'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules
    WHERE feature_class_id = fc.id AND subtype_id IS NULL AND domain_value_id IS NULL AND render_context = 'diagram'
  );

-- METER default -- new meter-dial glyph; instrument cyan.
INSERT INTO metadata.symbology_rules (feature_class_id, render_context, style_def)
SELECT fc.id, 'diagram', '{"icon_ref": "ansi-meter", "color": "#0891b2"}'::jsonb
FROM metadata.feature_classes fc
WHERE fc.code = 'METER'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules
    WHERE feature_class_id = fc.id AND subtype_id IS NULL AND domain_value_id IS NULL AND render_context = 'diagram'
  );

-- METER / ct_metered -- subtype-scoped override to the current-transformer glyph.
INSERT INTO metadata.symbology_rules (feature_class_id, subtype_id, render_context, style_def)
SELECT fc.id, st.id, 'diagram', '{"icon_ref": "ansi-ct", "color": "#0891b2"}'::jsonb
FROM metadata.feature_classes fc
JOIN metadata.subtypes st ON st.feature_class_id = fc.id AND st.name = 'ct_metered'
WHERE fc.code = 'METER'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules sr
    WHERE sr.feature_class_id = fc.id AND sr.subtype_id = st.id AND sr.domain_value_id IS NULL AND sr.render_context = 'diagram'
  );

-- STREET_LIGHT -- new street-light glyph; amber.
INSERT INTO metadata.symbology_rules (feature_class_id, render_context, style_def)
SELECT fc.id, 'diagram', '{"icon_ref": "ansi-street-light", "color": "#ca8a04"}'::jsonb
FROM metadata.feature_classes fc
WHERE fc.code = 'STREET_LIGHT'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules
    WHERE feature_class_id = fc.id AND subtype_id IS NULL AND domain_value_id IS NULL AND render_context = 'diagram'
  );
