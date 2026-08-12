-- 0006_create_metadata_versions.sql
-- Purpose: metadata_versions -- publish history for the schema compiler
-- itself. Distinct from a single feature row's draft/published state
-- (that's data-level, per-row, frequent); this is schema-level,
-- admin-only, infrequent. feature_classes.metadata_version_id points
-- here once compiled DDL has actually been applied for that version.

CREATE TABLE metadata.metadata_versions (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    version_no   integer GENERATED ALWAYS AS IDENTITY,
    published_at timestamptz,
    published_by text,
    status       text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'active', 'archived')),
    created_at   timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE metadata.metadata_versions IS 'One row per schema-compiler publish cycle: draft metadata edits get validated, compiled to DDL, applied, then this flips to active and everything downstream (API cache, Martin config, symbology) reloads. Keeps full history on purpose -- utility compliance audits need "what schema was in effect when this feature was created."';
COMMENT ON COLUMN metadata.metadata_versions.version_no IS 'Sequential, human-readable version number. Auto-generated, never reused.';
COMMENT ON COLUMN metadata.metadata_versions.published_at IS 'When DDL for this version was successfully applied. Null while still draft.';
COMMENT ON COLUMN metadata.metadata_versions.published_by IS 'Placeholder identity of who published -- text, not a FK, until real user accounts exist post-auth.';
COMMENT ON COLUMN metadata.metadata_versions.status IS 'draft (being authored) -> active (compiled, applied, live) -> archived (superseded by a later version). Only one row should be active at a time; not yet enforced by a constraint, the compiler owns that invariant.';
