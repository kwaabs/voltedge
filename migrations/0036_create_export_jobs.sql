-- 0036_create_export_jobs.sql
-- Purpose: create ops.export_jobs -- the async export queue behind Phase 17
-- (docs/phase-17-large-export.md). Every export is now a job: POST /export
-- inserts a 'queued' row and returns its id; a worker claims it with
-- FOR UPDATE SKIP LOCKED, runs the encoder, and marks it succeeded/failed.
-- The ops schema was reserved from day one (0001) for exactly these job tables.
--
-- One row carries three things: the request snapshot (format + scope, the
-- *resolved* authz scope captured at enqueue so the worker never re-derives
-- permissions), the lifecycle state (status + lease + attempts), and the result
-- pointer (object_key + byte_size + feature_count).

CREATE TABLE ops.export_jobs (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),

    -- Lifecycle: queued -> running -> succeeded | failed (cancelled is terminal
    -- too). CHECK-enforced so a bad transition can't be written by hand.
    status           text NOT NULL DEFAULT 'queued'
                         CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'cancelled')),

    -- The encoder to run. Same vocabulary as the export format registry.
    format           text NOT NULL
                         CHECK (format IN ('cim', 'kmz', 'shp', 'dxf')),

    -- The requesting user (authz + audit + download ownership). ON DELETE
    -- RESTRICT: a job's owner can't be deleted out from under an audit trail.
    requested_by     uuid NOT NULL REFERENCES iam.users(id) ON DELETE RESTRICT,

    -- The resolved request: {format, feature_classes, bbox, territory_ids
    -- (already expanded to descendants), cim_profile}. This is the authz
    -- snapshot -- the worker exports exactly this and nothing else.
    scope            jsonb NOT NULL,

    -- Result pointer, all null until the job succeeds. object_key is the RustFS
    -- key in the final design; in the Slice 2 in-process runner it temporarily
    -- holds a local file path (replaced by the real object store in Slice 3).
    object_key       text,
    byte_size        bigint,
    feature_count    integer,

    -- Failure message on 'failed'.
    error            text,

    -- Queue bookkeeping. attempts caps the crash-requeue loop; worker_id and
    -- lease_expires_at implement the heartbeat lease that reclaims a job whose
    -- worker died mid-run (status='running' AND lease_expires_at < now()).
    attempts         integer NOT NULL DEFAULT 0,
    worker_id        text,
    lease_expires_at timestamptz,

    created_at       timestamptz NOT NULL DEFAULT now(),
    started_at       timestamptz,
    finished_at      timestamptz
);

COMMENT ON TABLE ops.export_jobs IS 'Async export queue (Phase 17). One row per export request; workers claim with FOR UPDATE SKIP LOCKED. See docs/phase-17-large-export.md.';
COMMENT ON COLUMN ops.export_jobs.scope IS 'Resolved authz snapshot captured at enqueue: {format, feature_classes, bbox, territory_ids (expanded), cim_profile}. The worker exports exactly this; it never re-derives the caller''s permissions.';
COMMENT ON COLUMN ops.export_jobs.object_key IS 'Result location. RustFS object key in the worker design (Slice 3); a local temp-file path in the Slice 2 in-process runner.';
COMMENT ON COLUMN ops.export_jobs.lease_expires_at IS 'Heartbeat lease. A running job whose lease has lapsed is treated as a crashed worker''s and re-claimable.';

-- Claim path: find the oldest queued (or lease-expired running) job. A
-- (status, created_at) btree serves the ORDER BY created_at under the status
-- filter in the SKIP LOCKED select.
CREATE INDEX export_jobs_claim_idx ON ops.export_jobs (status, created_at);

-- History path: a user's own recent jobs, newest first.
CREATE INDEX export_jobs_owner_idx ON ops.export_jobs (requested_by, created_at DESC);
