-- 0199_switching_order_approval.sql
-- The ADMS switching-order governance gate: a switching program must be APPROVED
-- (authorised) before it can be issued and executed. Records who approved it and
-- when, so the order carries its own authorisation audit. A draft that is edited
-- after approval reverts to draft (the engine clears these), so an executed plan is
-- always the one that was approved.
--
-- The lifecycle becomes: draft -> approved -> issued -> in_progress -> completed
-- (or cancelled from any non-terminal state). Execution is additionally interlocked
-- against step sequence, the operated device's expected pre-state, and the existing
-- safety tags/clearances (api/internal/oms, api/internal/safety) -- those are engine
-- logic, not schema.

ALTER TABLE oms.switching_orders
    ADD COLUMN IF NOT EXISTS approved_at timestamptz,
    ADD COLUMN IF NOT EXISTS approved_by uuid;

COMMENT ON COLUMN oms.switching_orders.approved_at IS
    'When the order was approved/authorised (ADMS gate). NULL = not approved; cleared when an approved order is edited back to draft. See migration 0199.';
