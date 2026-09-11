-- 0043_create_sync_inbox.sql
-- Purpose: create ops.sync_inbox -- the durable landing zone that decouples
-- RECEIVE from APPLY in the field-sync path (Phase 23, docs/phase-23-sync-inbox.md).
-- Today POST /sync applies each device op straight into production and only then
-- acks, so the no-loss guarantee rides on apply-time business logic and a rejected
-- op leaves no server record (a poison op the device retries forever). This table
-- splits the two: POST /sync's only synchronous job becomes a cheap idempotent
-- INSERT here (ack = "durably captured"); a worker drains 'received' rows and
-- commits them to production through the existing crud/connectivity/relationship
-- engines. The ops schema was reserved from day one (0001) for exactly these queue
-- tables -- this sits beside ops.export_jobs (0036), whose queue pattern it mirrors.

CREATE TABLE ops.sync_inbox (
    -- op_id is the device-minted idempotency key (a client UUID; for a create it is
    -- also the feature's PK). TEXT, not uuid: idempotency needs only equality, and a
    -- malformed id must never fail the whole capture. ON CONFLICT (op_id) DO NOTHING
    -- absorbs a re-pushed op (a retried batch after an ambiguous ack).
    op_id            text PRIMARY KEY,

    -- recv_seq is the server receive order, assigned at insert. It is the causal
    -- ordering key the worker applies by: the device pushes its outbox oldest-first
    -- (ORDER BY created_at) and one bulk insert assigns recv_seq in array order, so
    -- recv_seq preserves each device's op order across pushes -- a connect always
    -- sorts after the create it references -- with no explicit client sequence.
    recv_seq         bigint GENERATED ALWAYS AS IDENTITY,

    -- Who submitted it (authz + audit + owner-scoped status reads). ON DELETE
    -- RESTRICT: an op's submitter can't be deleted out from under the audit trail.
    submitted_by     uuid NOT NULL REFERENCES iam.users(id) ON DELETE RESTRICT,

    -- device_id groups a physical device's ops for serialized apply (the claim
    -- query won't run a device's op while an earlier one is still in flight).
    -- Nullable: the S1 client sends none, so a user's ops group under NULL together
    -- -- conservative (over-serializes one user across devices) but always correct.
    device_id        text,
    submission_id    text,                 -- groups a batch/survey for review/posting (Slice 3)
    device_seq       bigint,               -- reserved: per-device monotonic hint (recv_seq carries order in S1)

    -- The op itself, verbatim from the device outbox -- same vocabulary as the
    -- syncOp wire shape. payload is the crud/graph input (empty for a delete, which
    -- is fully described by feature_class + feature_id).
    kind             text NOT NULL
                         CHECK (kind IN ('create','update_attrs','delete',
                                         'establish_connection','establish_relationship')),
    feature_class    text,
    feature_id       text,
    payload          jsonb,
    base_version     text,                 -- pack snapshot_at the edit was made against (LWW + future conflict detect)

    -- Lifecycle:
    --   received       : durably captured, not yet applied (the queued state)
    --   applying       : a worker has claimed it (the running state; lease-guarded)
    --   pending_review : QA mode, awaiting a supervisor's post (Slice 3)
    --   applied        : committed to production (terminal, success)
    --   rejected       : apply failed -- quarantined, NOT retried (terminal; the poison-op fix)
    --   conflict       : base_version diverged from production -- awaiting resolution (Slice 4)
    --   superseded     : a later op for the same feature replaced this one before apply (Slice 4)
    -- CHECK-enforced so a bad state can't be written by hand.
    status           text NOT NULL DEFAULT 'received'
                         CHECK (status IN ('received','applying','pending_review','applied',
                                           'rejected','conflict','superseded')),
    error            text,                 -- human message on rejected/conflict
    result_feature   jsonb,                -- the server row echoed back on applied

    -- Review/QA trail (Slice 3). ON DELETE RESTRICT protects the audit trail.
    reviewed_by      uuid REFERENCES iam.users(id) ON DELETE RESTRICT,
    reviewed_at      timestamptz,

    -- Queue bookkeeping, mirrored from ops.export_jobs. attempts caps the
    -- crash-requeue loop; worker_id + lease_expires_at implement the heartbeat lease
    -- that reclaims an op whose worker died mid-apply (status='applying' AND lapsed).
    attempts         integer NOT NULL DEFAULT 0,
    worker_id        text,
    lease_expires_at timestamptz,

    received_at      timestamptz NOT NULL DEFAULT now(),
    started_at       timestamptz,          -- when a worker last claimed it
    finished_at      timestamptz           -- when it reached a terminal status
);

COMMENT ON TABLE ops.sync_inbox IS 'Durable field-sync inbox (Phase 23). POST /sync captures device ops here (ack = "captured"); a worker drains ''received'' rows to production. Decouples receive from apply. See docs/phase-23-sync-inbox.md.';
COMMENT ON COLUMN ops.sync_inbox.op_id IS 'Device-minted idempotency key (client UUID). PK; ON CONFLICT DO NOTHING absorbs a re-pushed op.';
COMMENT ON COLUMN ops.sync_inbox.recv_seq IS 'Server receive order, assigned at insert. The causal ordering key the worker applies by (device pushes oldest-first, so recv_seq preserves per-device op order).';
COMMENT ON COLUMN ops.sync_inbox.status IS 'Lifecycle: received -> applying -> applied | rejected | conflict | pending_review | superseded. rejected is a quarantine, not a retry (poison-op fix).';
COMMENT ON COLUMN ops.sync_inbox.lease_expires_at IS 'Heartbeat lease. An ''applying'' op whose lease has lapsed is treated as a crashed worker''s and re-claimable (double-apply is safe -- every apply path is idempotent).';

-- Claim ordering: the worker takes the oldest applyable op in recv_seq order. A
-- PARTIAL index over just the active set keeps this tiny -- applied/rejected history
-- (the bulk of a mature table) is excluded, so the claim scan never wades through it.
CREATE INDEX sync_inbox_claim_idx  ON ops.sync_inbox (recv_seq)
    WHERE status IN ('received', 'applying');

-- Per-device single-flight guard: the claim query's correlated "is an EARLIER op
-- from this (submitted_by, device_id) still active?" NOT EXISTS. Same active-only
-- partial predicate so the guard is an index probe over a handful of live rows.
CREATE INDEX sync_inbox_device_idx ON ops.sync_inbox (submitted_by, device_id, recv_seq)
    WHERE status IN ('received', 'applying');

-- Review queue: a submission's ops and pending-review triage (Slice 3).
CREATE INDEX sync_inbox_review_idx ON ops.sync_inbox (status, submission_id, received_at);

-- Status pull-back (getInboxStatuses) reads by op_id (the PK) filtered to the owner,
-- so it rides the op_id primary-key index -- no extra index needed.
