-- 0153_add_export_jobs_asset_register.sql
-- Purpose: let ops.export_jobs carry asset-register builds alongside the Phase 17
-- geographic exports, so the register's async path reuses the same queue, worker,
-- heartbeat lease, poison reaper and retention sweep instead of duplicating them.
--
-- Two changes: a `kind` discriminator the worker dispatches on (runClaimedJob
-- branches geographic vs asset_register), and a widened `format` CHECK so a
-- register job can carry 'xlsx'. Every existing row is a geographic export.

ALTER TABLE ops.export_jobs
    ADD COLUMN kind text NOT NULL DEFAULT 'geographic'
        CHECK (kind IN ('geographic', 'asset_register'));

-- The format CHECK was declared inline on the column (0036), so Postgres named it
-- ops.export_jobs_format_check. Drop and re-add it with 'xlsx' allowed.
ALTER TABLE ops.export_jobs DROP CONSTRAINT export_jobs_format_check;
ALTER TABLE ops.export_jobs ADD CONSTRAINT export_jobs_format_check
    CHECK (format IN ('cim', 'kmz', 'shp', 'dxf', 'xlsx'));

COMMENT ON COLUMN ops.export_jobs.kind IS 'Job kind: geographic (the Phase 17 geometry export) or asset_register (the BoQ per-feature spreadsheet). The worker dispatches runClaimedJob on it; the asset-register path builds via an injected boq builder and uploads the .xlsx to the same RustFS bucket.';
