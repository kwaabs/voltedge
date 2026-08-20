-- 0026_add_actor_audit_columns.sql
-- Purpose: Phase 10 Slice E -- give every compiled feature table the
-- created_by / updated_by audit columns the CRUD engine now stamps the
-- acting user's id into on write. The three pre-auth placeholder columns
-- (runtime.feature_connectivity.established_by,
-- runtime.feature_relationships.established_by,
-- metadata.metadata_versions.published_by) keep their text type; the API
-- simply writes the acting user's uuid into them going forward, and their
-- historical placeholder rows ("api", "compiler-cli") are left as-is and
-- never rewritten. This migration only adds the missing physical-table
-- columns and refreshes the affected column comments.
--
-- Physical feature tables are compiler-generated and dynamically named
-- (gis.pole, gis.mv_conductor, ...), so they can't be named statically --
-- loop the metadata catalog and ALTER each. New tables compiled after this
-- point already carry the columns (compiler.buildCreateTable), and the
-- compiler's recompile path backfills them the same way it does
-- deleted_at/name, so ADD COLUMN IF NOT EXISTS keeps this idempotent and
-- safe to co-exist with both. Plain uuid, deliberately NOT a FK to
-- iam.users: the same no-enforced-join decision as the *_by text columns,
-- and it keeps the gis schema free of a hard dependency on iam.

DO $$
DECLARE
    t text;
BEGIN
    FOR t IN
        SELECT physical_table
        FROM metadata.feature_classes
        WHERE physical_table IS NOT NULL
    LOOP
        EXECUTE format('ALTER TABLE %s ADD COLUMN IF NOT EXISTS created_by uuid', t);
        EXECUTE format('ALTER TABLE %s ADD COLUMN IF NOT EXISTS updated_by uuid', t);
    END LOOP;
END $$;

-- Refresh the placeholder columns' comments to reflect that the API now
-- writes a real actor's id into them (as text) going forward.
COMMENT ON COLUMN runtime.feature_connectivity.established_by IS 'The acting user''s id (iam.users.id, held as text) since Phase 10 Slice E. Pre-auth rows hold the fixed "api" placeholder literal and are not retroactively rewritten. Text, not a FK -- no enforced join, by design.';
COMMENT ON COLUMN runtime.feature_relationships.established_by IS 'The acting user''s id (iam.users.id, held as text) since Phase 10 Slice E. Pre-auth rows hold the fixed "api" placeholder literal and are not retroactively rewritten. Text, not a FK -- no enforced join, by design.';
COMMENT ON COLUMN metadata.metadata_versions.published_by IS 'The acting user''s id (iam.users.id, held as text) when published via the admin route since Phase 10 Slice E; the session-less compile CLI still writes the "compiler-cli" system literal. Pre-auth rows are not rewritten. Text, not a FK, by design.';
