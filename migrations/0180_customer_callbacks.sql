-- 0180_customer_callbacks.sql
-- Purpose: CIS/IVR outbound (blocker #5) — the CUSTOMER CALLBACK QUEUE. When an
-- outage is restored, VoltEdge enqueues one callback per affected service point —
-- "notify this premise that supply is back". The external CIS/IVR pulls the queue
-- (GET /oms/callbacks), maps each service point to its own customer record, dials
-- or texts them, and marks each notified. VoltEdge owns WHICH supplies to notify
-- (from the frozen interruption set); the customer model and the actual
-- notification stay in the external CIS.
--
-- account_ref / phone are carried through from the service point's own trouble
-- calls when a caller left them (an IVR/CIS customer_report, 0179); null otherwise —
-- the CIS resolves the rest from service_point_id. reason records why the callback
-- was raised (today: 'restored'); status walks pending → notified (or cancelled).

CREATE TABLE IF NOT EXISTS oms.customer_callbacks (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    incident_id      uuid NOT NULL,                 -- bare ref → oms.incidents
    service_point_id text NOT NULL,                 -- the supply to notify
    account_ref      text,                          -- CIS premise id, if a caller left one
    phone            text,                          -- callback number, if a caller left one
    reason           text NOT NULL DEFAULT 'restored'
                     CHECK (reason IN ('restored', 'etr', 'confirmed')),
    status           text NOT NULL DEFAULT 'pending'
                     CHECK (status IN ('pending', 'notified', 'cancelled')),
    note             text,
    notified_by      uuid,                          -- the actor who marked it notified (bare ref → iam.users)
    created_at       timestamptz NOT NULL DEFAULT now(),
    notified_at      timestamptz
);

COMMENT ON TABLE oms.customer_callbacks IS 'Restoration callback queue (0180, blocker #5): one row per affected supply to notify when an outage is restored. The external CIS/IVR pulls and dials; VoltEdge says who.';

-- One pending callback per (incident, service point) — a re-restore or a retried
-- enqueue never piles up duplicates for the same supply.
CREATE UNIQUE INDEX IF NOT EXISTS customer_callbacks_incident_sp_idx
    ON oms.customer_callbacks (incident_id, service_point_id);
-- The CIS pulls the pending queue; index the common filter.
CREATE INDEX IF NOT EXISTS customer_callbacks_status_idx ON oms.customer_callbacks (status);
