-- scripts/scale/trace-scale-test.sql
--
-- Bet 3 (scale), slice 2: the TRACE path at scale. Two concerns, both grounded
-- in api/internal/trace/graph.go:
--   1. neighborsOf (graph.go:574) is ONE indexed query per node, run in a BFS
--      loop -- so a trace does up to maxTraceHops (2000) sequential round-trips.
--   2. The BFS loop stops at maxTraceHops. That cap was a hardcoded 2000 that
--      truncated SILENTLY; it is now a FLAGGED result (traceResponse.truncated,
--      the UI warns) and configurable via TRACE_MAX_HOPS (default 50000). This
--      harness shows how big a real feeder gets, i.e. whether traces approach
--      the cap at all.
--
-- This harness builds an isolated synthetic radial feeder and measures:
--   A. one hop (neighborsOf's query shape -- the index lookup per node),
--   B. the whole downstream trace set-based (recursive CTE): total reachable
--      nodes, feeder depth, and whether it exceeds the 2000-hop cap.
--
-- How to run (defaults to 5000 nodes -- above the 2000 cap, to demonstrate the
-- truncation; vary with NUM):
--     task trace-scale-test NUM=1500     (under the cap)
--     task trace-scale-test NUM=5000     (over the cap -- silent truncation)
--     task trace-scale-test NUM=100000   (a large feeder)
-- Uses an isolated scaletest schema it DROPS at the end -- never touches real
-- gis.* data, runtime.feature_connectivity, or the registry.

\set ON_ERROR_STOP on
\timing on

\if :{?num}
\else
  \set num 5000
\endif

\echo '================================================================'
\echo 'TRACE SCALE TEST  --  feeder nodes =' :num '  (engine cap maxTraceHops = 2000)'
\echo '================================================================'

CREATE SCHEMA IF NOT EXISTS scaletest;
DROP TABLE IF EXISTS scaletest.conn;
CREATE TABLE scaletest.conn (from_id int NOT NULL, to_id int NOT NULL);

-- Build a radial feeder tree rooted at node 1: every node 2..N attaches to an
-- earlier node (30% to the immediately-previous node = a trunk span, else a
-- random earlier node = a lateral tap). Acyclic by construction (parent id <
-- child id), so a DOWNSTREAM trace from node 1 reaches EVERY node -- i.e. a
-- "downstream customers" trace on this feeder must visit all :num nodes.
\echo '--- Generating a' :num '-node radial feeder ---'
INSERT INTO scaletest.conn (from_id, to_id)
SELECT CASE WHEN random() < 0.3 THEN c - 1
            ELSE floor(random() * (c - 1))::int + 1 END,
       c
FROM generate_series(2, :num) AS g(c);

-- Indexes mirroring the real runtime.feature_connectivity (from/to_feature_id).
CREATE INDEX scaletest_conn_from_idx ON scaletest.conn (from_id);
CREATE INDEX scaletest_conn_to_idx   ON scaletest.conn (to_id);
ANALYZE scaletest.conn;

-- A. One hop -- neighborsOf's query shape (index lookup by from_id). This is
--    what the engine runs ONCE PER NODE in its BFS: fast per query, but it runs
--    up to maxTraceHops (2000) of them, sequentially (round-trips), per trace.
\echo '--- A. One hop (neighborsOf): index lookup by from_id ---'
EXPLAIN (ANALYZE, BUFFERS)
SELECT to_id FROM scaletest.conn WHERE from_id = 1;

-- B. The WHOLE downstream trace, set-based (recursive CTE): total nodes reached
--    and feeder depth, plus the time for a ONE-QUERY traversal -- the reference
--    for what the engine's up-to-2000 per-hop round-trips are replacing.
\echo '--- B. Full downstream traversal (recursive CTE) -- plan + time ---'
EXPLAIN (ANALYZE, BUFFERS)
WITH RECURSIVE downstream AS (
    SELECT 1 AS id, 0 AS depth
    UNION ALL
    SELECT c.to_id, d.depth + 1
    FROM downstream d
    JOIN scaletest.conn c ON c.from_id = d.id
)
SELECT count(*) FROM downstream;

\echo '--- B. The numbers: reachable nodes, feeder depth, does it exceed the 2000-hop cap? ---'
WITH RECURSIVE downstream AS (
    SELECT 1 AS id, 0 AS depth
    UNION ALL
    SELECT c.to_id, d.depth + 1
    FROM downstream d
    JOIN scaletest.conn c ON c.from_id = d.id
)
SELECT count(*)                     AS reachable_nodes,
       max(depth)                   AS max_depth,
       (count(*) > 50000)           AS exceeds_default_cap_50000,
       least(count(*), 50000)       AS nodes_engine_would_visit
FROM downstream;

DROP SCHEMA scaletest CASCADE;
\echo '--- Done. reachable_nodes is how many a full downstream trace from the source'
\echo '    visits. If it exceeds the cap (default 50000, TRACE_MAX_HOPS), the engine'
\echo '    now returns a FLAGGED-incomplete result (truncated: true), never silently'
\echo '    partial. max_depth x per-hop (section A) approximates the per-hop latency. ---'
