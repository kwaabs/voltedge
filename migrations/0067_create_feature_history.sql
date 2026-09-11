-- 0067_create_feature_history.sql
-- Purpose: Phase 41 -- an append-only audit trail of every change to every
-- feature, so a feature's full history (who changed it, when, and what it
-- looked like after each change) is recoverable. Until now the feature tables
-- carried only "latest change" audit columns (created_by/at, updated_by/at,
-- migration 0026); an edit overwrote the prior values in place and they were
-- lost. This table keeps one immutable row per write.
--
-- One shared table for every feature class (not a per-class *_history table):
-- feature_class_code + feature_id identify the feature, and the full
-- post-change state is stored as a jsonb snapshot -- the same envelope the API
-- returns for a GET -- so no per-class schema and no compiler change. The write
-- path (api/internal/crud) inserts a row from create/update/publish/unpublish/
-- delete/restore/purge, in the same connection as the feature write.
--
-- Deliberately NO foreign keys:
--   * feature_id can't FK to one table (it spans every gis.* feature table);
--   * a purge (hard delete) must NOT cascade away the history of the feature it
--     removes -- the audit trail outlives the row;
--   * actor_id is a soft reference to iam.users (resolved by LEFT JOIN at read
--     time), left as a bare uuid so deleting a user never erases their history.

CREATE TABLE gis.feature_history (
    id                 bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    feature_class_code text        NOT NULL,
    feature_id         uuid        NOT NULL,
    -- create | update | publish | unpublish | delete | restore | purge
    action             text        NOT NULL,
    actor_id           uuid,       -- who did it; NULL for a system/CLI write
    at                 timestamptz NOT NULL DEFAULT now(),
    snapshot           jsonb       NOT NULL  -- full feature state after the change
);

-- The one access pattern: a single feature's timeline, newest first.
CREATE INDEX feature_history_feature_idx
    ON gis.feature_history (feature_class_code, feature_id, at DESC);
