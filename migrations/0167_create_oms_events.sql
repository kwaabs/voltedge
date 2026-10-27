-- 0167_create_oms_events.sql
-- Purpose: an append-only EVENT LOG for the OMS, so a trouble call and the
-- incident it rolls into each have a precise, actor-stamped TIMELINE -- "received
-- -> located -> attributed -> confirmed -> dispatched -> restored". Until now a
-- call carried only a single overwritten updated_at and the incident only
-- started_at / restored_at, so the moment a call was located, or the difference
-- between confirm and dispatch, was lost. This records each transition as its own
-- immutable row.
--
-- One table covers both entity kinds (entity_kind + entity_id, a bare ref with no
-- FK -- the same loose coupling the rest of oms uses, so purging a call/incident
-- never has to cascade here and the history can outlive it). actor_user_id is the
-- user who drove the transition (NULL for an engine/system step: prediction, a
-- SCADA auto-raise, an AMI last-gasp). occurred_at is when it happened (a SCADA
-- trip stamps the reported time, not the ingest time); created_at is the insert.
--
-- event_type is free text by convention 'call.*' / 'incident.*' (call.received,
-- call.located, call.attributed; incident.predicted, incident.planned,
-- incident.scada_raised, incident.confirmed, incident.dispatched,
-- incident.restored, incident.cancelled) -- kept free text so a new transition
-- never needs a migration, exactly as ops.validation_issues.kind is.

CREATE TABLE IF NOT EXISTS oms.events (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    entity_kind   text NOT NULL CHECK (entity_kind IN ('call', 'incident')),
    entity_id     uuid NOT NULL,
    event_type    text NOT NULL,
    actor_user_id text,
    note          text,
    occurred_at   timestamptz NOT NULL DEFAULT now(),
    created_at    timestamptz NOT NULL DEFAULT now()
);

-- The timeline read is "all events for this entity, oldest first".
CREATE INDEX IF NOT EXISTS oms_events_entity_idx
    ON oms.events (entity_kind, entity_id, occurred_at);

COMMENT ON TABLE oms.events IS 'Append-only OMS lifecycle event log (0167): one row per call/incident transition, for the timeline panel. entity_kind+entity_id are bare refs (no FK); actor_user_id NULL = engine/system.';
