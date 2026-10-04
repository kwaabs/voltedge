-- seed/cost_catalog_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations; this lives under
-- /seed on purpose, same reasoning as every other seed file. It seeds GENERIC,
-- illustrative cost data so the Bill of Quantities (BoQ) engine produces a real,
-- non-empty priced schedule against the seeded test / substation networks --
-- something to actually test the panel, the PDF/CSV export, and the currency
-- conversion against. Planning-grade round numbers in the base currency (USD),
-- not survey figures; re-price in the admin cost catalog (/admin/cost-catalog)
-- or on each material spec (/admin/materials) for a real deployment.
--
-- WHAT IT SEEDS (all in the base currency):
--   1. The USD base currency, if no currency exists yet (mirrors migration 0127's
--      own seed, so this file stands alone even before 0127's INSERT ran -- the
--      tables must exist, though: apply migrations 0127 + 0128 first).
--   2. metadata.material_specs.cost_per_km on every seeded conductor/cable catalog
--      row -- this is the PREFERRED rate source for LINEAR classes (a conductor's
--      cost follows the cable it selects). Only fills rows where cost_per_km is
--      still NULL, so an admin-entered price is never overwritten.
--   3. metadata.cost_rates 'each' class-default rows for the DISCRETE classes in
--      the test network (DIST_XFMR, MV_SWITCH, METER, SERVICE_POINT, POLE), plus
--      a 'per_km' class-default FALLBACK for MV_CONDUCTOR / LV_CONDUCTOR (used only
--      for a conductor whose material carries no cost_per_km -- the material rate
--      wins when present).
--
-- The discrete rates use a NULL subtype_code (the class-wide default), so they
-- cover every subtype without a more specific row -- the broadest, most generic
-- coverage. feature_class_code is a plain code (not an FK), so a rate for a class
-- that happens not to be seeded is harmless (it just never matches a feature).
--
-- PREREQUISITES: migrations 0127 (currencies + cost_rates) and 0128
-- (feature_cost_overrides) applied; the conductor material catalogs seeded
-- (seed/mv_conductor_specs.sql, seed/lv_conductors_example.sql) for the
-- cost_per_km UPDATEs to have rows to touch. No compile needed -- this is pure
-- metadata, like material_specs itself.
--
-- Fully idempotent: the currency insert is guarded (WHERE NOT EXISTS), the
-- cost_per_km UPDATEs only fill NULLs, and each cost_rates insert is guarded
-- against its (class, class-default) row already existing. A re-run is a clean
-- no-op.
--
-- Apply with psql (NOT the migration runner):
--   task seed-cost-catalog   (or the psql -f invocation directly)
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL has
-- been reviewed statically -- a live psql apply + a BoQ re-price are still owed to
-- call it verified.

-- =====================================================================
-- 1. Base currency (USD), only if none exists yet. Idempotent.
-- =====================================================================
INSERT INTO metadata.currencies (code, name, per_base, is_base)
SELECT 'USD', 'US Dollar', 1, true
WHERE NOT EXISTS (SELECT 1 FROM metadata.currencies);

-- =====================================================================
-- 2. Material per-km costs (the preferred LINEAR rate source). Fill only
--    where cost_per_km is still unset, so an admin price is never clobbered.
-- =====================================================================
UPDATE metadata.material_specs AS ms
SET cost_per_km = v.cost_per_km
FROM (VALUES
    -- MV overhead conductors (seed/mv_conductor_specs.sql)
    ('ACSR_50',       8000),
    ('ACSR_100',     11000),
    ('ACSR_150',     15000),
    ('AAAC_100',     12000),
    -- MV underground cables
    ('MV_XLPE_95',   45000),
    ('MV_XLPE_240',  75000),
    -- LV cables (seed/lv_conductors_example.sql)
    ('ABC_95',        9000),
    ('WAVEFORM_185', 22000),
    ('CONCENTRIC_25',  6000)
) AS v(code, cost_per_km)
WHERE ms.code = v.code
  AND ms.cost_per_km IS NULL;

-- =====================================================================
-- 3. Cost rates. Each guarded against its own class-default row existing,
--    so this is idempotent and never duplicates (the cost_rates_class_subtype
--    unique index would reject a duplicate anyway). subtype_code NULL = the
--    class-wide default that covers every subtype.
-- =====================================================================

-- Discrete assets -- priced per item ('each').
INSERT INTO metadata.cost_rates (feature_class_code, subtype_code, unit_basis, unit_rate, notes)
SELECT v.code, NULL, 'each', v.rate, 'generic seed (cost_catalog_example.sql)'
FROM (VALUES
    ('DIST_XFMR',      8000),
    ('MV_SWITCH',      3500),
    ('METER',           120),
    ('SERVICE_POINT',   250),
    ('POLE',            450)
) AS v(code, rate)
WHERE NOT EXISTS (
    SELECT 1 FROM metadata.cost_rates r
    WHERE r.feature_class_code = v.code AND r.subtype_code IS NULL
);

-- Linear classes -- a per-km FALLBACK used only when the conductor's material
-- carries no cost_per_km (the material rate from step 2 wins when present).
INSERT INTO metadata.cost_rates (feature_class_code, subtype_code, unit_basis, unit_rate, notes)
SELECT v.code, NULL, 'per_km', v.rate, 'generic per-km fallback (cost_catalog_example.sql)'
FROM (VALUES
    ('MV_CONDUCTOR', 10000),
    ('LV_CONDUCTOR',  7000)
) AS v(code, rate)
WHERE NOT EXISTS (
    SELECT 1 FROM metadata.cost_rates r
    WHERE r.feature_class_code = v.code AND r.subtype_code IS NULL
);

DO $$
BEGIN
    RAISE NOTICE 'seed/cost_catalog_example.sql applied: USD base currency (if absent), 9 material cost_per_km fills, 7 cost_rates (idempotent).';
END $$;
