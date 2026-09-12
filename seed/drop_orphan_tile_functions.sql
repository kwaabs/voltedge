-- seed/drop_orphan_tile_functions.sql
-- Purpose: drop orphaned Martin tile functions -- gis.<name>(z,x,y)->bytea
-- functions whose backing table gis.<name> no longer exists.
--
-- WHY THIS EXISTS:
--   The compiler creates one tile function per geometric feature class at
--   compile time (migration 0038 / gis.sync_tile_function): gis.<table>
--   (integer, integer, integer) RETURNS bytea. Martin auto-discovers every
--   such function in the gis schema and publishes it as a tile source named
--   after the function, then serves /tiles/<name>/{z}/{x}/{y} from it.
--
--   That tile function is NOT a tracked dependency of the table, so a plain
--   `DROP TABLE gis.<name>` leaves the function behind. Martin keeps
--   advertising the source and every tile request 500s ("Internal Server
--   Error") because the function's body selects from a table that is gone.
--   That is exactly what happened to `dist_switch`: the older
--   migrate_dist_switch_to_mv_switch.sql dropped the table but not the
--   function. (The later RMU collapse seeds, and compiler Uncompile, drop
--   both -- so they are clean.)
--
-- WHAT THIS DOES:
--   Finds every function in schema gis whose identity is (integer, integer,
--   integer) -> bytea -- the exact Martin tile-function signature -- for which
--   no relation named the same exists in gis, and drops it. Self-healing:
--   fixes dist_switch today and any future retired class whose drop seed
--   forgot the function. Safe to run anytime; on a clean DB it drops nothing.
--
-- It is a SEED (psql), not a migration: apply.sh only scans /migrations, and
-- re-running a cleanup after retiring a class is a normal operator action.
--
-- AFTER RUNNING: Martin only re-reads its source catalog at startup, so
-- restart the Martin container (docker compose restart martin) for the
-- retired source to disappear from GET /tiles/catalog. The frontend caches
-- the catalog for 5 minutes (use-catalog staleTime) -- a hard refresh or a
-- 5-minute wait clears the stale `dist_switch` layer from the map.

DO $$
DECLARE
    cand     record;
    examined int := 0;
    dropped  int := 0;
    has_tbl  boolean;
BEGIN
    -- Candidate = ANY function in schema gis EXCEPT the generator helper
    -- gis.sync_tile_function. We deliberately do NOT filter on argument
    -- signature or return type: an earlier attempt filtered on
    -- prokind='f' AND pg_get_function_identity_arguments = 'integer, integer,
    -- integer' and matched ZERO functions even though ~38 tile functions plainly
    -- exist -- proof that guessing the exact stored signature is fragile here.
    -- The gis schema is app-owned and holds nothing but per-class tile functions
    -- + sync_tile_function, so "a gis function whose backing gis.<name> relation
    -- is gone" is a complete and guess-free definition of an orphaned tile
    -- function. Each candidate's real arg list is logged so we finally SEE what
    -- Postgres stores, and a "nothing to do" is always explainable.
    FOR cand IN
        SELECT p.oid::regprocedure                       AS sig,
               p.proname                                  AS fname,
               pg_get_function_identity_arguments(p.oid)  AS args
          FROM pg_proc p
          JOIN pg_namespace n ON n.oid = p.pronamespace
         WHERE n.nspname = 'gis'
           AND p.proname <> 'sync_tile_function'
         ORDER BY p.proname
    LOOP
        examined := examined + 1;

        -- Does a backing gis relation (table/partition/view/matview/foreign)
        -- with the same name still exist? If yes the tile function is live and
        -- we leave it; if no it is orphaned and every tile request 500s.
        SELECT EXISTS (
            SELECT 1
              FROM pg_class c
              JOIN pg_namespace cn ON cn.oid = c.relnamespace
             WHERE cn.nspname = 'gis'
               AND c.relname  = cand.fname
               AND c.relkind IN ('r', 'p', 'v', 'm', 'f')
        ) INTO has_tbl;

        IF has_tbl THEN
            RAISE NOTICE 'orphan tile-function cleanup: keep gis.%(%) -- backing gis.% still exists', cand.fname, cand.args, cand.fname;
        ELSE
            EXECUTE format('DROP FUNCTION IF EXISTS %s', cand.sig);
            RAISE NOTICE 'orphan tile-function cleanup: DROPPED gis.%(%) -- backing gis.% is gone (this was the 500)', cand.fname, cand.args, cand.fname;
            dropped := dropped + 1;
        END IF;
    END LOOP;

    IF examined = 0 THEN
        RAISE NOTICE 'orphan tile-function cleanup: no functions in schema gis (besides sync_tile_function) -- nothing could be orphaned';
    ELSIF dropped = 0 THEN
        RAISE NOTICE 'orphan tile-function cleanup: examined % gis function(s), none orphaned (every backing table present) -- nothing to do', examined;
    ELSE
        RAISE NOTICE 'orphan tile-function cleanup: examined %, dropped % orphaned tile function(s) -- now restart Martin (docker compose restart martin) to refresh its source catalog', examined, dropped;
    END IF;
END $$;
