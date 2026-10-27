-- 0177_create_wam_subcontractors.sql
-- Purpose: a SUBCONTRACTOR registry. The utility may outsource field work to
-- external firms that do NOT operate in this system -- they receive the task and
-- run it on their own tools. This records WHO was tasked, as a structured entity
-- (consistent name, a contact, reportable load) rather than the free-text
-- wam.work_orders.assigned_to label. A subcontractor is deliberately NOT a crew
-- (oms.crews): it has no members, no status lifecycle, no field pack, no dispatch
-- -- none of that applies to a party outside the system. It is a rolodex row that
-- a work order (and, through a raised restoration order, an outage) can be tasked
-- to; the utility's own coordinator tracks the work-order status on their behalf.

CREATE TABLE IF NOT EXISTS wam.subcontractors (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name         text NOT NULL,
    contact_name text,
    phone        text,
    email        text,
    notes        text,
    active       boolean NOT NULL DEFAULT true,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE wam.subcontractors IS 'External firms the utility tasks field work to (0177). NOT a crew: no members/status/packs -- a tracked external party, work tasked via wam.work_orders.subcontractor_id.';

CREATE INDEX IF NOT EXISTS wam_subcontractors_active_idx ON wam.subcontractors (active);

-- Task a work order to a subcontractor. Nullable bare ref (no FK, matching WAM's
-- loose coupling). Parallel to assigned_to_user_id (an internal person) and crew_id
-- (an internal crew): subcontractor_id is an EXTERNAL firm. Unlike those two there
-- is no field-pack fan-out -- the firm does not operate in this system; the column
-- records responsibility, and the utility's coordinator tracks the status.
ALTER TABLE wam.work_orders ADD COLUMN IF NOT EXISTS subcontractor_id uuid;

COMMENT ON COLUMN wam.work_orders.subcontractor_id IS 'Tasked external firm (0177, bare ref → wam.subcontractors): recorded responsibility, no field-pack fan-out.';

CREATE INDEX IF NOT EXISTS wam_work_orders_subcontractor_idx
    ON wam.work_orders (subcontractor_id) WHERE subcontractor_id IS NOT NULL;
