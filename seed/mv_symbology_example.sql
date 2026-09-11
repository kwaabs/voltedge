-- seed/mv_symbology_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives under
-- /seed on purpose, same as seed/symbology_example.sql, diagram_symbology.sql
-- and ansi_stencils.sql. Phase 27, Slice 5: give the eight new MV plant classes
-- (Slices 1-4) their symbology and wire the existing ANSI palette stencils to
-- promote into them.
--
-- Three parts, all idempotent (NOT EXISTS on the rules, guarded UPDATE on the
-- stencil mappings -- re-applying never clobbers a hand-edited rule/mapping):
--   1. render_context = 'map' default rules -- the mandatory per-class default
--      (subtype_id + domain_value_id both NULL) that docs/metadata-core.md
--      requires before publish. Simple shapes from the client shape registry
--      (web/src/lib/symbology-icons.ts: circle/square/triangle/diamond), colour
--      -coded by function (switching green, protection orange, instrument cyan,
--      bus slate) -- the map fallback for the times a feature IS placed
--      geographically (these classes are geometry_optional, so they can be).
--   2. render_context = 'diagram' rules -- one per class, pointing at the ANSI
--      one-line glyph already seeded by ansi_stencils.sql (NO new artwork), so a
--      placement on a one-line reads as the real electrical symbol. The canvas
--      prefers the 'diagram' rule and falls back to 'map'
--      (web/src/components/map/diagram-canvas.tsx), same as diagram_symbology.sql.
--   3. metadata.diagram_stencils.promote_class -- map each switching/protection/
--      measuring stencil to its new feature class (migration 0051), so
--      double-clicking a stamped glyph offers to turn it into a real feature --
--      exactly the ansi-relay -> PROTECTIVE_RELAY wiring from ansi_stencils.sql,
--      now closing the loop for breaker/isolator/sectionalizer/fuse/arrester/
--      CT/VT. MV_BUSBAR gets no promotion (a bus is drawn with the diagram `bus`
--      primitive, not stamped from a point stencil).
--
-- Class -> glyph pairing (map shape / diagram glyph / colour):
--   MV_BREAKER       square   / ansi-breaker        / #16a34a (switching green)
--   MV_ISOLATOR      triangle / ansi-disconnect     / #16a34a
--   MV_SECTIONALIZER diamond  / ansi-load-break     / #16a34a
--   MV_FUSE          triangle / ansi-fuse           / #ea580c (protection orange)
--   MV_ARRESTER      diamond  / ansi-surge-arrester / #ea580c
--   MV_CT            circle   / ansi-ct             / #0891b2 (instrument cyan)
--   MV_VT            square   / ansi-vt             / #0891b2
--   MV_BUSBAR        square   / ansi-terminal       / #334155 (node slate)
--
-- No OPEN-state map override is seeded (DIST_SWITCH already demonstrates that
-- pattern). The client shape registry has no hollow variant of square/diamond,
-- so a shape-preserving OPEN override the way DIST_SWITCH does (triangle ->
-- triangle-outline) isn't available for the non-triangle classes; left for a
-- later slice that adds those hollow shapes. The device_state OPEN barrier
-- (Slices 1-2) is the functional isolation behaviour and is unaffected.
--
-- Depends on the Phase 27 class seeds (mv_switching / mv_protection /
-- mv_measuring / mv_busbar) and on seed/ansi_stencils.sql (the glyphs + stencil
-- catalog rows this points at). Rows for classes/stencils not present are simply
-- skipped. Nothing to compile -- symbology_rules and diagram_stencils are
-- resolved entirely client-side.

-- 1. Mandatory map-context defaults.
INSERT INTO metadata.symbology_rules (feature_class_id, render_context, style_def)
SELECT fc.id, 'map', jsonb_build_object('icon_ref', v.icon_ref, 'color', v.color)
FROM metadata.feature_classes fc
JOIN (VALUES
    ('MV_BREAKER',       'square',   '#16a34a'),
    ('MV_ISOLATOR',      'triangle', '#16a34a'),
    ('MV_SECTIONALIZER', 'diamond',  '#16a34a'),
    ('MV_FUSE',          'triangle', '#ea580c'),
    ('MV_ARRESTER',      'diamond',  '#ea580c'),
    ('MV_CT',            'circle',   '#0891b2'),
    ('MV_VT',            'square',   '#0891b2'),
    ('MV_BUSBAR',        'square',   '#334155')
) AS v(code, icon_ref, color) ON v.code = fc.code
WHERE NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules
    WHERE feature_class_id = fc.id AND subtype_id IS NULL AND domain_value_id IS NULL AND render_context = 'map'
);

-- 2. Diagram-context glyph rules.
INSERT INTO metadata.symbology_rules (feature_class_id, render_context, style_def)
SELECT fc.id, 'diagram', jsonb_build_object('icon_ref', v.icon_ref, 'color', v.color)
FROM metadata.feature_classes fc
JOIN (VALUES
    ('MV_BREAKER',       'ansi-breaker',        '#16a34a'),
    ('MV_ISOLATOR',      'ansi-disconnect',     '#16a34a'),
    ('MV_SECTIONALIZER', 'ansi-load-break',     '#16a34a'),
    ('MV_FUSE',          'ansi-fuse',           '#ea580c'),
    ('MV_ARRESTER',      'ansi-surge-arrester', '#ea580c'),
    ('MV_CT',            'ansi-ct',             '#0891b2'),
    ('MV_VT',            'ansi-vt',             '#0891b2'),
    ('MV_BUSBAR',        'ansi-terminal',       '#334155')
) AS v(code, icon_ref, color) ON v.code = fc.code
WHERE NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules
    WHERE feature_class_id = fc.id AND subtype_id IS NULL AND domain_value_id IS NULL AND render_context = 'diagram'
);

-- 3. Stencil -> feature promotion. One guarded set-generating UPDATE: only sets
-- a mapping that's still NULL and only for a class that actually exists, so a
-- hand-authored promote_class is never clobbered and unseeded classes are
-- skipped -- exactly the ansi-relay -> PROTECTIVE_RELAY guard from
-- ansi_stencils.sql.
UPDATE metadata.diagram_stencils ds
SET promote_class = m.class_code
FROM (VALUES
    ('ansi-breaker',        'MV_BREAKER'),
    ('ansi-disconnect',     'MV_ISOLATOR'),
    ('ansi-load-break',     'MV_SECTIONALIZER'),
    ('ansi-fuse',           'MV_FUSE'),
    ('ansi-surge-arrester', 'MV_ARRESTER'),
    ('ansi-ct',             'MV_CT'),
    ('ansi-vt',             'MV_VT')
) AS m(icon_ref, class_code)
WHERE ds.icon_ref = m.icon_ref
  AND ds.promote_class IS NULL
  AND EXISTS (SELECT 1 FROM metadata.feature_classes WHERE code = m.class_code);
