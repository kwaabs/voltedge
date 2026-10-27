-- 0174_work_order_crew.sql
-- Purpose: de-silo the crew, slice 2 -- let a WAM work order be assigned to a
-- CREW, not only to one internal user. Until now a work order's field ownership
-- was `assigned_to_user_id` (one person) or the free-text `assigned_to` label; a
-- real crew (oms.crews, 0170) with members (oms.crew_members, 0173) could be
-- dispatched to an OMS incident but could not own a job. This adds that link, so a
-- crew-assigned order fans out to every member's field pack.
--
-- Bare ref (uuid, no FK) to oms.crews, matching the loose coupling WAM already
-- uses for its asset/incident refs. Nullable: most orders are still assigned to a
-- person (or nobody); crew and user assignment are not mutually exclusive at the
-- column level (a lead may also be named), the UI picks one path.

ALTER TABLE wam.work_orders ADD COLUMN IF NOT EXISTS crew_id uuid;

COMMENT ON COLUMN wam.work_orders.crew_id IS 'Assigned field crew (0174, bare ref → oms.crews): a crew-assigned order reaches every member''s field pack.';

-- The field-pack fan-out filters open orders by crew; index the crew side.
CREATE INDEX IF NOT EXISTS wam_work_orders_crew_idx
    ON wam.work_orders (crew_id) WHERE crew_id IS NOT NULL;
