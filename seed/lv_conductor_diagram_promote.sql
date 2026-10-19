-- seed/lv_conductor_diagram_promote.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives under
-- /seed on purpose. Makes LV_CONDUCTOR promotable/placeable on a diagram with no
-- map geometry, so a schematic drop/feeder on an LV one-line (e.g. the LV feeder
-- pillar diagram's outgoing ways) can be double-clicked into a real LV_CONDUCTOR
-- feature keeping its drawn polyline. This is the LV twin of
-- seed/mv_conductor_diagram_promote.sql -- without it, the on-diagram "Promote
-- to…" picker offers MV_CONDUCTOR (which has these flags) but NOT LV_CONDUCTOR,
-- because the picker only lists geometry-less/optional + diagram-placeable classes.
--
-- Flips two flags on the existing class (base lv_conductors_example.sql seeds it
-- with both default false):
--   * is_diagram_placeable = true  -- lets a placement land on a diagram
--     (placeFeature's migration-0032 gate), which a promoted feature is.
--   * geometry_optional    = true  -- lets the feature be created with NO map
--     geometry (Phase 26; crud validateGeometry), which a diagram-only promote
--     always does (feature-create-form.tsx sends geometry only when drawn on the
--     map).
--
-- SAFE for the network engines (same reasoning as the MV twin): a conductor's
-- connectivity is explicit runtime.feature_connectivity, NOT geometry, so a
-- geometry-less LV_CONDUCTOR still traces as long as it's wired, and powerflow
-- treats a null-geom span as a ZERO-impedance ideal wire (resolve.go's
-- ST_Length guarded `WHERE geom IS NOT NULL`) rather than erroring. Give it real
-- map geometry later to restore its impedance.
--
-- No diagram_geometry_type override needed: LV_CONDUCTOR's geometry_type is
-- already 'line', so the diagram draws its placement as a line and the promote
-- carries the drawn path (migration 0053).
--
-- Idempotent + guarded to the still-false flag, so an admin who deliberately
-- turned either off isn't overridden on re-apply. No-op if LV_CONDUCTOR is absent.
--
-- !!! RECOMPILE REQUIRED after this seed: `task compile CODE=LV_CONDUCTOR` !!!
-- is_diagram_placeable is read at request time, but the PHYSICAL geom column was
-- baked NOT NULL when the class first compiled. Setting geometry_optional here
-- does NOT drop that constraint on its own -- so a diagram-only promote (geom
-- null) fails with 23502 until you recompile. The compiler relaxes it: on
-- recompile it sees geometry_optional + an existing geom and runs
-- `ALTER COLUMN geom DROP NOT NULL` (api/internal/compiler/compiler.go). Same
-- step MV_CONDUCTOR took (`task compile CODE=MV_CONDUCTOR`).
UPDATE metadata.feature_classes
SET is_diagram_placeable = true
WHERE code = 'LV_CONDUCTOR'
  AND is_diagram_placeable = false;

UPDATE metadata.feature_classes
SET geometry_optional = true
WHERE code = 'LV_CONDUCTOR'
  AND geometry_optional = false;
