-- 0028_add_feature_territory_column.sql
-- Purpose: Phase 11 Slice C -- give every already-compiled feature table
-- that has a geometry the nullable territory_id column the CRUD engine
-- will populate (by spatial assignment) and enforce ("at least a
-- district") in a later slice. This only lays the column; nothing writes
-- or requires it yet.
--
-- Physical feature tables are compiler-generated and dynamically named
-- (gis.pole, gis.mv_conductor, ...), so they can't be named statically --
-- loop the metadata catalog and ALTER each, exactly like migration 0026
-- did for created_by/updated_by. Restricted to geometry-bearing classes
-- (geometry_type != 'none'): a geometry-less feature class has no location
-- to place in a territory, so it never gets the column (matching
-- compiler.buildCreateTable, which only emits territory_id for geometric
-- classes). New tables compiled after this point already carry the column;
-- the compiler's recompile path backfills it the same way it does
-- created_by, so ADD COLUMN IF NOT EXISTS keeps this idempotent and safe
-- to co-exist with both.
--
-- Plain uuid, deliberately NOT a FK to gis.territories: the same
-- no-enforced-join convention as created_by/updated_by. Spatial assignment
-- and the "at least a district" invariant are enforced in the CRUD write
-- path (api/internal/crud), not by the database.

DO $$
DECLARE
    t text;
BEGIN
    FOR t IN
        SELECT physical_table
        FROM metadata.feature_classes
        WHERE physical_table IS NOT NULL
          AND geometry_type != 'none'
    LOOP
        EXECUTE format('ALTER TABLE %s ADD COLUMN IF NOT EXISTS territory_id uuid', t);
    END LOOP;
END $$;
