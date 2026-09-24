-- seed/circuit_ref_backfill_audit.sql
-- READ-ONLY audit for the circuit_ref derive-only change. Run BEFORE relying on
-- the derived semantics to see what today's data would need. Writes nothing.
--
-- After circuit_ref becomes derive-only, the ONLY writer is assign-by-trace,
-- which stamps circuit_ref = the circuit name onto every conductor a circuit
-- (via its circuit_id membership) reaches. So a conductor's label is correct iff
-- its circuit_id is set and its circuit has been assigned. These queries surface
-- the rows where that isn't yet true.
--
--   task ... (run manually) psql -f /workspace/seed/circuit_ref_backfill_audit.sql

\echo '== 1. Hand-tagged but UNCATALOGUED (circuit_ref set, circuit_id NULL) =='
\echo '   These labels are now frozen (no assign maintains them) and uneditable.'
\echo '   Fix: create the circuit in gis.circuits, set circuit_id, run assign --'
\echo '   or accept the frozen text.'
SELECT 'MV_CONDUCTOR' AS class, circuit_ref, count(*) AS rows
FROM gis.mv_conductor
WHERE circuit_ref IS NOT NULL AND circuit_ref <> '' AND circuit_id IS NULL
GROUP BY circuit_ref
UNION ALL
SELECT 'LV_CONDUCTOR' AS class, circuit_ref, count(*) AS rows
FROM gis.lv_conductor
WHERE circuit_ref IS NOT NULL AND circuit_ref <> '' AND circuit_id IS NULL
GROUP BY circuit_ref
ORDER BY class, rows DESC;

\echo ''
\echo '== 2. Assigned but UNSTAMPED (circuit_id set, circuit_ref NULL/blank) =='
\echo '   Membership exists but no label yet -- re-run AssignCircuit for the'
\echo '   circuit to stamp the name.'
SELECT 'MV_CONDUCTOR' AS class, count(*) AS rows
FROM gis.mv_conductor
WHERE circuit_id IS NOT NULL AND (circuit_ref IS NULL OR circuit_ref = '')
UNION ALL
SELECT 'LV_CONDUCTOR' AS class, count(*) AS rows
FROM gis.lv_conductor
WHERE circuit_id IS NOT NULL AND (circuit_ref IS NULL OR circuit_ref = '');

\echo ''
\echo '== 3. STALE (circuit_ref set but != its circuit_id circuit name) =='
\echo '   Hand value or a rename drifted from the catalog -- re-assign to fix.'
SELECT 'MV_CONDUCTOR' AS class, c.circuit_ref AS stored_ref, cir.name AS catalog_name, count(*) AS rows
FROM gis.mv_conductor c
JOIN gis.circuits cir ON cir.id = c.circuit_id
WHERE c.circuit_ref IS DISTINCT FROM cir.name
GROUP BY c.circuit_ref, cir.name
UNION ALL
SELECT 'LV_CONDUCTOR' AS class, c.circuit_ref AS stored_ref, cir.name AS catalog_name, count(*) AS rows
FROM gis.lv_conductor c
JOIN gis.circuits cir ON cir.id = c.circuit_id
WHERE c.circuit_ref IS DISTINCT FROM cir.name
GROUP BY c.circuit_ref, cir.name
ORDER BY class, rows DESC;
