-- seed/mv_busbar_voltage_symbology.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives under
-- /seed on purpose, same as seed/mv_symbology_example.sql. Colours an MV busbar
-- by its VOLTAGE class: 11 kV red, 33 kV blue -- the "blue for 33, red for 11"
-- ask on the double-busbar one-line.
--
-- HOW it colours (docs/metadata-core.md "Shape vs. modifier"): a symbology rule
-- scoped to a domain_value_id (the coded voltage_class value) is MORE specific
-- than the plain per-class default (subtype_id + domain_value_id both NULL), so
-- resolveSymbology (web/src/lib/symbology.ts) picks it whenever a bus feature's
-- voltage_class matches. It layers ON TOP of the flat slate default seeded by
-- seed/mv_symbology_example.sql -- that default stays as the fallback for a bus
-- with no/other voltage_class. This is per-VALUE conditional colour, not
-- arbitrary expression symbology; it has worked since Phase 1.
--
-- TWO HARD LIMITS worth stating so nobody expects more than it does:
--   1. Symbology is resolved for LIVE FEATURES (placements) only. The decorative
--      seed/double_busbar_template.sql busbars are baked `element` furniture with
--      a fixed stroke -- they will NEVER colour by voltage. You only see this on
--      a REAL MV_BUSBAR feature (e.g. one promoted from a template bus, or the
--      seed/double_busbar_diagram_example.sql live one-line).
--   2. The double-busbar NETWORK model is all-11 kV, so every bus there renders
--      red. The blue/red split only shows on a mixed-voltage site (e.g. the
--      33/11 kV seed/substation_network_example.sql).
--
-- Colours match the client convention (Tailwind red-600 / blue-600): 11 kV
-- #dc2626, 33 kV #2563eb. Both render contexts covered: 'diagram' (colours the
-- one-line bus stroke) and 'map' (the geographic fallback -- MV_BUSBAR is
-- geometry_optional, so a bus CAN be placed on the map). icon_ref mirrors each
-- context's default rule (diagram ansi-terminal / map square) since a rule
-- carries a COMPLETE style_def, not a partial; for a line-drawn bus the canvas
-- uses only the colour as the stroke and ignores the icon_ref.
--
-- Idempotent (NOT EXISTS per (class, domain value, context)); re-applying never
-- clobbers a hand-edited rule. No-op if MV_BUSBAR / VOLTAGE_CLASS aren't seeded.
-- Nothing to compile -- symbology_rules resolve entirely client-side. Apply
-- after seed/mv_busbar_example.sql and seed/mv_symbology_example.sql.

INSERT INTO metadata.symbology_rules (feature_class_id, domain_value_id, render_context, style_def)
SELECT bb.id, dv.id, ctx.render_context,
       jsonb_build_object('icon_ref', ctx.icon_ref, 'color', c.color)
FROM metadata.feature_classes bb
JOIN metadata.fields f ON f.feature_class_id = bb.id AND f.name = 'voltage_class'
JOIN metadata.domain_values dv ON dv.domain_id = f.domain_id
JOIN (VALUES
    ('11KV', '#dc2626'),
    ('33KV', '#2563eb')
) AS c(code, color) ON c.code = dv.code
JOIN (VALUES
    ('diagram', 'ansi-terminal'),
    ('map',     'square')
) AS ctx(render_context, icon_ref) ON true
WHERE bb.code = 'MV_BUSBAR'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules r
    WHERE r.feature_class_id = bb.id
      AND r.subtype_id IS NULL
      AND r.domain_value_id = dv.id
      AND r.render_context = ctx.render_context
  );
