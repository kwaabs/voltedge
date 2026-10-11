-- Network one-line conductors render with their own symbology.
--
-- The generated one-line (migration 0135) now PLACES the conductor features
-- themselves rather than flattening every conductor to an identical generic
-- line, so the canvas draws each wire between its endpoint placements styled by
-- the conductor's own diagram symbology (MV vs LV colour/weight) -- and the
-- conductor becomes clickable and energisation-aware like any other placement.
--
-- For that a conductor's class must be diagram-placeable. MV_CONDUCTOR was
-- already flipped by seed/assembly_diagram_templates.sql; this generalises it to
-- every electrical line class -- geometry_type='line' AND network_role='edge'
-- (conductors) -- so LV_CONDUCTOR and the rest place too. Civil/duct line
-- classes (network_role <> 'edge') are deliberately untouched: they are not
-- electrical wires and have no place on a one-line.
UPDATE metadata.feature_classes
SET is_diagram_placeable = true
WHERE geometry_type = 'line'
  AND network_role = 'edge'
  AND is_diagram_placeable = false;
