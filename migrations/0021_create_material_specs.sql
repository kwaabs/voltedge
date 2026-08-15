-- 0021_create_material_specs.sql
-- Purpose: material_specs -- a catalog of real physical/electrical
-- properties (resistance, reactance, ampacity, ...) that a coded
-- domain value can optionally carry, so a field like MV_CONDUCTOR's
-- conductor_spec isn't just a display label (see metadata.domain_values,
-- migrations/0004) but a lookup into real engineering numbers. One row
-- per catalog entry -- material and size are combined into a single
-- entry (e.g. code ACSR_50 = ACSR, 50 mm^2) rather than two separate
-- domains crossed together, matching how conductor tables actually work
-- in practice: resistance/ampacity are properties of the combination,
-- not something that decomposes cleanly across material x size.
--
-- This is the dependency the next phase of electrical analysis (voltage
-- drop, ampacity/loading checks along a traced path) needs: those calcs
-- want real per-unit-length numbers for whatever conductor a feature
-- says it's made of, not an opaque code.

CREATE TABLE metadata.material_specs (
    id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code                  text NOT NULL UNIQUE,
    name                  text NOT NULL,
    category              text NOT NULL DEFAULT 'conductor',
    resistance_ohm_per_km numeric CHECK (resistance_ohm_per_km IS NULL OR resistance_ohm_per_km > 0),
    reactance_ohm_per_km  numeric CHECK (reactance_ohm_per_km IS NULL OR reactance_ohm_per_km > 0),
    ampacity_amps         numeric CHECK (ampacity_amps IS NULL OR ampacity_amps > 0),
    diameter_mm           numeric CHECK (diameter_mm IS NULL OR diameter_mm > 0),
    weight_kg_per_km      numeric CHECK (weight_kg_per_km IS NULL OR weight_kg_per_km > 0),
    created_at            timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE metadata.material_specs IS 'A catalog of physical/electrical properties for one real-world material+size combination (e.g. ACSR_50). Linked to a field''s value via domain_values.material_spec_id, not directly to a feature class -- the domain value a feature already selects (e.g. "ACSR 50mm^2") is what the spec attaches to, so no compiler or CRUD-engine change was needed to introduce this.';
COMMENT ON COLUMN metadata.material_specs.code IS 'Stable machine identifier, e.g. ACSR_50, AAAC_35. Convention bakes size into the code -- one row is one full catalog entry, not a material/size pair to cross.';
COMMENT ON COLUMN metadata.material_specs.category IS 'Free-text grouping (conductor, pole, cable, ...) for admin UI filtering -- not a domain, since a material spec''s own applicable columns can differ by category and there is nothing today (only conductor specs) to enumerate against.';
COMMENT ON COLUMN metadata.material_specs.resistance_ohm_per_km IS 'AC resistance in ohms per kilometer at rated temperature -- the primary input to a voltage-drop calc.';
COMMENT ON COLUMN metadata.material_specs.reactance_ohm_per_km IS 'Inductive reactance in ohms per kilometer -- paired with resistance for a full impedance-based voltage-drop calc.';
COMMENT ON COLUMN metadata.material_specs.ampacity_amps IS 'Continuous current-carrying capacity in amps -- the input to a loading/overload check.';
COMMENT ON COLUMN metadata.material_specs.diameter_mm IS 'Overall conductor diameter in millimeters -- informational, and a future input to sag/clearance checks.';
COMMENT ON COLUMN metadata.material_specs.weight_kg_per_km IS 'Weight per kilometer -- informational, and a future input to sag/tension checks.';

ALTER TABLE metadata.domain_values
    ADD COLUMN material_spec_id uuid REFERENCES metadata.material_specs(id);

COMMENT ON COLUMN metadata.domain_values.material_spec_id IS 'Optional link to metadata.material_specs -- set when this domain value represents a material/size catalog entry (e.g. a CONDUCTOR_SPEC domain''s ACSR_50 value) rather than a plain label. NULL for every domain value that isn''t material-backed.';
