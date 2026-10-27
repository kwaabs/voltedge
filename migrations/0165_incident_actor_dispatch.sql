-- 0165_incident_actor_dispatch.sql
-- Purpose: make an incident's lifecycle ATTRIBUTABLE. Until now oms.incidents
-- recorded what happened (status transitions) but never WHO drove them -- the
-- board could not say "confirmed by", "dispatched by", "restored by". The device
-- operations below a switching step are already attributed (crew source + the
-- deviceops log), but the incident itself carried no actor at all.
--
-- One column, last_actor_user_id: the user who last advanced the incident
-- (confirm / dispatch / restore / cancel). Nullable -- a predicted incident the
-- engine raised with no human in the loop, or a pre-0165 row, simply has none.
-- No FK to iam.users: the acting user may be disabled/removed on its own schedule
-- and an incident outliving the account is harmless (the id just points nowhere,
-- the same tolerance crew_ref and the webhook outbox actor_id already take). The
-- granular per-operation trail stays in feature_history / ops.operations; this is
-- the incident-level "last touched by" the OMS board shows.
--
-- This also unblocks the 'dispatched' status (defined in the model since 0087 but
-- never set): a confirmed incident advances to 'dispatched' when a crew is sent,
-- stamping this column, and that transition fires the oms.incident.updated
-- webhook event (also defined-but-unfired until now).

ALTER TABLE oms.incidents
    ADD COLUMN IF NOT EXISTS last_actor_user_id text;

COMMENT ON COLUMN oms.incidents.last_actor_user_id IS 'User who last advanced this incident (confirm/dispatch/restore/cancel), or NULL for an engine-predicted or pre-0165 incident. No FK -- tolerates the account being removed later.';
