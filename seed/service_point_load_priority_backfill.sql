-- seed/service_point_load_priority_backfill.sql
-- Phase 62 (service / consumption traces), Slice 1 -- the backfill.
--
-- Run this AFTER seed/service_point_load_priority.sql AND after recompiling
-- SERVICE_POINT (the `load_priority` column has to physically exist first):
--   task seed-service-point-load-priority
--   task compile CODE=SERVICE_POINT
--   task seed-service-point-load-priority-backfill
--
-- ALTER TABLE ADD COLUMN left load_priority NULL on every existing row. This
-- maps the old boolean world onto the new graded axis so nothing is stranded at
-- NULL, and preserves the meaning every existing is_critical flag carried:
--   * is_critical = true   -> P2 (Essential)  -- the old "critical" == rank <= P2
--   * everything else       -> P4 (Standard)   -- the honest default
--
-- P2 (not P1) is the faithful lift: the old flag meant "critical facility", and
-- critical is DERIVED as rank <= P2. Nobody was distinguished as life-support
-- under the boolean, so promoting them all to P1 would invent a distinction that
-- was never recorded; P2 keeps every previously-critical point critical without
-- over-claiming. Operators re-grade the genuine P1 life-support points by hand.
--
-- Guarded + idempotent: only touches rows still NULL, and only runs when both
-- the load_priority AND is_critical columns physically exist on gis.service_point
-- (a fresh install that never seeded is_critical simply gets every row -> P4).
-- physical_table is already schema-qualified (e.g. gis.service_point).

DO $$
DECLARE
    tbl text;
    has_is_critical boolean;
BEGIN
    SELECT fc.physical_table INTO tbl
    FROM metadata.feature_classes fc
    WHERE fc.code = 'SERVICE_POINT'
      AND fc.physical_table IS NOT NULL
      AND EXISTS (
          SELECT 1 FROM metadata.fields f
          WHERE f.feature_class_id = fc.id
            AND f.name = 'load_priority'
            AND f.status = 'active'
      );

    IF tbl IS NULL THEN
        RAISE NOTICE 'SERVICE_POINT not compiled with load_priority yet -- skipping backfill';
        RETURN;
    END IF;

    -- Does the legacy is_critical column physically exist on the table?
    SELECT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema || '.' || table_name = tbl
          AND column_name = 'is_critical'
    ) INTO has_is_critical;

    IF has_is_critical THEN
        EXECUTE format(
            'UPDATE %s SET load_priority = ''P2'' '
            'WHERE load_priority IS NULL AND is_critical IS TRUE',
            tbl
        );
    END IF;

    -- Everyone else (and everyone if is_critical was never seeded) -> Standard.
    EXECUTE format(
        'UPDATE %s SET load_priority = ''P4'' WHERE load_priority IS NULL',
        tbl
    );
END $$;
