-- 0161_work_orders_source.sql
-- Purpose: record where a work order CAME FROM, so a board can be filtered by
-- provenance ("every job raised from the data-quality audit", "every vegetation
-- trim") and an order's origin is legible. Until now a work order's origin lived
-- only in its free-text description; this makes it a first-class, queryable field.
--
-- Nullable, no CHECK: a manually-created order may name no source, and the set of
-- origins is open (validation, vegetation, inspection, outage, …) and grows as new
-- modules raise work -- an enum CHECK would need a migration per new origin, which
-- a plain text tag the writers set avoids.

ALTER TABLE wam.work_orders
    ADD COLUMN IF NOT EXISTS source text;

COMMENT ON COLUMN wam.work_orders.source IS 'Where the order originated: validation (a data-quality audit fix), vegetation (a clearance trim), inspection, outage, etc., or NULL for a plain manual order. A free-text tag the raising code sets; used to filter the board by provenance.';
