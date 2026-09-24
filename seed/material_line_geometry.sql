-- seed/material_line_geometry.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives under
-- /seed on purpose. Phase 55, Tier 2 (Carson-coupled unbalanced load flow): give
-- a handful of existing conductor material_specs real LINE GEOMETRY (the columns
-- migrations/0117 added) so the coupled solver has an asymmetric 3x3 to build.
-- Until a spec carries this geometry the coupled run silently degrades to the
-- decoupled diagonal for that segment, so this is what actually turns the mutual
-- coupling on for the seeded example networks.
--
-- Every value is an idempotent UPDATE keyed on material_specs.code: applying it
-- twice sets the same numbers, and a spec these codes don't name is untouched.
-- Depends on migration 0117 and the two catalog seeds that create these rows
-- (seed/lv_conductors_example.sql, seed/mv_conductor_specs.sql).
--
-- Geometry conventions (see migrations/0117):
--   * positions in METRES from any consistent per-spec origin (only pairwise
--     distances enter Carson); x horizontal, y height.
--   * gmr_*_mm the wire's geometric mean radius in mm (the solver divides by
--     1000). Planning-grade figures (~0.39 x conductor diameter for stranded
--     line, a core-radius estimate for cable) -- real survey data replaces them.
--   * a neutral (pos_n_* + gmr_neutral_mm + resistance_neutral_ohm_per_km) is
--     given for the LV cables (4-wire, Kron-reduced) and omitted for the MV
--     overhead lines (modelled 3-wire), which is the usual construction.
--
-- Apply with psql (no compile needed -- these are catalog columns the powerflow
-- engine reads straight off metadata.MaterialSpec, not compiled feature fields):
--   task seed-material-line-geometry   (or psql -f this file)
--
-- Static-review note: shell here can't spawn (bash.exe EPERM); a live psql apply
-- + a coupled unbalanced-load-flow re-check are still owed to call it verified.

-- LV aerial bundled + underground cables: four cores held close together, so the
-- mutual terms are large relative to the small self-vs-mutual gap -- exactly where
-- coupling and the neutral's Kron reduction matter most on an unbalanced LV feeder.
UPDATE metadata.material_specs
SET gmr_phase_mm = 4.3,
    gmr_neutral_mm = 4.3,
    resistance_neutral_ohm_per_km = 0.320,
    pos_a_x_m = 0.000, pos_a_y_m = 0.000,
    pos_b_x_m = 0.030, pos_b_y_m = 0.000,
    pos_c_x_m = 0.015, pos_c_y_m = 0.026,
    pos_n_x_m = 0.015, pos_n_y_m = 0.009
WHERE code = 'ABC_95';

UPDATE metadata.material_specs
SET gmr_phase_mm = 6.0,
    gmr_neutral_mm = 5.0,
    resistance_neutral_ohm_per_km = 0.164,
    pos_a_x_m = 0.000, pos_a_y_m = 0.000,
    pos_b_x_m = 0.050, pos_b_y_m = 0.000,
    pos_c_x_m = 0.025, pos_c_y_m = 0.043,
    pos_n_x_m = 0.025, pos_n_y_m = 0.014
WHERE code = 'WAVEFORM_185';

-- MV overhead ACSR: bare phase conductors on a flat crossarm ~0.9-1.1 m apart,
-- ~8 m up, no aerial earth wire modelled (3-wire). Wider spacing -> smaller
-- mutual coupling than the LV cables, but still non-zero and asymmetric.
UPDATE metadata.material_specs
SET gmr_phase_mm = 3.6,
    pos_a_x_m = -0.90, pos_a_y_m = 7.50,
    pos_b_x_m =  0.00, pos_b_y_m = 7.50,
    pos_c_x_m =  0.90, pos_c_y_m = 7.50
WHERE code = 'ACSR_50';

UPDATE metadata.material_specs
SET gmr_phase_mm = 5.0,
    pos_a_x_m = -1.00, pos_a_y_m = 8.00,
    pos_b_x_m =  0.00, pos_b_y_m = 8.00,
    pos_c_x_m =  1.00, pos_c_y_m = 8.00
WHERE code = 'ACSR_100';

UPDATE metadata.material_specs
SET gmr_phase_mm = 6.1,
    pos_a_x_m = -1.10, pos_a_y_m = 8.50,
    pos_b_x_m =  0.00, pos_b_y_m = 8.50,
    pos_c_x_m =  1.10, pos_c_y_m = 8.50
WHERE code = 'ACSR_150';
