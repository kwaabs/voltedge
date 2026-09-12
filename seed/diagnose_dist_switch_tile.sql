-- seed/diagnose_dist_switch_tile.sql
-- Read-only diagnostic for the "GET /tiles/dist_switch 500" report. Prints,
-- via RAISE NOTICE, the exact live state Martin sees so we can tell WHY the
-- source still 500s after the orphan-function cleanup + Martin restart found
-- nothing to drop. Changes nothing. Run:
--   task diagnose-dist-switch-tile
-- (or) psql ... -f seed/diagnose_dist_switch_tile.sql

DO $$
DECLARE
    has_class    boolean;
    has_table    boolean;
    has_func     boolean;
    func_sig     text;
    n_rows       bigint;
    call_err     text;
BEGIN
    -- 1. Metadata class still registered?
    SELECT EXISTS (SELECT 1 FROM metadata.feature_classes WHERE code = 'DIST_SWITCH')
      INTO has_class;
    RAISE NOTICE '[1] metadata.feature_classes DIST_SWITCH present? %', has_class;

    -- 2. Physical table still there?
    SELECT EXISTS (
        SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
        WHERE n.nspname = 'gis' AND c.relname = 'dist_switch' AND c.relkind = 'r'
    ) INTO has_table;
    RAISE NOTICE '[2] gis.dist_switch TABLE present? %', has_table;

    -- 3. Martin tile FUNCTION still there? (this is what Martin publishes)
    SELECT EXISTS (
        SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
        WHERE n.nspname = 'gis' AND p.proname = 'dist_switch'
    ) INTO has_func;
    RAISE NOTICE '[3] gis.dist_switch FUNCTION present? %', has_func;

    IF has_func THEN
        SELECT string_agg(p.oid::regprocedure::text, ' | ')
          INTO func_sig
          FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
         WHERE n.nspname = 'gis' AND p.proname = 'dist_switch';
        RAISE NOTICE '[3a] function signature(s): %', func_sig;
    END IF;

    -- 4. If the table exists, how many live rows?
    IF has_table THEN
        EXECUTE 'SELECT count(*) FROM gis.dist_switch WHERE deleted_at IS NULL' INTO n_rows;
        RAISE NOTICE '[4] gis.dist_switch live rows (deleted_at IS NULL): %', n_rows;
    END IF;

    -- 5. THE KEY TEST: actually invoke the tile function the way Martin does,
    --    for the failing tile z/x/y = 11/1021/680, and capture the real error.
    IF has_func THEN
        BEGIN
            PERFORM gis.dist_switch(11, 1021, 680);
            RAISE NOTICE '[5] gis.dist_switch(11,1021,680) executed OK (no error) -- the 500 is NOT this function throwing; suspect a stale browser catalog or a different source.';
        EXCEPTION WHEN OTHERS THEN
            GET STACKED DIAGNOSTICS call_err = MESSAGE_TEXT;
            RAISE NOTICE '[5] gis.dist_switch(11,1021,680) FAILED -> SQLSTATE % : %', SQLSTATE, call_err;
            RAISE NOTICE '[5a] ^ THIS is the 500 Martin returns. Fix the cause above.';
        END;
    ELSE
        RAISE NOTICE '[5] no gis.dist_switch function -> Martin would 404, not 500. If the browser still requests it, its catalog cache is stale: hard-reload (Ctrl-Shift-R).';
    END IF;
END $$;
