-- 0149_webhook_target_operation.sql
-- Add 'operation' to the ingestion-target set so a webhook endpoint can feed the
-- device-operation log (ops.operations, migration 0147) from an external system
-- -- a SCADA/relay/DMS pushing trips, lockouts, recloses and open/close events.
-- The operationAdapter (api/internal/webhook) decodes each change and records it
-- via deviceops.Record, correlating the device by external id through the
-- external_ref reverse index (migration 0145).
--
-- Only the endpoints table's target CHECK needs widening: an operation is an
-- immutable event deduped on its own source_event_id (ops.operations' partial
-- unique index), so the adapter does NOT use ops.webhook_entity_refs (whose
-- 'work'/'fault' CHECK is for upsertable entities) -- that table is untouched.

ALTER TABLE ops.webhook_endpoints DROP CONSTRAINT IF EXISTS webhook_endpoints_target_check;
ALTER TABLE ops.webhook_endpoints ADD CONSTRAINT webhook_endpoints_target_check
    CHECK (target IN ('feature', 'reading', 'work', 'fault', 'operation'));
