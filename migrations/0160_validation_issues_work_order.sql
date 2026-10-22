-- 0160_validation_issues_work_order.sql
-- Purpose: let a data-quality issue be ASSIGNED as a fix to an office or field
-- user by raising a WAM work order from it. The work order is the assignable,
-- field-syncable unit (it already rides into field packs); the issue just needs to
-- remember which order was raised for it, so the queue can show "Assigned" instead
-- of an open problem and not raise a second order for the same issue.
--
-- Nullable: the vast majority of issues are never assigned (they're fixed inline or
-- auto-resolve when the audit reconciles). No FK to wam_work_orders -- the work
-- order is operational and may be purged/archived on its own schedule, and the
-- issue outliving a tidied-up order is harmless (the column simply points nowhere).
-- The link is set on assign and left as history; resolving the issue is still driven
-- by the audit reconcile, not by the order's completion.

ALTER TABLE ops.validation_issues
    ADD COLUMN IF NOT EXISTS work_order_id uuid;

COMMENT ON COLUMN ops.validation_issues.work_order_id IS 'The WAM work order raised to fix this issue (assignment), or NULL if unassigned. Set on assign; the issue still resolves via the audit reconcile, not the order''s completion.';
