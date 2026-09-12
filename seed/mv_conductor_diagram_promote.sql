-- seed/mv_conductor_diagram_promote.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives under
-- /seed on purpose. Makes MV_CONDUCTOR promotable/placeable on a diagram with no
-- map geometry, so a schematic riser/drop on a one-line (e.g. the "Double
-- busbar" template's bay risers) can be double-clicked into a real MV_CONDUCTOR
-- feature keeping its drawn polyline -- the same treatment MV_BUSBAR already has
-- (seed/mv_busbar_example.sql).
--
-- Flips two flags on the existing class (base connectivity_example.sql seeds it
-- with both default false):
--   * is_diagram_placeable = true  -- lets a placement land on a diagram
--     (placeFeature's migration-0032 gate), which a promoted feature is.
--   * geometry_optional    = true  -- lets the feature be created with NO map
--     geometry (Phase 26; crud validateGeometry), which a diagram-only promote
--     always does (feature-create-form.tsx sends geometry only when drawn on the
--     map).
--
-- SAFE for the network engines (verified 2026-09-11): a conductor's connectivity
-- is explicit runtime.feature_connectivity, NOT geometry, so a geometry-less
-- conductor still traces as long as it's wired. powerflow resolve.go computes
-- branch impedance from ST_Length(geom) guarded `WHERE geom IS NOT NULL` and
-- returns a ZERO-impedance ideal wire (no error) when geom is null -- so a
-- diagram-only conductor degrades to a lossless span rather than breaking load
-- flow / fault calcs. Give it real map geometry later to restore its impedance.
--
-- No diagram_geometry_type override needed: MV_CONDUCTOR's geometry_type is
-- already 'line', so geometryTypeByClass (diagram-canvas.tsx) draws its diagram
-- placement as a line and the promote carries the drawn path (migration 0053).
--
-- Idempotent + guarded to the still-false flag, so an admin who deliberately
-- turned either off isn't overridden on re-apply. No-op if MV_CONDUCTOR is
-- absent. Nothing to compile -- these are metadata flags read at request time.
UPDATE metadata.feature_classes
SET is_diagram_placeable = true
WHERE code = 'MV_CONDUCTOR'
  AND is_diagram_placeable = false;

UPDATE metadata.feature_classes
SET geometry_optional = true
WHERE code = 'MV_CONDUCTOR'
  AND geometry_optional = false;
