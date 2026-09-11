-- seed/substation_panel_symbology.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives under
-- /seed on purpose, same as seed/diagram_symbology.sql, mv_metering_symbology.sql
-- and symbology_example.sql.
--
-- Phase 45 Slice 2 (DEPICTION only -- docs/phase-45-substation-panels.md): make
-- a SUBSTATION_PANEL actually *drawable* on a diagram, keyed on panel_type. The
-- user's chosen scope was "just depiction" -- the panel glyph, no bespoke
-- link-authoring UI (panel<->bay / panel<->relay links stay established through
-- the generic relationship-suggestion flow).
--
-- Slice 1 (seed/substation_panel_example.sql) created the class but left it
-- unpublishable and unplaceable -- it has NO symbology_rules row at all (fails
-- the publish gate: docs/metadata-core.md requires one subtype+domain-null rule
-- before publish) and is_diagram_placeable is still false (migration 0032 gate:
-- placeFeature refuses a class that isn't flagged placeable). This file closes
-- both, plus adds the per-panel_type diagram glyph. Three parts, all idempotent:
--
--   1. Flag SUBSTATION_PANEL is_diagram_placeable = true (so it can be dropped
--      on the substation's diagram -- the yard is a ONE_LINE container via
--      seed/diagram_example.sql; the (SUBSTATION x ONE_LINE) placement-rule
--      pairing is unconstrained, so a placeable class is accepted with no extra
--      diagram_placement_rules row). A dedicated CONTROL_SCHEME diagram_type is a
--      later nicety, deliberately out of "just depiction" scope.
--   2. One NEW glyph 'panel' in metadata.symbology_icons -- a cabinet rectangle
--      (no existing ANSI symbol reads as a control panel). Same {{COLOR}}-tint +
--      explicit width/height convention as seed/ansi_stencils.sql (the Chromium
--      intrinsic-size bug, migrations/0023).
--   3. symbology_rules -- a mandatory 'map' default (publish gate), a 'diagram'
--      default (the panel block), and six panel_type-scoped 'diagram' overrides
--      (most-specific-wins: the canvas resolves the panel_type domain_value_id
--      rule over the default, exactly as diagram_symbology.sql does for
--      DIST_SWITCH/automatic_recloser and METER/ct_metered). The canvas prefers a
--      'diagram' rule and falls back to 'map' (web/src/components/map/
--      diagram-canvas.tsx: resolveSymbology(...,'diagram') ?? (...,'map')).
--
-- Colours -- a coherent scheme matching the existing palette (protection orange,
-- control blue, instrument cyan, telecontrol violet, auxiliary amber, container
-- slate), so panels read as one family colour-coded by role:
--   SUBSTATION_PANEL default            -> panel  slate  (#475569)
--   SUBSTATION_PANEL / PROTECTION       -> panel  orange (#ea580c)
--   SUBSTATION_PANEL / CONTROL          -> panel  blue   (#2563eb)
--   SUBSTATION_PANEL / METERING         -> panel  cyan   (#0891b2)
--   SUBSTATION_PANEL / RTU_SCADA        -> panel  violet (#7c3aed)
--   SUBSTATION_PANEL / AUXILIARY_DC     -> panel  amber  (#ca8a04)
--   SUBSTATION_PANEL / MARSHALLING      -> panel  slate  (#475569)
--
-- Depends on seed/substation_panel_example.sql (the class + PANEL_TYPE domain).
-- Apply with psql after it. Nothing to compile -- symbology_rules /
-- symbology_icons rows are resolved entirely client-side; is_diagram_placeable is
-- a plain metadata flag read live (no recompile), same as the METER_CHAMBER /
-- METER flips in seed/lv_metering_example.sql.

-- ===========================================================================
-- 1. Make the panel diagram-placeable (migration 0032 gate). Idempotent -- a
--    no-op once already true; guarded so re-applying never thrashes the row.
-- ===========================================================================
UPDATE metadata.feature_classes
   SET is_diagram_placeable = true
 WHERE code = 'SUBSTATION_PANEL'
   AND is_diagram_placeable IS DISTINCT FROM true;

-- ===========================================================================
-- 2. The 'panel' glyph -- a cabinet: tall rounded rectangle, two internal
--    dividers (panel sections) and a small indicator dot. Upright (rotation 0
--    points "up"), explicit width/height, {{COLOR}}-tintable.
-- ===========================================================================
INSERT INTO metadata.symbology_icons (icon_ref, svg) VALUES
  ('panel', '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><g fill="none" stroke="{{COLOR}}" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><rect x="6" y="3" width="12" height="18" rx="1"/><line x1="6" y1="9" x2="18" y2="9"/><line x1="6" y1="15" x2="18" y2="15"/><circle cx="12" cy="6" r="0.9" fill="{{COLOR}}" stroke="none"/></g></svg>')
ON CONFLICT (icon_ref) DO NOTHING;

-- ===========================================================================
-- 3a. Mandatory 'map' default (subtype_id + domain_value_id both null) -- the
--     publish gate. A panel has no map footprint (geometry_type none), but the
--     gate requires a default rule regardless, exactly like MV_METERING_CUBICLE
--     (seed/mv_metering_symbology.sql). Slate square -- the container identity.
-- ===========================================================================
INSERT INTO metadata.symbology_rules (feature_class_id, render_context, style_def)
SELECT fc.id, 'map', '{"icon_ref": "square", "color": "#475569"}'::jsonb
FROM metadata.feature_classes fc
WHERE fc.code = 'SUBSTATION_PANEL'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules
    WHERE feature_class_id = fc.id AND subtype_id IS NULL AND domain_value_id IS NULL AND render_context = 'map'
  );

-- ===========================================================================
-- 3b. 'diagram' default (subtype_id + domain_value_id both null) -- the panel
--     block in slate, the fallback for a panel with no/unknown panel_type.
-- ===========================================================================
INSERT INTO metadata.symbology_rules (feature_class_id, render_context, style_def)
SELECT fc.id, 'diagram', '{"icon_ref": "panel", "color": "#475569"}'::jsonb
FROM metadata.feature_classes fc
WHERE fc.code = 'SUBSTATION_PANEL'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules
    WHERE feature_class_id = fc.id AND subtype_id IS NULL AND domain_value_id IS NULL AND render_context = 'diagram'
  );

-- ===========================================================================
-- 3c. Per-panel_type 'diagram' overrides -- one row per PANEL_TYPE value, keyed
--     on its domain_value_id (most-specific-wins). One set-generating INSERT
--     over (code -> colour), each guarded by NOT EXISTS so a hand-edited rule is
--     never clobbered and re-applying is a no-op.
-- ===========================================================================
INSERT INTO metadata.symbology_rules (feature_class_id, domain_value_id, render_context, style_def)
SELECT fc.id, dv.id, 'diagram',
       jsonb_build_object('icon_ref', 'panel', 'color', c.color)
FROM metadata.feature_classes fc
JOIN metadata.fields f ON f.feature_class_id = fc.id AND f.name = 'panel_type'
JOIN metadata.domain_values dv ON dv.domain_id = f.domain_id
JOIN (VALUES
    ('PROTECTION',   '#ea580c'),
    ('CONTROL',      '#2563eb'),
    ('METERING',     '#0891b2'),
    ('RTU_SCADA',    '#7c3aed'),
    ('AUXILIARY_DC', '#ca8a04'),
    ('MARSHALLING',  '#475569')
) AS c(code, color) ON c.code = dv.code
WHERE fc.code = 'SUBSTATION_PANEL'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules sr
    WHERE sr.feature_class_id = fc.id AND sr.domain_value_id = dv.id
      AND sr.subtype_id IS NULL AND sr.render_context = 'diagram'
  );
