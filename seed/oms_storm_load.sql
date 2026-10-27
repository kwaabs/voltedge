-- oms_storm_load.sql
-- Storm-scale soak seed (blocker #3): bulk-insert open trouble calls jittered
-- around random real service points, so trouble-spot clustering, Area Impact and
-- the outage board can be exercised at a storm's call volume -- the one time an
-- OMS must not fall over.
--
-- The calls reference real service points (so they resolve to real territories)
-- and are scattered ~±150 m around them, so single-linkage clustering forms a
-- realistic mix of hotspots and singletons. Every call is tagged
-- caller_ref = 'STORM-TEST' so the whole lot is trivial to remove afterwards:
--
--     DELETE FROM oms.trouble_calls WHERE caller_ref = 'STORM-TEST';
--     (or: task clean-oms-storm)
--
-- Adjust the generate_series count below for the volume you want (2000 default).

INSERT INTO oms.trouble_calls (service_point_id, call_type, status, caller_ref, location, reported_at)
SELECT sp.id,
       (ARRAY['no_supply', 'partial', 'flicker'])[1 + floor(random() * 3)]::text,
       'open',
       'STORM-TEST',
       ST_Translate(sp.geom, (random() - 0.5) * 0.0015, (random() - 0.5) * 0.0015),
       now() - (random() * interval '2 hours')
FROM generate_series(1, 2000) AS g
CROSS JOIN LATERAL (
    SELECT id, geom
    FROM gis.service_point
    WHERE deleted_at IS NULL AND geom IS NOT NULL
    ORDER BY random()
    LIMIT 1
) AS sp;
