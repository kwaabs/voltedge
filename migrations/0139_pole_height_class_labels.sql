-- 0139_pole_height_class_labels.sql
--
-- Show the nominal pole height alongside each POLE_HEIGHT_CLASS code, so the
-- attribute panel and dropdowns read "Class 1 (9 m)" instead of a bare
-- "Class 1". Only the human-readable LABEL changes; the stored code
-- (CLASS_1/2/3) is untouched, so no feature data migrates and no recompile is
-- needed (the compiled CHECK constraint keys off the code, not the label).
--
-- The heights are the common distribution-pole sizes; an admin can adjust any
-- of them later via Admin -> Domains without another migration.
--
-- NOTE: the TOWER class reuses this same POLE_HEIGHT_CLASS domain, so its
-- height-class labels now read the same metres. If towers need their own
-- (taller) classes, split them into a dedicated domain instead.
UPDATE metadata.domain_values dv
SET label = v.label
FROM (VALUES
    ('CLASS_1', 'Class 1 (9 m)'),
    ('CLASS_2', 'Class 2 (12 m)'),
    ('CLASS_3', 'Class 3 (15 m)')
) AS v(code, label)
WHERE dv.code = v.code
  AND dv.domain_id = (SELECT id FROM metadata.domains WHERE code = 'POLE_HEIGHT_CLASS');
