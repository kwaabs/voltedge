-- 0166_webhook_target_outage.sql
-- Add 'outage' to the ingestion-target set so a webhook endpoint can feed FIELD
-- GROUND-TRUTH outage signals into the OMS -- a SCADA/DMS pushing device trips and
-- an AMI head-end pushing meter "last gasp" frames. The outageAdapter
-- (api/internal/webhook/adapter_outage.go) decodes each change and, by `kind`:
--   device_trip -> oms.RaiseIncidentFromDevice (raise/return the open incident
--                  blamed on the tripped device, idempotent per device)
--   last_gasp   -> oms.RecordLastGasp (a machine trouble call for the dark SP)
-- correlating the device/SP by external id through the external_ref reverse index
-- (migration 0145), or by a direct ref.
--
-- Only the endpoints table's target CHECK needs widening: the outage adapter is
-- idempotent in the OMS layer (one open incident per device; one open AMI call per
-- SP) and dedupes there, so it does NOT use ops.webhook_entity_refs (whose
-- 'work'/'fault' CHECK is for upsertable entities) -- that table is untouched.

ALTER TABLE ops.webhook_endpoints DROP CONSTRAINT IF EXISTS webhook_endpoints_target_check;
ALTER TABLE ops.webhook_endpoints ADD CONSTRAINT webhook_endpoints_target_check
    CHECK (target IN ('feature', 'reading', 'work', 'fault', 'operation', 'outage'));
