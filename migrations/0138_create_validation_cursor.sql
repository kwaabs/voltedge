-- 0138_create_validation_cursor.sql
-- Purpose: the tail cursor for the incremental validation worker (Step 3 of
-- incremental validation, api/internal/connectivity/sweep.go). The worker treats
-- gis.feature_history -- which already records one row per feature write, with an
-- auto-increment id -- as the DIRTY-FEATURE LOG: it reads the history rows newer than
-- this cursor, re-validates just those features (the scoped audit from Step 1 +
-- reconcile from Step 2), and advances the cursor. So every edit automatically
-- re-validates its feature, with ZERO change to the write path.
--
-- One row, enforced by a boolean primary key fixed to true. Re-processing a batch is
-- harmless (ReconcileIssues is idempotent), so the worker needs no lease -- the cursor
-- only ever moves forward.

CREATE TABLE ops.validation_cursor (
    singleton       boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    last_history_id bigint NOT NULL DEFAULT 0,
    updated_at      timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE ops.validation_cursor IS 'Single-row tail cursor for the incremental validation worker: the highest gis.feature_history.id whose feature has been re-validated. The worker reads history rows above it, re-validates those features (scoped audit + reconcile), and advances it.';

-- The single row the worker reads/advances.
INSERT INTO ops.validation_cursor (singleton) VALUES (true);
