-- 0109_create_fault_event_history.sql
-- Purpose: an append-only audit trail for the Faults module -- one immutable row
-- per change to a fault event, recording WHO made the change, WHEN, WHAT kind of
-- change it was, and the full fault state AFTER it. Until now faults.fault_events
-- carried only "latest state" timestamps (created_at / updated_at / cleared_at):
-- a status transition or a PATCH overwrote the row in place, so the question
-- "who moved this fault to cleared, and when?" had no answer. This table keeps
-- the timeline.
--
-- This is the faults-schema analogue of gis.feature_history (migration 0067),
-- built the same way and for the same reason:
--   * ONE shared history table, not a per-something split -- fault_id identifies
--     the fault and the whole post-change state rides as a jsonb `snapshot` (the
--     same envelope the /faults API returns for a GET), so no extra schema.
--   * The write path (api/internal/faults) inserts one row from
--     create / status-change / update / defect-booking, in the same connection
--     (or transaction) as the fault write, so the audit row lands with the change.
--
-- Deliberately NO foreign keys (matching gis.feature_history):
--   * fault_id is left a bare uuid so the audit trail can outlive the fault row
--     it describes -- an audit that a delete could erase is not an audit.
--   * actor_id is a soft reference to iam.users, resolved by LEFT JOIN at read
--     time, so deleting a user never erases the history of what they did.

CREATE TABLE faults.fault_event_history (
    id        bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    fault_id  uuid        NOT NULL,
    -- created | status_changed | updated | defect_booked -- one verb per distinct
    -- write the faults engine performs, so a reader can tell a transition from an
    -- attribute edit from a defect-booking without diffing snapshots.
    action    text        NOT NULL,
    actor_id  uuid,       -- who did it; NULL for a system / CLI / unauthenticated write
    at        timestamptz NOT NULL DEFAULT now(),
    snapshot  jsonb       NOT NULL  -- full fault_events state after the change
);

COMMENT ON TABLE faults.fault_event_history IS 'Append-only audit trail of every change to a fault event: who (actor_id -> iam.users, soft ref), when (at), what kind (action), and the full post-change state (snapshot jsonb). The faults analogue of gis.feature_history; written by api/internal/faults in the same connection as each fault write. No FKs so the trail outlives both the fault and the user.';

-- The one access pattern: a single fault's timeline, newest first.
CREATE INDEX fault_event_history_fault_idx
    ON faults.fault_event_history (fault_id, at DESC);
