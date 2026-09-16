-- 0100_work_order_assignee.sql
-- Purpose: Phase 59 -- make work-order assignees real people. Until now
-- wam.work_orders.assigned_to was free text (a crew label), so "assign this job
-- to a specific field worker" and "show me my jobs on the field tool" were not
-- expressible. This adds a real user reference alongside the existing free-text
-- column:
--   assigned_to_user_id -- FK to iam.users; the internal field worker a work
--                          order is assigned to. NULL when unassigned or handed
--                          to an external crew (named via the free-text
--                          assigned_to, which is kept as a fallback label).
-- ON DELETE SET NULL: removing a user unassigns their open work rather than
-- deleting the work. The index backs the "my work orders" filter
-- (WorkOrderFilter.AssignedToUserID / ?mine=true) the field tool uses.

ALTER TABLE wam.work_orders
    ADD COLUMN assigned_to_user_id uuid REFERENCES iam.users(id) ON DELETE SET NULL;

COMMENT ON COLUMN wam.work_orders.assigned_to_user_id IS 'The internal user (field worker) this work order is assigned to. NULL = unassigned or handed to an external crew named in assigned_to.';

CREATE INDEX work_orders_assigned_to_user_id_idx
    ON wam.work_orders (assigned_to_user_id)
    WHERE assigned_to_user_id IS NOT NULL;
