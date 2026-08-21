-- 0035_add_phase_transform.sql
-- Purpose: extend the metadata.cim_attribute_mappings.transform vocabulary with
-- 'phase' -- the transform that drives CIM per-phase objects in Phase 16 Slice 4
-- (docs/phase-16-cim-export.md). The Slice 1-3 transforms all yield a single
-- literal/reference on the equipment; phasing is different in kind: one coded
-- PHASE value (e.g. L1L2L3N) expands into several separate CIM objects
-- (ACLineSegmentPhase / EnergyConsumerPhase, one per SinglePhaseKind), each a
-- sibling of the equipment referencing it. The behavioral expansion lives in Go
-- (api/internal/export/phasing.go); this migration only widens the CHECK so the
-- seed can author the 'phase' rows.
--
-- The CHECK is an inline column constraint, so Postgres named it
-- cim_attribute_mappings_transform_check. Drop + re-add is the only way to alter
-- an enumerated CHECK; IF EXISTS keeps it safe on a DB where 0034's exact name
-- ever differs.

ALTER TABLE metadata.cim_attribute_mappings
    DROP CONSTRAINT IF EXISTS cim_attribute_mappings_transform_check;

ALTER TABLE metadata.cim_attribute_mappings
    ADD CONSTRAINT cim_attribute_mappings_transform_check
    CHECK (transform IN (
        'direct', 'scale', 'domain_map',
        'boolean', 'from_geometry', 'material_spec', 'phase'));

COMMENT ON COLUMN metadata.cim_attribute_mappings.transform IS 'How the field value is coerced: direct (as-is), scale (x scale_factor, e.g. km->m), domain_map (value_map lookup, e.g. 11KV->11000), boolean (value_map coded->bool, e.g. OPEN->true), from_geometry (derive from the feature geometry, e.g. conductor length -- field is a placeholder), material_spec (resolve the field''s domain value to metadata.material_specs for r/x/ampacity), phase (expand a coded PHASE value into per-phase CIM objects -- ACLineSegmentPhase/EnergyConsumerPhase, one per SinglePhaseKind).';
