-- 0133_create_webhook_outbound_events.sql
-- Purpose: the OUTBOX for the outbound event emitter (docs/outbound-webhooks-design.md,
-- Slice 1). The counterpart to the inbound ingestion engine (0124/0125): where that
-- brings data INTO VoltEdge, the outbound emitter pushes VoltEdge's own changes OUT to
-- subscribers (CIS / work-management) when something happens here.
--
-- This slice is CAPTURE only. Each lifecycle write (an OMS incident, a WAM work order
-- or defect, a Faults event) inserts one row here IN THE SAME TRANSACTION as the change
-- itself -- the transactional-outbox pattern -- so an event is never emitted for a
-- change that rolled back, and a committed change always leaves its event. Slice 2 adds
-- the subscriptions, the fan-out, and the delivery worker that drains this table and
-- POSTs each event to its subscribers; nothing reads this table yet.

CREATE TABLE ops.webhook_outbound_events (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),

    -- Global insertion order. A worker delivers a given entity's events in this order
    -- (per-entity ordering); it is best-effort as a global clock (transactions commit
    -- out of assignment order), but monotonic for the sequential writes to one entity,
    -- which is what ordering actually needs.
    seq           bigint GENERATED ALWAYS AS IDENTITY,

    -- The semantic event, e.g. 'oms.incident.raised', 'wam.workorder.completed',
    -- 'faults.event.cleared'. Chosen by the write path, which knows the meaning (a
    -- publish vs a plain update) a row-diff never could.
    event_type    text NOT NULL,

    -- The entity the event is about: a kind ('incident' | 'work_order' | 'defect' |
    -- 'fault' | ...) and its id (polymorphic, no FK -- the id points into whichever
    -- module's table owns it, same convention the inbound entity-refs use).
    entity_kind   text NOT NULL,
    entity_id     text NOT NULL,

    -- The event envelope's `data`: the changed record (or the salient fields of it),
    -- captured at write time so the delivery worker needs no re-query.
    payload       jsonb NOT NULL,

    -- Who caused the change (audit / the envelope's `actor`); null for a system write.
    actor_id      uuid,

    occurred_at   timestamptz NOT NULL DEFAULT now(),

    -- NULL until Slice 2's fan-out has created this event's per-subscription deliveries.
    -- The partial index below is the worker's claim set (events awaiting fan-out).
    fanned_out_at timestamptz
);

COMMENT ON TABLE ops.webhook_outbound_events IS 'Transactional outbox for the outbound event emitter (docs/outbound-webhooks-design.md). One row per real change, inserted in the same tx as the change; Slice 2''s fan-out + delivery worker drain it to subscribers. Semantic events (not row-diffs), captured at the OMS/WAM/Faults write paths.';
COMMENT ON COLUMN ops.webhook_outbound_events.seq IS 'Global insertion order; per-entity ordering reads this. Best-effort as a global clock (txs commit out of order), monotonic for one entity''s sequential writes.';
COMMENT ON COLUMN ops.webhook_outbound_events.fanned_out_at IS 'NULL until the fan-out (Slice 2) has created this event''s deliveries. The partial index is the worker''s claim set.';

-- The worker's claim index: events not yet fanned out, oldest first.
CREATE INDEX webhook_outbound_events_pending_idx
    ON ops.webhook_outbound_events (seq)
    WHERE fanned_out_at IS NULL;
