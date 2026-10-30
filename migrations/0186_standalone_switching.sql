-- 0186_standalone_switching.sql
-- Purpose: DU switching-gap #2 -- STANDALONE planned switching programs.
--
-- A switching order was always tied to an incident (migration 0089:
-- incident_id NOT NULL). A planned switching PROGRAM authored for planned work
-- (a maintenance isolation, a construction cut-over) has no incident behind it:
-- an operator writes the open/verify/earth/close step list directly. Rather than
-- a parallel table, reuse oms.switching_orders / oms.switching_steps wholesale --
-- the execution, clearance (permit-to-work) and live device_state flip all key on
-- the order/step ids, not the incident -- and just:
--
--   1. make incident_id NULLABLE (incident_id IS NULL => a standalone program);
--   2. add a booked window (planned_start / planned_end) and a free-text purpose,
--      the context an incident-backed order inherits from its incident but a
--      standalone program must carry itself (null on an incident-backed order).

ALTER TABLE oms.switching_orders ALTER COLUMN incident_id DROP NOT NULL;

ALTER TABLE oms.switching_orders ADD COLUMN IF NOT EXISTS planned_start timestamptz;
ALTER TABLE oms.switching_orders ADD COLUMN IF NOT EXISTS planned_end   timestamptz;
ALTER TABLE oms.switching_orders ADD COLUMN IF NOT EXISTS purpose       text;

-- Standalone programs are listed on their own (incident_id IS NULL); a partial
-- index keeps that scan cheap without touching the existing per-incident index.
CREATE INDEX IF NOT EXISTS switching_orders_standalone_idx
    ON oms.switching_orders (created_at DESC)
    WHERE incident_id IS NULL;
