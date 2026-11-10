-- seed/packaged_substation_symbology.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives
-- under /seed like the other symbology seeds. Gives PACKAGED_SUBSTATION a
-- GEOGRAPHIC-MAP glyph that matches an ALREADY-PLACED unit, keyed on a
-- column the feature already carries (package_config), rather than on the
-- assembly_ref placement marker.
--
-- Why not assembly_ref: the kit editor's "Map symbol" field writes map
-- rules that match on assembly_ref = <kit code> (e.g. PACKED_RMU_FIXED).
-- assembly_ref is stamped onto the anchor only at PLACEMENT time by the
-- fan-out engine (migration 0192). A packaged substation placed before
-- that shipped has no assembly_ref, so those rules never match it and the
-- map draws the plain fallback glyph. package_config, by contrast, is a
-- nullable:false domain field present on EVERY packaged substation (COMPACT
-- = "MV switch + transformer + LV board"), already published as a tile
-- property, so a rule on it matches existing AND future units with no
-- marker, backfill or recompile.
--
-- Two rows:
--   1. the mandatory class DEFAULT (subtype_id + domain_value_id both null)
--      -- docs/metadata-core.md requires exactly one per class; this class
--      currently has none, which is the other half of why an unmatched unit
--      shows nothing styled. A neutral square placeholder so any future,
--      non-COMPACT config still draws something.
--   2. the package_config = COMPACT override -- the real packaged-station
--      artwork (voltedge-packaged-station-rmu-dist-xfmr-lv-feeder-pillar,
--      already in metadata.symbology_icons). Most-specific-wins resolution
--      (web/src/lib/symbology.ts) picks this over the default for a COMPACT
--      unit.
--
-- Nothing to compile -- symbology_rules rows have no physical footprint,
-- resolved entirely client-side. A map session must reload to pick them up.

-- 1. Mandatory class default (subtype + domain both null).
INSERT INTO metadata.symbology_rules (feature_class_id, render_context, style_def)
SELECT fc.id, 'map', '{"icon_ref": "square", "color": "#2563eb"}'::jsonb
FROM metadata.feature_classes fc
WHERE fc.code = 'PACKAGED_SUBSTATION'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules
    WHERE feature_class_id = fc.id AND subtype_id IS NULL AND domain_value_id IS NULL AND render_context = 'map'
  );

-- 2. package_config = COMPACT -> the packaged-station glyph.
INSERT INTO metadata.symbology_rules (feature_class_id, domain_value_id, render_context, style_def)
SELECT fc.id, dv.id, 'map',
       '{"icon_ref": "voltedge-packaged-station-rmu-dist-xfmr-lv-feeder-pillar", "color": "#2563eb"}'::jsonb
FROM metadata.feature_classes fc
JOIN metadata.fields f        ON f.feature_class_id = fc.id AND f.name = 'package_config'
JOIN metadata.domain_values dv ON dv.domain_id = f.domain_id AND dv.code = 'COMPACT'
WHERE fc.code = 'PACKAGED_SUBSTATION'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules
    WHERE feature_class_id = fc.id AND subtype_id IS NULL AND domain_value_id = dv.id AND render_context = 'map'
  );
