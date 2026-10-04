-- 0128_create_feature_cost_overrides.sql
-- Purpose: the "(+ overrides)" half of the BoQ's central-catalog-plus-overrides
-- cost model (Slice 2). One optional per-feature unit cost that supersedes the
-- catalog rate the feature would otherwise resolve, for the cases where a single
-- asset's real price differs from the class/material book (a negotiated cable
-- run, a one-off transformer quote). A side table keyed by (class, feature_id),
-- NOT a column on every gis.* table -- so it needs no compiler pass and no
-- per-class migration, exactly like material_specs and cost_rates.
--
--   metadata.feature_cost_overrides -- unit_cost is in the base currency and in
--       the feature's natural unit: per_km for a linear (line-geometry) feature,
--       'each' for a discrete one -- the same basis the catalog rate would use,
--       so an override is a drop-in replacement for that one feature's rate. A
--       feature with an override is priced off it and never reported "unpriced".
--
-- Purely additive and idempotent (IF NOT EXISTS); a re-run is a clean no-op.

CREATE TABLE IF NOT EXISTS metadata.feature_cost_overrides (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    feature_class_code text NOT NULL,
    feature_id         uuid NOT NULL,
    unit_cost          numeric NOT NULL CHECK (unit_cost >= 0),
    notes              text,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now()
);

-- One override per feature. feature_class_code + feature_id is a plain
-- (polymorphic) reference, not an FK -- every gis.* table is a separate physical
-- table, so there is no single parent to cascade from (the same pattern
-- diagram placements and connectivity rows use to point at any class).
CREATE UNIQUE INDEX IF NOT EXISTS feature_cost_overrides_feature
    ON metadata.feature_cost_overrides (feature_class_code, feature_id);

COMMENT ON TABLE metadata.feature_cost_overrides IS 'Optional per-feature unit cost overriding the BoQ catalog rate for a single asset. In the base currency, in the feature''s natural unit (per_km for lines, each for discrete). Keyed polymorphically by (feature_class_code, feature_id).';
COMMENT ON COLUMN metadata.feature_cost_overrides.unit_cost IS 'The per-unit cost to use for this one feature instead of its resolved catalog rate -- per_km for a line, each for a discrete feature.';
