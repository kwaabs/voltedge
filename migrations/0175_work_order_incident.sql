-- 0175_work_order_incident.sql
-- Purpose: de-silo the crew, slice 3 -- close the loop outage → crew dispatch →
-- field job → mobile → completion. Dispatching a crew to an OMS incident (0170)
-- can now raise a crew-assigned restoration work order; this adds the direct link
-- from that order back to the incident it serves, so the outage board can show
-- "field work raised" and the work order carries which outage it belongs to.
--
-- Bare ref (uuid, no FK) to oms.incidents, matching WAM's loose coupling. Nullable:
-- most work orders are not outage restorations. Distinct from the existing
-- defect→incident link (wam.defects.incident_id, source='outage'): that explains a
-- defect FOUND during an outage; this is the restoration JOB dispatched for one.

ALTER TABLE wam.work_orders ADD COLUMN IF NOT EXISTS incident_id uuid;

COMMENT ON COLUMN wam.work_orders.incident_id IS 'Served OMS incident (0175, bare ref → oms.incidents): a restoration order raised when a crew is dispatched to the outage.';

-- "work orders for this incident" is the board lookup; index the incident side.
CREATE INDEX IF NOT EXISTS wam_work_orders_incident_idx
    ON wam.work_orders (incident_id) WHERE incident_id IS NOT NULL;
