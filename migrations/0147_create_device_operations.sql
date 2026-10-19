-- 0147_create_device_operations.sql
-- The device-OPERATION event log: one append-only row per time a protective /
-- switching device operated on the network -- a relay/breaker trip, a recloser
-- lockout or reclose, a manual open/close, an earth. This is the gap the
-- existing surfaces don't fill: device_state (a feature column) is CURRENT state
-- only; gis.feature_history captures a flip as a generic jsonb snapshot but with
-- no typed cause/type/source and misses a momentary that never commits;
-- oms.incidents is the customer outage, not the electrical operation; and
-- faults.fault_events is a fault INVESTIGATION (not every operation is a fault --
-- a manual open or a test isn't). So device operations get their own lean,
-- append-only log, feedable from several sources (crew entry, an executed OMS
-- switching step, a booked fault, and -- later -- SCADA/relay via the webhook
-- ingestion engine's new "operation" target adapter).
--
-- Bare device refs (device_class + device_id), no FK -- same convention
-- oms.incidents.predicted_device_* and faults.fault_events.protective_device_*
-- use, because a feature lives in its own per-class gis.<code> table, not one
-- table a FK could point at. The optional fault_id / incident_id /
-- switching_step_id are correlations to the records this operation relates to.
--
-- source_system + source_event_id give EVENT-LEVEL idempotency for an external
-- feed: a re-delivered SCADA batch (SCADA loves to re-send) dedupes on the
-- external system's own event id rather than double-logging a trip. Internal
-- rows leave both null.

CREATE SCHEMA IF NOT EXISTS ops;

CREATE TABLE ops.operations (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),

    -- the device that operated (bare ref, like oms/faults)
    device_class      text NOT NULL,
    device_id         uuid NOT NULL,

    -- what it did, and where the record came from
    op_type           text NOT NULL CHECK (op_type IN
                          ('trip', 'lockout', 'reclose', 'open', 'close', 'earth', 'alarm', 'other')),
    source            text NOT NULL CHECK (source IN
                          ('crew', 'scada', 'relay', 'switching_order', 'derived', 'other')),

    occurred_at       timestamptz NOT NULL,  -- when the operation HAPPENED (not when logged)
    cause             text,                  -- free text: overcurrent, earth fault, manual, test...
    notes             text,

    -- optional correlations to the records this operation relates to
    fault_id          uuid,
    incident_id       uuid,
    switching_step_id uuid,

    -- external-feed provenance + idempotency (null for an internal row)
    source_system     text,
    source_event_id   text,

    actor_id          uuid,                  -- who recorded it (manual/service user); null for a system write
    created_at        timestamptz NOT NULL DEFAULT now()
);

-- Per-device timeline + trip-count queries (newest first).
CREATE INDEX operations_device_idx ON ops.operations (device_class, device_id, occurred_at DESC);
-- Recent-operations board across all devices.
CREATE INDEX operations_recent_idx ON ops.operations (occurred_at DESC);
-- Filter the board by what the device did.
CREATE INDEX operations_type_idx ON ops.operations (op_type);
-- Event-level idempotency for an external feed: a re-delivered event dedupes.
-- Only external rows carry a source_event_id, so the uniqueness is partial.
CREATE UNIQUE INDEX operations_source_event_idx ON ops.operations (source_system, source_event_id)
    WHERE source_event_id IS NOT NULL;

COMMENT ON TABLE ops.operations IS
    'Append-only device-operation event log: one row per protective/switching device operation (trip, lockout, reclose, open, close, earth). Current device_state lives on the feature; THIS is the history of operations with typed cause/source/time. See migration 0147.';
