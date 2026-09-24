-- seed/switch_open_symbology.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations; this lives under
-- /seed on purpose, same as seed/symbology_example.sql and the other symbology
-- seeds. Phase 49 Slice 3 (live switching state, 2026-09-24): now that executing
-- a switching step flips a device's live device_state (api/internal/oms/
-- switching.go), an OPEN switch should LOOK open on the map, so an operator can
-- see the network the crew has actually opened -- the visual half of "mimic a
-- real OMS".
--
-- It gives the four OMS switching classes (MV_SWITCH / MV_ISOLATOR /
-- MV_RECLOSER / MV_BREAKER -- oms.SwitchingDeviceClasses) a device_state=OPEN
-- map override, mirroring the DIST_SWITCH OPEN override symbology_example.sql
-- already seeds. Rather than hardcode each class's shape, the override is built
-- from the class's OWN default map rule: keep its shape, force the colour to red
-- (#dc2626 = open/de-energized), and where the default shape is a solid triangle
-- swap to the hollow `triangle-outline` variant for the same shape-change signal
-- DIST_SWITCH uses (the client shape registry only has a hollow triangle; the
-- other shapes fall back to a colour-only change -- web/src/lib/symbology-icons.ts).
-- resolveSymbology returns the winning rule's style_def whole (it does NOT merge
-- onto the default), so the override must carry the full shape+colour, which is
-- exactly why it's copied from the default rather than authored as colour-only.
--
-- Idempotent (NOT EXISTS on the OPEN rule) and self-guarding: a class with no
-- device_state field, no OPEN domain value, or no default map rule to copy is
-- simply skipped, never erroring. Nothing to compile -- symbology_rules rows are
-- resolved entirely client-side (web/src/lib/symbology.ts). DIST_SWITCH is left
-- to its own override in symbology_example.sql.
--
-- Depends on the switching-class seeds and their default map symbology
-- (seed/mv_symbology_example.sql et al.) having been applied first.

INSERT INTO metadata.symbology_rules (feature_class_id, domain_value_id, render_context, style_def)
SELECT fc.id, dv.id, 'map',
    CASE
        WHEN def.style_def->>'icon_ref' = 'triangle'
            THEN jsonb_set(def.style_def, '{icon_ref}', '"triangle-outline"') || '{"color": "#dc2626"}'::jsonb
        ELSE def.style_def || '{"color": "#dc2626"}'::jsonb
    END
FROM metadata.feature_classes fc
JOIN metadata.fields f
    ON f.feature_class_id = fc.id AND f.name = 'device_state'
JOIN metadata.domain_values dv
    ON dv.domain_id = f.domain_id AND dv.code = 'OPEN'
JOIN LATERAL (
    SELECT sr.style_def
    FROM metadata.symbology_rules sr
    WHERE sr.feature_class_id = fc.id
      AND sr.subtype_id IS NULL
      AND sr.domain_value_id IS NULL
      AND sr.render_context = 'map'
    LIMIT 1
) def ON true
WHERE fc.code IN ('MV_SWITCH', 'MV_ISOLATOR', 'MV_RECLOSER', 'MV_BREAKER')
  AND NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules
    WHERE feature_class_id = fc.id
      AND subtype_id IS NULL
      AND domain_value_id = dv.id
      AND render_context = 'map'
  );
