-- 0172_create_device_telemetry.sql
-- Purpose: live-state / telemetry ingestion (blocker #1). A LATEST-VALUE store of
-- device telemetry, one row per device -- deliberately NOT the durable, signed,
-- retried business-event webhook engine. Telemetry is a high-rate, latest-value,
-- loss-tolerant stream (a SCADA poll every few seconds): a new sample instantly
-- supersedes the old, a re-delivery is harmless, and a lost sample is dropped
-- (the next is seconds away). So this is a thin upsert table keyed by the device,
-- fed by a dedicated endpoint (api/internal/telemetry), reconciled against the
-- model's own device_state. The SCADA/DMS gateway that speaks DNP3 / IEC 61850 /
-- ICCP into the endpoint is external -- as it is for every OMS.
--
-- state is the reported switch position (OPEN/CLOSED/...), nullable (a feed may
-- carry measurements only). measurements is an optional jsonb bag (voltage,
-- current, ...). quality is the telemetry quality flag. measured_at is when the
-- field measured it (NOT when we ingested it) -- an out-of-order older sample is
-- rejected by the upsert so the row always holds the newest reading.

CREATE TABLE IF NOT EXISTS ops.device_telemetry (
    feature_class_code text NOT NULL,
    feature_id         uuid NOT NULL,
    state              text,
    measurements       jsonb,
    quality            text NOT NULL DEFAULT 'good'
                       CHECK (quality IN ('good', 'suspect', 'stale', 'bad')),
    source             text,
    measured_at        timestamptz NOT NULL,
    ingested_at        timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (feature_class_code, feature_id)
);

COMMENT ON TABLE ops.device_telemetry IS 'Latest-value device telemetry (0172): one row per device, upserted by the telemetry ingestion endpoint (newest measured_at wins), reconciled against the model device_state. Not the durable webhook event log.';

-- The reconciliation / health reads scan by measured_at (staleness) and state.
CREATE INDEX IF NOT EXISTS ops_device_telemetry_measured_at_idx ON ops.device_telemetry (measured_at);

-- --- permissions ----------------------------------------------------------
-- telemetry:ingest gates the machine ingestion endpoint (a SCADA/DMS gateway
-- service account holds it); telemetry:read gates the live-state + reconciliation
-- views. Same catalog-as-data pattern as operations (0148) / oms (0088).
INSERT INTO iam.permissions (key, description) VALUES
    ('telemetry:read',   'View live device telemetry, staleness and the model-vs-telemetry reconciliation.'),
    ('telemetry:ingest', 'Feed device telemetry (switch state / measurements) into the live-state store -- the SCADA/DMS gateway service account.')
ON CONFLICT (key) DO NOTHING;

-- read: everyone who sees the map (viewer/analyst/editor + admins + field).
INSERT INTO iam.role_permissions (role_id, permission_key)
SELECT r.id, 'telemetry:read'
FROM iam.roles r
WHERE r.key IN ('viewer', 'analyst', 'editor', 'admin', 'break-glass-admin', 'field-technician')
ON CONFLICT DO NOTHING;

-- ingest: editors + admins (a deployment assigns the SCADA service account a role
-- carrying this -- editor, or a custom integration role).
INSERT INTO iam.role_permissions (role_id, permission_key)
SELECT r.id, 'telemetry:ingest'
FROM iam.roles r
WHERE r.key IN ('editor', 'admin', 'break-glass-admin')
ON CONFLICT DO NOTHING;
