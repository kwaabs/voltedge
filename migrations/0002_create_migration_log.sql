-- 0002_create_migration_log.sql
-- Purpose: manual migration history. Every file under /migrations gets one
-- row here once applied, written by migrations/apply.sh. No ORM
-- auto-migration tool involved on purpose -- this is the audit trail for
-- hand-written, reviewable SQL changes to the database itself. Separate
-- from metadata.metadata_versions (Phase 1), which tracks published
-- *metadata* versions, not raw schema migrations.

CREATE TABLE IF NOT EXISTS ops.migration_log (
    id              bigserial PRIMARY KEY,
    filename        text NOT NULL UNIQUE,
    description     text NOT NULL,
    checksum_sha256 text NOT NULL,
    applied_by      text NOT NULL DEFAULT current_user,
    applied_at      timestamptz NOT NULL DEFAULT now(),
    status          text NOT NULL DEFAULT 'applied' CHECK (status IN ('applied', 'rolled_back')),
    notes           text
);

COMMENT ON TABLE ops.migration_log IS 'Manual migration history: one row per SQL file under /migrations once applied. Written by migrations/apply.sh, never hand-edited.';
COMMENT ON COLUMN ops.migration_log.id IS 'Surrogate key, application order.';
COMMENT ON COLUMN ops.migration_log.filename IS 'Migration filename, e.g. 0003_create_metadata_schema.sql. Unique -- a given filename can only be applied once.';
COMMENT ON COLUMN ops.migration_log.description IS 'One-line human description of what the migration does, read from the file''s "-- Purpose:" header line.';
COMMENT ON COLUMN ops.migration_log.checksum_sha256 IS 'SHA-256 of the file contents at apply time, so drift between the file on disk and what actually ran is detectable later.';
COMMENT ON COLUMN ops.migration_log.applied_by IS 'Postgres role that ran the migration. Placeholder identity until real user accounts exist post-auth; historical rows will not be retroactively rewritten once auth lands.';
COMMENT ON COLUMN ops.migration_log.applied_at IS 'Timestamp the migration was applied.';
COMMENT ON COLUMN ops.migration_log.status IS 'applied (default) or rolled_back. Rollback is a manual, deliberate status flip -- there is no automatic down-migration mechanism.';
COMMENT ON COLUMN ops.migration_log.notes IS 'Free-text notes, e.g. why a migration was later rolled back.';
