-- 0039_create_field_packs.sql
-- Purpose: create ops.field_packs -- the async build queue for the Phase 21
-- field tool (docs/phase-21-field-tool.md). A field tech asks cmd/fieldsync for
-- an offline "region pack" covering a bbox + set of feature classes; a build is
-- enqueued here, a worker claims it (FOR UPDATE SKIP LOCKED, exactly like the
-- Phase 17 export queue in ops.export_jobs), reads the scope under one
-- REPEATABLE READ snapshot, and produces TWO artifacts into RustFS:
--
--   * a content-pruned MBTiles vector tileset (tippecanoe) for fast offline
--     display, and
--   * a plain SQLite "data pack" (full-precision WKB geometry + attributes +
--     the connectivity/relationship graph + a metadata snapshot) that is the
--     on-device edit truth.
--
-- Hence two object keys / byte sizes on one row (unlike an export job's single
-- artifact). snapshot_at records the exact point-in-time the pack reflects, so a
-- future delta refresh (Slice 6) can return only features changed since.
--
-- Modeled column-for-column on ops.export_jobs (migration 0036): same status /
-- lease / heartbeat / attempts machinery so internal/fieldpack can reuse the
-- export queue's claim/heartbeat/complete/fail/cancel shape. requested_by is the
-- iam.users owner (ON DELETE RESTRICT, matching export_jobs): a pack belongs to
-- the user who built it, and every pack + sync call authenticates as that user
-- and runs under exactly their RBAC permissions -- no new authorization surface.

CREATE TABLE ops.field_packs (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    status       text NOT NULL DEFAULT 'queued'
                     CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'cancelled')),
    requested_by uuid NOT NULL REFERENCES iam.users(id) ON DELETE RESTRICT,

    -- Resolved build request: {bbox:[minX,minY,maxX,maxY], feature_classes:[...],
    -- territory_ids:[...]}. The authz snapshot captured at enqueue (matching the
    -- export scope pattern); the worker builds exactly this and never re-derives
    -- the caller's rights.
    scope        jsonb NOT NULL,

    -- Two artifacts, two keys/sizes -- NULL until the build succeeds.
    tiles_object_key text,
    data_object_key  text,
    tiles_bytes      bigint,
    data_bytes       bigint,

    feature_count int,
    -- The transaction snapshot instant the pack reflects (set at build). A delta
    -- refresh asks "what changed since this?" -- so it is recorded even though
    -- v1 refreshes by full re-download.
    snapshot_at   timestamptz,
    error         text,

    attempts         int NOT NULL DEFAULT 0,
    worker_id        text,
    lease_expires_at timestamptz,

    created_at  timestamptz NOT NULL DEFAULT now(),
    started_at  timestamptz,
    finished_at timestamptz
);

-- Claim order: the worker scans queued (and lease-expired running) jobs oldest
-- first, exactly like the export queue.
CREATE INDEX field_packs_status_created_idx ON ops.field_packs (status, created_at);
-- History: a device lists its owner's recent packs, newest first.
CREATE INDEX field_packs_requested_by_idx ON ops.field_packs (requested_by, created_at DESC);

COMMENT ON TABLE ops.field_packs IS 'Async build queue for Phase 21 field-tool offline region packs. A build produces two RustFS artifacts (an MBTiles display tileset + a SQLite edit-truth data pack) for a bbox + class set, under one REPEATABLE READ snapshot. Same SKIP LOCKED / lease machinery as ops.export_jobs. See docs/phase-21-field-tool.md.';
COMMENT ON COLUMN ops.field_packs.scope IS 'Resolved build request captured at enqueue: {bbox, feature_classes, territory_ids}. The authz snapshot -- the worker builds exactly this.';
COMMENT ON COLUMN ops.field_packs.tiles_object_key IS 'RustFS key of the content-pruned MBTiles vector tileset (tippecanoe output) for offline display. NULL until the build succeeds.';
COMMENT ON COLUMN ops.field_packs.data_object_key IS 'RustFS key of the SQLite data pack (WKB geometry + attributes + connectivity/relationship graph + metadata) -- the on-device edit truth. NULL until the build succeeds.';
COMMENT ON COLUMN ops.field_packs.snapshot_at IS 'The transaction snapshot instant this pack reflects. Recorded so a future delta refresh can return only features changed since; v1 refreshes by full re-download.';
