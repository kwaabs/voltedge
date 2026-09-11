-- 0045_sync_conflict.sql
-- Purpose: Phase 23 Slice 4 -- turn today's silent last-writer-wins into explicit,
-- resolvable conflict detection (docs/phase-23-sync-inbox.md). Slices 1-3 capture,
-- apply, and (in QA mode) hold ops for review, but an update/delete whose device
-- snapshot predates a concurrent production edit still overwrites it silently. This
-- migration adds the optimistic-concurrency gate: a captured update/delete whose
-- base_version (the pack snapshot it was made against) diverges from the current
-- production row lands 'conflict' -- held for a supervisor to resolve -- instead of
-- clobbering the newer value. Three additive pieces, no change to feature tables:
--
--   1. ops.sync_policies.detect_conflicts -- the opt-in switch. Default false, so an
--      empty/existing policy set keeps Slice 1-3 behaviour (silent LWW). Turned on
--      per class (or globally via the '*' row) to make that class's update/delete ops
--      conflict-checked. Independent of mode: a class can auto-apply AND conflict-check.
--   2. ops.sync_inbox.conflict_override -- set true when a supervisor resolves a
--      conflict by "apply anyway". The op flips back to 'received' and the worker
--      re-applies it skipping the version check (the human has waived it).
--   3. Widen sync_inbox_device_idx to cover 'conflict'. A held conflict now blocks the
--      device's later ops (like pending_review) so resolving it can't regress a newer
--      op -- so 'conflict' joins the "active" set the single-flight guard probes.

-- 1. Per-class conflict-detection switch -----------------------------------------

ALTER TABLE ops.sync_policies
    ADD COLUMN detect_conflicts boolean NOT NULL DEFAULT false;

COMMENT ON COLUMN ops.sync_policies.detect_conflicts IS 'Phase 23 Slice 4: when true, the worker conflict-checks this class''s update/delete ops (base_version vs production updated_at) and holds a divergence as ''conflict'' instead of silent LWW. Default false = Slice 1-3 behaviour. Independent of mode.';

-- 2. Force-apply flag for a resolved conflict ------------------------------------

ALTER TABLE ops.sync_inbox
    ADD COLUMN conflict_override boolean NOT NULL DEFAULT false;

COMMENT ON COLUMN ops.sync_inbox.conflict_override IS 'Phase 23 Slice 4: set when a supervisor resolves a ''conflict'' with "apply anyway". The op returns to ''received'' and the worker re-applies it skipping the base_version check.';

-- 3. Widen the per-device single-flight guard index to include conflict -----------
-- claimInboxOp (inbox.go) now treats a held 'conflict' op as active: it blocks the
-- same device's later ops so a force-apply of the conflicted op can't overwrite a
-- newer op that already applied. Match the index partial predicate to that expanded
-- "active" set (received/applying/pending_review/conflict) so the guard stays an
-- index probe over a handful of live rows.
DROP INDEX IF EXISTS ops.sync_inbox_device_idx;
CREATE INDEX sync_inbox_device_idx ON ops.sync_inbox (submitted_by, device_id, recv_seq)
    WHERE status IN ('received', 'applying', 'pending_review', 'conflict');
