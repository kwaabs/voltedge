-- 0198_create_alarms.sql
-- The operator ALARM ANNUNCIATOR store -- the ADMS control-room worklist of
-- conditions needing attention, distinct from the historical logs around it:
-- ops.operations (0147) is every device operation that happened, oms.events is
-- the outage lifecycle, gis.feature_history is a committed flip. An ALARM is a
-- condition an operator must SEE and ACKNOWLEDGE, prioritised and self-pruning.
--
-- Two alarm shapes share this table, distinguished by `active`:
--   * EVENT alarms (a lockout/earth/alarm operation occurred) are raised with
--     active = false -- a point-in-time event. They stay on the annunciator until
--     acknowledged, then drop off.
--   * STANDING alarms (a device is off its normal position right now) are raised
--     with active = true while the condition holds, and CLEARED (active = false,
--     cleared_at set) by a reconcile when it resolves -- the same self-pruning the
--     validation-issues queue (0137) uses.
--
-- The live annunciator shows an alarm while it is active OR not-yet-acknowledged
-- (so a transient event isn't missed) and while not shelved -- the classic
-- unacknowledged/return-to-normal behaviour. fingerprint is the stable upsert key
-- (one row per condition), so a re-occurrence bumps the count and re-alarms rather
-- than piling up duplicates. Bare device refs (device_class + device_id text), no
-- FK, like ops.operations / oms.incidents.

CREATE SCHEMA IF NOT EXISTS ops;

CREATE TABLE ops.alarms (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),

    -- stable upsert key: one row per condition (e.g. 'abnormal_config:MV_SWITCH:<id>')
    fingerprint      text NOT NULL UNIQUE,

    source           text NOT NULL,                       -- 'device_op' | 'abnormal_config' | ...
    category         text NOT NULL DEFAULT 'general',      -- 'protection' | 'safety' | 'configuration' | ...
    severity         text NOT NULL CHECK (severity IN ('critical', 'high', 'medium', 'low')),
    message          text NOT NULL,

    -- the device/feature the alarm is about (bare ref, nullable for a non-device alarm)
    device_class     text,
    device_id        text,

    -- lifecycle
    active           boolean      NOT NULL DEFAULT true,   -- condition present now (standing) / false for an event
    occurrence_count integer      NOT NULL DEFAULT 1,
    raised_at        timestamptz  NOT NULL DEFAULT now(),
    last_raised_at   timestamptz  NOT NULL DEFAULT now(),
    cleared_at       timestamptz,                          -- when active went false
    acknowledged_at  timestamptz,
    acknowledged_by  uuid,
    shelved_until    timestamptz,                          -- suppressed from the annunciator until this time
    shelve_reason    text,
    shelved_by       uuid,

    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);

-- The annunciator reads the live set severity-first, newest-raised next.
CREATE INDEX alarms_live_idx ON ops.alarms (severity, last_raised_at DESC);
-- Reconcile/clear a source's standing alarms.
CREATE INDEX alarms_source_idx ON ops.alarms (source, active);

COMMENT ON TABLE ops.alarms IS
    'Operator alarm annunciator: prioritised, acknowledgeable conditions needing attention (device-operation events + off-normal standing alarms). Self-pruning via fingerprint upsert + reconcile. See migration 0198.';
