-- 0127_create_cost_rates_and_currencies.sql
-- Purpose: the catalog backbone for the Bill of Quantities (BoQ) engine -- a
-- priced schedule of the network's materials and assets. Two small metadata
-- tables, no physical gis.* change and no recompile (pure metadata, like
-- material_specs itself).
--
--   metadata.cost_rates -- the unit-rate price book for DISCRETE assets, keyed
--       by (feature_class_code, subtype_code). A discrete asset (transformer,
--       switch, RMU, service point) is priced per item ('each'); a linear asset
--       may carry a class-level 'per_km' fallback here, but its preferred rate is
--       material_specs.cost_per_km (migration 0060) resolved through the material
--       the conductor selects. subtype_code NULL = the rate for every subtype of
--       the class that has no more specific row.
--
--   metadata.currencies -- the presentation currency table. Every stored cost
--       (cost_rates.unit_rate, material_specs.cost_per_km / unit_cost) is in ONE
--       house/base currency; each row here gives another currency and its
--       conversion, so the BoQ can present totals in any of them. per_base = how
--       many units of THIS currency equal one base unit (base row = 1). Exactly
--       one row is the base (is_base), enforced by a partial unique index.
--
-- Rates are intentionally NOT seeded here (the price book is authored per
-- deployment, Slice 2's admin UI) -- only a single base currency is seeded so the
-- BoQ runs immediately and reports every line as "unpriced" until rates exist.
-- Purely additive and idempotent (IF NOT EXISTS / guarded insert), so a re-run is
-- a clean no-op.

CREATE TABLE IF NOT EXISTS metadata.currencies (
    code       text PRIMARY KEY,
    name       text NOT NULL,
    per_base   numeric NOT NULL CHECK (per_base > 0),
    is_base    boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    -- The base currency is the unit costs are stored in, so its own conversion
    -- factor must be exactly 1 -- a non-1 base would silently rescale every price.
    CONSTRAINT currencies_base_is_unit CHECK (NOT is_base OR per_base = 1)
);

-- At most one base currency: a partial unique index over the single TRUE value.
CREATE UNIQUE INDEX IF NOT EXISTS currencies_single_base
    ON metadata.currencies (is_base) WHERE is_base;

COMMENT ON TABLE metadata.currencies IS 'Presentation currencies for the BoQ. All stored costs are in the single base currency (is_base); per_base converts the base to each other currency for display.';
COMMENT ON COLUMN metadata.currencies.per_base IS 'How many units of this currency equal one base unit (e.g. GHS.per_base = 12 when 1 USD base = 12 GHS). The base row is always 1.';
COMMENT ON COLUMN metadata.currencies.is_base IS 'True for the single house currency every stored cost is denominated in. Exactly one row carries it (partial unique index).';

CREATE TABLE IF NOT EXISTS metadata.cost_rates (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    feature_class_code text NOT NULL,
    subtype_code       integer,
    unit_basis         text NOT NULL CHECK (unit_basis IN ('each', 'per_km')),
    unit_rate          numeric NOT NULL CHECK (unit_rate >= 0),
    notes              text,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now()
);

-- One rate per (class, subtype); a NULL subtype (the class-wide default) folds to
-- the -1 sentinel so it can't collide with, and is distinct from, any real subtype
-- code. feature_class_code is a plain code, not an FK (mirroring
-- diagram_stencils.promote_class) -- a rate can be authored ahead of, or outlive,
-- a class without a cascade.
CREATE UNIQUE INDEX IF NOT EXISTS cost_rates_class_subtype
    ON metadata.cost_rates (feature_class_code, COALESCE(subtype_code, -1));

COMMENT ON TABLE metadata.cost_rates IS 'Unit-rate price book, in the base currency, keyed by feature class + subtype. Primarily the per-item (''each'') rate for discrete assets; conductors prefer material_specs.cost_per_km resolved via their material. NULL subtype_code = the class-wide default rate.';
COMMENT ON COLUMN metadata.cost_rates.unit_basis IS 'How unit_rate is multiplied: ''each'' x feature count, or ''per_km'' x geometry length in km.';

-- Seed exactly one base currency so the BoQ endpoint is usable out of the box.
-- USD is a neutral default -- rename it (or set a different base) via the Slice 2
-- admin UI; the code/name are presentation only. Skipped if any currency exists.
INSERT INTO metadata.currencies (code, name, per_base, is_base)
SELECT 'USD', 'US Dollar', 1, true
WHERE NOT EXISTS (SELECT 1 FROM metadata.currencies);
