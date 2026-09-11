-- 0044_sync_review.sql
-- Purpose: Phase 23 Slice 3 -- turn on the "system-of-record / QA" personality of
-- the field-sync inbox (docs/phase-23-sync-inbox.md). Slice 1 gave every op one
-- path: capture -> worker auto-applies. This migration adds the gate that lets a
-- feature class (or the whole system) instead hold its ops for a supervisor to
-- review and POST before they touch production. Three additive pieces, no change
-- to production feature tables:
--
--   1. Two RBAC permissions -- sync:review (see the queue) and sync:post (post or
--      reject a reviewed op, and set the policy). Granted to admin + break-glass-admin
--      only, following 0040's precedent (admin:field-packs): 0025's CROSS JOIN seed
--      ran before these rows existed, so the grant is added explicitly here.
--   2. ops.sync_policies -- the per-class-or-global apply mode. Absence of any row
--      means the global default 'auto' (Slice 1 behaviour), so this is backward
--      compatible: QA mode is off until a row turns it on.
--   3. Widen sync_inbox_device_idx to cover 'pending_review'. The claim query's
--      per-device single-flight guard now blocks a later op while an EARLIER op of
--      the same device is held in review (so a connect can't apply before the create
--      it references is posted); pending_review is thus a live "active" state the
--      guard must probe by index, not just received/applying.

-- 1. Permissions ------------------------------------------------------------------

INSERT INTO iam.permissions (key, description) VALUES
    ('sync:review', 'View the field-sync review queue -- device ops held pending a supervisor''s post (QA mode).'),
    ('sync:post',   'Post (apply) or reject a reviewed field-sync op, and set the per-class apply policy.')
ON CONFLICT (key) DO NOTHING;

INSERT INTO iam.role_permissions (role_id, permission_key)
SELECT r.id, p.key
FROM iam.roles r
CROSS JOIN (VALUES ('sync:review'), ('sync:post')) AS p(key)
WHERE r.key IN ('admin', 'break-glass-admin')
ON CONFLICT DO NOTHING;

-- 2. Per-class (or global) apply policy ------------------------------------------

CREATE TABLE ops.sync_policies (
    -- A feature_class code whose ops this row governs, OR the sentinel '*' for the
    -- global default that applies to any class without its own row (and to ops with
    -- no class, e.g. establish_*). No row at all for '*' means the built-in default
    -- 'auto' -- so an empty table = Slice 1 behaviour (everything auto-applies).
    feature_class text PRIMARY KEY,

    -- 'auto'   : ops land 'received' and the worker applies them immediately (trusted
    --            field-capture -- Slice 1's behaviour, plus durability + audit).
    -- 'review' : ops land 'pending_review'; the worker skips them until a supervisor
    --            posts them (system-of-record / QA mode).
    mode text NOT NULL CHECK (mode IN ('auto', 'review')),

    -- Who last set this policy, for the audit trail. SET NULL (not RESTRICT): a
    -- policy row is config, not an immutable audit record like an applied op, so a
    -- departed admin's user row can still be removed -- the policy just loses its
    -- attribution.
    updated_by uuid REFERENCES iam.users(id) ON DELETE SET NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE ops.sync_policies IS 'Per-feature-class (or global ''*'') field-sync apply mode (Phase 23 Slice 3). auto = worker applies on receive; review = hold pending_review for a supervisor to post. No row => global default ''auto''. See docs/phase-23-sync-inbox.md.';

-- 3. Widen the per-device single-flight guard index to include pending_review ------
-- The claim guard (inbox.go claimInboxOp) treats a held-in-review op as active so a
-- device's later op can't jump ahead of an earlier one awaiting a post. Match the
-- index partial predicate to that expanded "active" set so the guard stays an index
-- probe over a handful of live rows rather than a scan of applied/rejected history.
DROP INDEX IF EXISTS ops.sync_inbox_device_idx;
CREATE INDEX sync_inbox_device_idx ON ops.sync_inbox (submitted_by, device_id, recv_seq)
    WHERE status IN ('received', 'applying', 'pending_review');
