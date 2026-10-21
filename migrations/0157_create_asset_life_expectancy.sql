-- 0157_create_asset_life_expectancy.sql
-- Purpose: the expected-useful-life catalog behind the asset-lifecycle aging
-- report (api/internal/assetlife). One row declares how many years a class (or a
-- specific subtype of it) is expected to last in service -- the report joins it
-- to each feature's in-service age (commissioned_at, or created_at as a fallback)
-- to derive remaining useful life and flag assets that are due or overdue for
-- replacement. Admin-authored, deployment-specific policy (a wood pole and a
-- steel tower age very differently), so it is a catalog an admin tunes rather
-- than schema the compiler reads -- the same shape as ops.validation_rules (0155)
-- and the cost catalog.
--
-- subtype_code NULL = the class default (applies to every subtype); a row with a
-- subtype_code overrides the default for just that subtype. UNIQUE NULLS NOT
-- DISTINCT makes (class, NULL) a single enforceable default row.

CREATE TABLE IF NOT EXISTS ops.asset_life_expectancy (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    feature_class_code text NOT NULL,
    subtype_code       integer,
    expected_years     integer NOT NULL CHECK (expected_years > 0),
    notes              text,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT asset_life_expectancy_uq UNIQUE NULLS NOT DISTINCT (feature_class_code, subtype_code)
);

COMMENT ON TABLE ops.asset_life_expectancy IS 'Expected useful life (years) per feature class / subtype -- the catalog the asset-lifecycle aging report draws on to compute remaining useful life and flag replacement candidates.';
COMMENT ON COLUMN ops.asset_life_expectancy.subtype_code IS 'NULL = the class default (every subtype); a set value overrides the default for that one subtype.';
