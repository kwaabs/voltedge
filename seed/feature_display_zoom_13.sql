-- seed/feature_display_zoom_13.sql
-- Purpose: set every MAP-drawable feature class to display only at zoom 13 and
-- above -- i.e. stamp min_display_zoom = 13 on all geographical feature classes
-- (migration 0090's per-class display-zoom band). Below zoom 13 those features
-- draw nothing on the geographic map (glyph AND label); at 13+ they appear as
-- normal. A blanket declutter so a zoomed-out map isn't a wall of overlapping
-- symbols.
--
-- SCOPE = "geographical capabilities": a feature class draws on the map only if
-- it has a real geometry column -- geometry_type in (point, line, polygon).
-- Classes with geometry_type = 'none' (relays, panels, batteries, ... -- the
-- inventoried-but-unplaced assets) have no map tile at all, so a display-zoom
-- band is meaningless for them; they're excluded. Draft classes (not yet
-- compiled, no physical table/tile) are stamped too -- harmless, and it means a
-- class picks up the 13+ floor as soon as it's compiled rather than needing a
-- second pass.
--
-- It is a SEED (psql), NOT a migration: apply.sh only scans /migrations, and
-- re-running this to re-stamp the floor after adding new classes is a normal
-- operator action, not a one-time schema change. Pure metadata + map-render:
-- no recompile, the map style function reads min_display_zoom live on next load.
--
-- NON-DESTRUCTIVE / SAFE:
--   * Only touches classes whose min_display_zoom differs from 13, so a re-run
--     is a clean no-op and doesn't churn rows.
--   * Skips any class that already has a max_display_zoom BELOW 13 -- stamping
--     min=13 there would make min > max and trip 0090's CHECK constraint. Those
--     (deliberately-capped) classes are left alone and reported.
--   * Leaves geometry_type='none' classes untouched (no map presence).
--
-- Idempotent. Requires migration 0090 (min_display_zoom column) via `task
-- migrate`; fails loudly with the fix if it's missing.

DO $$
DECLARE
    stamped  int;
    skipped  int;
BEGIN
    -- Guard: migration 0090 must have added the column.
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
         WHERE table_schema = 'metadata'
           AND table_name   = 'feature_classes'
           AND column_name  = 'min_display_zoom'
    ) THEN
        RAISE EXCEPTION 'metadata.feature_classes.min_display_zoom is missing -- run migration 0090 first (task migrate)';
    END IF;

    -- Classes we CAN'T stamp because they carry a max below 13 (would violate
    -- the min<=max CHECK). Reported, then left as-is.
    SELECT count(*) INTO skipped
      FROM metadata.feature_classes
     WHERE geometry_type IN ('point', 'line', 'polygon')
       AND max_display_zoom IS NOT NULL
       AND max_display_zoom < 13;

    IF skipped > 0 THEN
        RAISE NOTICE 'display-zoom seed: % geographical class(es) already capped below zoom 13 (max_display_zoom < 13) -- left unchanged to satisfy the min<=max band rule', skipped;
    END IF;

    -- Stamp the 13+ floor on every geographical class whose floor isn't already
    -- 13, excluding the capped-below-13 classes above.
    UPDATE metadata.feature_classes
       SET min_display_zoom = 13
     WHERE geometry_type IN ('point', 'line', 'polygon')
       AND (min_display_zoom IS DISTINCT FROM 13)
       AND (max_display_zoom IS NULL OR max_display_zoom >= 13);

    GET DIAGNOSTICS stamped = ROW_COUNT;
    RAISE NOTICE 'display-zoom seed: set min_display_zoom = 13 on % geographical feature class(es)', stamped;
END $$;
