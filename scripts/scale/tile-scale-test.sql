-- scripts/scale/tile-scale-test.sql
--
-- Bet 3 (scale), slice 1: measure the TILE path at scale, since the deployment
-- & scaling guide flags uncached per-request ST_AsMVT as the #1 bottleneck.
--
-- The decisive question this answers: does tile rendering cost scale with
-- FEATURES-PER-TILE (good -- a GiST index makes it O(log N + k)) or with TOTAL
-- FEATURE COUNT (catastrophic -- a sequential scan, O(N), on every tile)? The
-- real tile query (api/internal/tiles/tiles.go) filters with
--     t.geom && ST_Transform(ST_TileEnvelope(z,x,y), srid)
-- which is index-able by a GiST index on geom. So the whole scale story hinges
-- on whether that index exists and is used.
--
-- How to run, adjusting the feature count for each scale point. Via the
-- Taskfile (runs through the gitbash sidecar; the repo is mounted at /workspace):
--     task tile-scale-test NUM=10000
--     task tile-scale-test NUM=100000
--     task tile-scale-test NUM=500000
-- GEOMTYPE=line generates conductor-like polylines (the heavier ST_AsMVTGeom
-- case) instead of points:
--     task tile-scale-test NUM=100000 GEOMTYPE=line
-- Or directly through the sidecar:
--     docker compose exec -T gitbash psql "<container DSN>" \
--       -v num=100000 -f /workspace/scripts/scale/tile-scale-test.sql
-- (defaults below apply if -v isn't given). It uses an ISOLATED scaletest
-- schema and DROPS it at the end, so it never touches real gis.* data, the
-- feature registry, or the map.
--
-- What to read:
--   * Section A: do your REAL gis.* tables have a GiST index on geom? If any are
--     missing, tiles for that class are O(N) -- fix before anything else.
--   * Section D vs E: the SAME tile query WITHOUT then WITH the GiST index.
--     Without -> "Seq Scan", time grows with :num. With -> "Index Scan", time
--     ~flat across :num (scales with features-in-the-tile). The gap between the
--     two, and how D grows as you raise :num, is the headline measurement.

\set ON_ERROR_STOP on
\timing on

-- Defaults (override with -v num=... -v zoom=...). psql leaves :'num' unset if
-- not passed, so guard with a coalesce via \set only when undefined isn't given;
-- simplest is to require -v, but provide sane fallbacks here:
\if :{?num}
\else
  \set num 100000
\endif
\if :{?zoom}
\else
  \set zoom 12
\endif
\if :{?geomtype}
\else
  \set geomtype point
\endif
\set lon0 0.0
\set lat0 5.8

-- Derive a 1/0 flag psql's \if can branch on (it accepts 1/0, not Postgres t/f).
SELECT (:'geomtype' = 'line')::int AS is_line \gset

\echo '================================================================'
\echo 'TILE SCALE TEST  --  features =' :num '  geom =' :geomtype '  zoom =' :zoom
\echo '================================================================'

-- ----------------------------------------------------------------------------
-- A. Reality check: GiST coverage on the ACTUAL gis.* tile sources.
--    Every compiled gis.<class> should have a GiST index on geom. Any class
--    listed as MISSING renders tiles with a sequential scan -> O(total rows).
-- ----------------------------------------------------------------------------
\echo '--- A. GiST indexes on real gis.* geom columns (should cover every geometric class) ---'
SELECT n.nspname || '.' || c.relname AS gis_table,
       COALESCE(string_agg(i.relname, ', ') FILTER (WHERE am.amname = 'gist'), '(NONE -- O(N) tiles!)') AS gist_indexes
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
LEFT JOIN pg_index x ON x.indrelid = c.oid
LEFT JOIN pg_class i ON i.oid = x.indexrelid
LEFT JOIN pg_am am ON am.oid = i.relam
WHERE n.nspname = 'gis' AND c.relkind = 'r'
  AND EXISTS (SELECT 1 FROM information_schema.columns
              WHERE table_schema = 'gis' AND table_name = c.relname AND column_name = 'geom')
GROUP BY 1
ORDER BY 1;

-- ----------------------------------------------------------------------------
-- B. Build an isolated synthetic tile source mimicking a gis.* table's
--    tile-relevant columns (id, status, geom, deleted_at, territory_id).
-- ----------------------------------------------------------------------------
CREATE SCHEMA IF NOT EXISTS scaletest;
DROP TABLE IF EXISTS scaletest.features;
CREATE TABLE scaletest.features (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  status       text NOT NULL DEFAULT 'published',
  name         text,
  territory_id uuid,
  deleted_at   timestamptz,
  geom         geometry(Geometry, 4326) NOT NULL  -- Point or LineString, per :geomtype
);

-- ----------------------------------------------------------------------------
-- C. Generate :num points spread over a ~0.6 deg box centered at (lon0, lat0).
--    (The absolute region is arbitrary for an isolated table; what matters is
--    the DENSITY -- how many points land in the test tile.)
-- ----------------------------------------------------------------------------
\echo '--- C. Generating' :num 'synthetic' :geomtype 'features ---'
\if :is_line
-- Conductor-like 4-vertex polylines (~100 m with a couple of bends), so
-- ST_AsMVTGeom does real line-clipping work per feature -- the heavier case.
INSERT INTO scaletest.features (geom)
SELECT ST_SetSRID(ST_MakeLine(ARRAY[
         ST_MakePoint(lng,                     lat),
         ST_MakePoint(lng + 0.0003 * random(), lat + 0.0003 * random()),
         ST_MakePoint(lng + 0.0006 * random(), lat + 0.0006 * random()),
         ST_MakePoint(lng + 0.0009 * random(), lat + 0.0009 * random())
       ]), 4326)
FROM (
  SELECT (:lon0) - 0.3 + random() * 0.6 AS lng,
         (:lat0) - 0.3 + random() * 0.6 AS lat
  FROM generate_series(1, :num)
) s;
\else
INSERT INTO scaletest.features (geom)
SELECT ST_SetSRID(
         ST_MakePoint((:lon0) - 0.3 + random() * 0.6,
                      (:lat0) - 0.3 + random() * 0.6),
         4326)
FROM generate_series(1, :num);
\endif

-- Pick the Web-Mercator XYZ tile covering the box center at :zoom, so the test
-- tile is guaranteed to contain features. Captured into :tx / :ty for the query.
SELECT floor(((:lon0) + 180) / 360 * (2 ^ (:zoom)))::int AS tx,
       floor((1 - ln(tan(radians(:lat0)) + 1 / cos(radians(:lat0))) / pi()) / 2 * (2 ^ (:zoom)))::int AS ty
\gset
\echo '--- Test tile z/x/y =' :zoom '/' :tx '/' :ty ---

SELECT count(*) AS features_in_test_tile
FROM scaletest.features t
WHERE t.deleted_at IS NULL
  AND t.geom && ST_Transform(ST_TileEnvelope(:zoom, :tx, :ty), 4326);

-- ----------------------------------------------------------------------------
-- D. BASELINE: the real tile query, NO index. Expect a Seq Scan; note the time.
--    Re-run the whole script at larger :num and watch this grow ~linearly.
-- ----------------------------------------------------------------------------
\echo '--- D. Tile render WITHOUT a GiST index (expect Seq Scan; time grows with :num) ---'
EXPLAIN (ANALYZE, BUFFERS, TIMING)
SELECT ST_AsMVT(tile, 'scaletest', 4096, 'geom')
FROM (
    SELECT ST_AsMVTGeom(ST_Transform(t.geom, 3857),
                        ST_TileEnvelope(:zoom, :tx, :ty),
                        4096, 64, true) AS geom,
           t.id, t.status, t.name, t.territory_id
    FROM scaletest.features t
    WHERE t.deleted_at IS NULL
      AND t.geom && ST_Transform(ST_TileEnvelope(:zoom, :tx, :ty), 4326)
) AS tile
WHERE tile.geom IS NOT NULL;

-- ----------------------------------------------------------------------------
-- E. WITH a GiST index (what the compiler should emit for a real gis.* table).
--    Expect an Index Scan; time should be ~flat as :num grows (it scales with
--    features-in-the-tile, not total rows).
-- ----------------------------------------------------------------------------
\echo '--- E. Tile render WITH a GiST index (expect Index Scan; time ~flat across :num) ---'
CREATE INDEX scaletest_features_geom_gist ON scaletest.features USING gist (geom);
ANALYZE scaletest.features;

EXPLAIN (ANALYZE, BUFFERS, TIMING)
SELECT ST_AsMVT(tile, 'scaletest', 4096, 'geom')
FROM (
    SELECT ST_AsMVTGeom(ST_Transform(t.geom, 3857),
                        ST_TileEnvelope(:zoom, :tx, :ty),
                        4096, 64, true) AS geom,
           t.id, t.status, t.name, t.territory_id
    FROM scaletest.features t
    WHERE t.deleted_at IS NULL
      AND t.geom && ST_Transform(ST_TileEnvelope(:zoom, :tx, :ty), 4326)
) AS tile
WHERE tile.geom IS NOT NULL;

-- ----------------------------------------------------------------------------
-- F. Clean up -- leave no trace in the database.
-- ----------------------------------------------------------------------------
DROP SCHEMA scaletest CASCADE;
\echo '--- Done. Re-run with -v num=10000 / 100000 / 500000 and compare D (no index) vs E (index). ---'
