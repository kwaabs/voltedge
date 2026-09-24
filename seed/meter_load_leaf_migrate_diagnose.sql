-- seed/meter_load_leaf_migrate_diagnose.sql
-- READ-ONLY. Explains WHY each surviving METER<->SERVICE_POINT link was NOT
-- migrated by meter_load_leaf_migrate.sql, so you can resolve them by hand and
-- re-run the migrate to finish retiring METER.T2 + the rule.
-- Apply: docker compose exec -T gitbash psql "$URL" -f /workspace/seed/meter_load_leaf_migrate_diagnose.sql

WITH mcls AS (SELECT id FROM metadata.feature_classes WHERE code = 'METER'),
     scls AS (SELECT id FROM metadata.feature_classes WHERE code = 'SERVICE_POINT'),
     links AS (
        SELECT c.from_feature_id AS meter_id, c.to_feature_id AS sp_id
        FROM runtime.feature_connectivity c
        WHERE c.from_feature_class_id = (SELECT id FROM mcls)
          AND c.to_feature_class_id   = (SELECT id FROM scls)
        UNION
        SELECT c.to_feature_id, c.from_feature_id
        FROM runtime.feature_connectivity c
        WHERE c.to_feature_class_id   = (SELECT id FROM mcls)
          AND c.from_feature_class_id = (SELECT id FROM scls)
     ),
     sp_deg AS (SELECT sp_id, count(DISTINCT meter_id) AS n FROM links GROUP BY sp_id),
     m_deg  AS (SELECT meter_id, count(DISTINCT sp_id) AS n FROM links GROUP BY meter_id)
SELECT
    m.name  AS meter,
    sp.name AS service_point,
    sp_deg.n AS meters_on_this_sp,
    m_deg.n  AS sps_on_this_meter,
    m.meter_role,
    -- The first matching reason is the blocker to fix.
    CASE
        WHEN sp.id IS NULL OR sp.deleted_at IS NOT NULL THEN 'sp already retired (should have migrated?)'
        WHEN m.meter_role IS NOT NULL                    THEN 'meter already carries load (role set)'
        WHEN m_deg.n > 1                                 THEN 'meter feeds >1 SP (check-meter/shared)'
        WHEN sp_deg.n > 1                                THEN 'SP fed by >1 meter (multi-meter)'
        WHEN EXISTS (SELECT 1 FROM oms.customer_interruptions ci
                     WHERE ci.service_point_id = sp.id AND ci.restored_at IS NULL)
                                                         THEN 'SP has unrestored interruption'
        WHEN EXISTS (SELECT 1 FROM oms.trouble_calls tc
                     WHERE tc.service_point_id = sp.id AND tc.status IN ('open','associated'))
                                                         THEN 'SP has active trouble call'
        ELSE 'unknown -- inspect manually'
    END AS skip_reason
FROM links l
JOIN gis.meter m          ON m.id  = l.meter_id
LEFT JOIN gis.service_point sp ON sp.id = l.sp_id
JOIN sp_deg ON sp_deg.sp_id = l.sp_id
JOIN m_deg  ON m_deg.meter_id = l.meter_id
ORDER BY m.name, sp.name;
