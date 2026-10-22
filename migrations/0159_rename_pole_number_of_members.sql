-- 0159_rename_pole_number_of_members.sql
-- Purpose: fix a typo'd field name on the POLE class -- number_ofmembers ->
-- number_of_members. The field was added at runtime through the admin metadata
-- UI (it is in no seed), so it exists only as two things that must stay in step:
-- the metadata.fields row and the compiled physical column gis.pole."...". The
-- compiler is additive-only (no rename path) and there is no delete-field
-- endpoint, so neither the admin UI nor a recompile can do this -- it has to be a
-- direct, paired rename here.
--
-- Safe and data-preserving: the column is a plain nullable integer with no domain
-- and therefore no CHECK constraint to carry along, and RENAME COLUMN keeps any
-- existing values. After this runs the metadata and the physical table already
-- agree, so a later `recompile POLE` is a no-op for this field (it sees the
-- column present and adds nothing). No recompile is required.
--
-- Guarded/idempotent: the metadata UPDATE matches only the old name, and the
-- column rename runs only if the old column still exists and the new one does
-- not, so re-running (or running against an already-fixed DB) is harmless.

-- 1. The metadata field record (scoped to the POLE class via its code, so this
--    can never touch a same-named field on another class).
UPDATE metadata.fields
SET name = 'number_of_members'
WHERE name = 'number_ofmembers'
  AND feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'POLE');

-- 2. The compiled physical column on gis.pole.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'gis' AND table_name = 'pole' AND column_name = 'number_ofmembers'
    ) AND NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'gis' AND table_name = 'pole' AND column_name = 'number_of_members'
    ) THEN
        ALTER TABLE gis.pole RENAME COLUMN number_ofmembers TO number_of_members;
    END IF;
END $$;
