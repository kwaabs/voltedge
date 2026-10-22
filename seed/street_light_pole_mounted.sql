-- seed/street_light_pole_mounted.sql
-- Purpose: NOT a migration -- lives under /seed, same convention as the other
-- seed files. The data-model foundation (Slice 1) for attaching a lamp to a
-- POLE: a street light that is just a luminaire bracketed onto an existing
-- distribution pole, NOT a traditional standalone column.
--
-- Three parts, all additive to the existing STREET_LIGHT class
-- (seed/lv_unmetered_ct_example.sql: a point unmetered-LV leaf with subtypes
-- column_mounted(1) / wall_mounted(2), fields phase/luminaire_type/
-- condition_rating, terminal T1, fed by LV_CONDUCTOR -> STREET_LIGHT T1):
--
--   1. a 3rd mounting subtype pole_mounted(3) -- same fields as the others.
--   2. STREET_LIGHT made geometry_optional, so a pole-mounted lamp is created
--      with NO map geometry -- it has no independent location, it rides its pole
--      (via the support relationship below). Column/wall lamps keep their point.
--      This DROPs NOT NULL on the geom column -> RECOMPILE STREET_LIGHT.
--   3. a POLE supports STREET_LIGHT relationship class (spatial_support,
--      one_to_many -- a pole may carry several lamps), the same machinery the
--      pole-mount assemblies use (seed/overhead_pole_transformer.sql).
--
-- The LV feed rule (LV_CONDUCTOR -> STREET_LIGHT T1, phase-matched) already
-- ships, so a geometry-less pole lamp can still be wired to the LV it taps.
--
-- Slice 2 (the "Attach street light" action: create the geometry-less lamp +
-- the support link + auto-wire to the pole's single nearby LV conductor) and
-- Slice 3 (the pole-panel button) build on this.
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL is
-- reviewed statically -- a live psql apply + `task compile CODE=STREET_LIGHT`
-- are owed to call it verified.
--   task seed-street-light-pole + task compile CODE=STREET_LIGHT

-- ---------------------------------------------------------------------------
-- 1. The pole_mounted mounting subtype.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, 3, 'pole_mounted'
FROM metadata.feature_classes fc
WHERE fc.code = 'STREET_LIGHT'
ON CONFLICT (feature_class_id, code) DO NOTHING;

-- Same required fields as the column/wall subtypes (phase, luminaire, condition).
-- These are already field-level NOT NULL, so this mirrors the existing block for
-- consistency rather than adding a new requirement. The load fields (demand_kw /
-- power_factor / load_priority) stay nullable, as for every subtype.
INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, true
FROM metadata.subtypes s
JOIN metadata.fields f ON f.feature_class_id = s.feature_class_id
WHERE s.feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'STREET_LIGHT')
  AND s.code = 3
  AND f.name IN ('phase', 'luminaire_type', 'condition_rating')
ON CONFLICT (subtype_id, field_id) DO NOTHING;

-- ---------------------------------------------------------------------------
-- 2. geometry_optional: a pole-mounted lamp has no geometry of its own; it
--    rides its pole. Flips geom to nullable on recompile. Idempotent.
-- ---------------------------------------------------------------------------
UPDATE metadata.feature_classes
SET geometry_optional = true
WHERE code = 'STREET_LIGHT' AND geometry_optional = false;

-- ---------------------------------------------------------------------------
-- 3. POLE supports STREET_LIGHT -- the structural attachment, like POLE supports
--    MV_CONDUCTOR / the pole-mount kit components. one_to_many: one pole, many
--    lamps; each lamp on exactly one pole.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.relationship_classes (name, origin_feature_class_id, destination_feature_class_id, kind, cardinality)
SELECT 'POLE supports STREET_LIGHT', pole.id, sl.id, 'spatial_support', 'one_to_many'
FROM metadata.feature_classes pole
JOIN metadata.feature_classes sl ON sl.code = 'STREET_LIGHT'
WHERE pole.code = 'POLE'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.relationship_classes rc
    WHERE rc.origin_feature_class_id = pole.id AND rc.destination_feature_class_id = sl.id AND rc.kind = 'spatial_support'
  );

DO $$
BEGIN
    RAISE NOTICE 'seed/street_light_pole_mounted.sql applied: + STREET_LIGHT subtype 3 pole_mounted, + geometry_optional, + POLE supports STREET_LIGHT relationship class. RECOMPILE: task compile CODE=STREET_LIGHT (geometry_optional drops NOT NULL on geom).';
END $$;
