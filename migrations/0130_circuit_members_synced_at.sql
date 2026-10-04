-- 0130_circuit_members_synced_at.sql
-- Purpose: give a circuit (a feeder / subnetwork, migration 0062) a FRESHNESS
-- signal. A circuit's membership is stamped onto features by assign-by-trace
-- (api/internal/circuit.AssignCircuit) and then drifts silently when the network
-- is re-wired or a member is edited -- there's no sign it's gone stale. This adds
-- the one column needed to tell:
--
--   members_synced_at -- when assign-by-trace last full-synced this circuit's
--                        membership. NULL = never assigned. A circuit is "stale"
--                        when any member feature has been edited since (its
--                        updated_at > members_synced_at), surfaced as a
--                        "re-assign" badge. Because Postgres now() is the
--                        transaction timestamp, AssignCircuit stamps every member's
--                        updated_at AND this column with the SAME value, so a
--                        just-assigned circuit is never falsely flagged (equal, not
--                        greater); only a LATER edit trips it.
--
-- Pure additive metadata on the catalog row -- no gis.<class> change, no
-- recompile. Idempotent (IF NOT EXISTS).

ALTER TABLE gis.circuits
    ADD COLUMN IF NOT EXISTS members_synced_at timestamptz;

COMMENT ON COLUMN gis.circuits.members_synced_at IS 'When assign-by-trace last full-synced this circuit''s membership. NULL = never assigned. A member edited after this (updated_at > members_synced_at) makes the circuit stale.';
