-- 0001_create_ops_schema.sql
-- Purpose: create the 'ops' schema -- home for VoltEdge's own operational
-- bookkeeping tables (migration history now, job/audit tables later).
-- Kept separate from 'metadata' (the schema compiler's own tables, Phase 1)
-- and from every schema the supabase/postgres image reserves for itself
-- (auth, storage, realtime, extensions, graphql, vault, and friends).

CREATE SCHEMA IF NOT EXISTS ops;

COMMENT ON SCHEMA ops IS 'VoltEdge operational bookkeeping: migration history and infra-level audit tables. Not part of the metadata-driven application schema.';
