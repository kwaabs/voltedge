-- 0117_add_material_line_geometry.sql
-- Purpose: conductor LINE GEOMETRY columns on metadata.material_specs
-- (migrations/0021), the input a full Carson's-equations series-impedance
-- build needs and that the sequence/per-km columns cannot supply. The
-- three-phase unbalanced load flow's Tier 1 solver
-- (api/internal/powerflow/unbalanced.go) treats phases A/B/C as three
-- INDEPENDENT ladders -- honest, but it never sees the mutual coupling a
-- real multi-conductor line has, so a heavily loaded single phase does not
-- pull its neighbours' voltages the way it does in the field. Tier 2 builds
-- the asymmetric 3x3 phase-impedance matrix straight from conductor geometry
-- via Carson with an earth-return path, then Kron-reduces the neutral into
-- the phase partition -- and that needs, per conductor type, the geometric
-- mean radius of each wire and where each wire sits on the structure.
--
-- Geometry lives on the SPEC, not on each feature: a conductor already
-- resolves its electrical spec through a domain value -> material_specs row
-- (resolveMaterialSpecByCode), so attaching the standard construction here
-- lights up every conductor of that type at once with no per-feature entry.
-- A spec that bundles "477 ACSR, flat 1 m, neutral 0.6 m below" is the wire
-- AND its usual construction; a differently-strung line is simply a
-- different spec, the same way the catalog already distinguishes ratings.
--
--   Phase-conductor geometry
--   * gmr_phase_mm            -- geometric mean radius of a PHASE conductor
--     (mm). The self-GMD that sets the self-impedance ln(De/GMR) term. NULL
--     and the solver falls back to 0.7788 x (diameter_mm / 2) -- the solid
--     round-wire approximation -- or, failing that, to the Tier 1 decoupled
--     path for that segment.
--
--   Neutral-conductor geometry (4-wire, for the Kron reduction)
--   * gmr_neutral_mm                 -- GMR of the NEUTRAL conductor (mm).
--   * resistance_neutral_ohm_per_km  -- AC resistance of the neutral (ohm/km);
--     the neutral's own resistance_ohm_per_km, needed on its matrix diagonal
--     before it is folded away.
--   With no neutral position/GMR the line is treated as 3-wire (no Kron
--   step) -- the phase 3x3 is built directly.
--
--   Wire positions on the structure (metres, arbitrary but CONSISTENT origin
--   -- only pairwise distances matter to Carson, so any fixed datum works;
--   x horizontal, y vertical/height)
--   * pos_a_x_m, pos_a_y_m   -- phase A
--   * pos_b_x_m, pos_b_y_m   -- phase B
--   * pos_c_x_m, pos_c_y_m   -- phase C
--   * pos_n_x_m, pos_n_y_m   -- neutral (NULL => 3-wire)
--
-- Every column NULLABLE, same "author now, fill later" discipline as the
-- zero-sequence pair (migrations/0071): a spec with no geometry changes no
-- existing behaviour -- the Tier 1 decoupled unbalanced solve, and every
-- balanced/sequence analysis, read none of these columns. Only the Tier 2
-- coupled solve consults them, and only when present. No compiler or CRUD
-- change: a domain value still just points at a material_specs row, and the
-- powerflow engine reads these columns straight off metadata.MaterialSpec
-- (models.go).

ALTER TABLE metadata.material_specs
    ADD COLUMN gmr_phase_mm                numeric CHECK (gmr_phase_mm                IS NULL OR gmr_phase_mm                > 0),
    ADD COLUMN gmr_neutral_mm              numeric CHECK (gmr_neutral_mm              IS NULL OR gmr_neutral_mm              > 0),
    ADD COLUMN resistance_neutral_ohm_per_km numeric CHECK (resistance_neutral_ohm_per_km IS NULL OR resistance_neutral_ohm_per_km >= 0),
    ADD COLUMN pos_a_x_m                   numeric,
    ADD COLUMN pos_a_y_m                   numeric,
    ADD COLUMN pos_b_x_m                   numeric,
    ADD COLUMN pos_b_y_m                   numeric,
    ADD COLUMN pos_c_x_m                   numeric,
    ADD COLUMN pos_c_y_m                   numeric,
    ADD COLUMN pos_n_x_m                   numeric,
    ADD COLUMN pos_n_y_m                   numeric;

COMMENT ON COLUMN metadata.material_specs.gmr_phase_mm                  IS 'Geometric mean radius of a PHASE conductor (mm); the self-GMD in Carson''s ln(De/GMR) self term. NULL => solver derives 0.7788 x radius from diameter_mm, else falls back to the decoupled per-phase solve.';
COMMENT ON COLUMN metadata.material_specs.gmr_neutral_mm                IS 'GMR of the NEUTRAL conductor (mm), used with its position + resistance to Kron-reduce the neutral into the phase 3x3. NULL (or no neutral position) => the line is treated as 3-wire.';
COMMENT ON COLUMN metadata.material_specs.resistance_neutral_ohm_per_km IS 'AC resistance of the NEUTRAL conductor (ohm/km); the neutral''s matrix-diagonal resistance before Kron reduction. NULL => line treated as 3-wire.';
COMMENT ON COLUMN metadata.material_specs.pos_a_x_m IS 'Phase A wire horizontal position on the structure (m). Origin is arbitrary but must be consistent across a spec''s wires -- only pairwise distances enter Carson.';
COMMENT ON COLUMN metadata.material_specs.pos_a_y_m IS 'Phase A wire vertical/height position on the structure (m).';
COMMENT ON COLUMN metadata.material_specs.pos_b_x_m IS 'Phase B wire horizontal position on the structure (m).';
COMMENT ON COLUMN metadata.material_specs.pos_b_y_m IS 'Phase B wire vertical/height position on the structure (m).';
COMMENT ON COLUMN metadata.material_specs.pos_c_x_m IS 'Phase C wire horizontal position on the structure (m).';
COMMENT ON COLUMN metadata.material_specs.pos_c_y_m IS 'Phase C wire vertical/height position on the structure (m).';
COMMENT ON COLUMN metadata.material_specs.pos_n_x_m IS 'Neutral wire horizontal position on the structure (m). NULL => line treated as 3-wire (no Kron reduction).';
COMMENT ON COLUMN metadata.material_specs.pos_n_y_m IS 'Neutral wire vertical/height position on the structure (m). NULL => line treated as 3-wire.';
