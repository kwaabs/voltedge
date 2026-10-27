-- 0169_incident_territory.sql
-- Purpose: give each incident a PRIMARY territory so the outage board can be
-- scoped and rolled up by region/district -- without inverting the model. An
-- outage's impact is still traced electrically (device -> cone); territory_id is
-- just the DOMINANT territory of that affected set (the deepest territory holding
-- the most affected customers), denormalised onto the incident so the board can
-- filter by territory cheaply instead of re-tracing every row.
--
-- A bare ref (uuid, no FK) matching the rest of oms's loose coupling -- a
-- territory can be reorganised without cascading here. Nullable: a no-device /
-- zero-supply incident, or one whose supplies carry no territory_id, has none.
-- Board filtering by a region resolves that region's whole subtree (in Go), so a
-- leaf-tagged incident still shows under its ancestors. Note: an incident is
-- tagged to its ONE dominant territory, so a cone spanning two regions is counted
-- under the busier one -- a per-incident territory bridge table is a later step if
-- multi-territory membership is needed.

ALTER TABLE oms.incidents
    ADD COLUMN IF NOT EXISTS territory_id uuid;

CREATE INDEX IF NOT EXISTS oms_incidents_territory_idx ON oms.incidents (territory_id);

-- Backfill from the FROZEN interruptions (confirmed incidents): the dominant
-- territory = the one with the most interrupted customers. A still-predicted
-- incident (no frozen rows) is left null -- it gets stamped on its next raise /
-- re-prediction, and the live board can fall back to its affected trace. Each
-- interruption's supply resolves to a territory via gis.service_point / gis.meter.
WITH supply_terr AS (
    SELECT ci.incident_id,
           COALESCE(sp.territory_id, m.territory_id) AS territory_id,
           ci.customers
      FROM oms.customer_interruptions ci
      LEFT JOIN gis.service_point sp ON ci.feature_class = 'SERVICE_POINT' AND sp.id = ci.service_point_id
      LEFT JOIN gis.meter         m  ON ci.feature_class = 'METER'         AND m.id = ci.service_point_id
),
ranked AS (
    SELECT incident_id,
           territory_id,
           ROW_NUMBER() OVER (
               PARTITION BY incident_id
               ORDER BY SUM(customers) DESC, territory_id
           ) AS rn
      FROM supply_terr
     WHERE territory_id IS NOT NULL
     GROUP BY incident_id, territory_id
)
UPDATE oms.incidents i
   SET territory_id = r.territory_id
  FROM ranked r
 WHERE r.incident_id = i.id
   AND r.rn = 1
   AND i.territory_id IS NULL;

COMMENT ON COLUMN oms.incidents.territory_id IS 'Dominant territory of the affected set (0169): the deepest territory with the most affected customers. Denormalised for board scoping/rollup; impact is still traced electrically. Bare ref, nullable.';
