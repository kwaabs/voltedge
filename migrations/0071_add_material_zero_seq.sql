-- 0071_add_material_zero_seq.sql
-- Purpose: two nullable zero-sequence impedance columns on
-- metadata.material_specs (migrations/0021), the exact zero-sequence
-- counterparts of the positive-sequence resistance_ohm_per_km /
-- reactance_ohm_per_km this table has always carried. Ground-fault Slice 3:
-- the single-line-to-ground calc in api/internal/powerflow (groundfault.go)
-- has until now defaulted a line's zero-sequence impedance to 3× its
-- positive-sequence value (zeroSeqLineFactor) -- a standard planning estimate
-- for an overhead earth-return path, but only an estimate. A spec that
-- records its measured r0/x0 lets the SLG calc use the real figure and flag
-- the result as non-estimated; a spec that doesn't keeps the 3× default,
-- unchanged.
--
--   * resistance_zero_ohm_per_km -- zero-sequence series resistance per km.
--     Higher than the positive-sequence value: zero-sequence current returns
--     through earth, whose resistance the phase-only loop never sees.
--   * reactance_zero_ohm_per_km  -- zero-sequence series reactance per km.
--     Also higher (a larger earth-return loop), the reason ~3× is the usual
--     lumped default these two columns refine.
--
-- Both NULLABLE, same "author now, fill in later" discipline every other
-- numeric column on this table follows: a spec with no zero-sequence data
-- simply falls back to the engine's 3× default, so no existing catalog row
-- or load flow changes behaviour until real r0/x0 is entered. No compiler or
-- CRUD-engine change -- a domain value still just carries an id pointer to
-- whichever material_specs row is current, and the powerflow engine reads
-- these two columns straight off metadata.MaterialSpec (models.go).
--
-- Bare numeric, ohm/km, same basis (per-phase, line-to-line nominal) the
-- positive-sequence columns already use, so the two compose directly in the
-- accumulated Z0 path.

ALTER TABLE metadata.material_specs
    ADD COLUMN resistance_zero_ohm_per_km numeric CHECK (resistance_zero_ohm_per_km IS NULL OR resistance_zero_ohm_per_km >= 0),
    ADD COLUMN reactance_zero_ohm_per_km  numeric CHECK (reactance_zero_ohm_per_km  IS NULL OR reactance_zero_ohm_per_km  >= 0);

COMMENT ON COLUMN metadata.material_specs.resistance_zero_ohm_per_km IS 'Zero-sequence series resistance per kilometre (ohm/km), the earth-return counterpart of resistance_ohm_per_km. NULL => the ground-fault calc falls back to 3x the positive-sequence value.';
COMMENT ON COLUMN metadata.material_specs.reactance_zero_ohm_per_km  IS 'Zero-sequence series reactance per kilometre (ohm/km), the earth-return counterpart of reactance_ohm_per_km. NULL => the ground-fault calc falls back to 3x the positive-sequence value.';
