-- 0095_territory_id_indexes.sql
-- Purpose: give every already-compiled geometric feature table a btree index on
-- territory_id, so Phase 58's territory-scoped reads are fast.
--
-- The Phase 58 Go MVT tile server (api/internal/tiles) and the CRUD read/write
-- scoping (api/internal/crud/scope.go) both filter features by their territory
-- with `territory_id IN (...)` for a scoped user. Without an index that's a
-- sequential scan of the whole table on every tile and every list. The compiler
-- now emits this index for each geometric class on compile (CREATE INDEX IF NOT
-- EXISTS, api/internal/compiler/compiler.go), which covers newly compiled and
-- recompiled classes; this migration backfills every table that already exists
-- at deploy time so the index is present without waiting for a recompile,
-- exactly mirroring how migration 0038 backfilled the tile functions.
--
-- The GIST index on geom (created at compile time) already serves the tile's
-- bbox `geom &&` filter; this btree serves the territory equality alongside it.
-- Restricted to geometry-bearing classes: a geometry-less class has no geom and
-- never got a territory_id column (Phase 11), so it has nothing to index.
--
-- IF NOT EXISTS makes this safe to run even where the compiler already created
-- the index, and idempotent on re-run.

DO $mig$
DECLARE
    r record;
BEGIN
    FOR r IN
        SELECT lower(code) AS tbl
        FROM metadata.feature_classes
        WHERE physical_table IS NOT NULL
          AND geometry_type != 'none'
    LOOP
        -- Only tables that actually have the territory_id column (every
        -- geometric table should after migration 0028, but guard so a table
        -- somehow missing it doesn't abort the whole backfill).
        IF EXISTS (
            SELECT 1 FROM information_schema.columns
            WHERE table_schema = 'gis'
              AND table_name = r.tbl
              AND column_name = 'territory_id'
        ) THEN
            EXECUTE format(
                'CREATE INDEX IF NOT EXISTS %I ON gis.%I (territory_id)',
                r.tbl || '_territory_id_idx', r.tbl
            );
        END IF;
    END LOOP;
END $mig$;
