-- 0022_add_material_thermal_properties.sql
-- Purpose: four new nullable columns on metadata.material_specs
-- (migrations/0021) carrying the conductor-intrinsic properties a
-- first-principles ampacity calc (IEEE Std 738 steady-state thermal
-- rating) needs beyond the static resistance/reactance/ampacity
-- columns that table already has. Environmental conditions the same
-- calc needs -- ambient temperature, wind speed/angle, solar heat gain
-- -- are deliberately NOT stored here: those vary per calculation run,
-- not per conductor, so they're request-time parameters to the
-- ampacity endpoint instead.

ALTER TABLE metadata.material_specs
    ADD COLUMN max_operating_temp_c            numeric CHECK (max_operating_temp_c IS NULL OR max_operating_temp_c > 0),
    ADD COLUMN emissivity                      numeric CHECK (emissivity IS NULL OR (emissivity > 0 AND emissivity <= 1)),
    ADD COLUMN absorptivity                    numeric CHECK (absorptivity IS NULL OR (absorptivity > 0 AND absorptivity <= 1)),
    ADD COLUMN resistance_temp_coefficient_per_c numeric CHECK (resistance_temp_coefficient_per_c IS NULL OR resistance_temp_coefficient_per_c > 0);

COMMENT ON COLUMN metadata.material_specs.max_operating_temp_c IS 'Maximum continuous conductor operating temperature in Celsius (e.g. 75 for standard ACSR) -- the thermal equilibrium point an IEEE 738 ampacity calc solves for.';
COMMENT ON COLUMN metadata.material_specs.emissivity IS 'Surface emissivity (0-1, unitless) -- radiative heat loss coefficient in an IEEE 738 calc. New/bright conductor is lower, weathered/oxidized is higher (typically 0.23-0.91).';
COMMENT ON COLUMN metadata.material_specs.absorptivity IS 'Solar absorptivity (0-1, unitless) -- fraction of incident solar heat gain absorbed in an IEEE 738 calc. Usually tracks emissivity for a given surface condition.';
COMMENT ON COLUMN metadata.material_specs.resistance_temp_coefficient_per_c IS 'Fractional change in resistance_ohm_per_km per degree Celsius -- lets an ampacity calc correct the catalog''s rated-temperature resistance to the conductor''s actual operating temperature rather than treating resistance as constant.';
