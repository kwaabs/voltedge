-- 0038_add_tile_functions.sql
-- Purpose: stop soft-deleted features (deleted_at set) from reappearing on the
-- map. Martin auto-published each compiled feature class's raw gis.<code> table
-- directly, and a raw table source has no WHERE clause, so it served every row
-- including soft-deleted ones. The frontend's deleted-features-store was only a
-- client-side, non-persisted mask -- a reload (or any other user's browser)
-- wiped it and the deleted feature came straight back. It also made the
-- assistant's suggestions endpoint 400 ("feature is deleted") when a stale
-- deleted feature was clicked.
--
-- The real fix: Martin now auto-publishes tile *functions* instead of tables
-- (see infra/martin/config.yaml, auto_publish.tables:false), and each function
-- filters deleted_at IS NULL. gis.sync_tile_function(table, srid) generates one
-- such function per feature class -- gis.<code>(z,x,y) returning MVT bytea --
-- named exactly like the table so its Martin source id is unchanged (the bare,
-- lowercased class code the frontend already keys on). It introspects the
-- table's columns so the MVT carries every attribute as a property just as the
-- raw table source did, minus geom (which becomes the tile geometry) and
-- deleted_at (the row is filtered on it, never a useful property).
--
-- The compiler (api/internal/compiler) calls this same helper on every compile,
-- so the helper is the single source of truth for a tile function's shape and
-- the two never drift; this migration additionally backfills every already-
-- compiled geometric class so the switch to function-only publishing doesn't
-- leave any class un-served.
--
-- The helper's own (text, integer) -> void signature does NOT match Martin's
-- tile-function signature (z,x,y -> bytea), so Martin ignores it; only the
-- generated per-class functions are published.

CREATE OR REPLACE FUNCTION gis.sync_tile_function(p_table text, p_srid integer)
RETURNS void
LANGUAGE plpgsql
AS $fn$
DECLARE
    v_cols text;
BEGIN
    -- Every column becomes an MVT property except the geometry itself (it is
    -- the tile geometry) and deleted_at (the row is filtered on it). Order by
    -- ordinal_position so the generated SQL is stable across recompiles.
    SELECT string_agg(format('t.%I', column_name), ', ' ORDER BY ordinal_position)
      INTO v_cols
    FROM information_schema.columns
    WHERE table_schema = 'gis'
      AND table_name = p_table
      AND column_name NOT IN ('geom', 'deleted_at');

    EXECUTE format($tmpl$
        CREATE OR REPLACE FUNCTION gis.%1$I(z integer, x integer, y integer)
        RETURNS bytea
        LANGUAGE sql
        STABLE PARALLEL SAFE
        AS $body$
            SELECT ST_AsMVT(tile, %1$L, 4096, 'geom')
            FROM (
                SELECT
                    ST_AsMVTGeom(
                        ST_Transform(t.geom, 3857),
                        ST_TileEnvelope(z, x, y),
                        4096, 64, true
                    ) AS geom,
                    %2$s
                FROM gis.%1$I t
                WHERE t.deleted_at IS NULL
                  AND t.geom && ST_Transform(ST_TileEnvelope(z, x, y), %3$s)
            ) AS tile
            WHERE tile.geom IS NOT NULL;
        $body$
    $tmpl$, p_table, v_cols, p_srid);
END;
$fn$;

COMMENT ON FUNCTION gis.sync_tile_function(text, integer) IS
    'Generates/refreshes the gis.<table>(z,x,y) Martin tile function that serves the table as MVT filtered on deleted_at IS NULL. Called by api/internal/compiler on every compile -- keep the two in step.';

-- Backfill: give every already-compiled geometric feature class its tile
-- function now, so function-only auto-publish (config.yaml) doesn't leave any
-- class un-served. New classes compiled after this are covered by the compiler
-- calling the same helper. Restricted to geometry-bearing classes: a geometry-
-- less class has no geom column and is never a map tile source.
DO $mig$
DECLARE
    r record;
BEGIN
    FOR r IN
        SELECT lower(code) AS tbl, srid
        FROM metadata.feature_classes
        WHERE physical_table IS NOT NULL
          AND geometry_type != 'none'
          AND srid IS NOT NULL
    LOOP
        PERFORM gis.sync_tile_function(r.tbl, r.srid);
    END LOOP;
END $mig$;
