-- 0171_oms_scale_indexes.sql
-- Purpose: storm-scale hardening (blocker #3) — the supporting indexes the OMS
-- hot paths need so they stay fast under a storm's call/incident volume instead
-- of degrading to sequential scans. No behaviour change; indexes only.
--
--   * GiST on trouble_calls.location — ST_ClusterDBSCAN (the trouble-spot
--     clustering, now done in-DB) and any spatial filter use it.
--   * trouble_calls.status — the "open calls" filter (predict, clustering, board)
--     runs constantly.
--   * customer_interruptions.incident_id — the batched area-impact rollup and the
--     re-attribution re-freeze both key on it.
--   * customer_interruptions.interrupted_at — the reliability-index window scan.
--   * incidents.restored_at — the open-incident filter the board + area impact use.

CREATE INDEX IF NOT EXISTS oms_trouble_calls_location_gix
    ON oms.trouble_calls USING gist (location);

CREATE INDEX IF NOT EXISTS oms_trouble_calls_status_idx
    ON oms.trouble_calls (status);

CREATE INDEX IF NOT EXISTS oms_customer_interruptions_incident_idx
    ON oms.customer_interruptions (incident_id);

CREATE INDEX IF NOT EXISTS oms_customer_interruptions_interrupted_at_idx
    ON oms.customer_interruptions (interrupted_at);

CREATE INDEX IF NOT EXISTS oms_incidents_restored_at_idx
    ON oms.incidents (restored_at);
