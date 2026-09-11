-- 0060_add_material_cost.sql
-- Purpose: two nullable cost columns on metadata.material_specs
-- (migrations/0021) so the catalog can carry a price alongside the
-- physical/electrical numbers it already holds. Both are optional, same
-- "author now, fill in later" discipline every other numeric column on
-- this table follows -- a spec with no cost simply omits it.
--
--   * cost_per_km  -- price of the material per kilometre. Natural for the
--     line materials this table actually holds today (conductor/cable):
--     it multiplies by a run's length for a length-based estimate, the
--     same way resistance_ohm_per_km already scales by length.
--   * unit_cost    -- flat price per item/each. There for a material that
--     is priced per piece rather than per length (a future pole/fitting
--     catalogued here), so the two cost bases don't have to fight over
--     one column.
--
-- Bare numeric, no currency column: one house currency is assumed, matching
-- how every other numeric column here is a bare figure. If mixed-currency
-- pricing is ever needed, that's a later migration adding a currency code,
-- not a reinterpretation of these numbers.
--
-- Nothing in the app consumes these yet -- this is catalog data an admin
-- edits (like weight_kg_per_km), ready for a future BOM/estimate calc to
-- read. No compiler or CRUD-engine change: a domain value still just
-- carries an id pointer to whichever material_specs row is current.

ALTER TABLE metadata.material_specs
    ADD COLUMN cost_per_km numeric CHECK (cost_per_km IS NULL OR cost_per_km >= 0),
    ADD COLUMN unit_cost   numeric CHECK (unit_cost   IS NULL OR unit_cost   >= 0);

COMMENT ON COLUMN metadata.material_specs.cost_per_km IS 'Material cost per kilometre, bare numeric in the house currency -- pairs with the other per-km columns for a length-based estimate. NULL when not priced per length.';
COMMENT ON COLUMN metadata.material_specs.unit_cost IS 'Flat material cost per item/each, bare numeric in the house currency -- for a material priced per piece rather than per length. NULL when not priced per unit.';
