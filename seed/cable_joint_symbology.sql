-- seed/cable_joint_symbology.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives under
-- /seed on purpose, same as the other symbology seeds. Phase 44 Slice 2
-- (docs/phase-44-cable-management.md): the mandatory 'map' default symbology rule
-- for the two cable-joint junction classes MV_CABLE_JOINT / LV_CABLE_JOINT.
--
-- Why: the publish gate (docs/metadata-core.md) requires every feature class to
-- have one symbology_rules row with subtype_id AND domain_value_id both null (a
-- 'map' default) before it can leave draft. Points get {icon_ref,color}
-- (seed/symbology_example.sql). No 'diagram' rule -- joints are not placed on a
-- schematic in this slice (map only).
--
-- A joint reads as a small accessory DOT sitting on the cable run, so a filled
-- circle in a darker shade than the conductor line it splices (so the dot shows
-- against the line): MV_CABLE_JOINT deep teal (the MV family, darker than
-- MV_CONDUCTOR's #3f8fa6), LV_CABLE_JOINT slate (the LV family). Distinct from
-- the Slice 1 CIVIL earth tones so the electrical joint reads apart from a
-- duct/trench.
--
-- Depends on seed/cable_joint_example.sql. Apply with psql after it, e.g.
--   task seed-cable-joint-symbology
-- Nothing to compile -- symbology_rules resolve entirely client-side
-- (web/src/lib/symbology.ts).

-- MV_CABLE_JOINT -- deep-teal splice dot.
INSERT INTO metadata.symbology_rules (feature_class_id, render_context, style_def)
SELECT fc.id, 'map', '{"icon_ref": "circle", "color": "#1f5b6b"}'::jsonb
FROM metadata.feature_classes fc
WHERE fc.code = 'MV_CABLE_JOINT'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules
    WHERE feature_class_id = fc.id AND subtype_id IS NULL AND domain_value_id IS NULL AND render_context = 'map'
  );

-- LV_CABLE_JOINT -- slate splice dot.
INSERT INTO metadata.symbology_rules (feature_class_id, render_context, style_def)
SELECT fc.id, 'map', '{"icon_ref": "circle", "color": "#475569"}'::jsonb
FROM metadata.feature_classes fc
WHERE fc.code = 'LV_CABLE_JOINT'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules
    WHERE feature_class_id = fc.id AND subtype_id IS NULL AND domain_value_id IS NULL AND render_context = 'map'
  );
