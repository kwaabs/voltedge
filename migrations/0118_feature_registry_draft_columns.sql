-- 0118_feature_registry_draft_columns.sql
-- Purpose: let the cross-class feature registry (0108) enforce the Phase-61
-- private-drafts filter, closing an id-only leak. Before this, the registry
-- carried `status` but not `created_by` / `published_at`, so its read endpoint
-- (api/internal/featureregistry) could only territory-scope (Phase 58) -- it
-- could still surface the id + class + existence of a peer's UNPUBLISHED private
-- draft that fell inside the caller's own territories. Attributes/geometry were
-- always safe (the CRUD/tiles/export/pack read paths gate them), but the bare id
-- was enumerable. Adding the two draft columns lets the registry apply the exact
-- same author-visibility predicate crud.enforceReadScope + drafts.WhereClause use.
--
-- Crucially this needs NO compiler change and NO recompile-all: the per-class
-- triggers the compiler attaches all call the ONE shared function
-- runtime.sync_feature_registry(), and that function reads columns generically
-- via to_jsonb(NEW). Replacing the function body (below) updates the sync
-- behaviour for every already-attached class at once -- geometry-less tables that
-- lack these columns simply yield NULL through ->> , exactly as territory_id
-- already does.

ALTER TABLE runtime.feature_registry
    ADD COLUMN IF NOT EXISTS created_by   text,        -- mirrors the gis row's author (Phase-61 draft visibility)
    ADD COLUMN IF NOT EXISTS published_at timestamptz; -- non-null once ever published: the sticky "is public" marker

COMMENT ON COLUMN runtime.feature_registry.created_by IS 'Mirrors the gis row created_by -- the draft author. With published_at + status it drives the Phase-61 private-drafts visibility predicate on the registry read path. NULL for rows/classes without the column.';
COMMENT ON COLUMN runtime.feature_registry.published_at IS 'Mirrors the gis row published_at (sticky: set once and kept). Non-null means the feature is/was public, so it is visible to everyone regardless of author -- the same rule drafts.WhereClause applies.';

-- Replace the shared maintenance function so it also mirrors created_by and
-- published_at. Read generically via to_jsonb(NEW) so the one function still
-- serves every table shape (a table without the column yields NULL). Every
-- existing per-class trigger calls this by name, so the change takes effect for
-- all classes immediately -- no re-attach, no recompile.
CREATE OR REPLACE FUNCTION runtime.sync_feature_registry() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        DELETE FROM runtime.feature_registry WHERE feature_id = OLD.id;
        RETURN OLD;
    END IF;
    INSERT INTO runtime.feature_registry
        (feature_id, feature_class_code, status, deleted_at, created_at, territory_id, created_by, published_at)
    VALUES (
        NEW.id, TG_ARGV[0], NEW.status, NEW.deleted_at, NEW.created_at,
        (to_jsonb(NEW) ->> 'territory_id')::uuid,
        to_jsonb(NEW) ->> 'created_by',
        (to_jsonb(NEW) ->> 'published_at')::timestamptz
    )
    ON CONFLICT (feature_id) DO UPDATE SET
        feature_class_code = EXCLUDED.feature_class_code,
        status             = EXCLUDED.status,
        deleted_at         = EXCLUDED.deleted_at,
        territory_id       = EXCLUDED.territory_id,
        created_by         = EXCLUDED.created_by,
        published_at       = EXCLUDED.published_at;
    RETURN NEW;
END;
$$;

-- Backfill the two new columns for every existing registry row from its gis
-- table. Built per table because a table may lack either column (geometry-less
-- classes, or classes predating Phase 61); an absent column contributes NULL,
-- which the visibility predicate treats as "not published" / "no author" -- the
-- fail-closed direction, matching enforceDraftScope on a scanned row.
DO $$
DECLARE
    r          record;
    tbl        text;
    cb_expr    text;
    pub_expr   text;
BEGIN
    FOR r IN
        SELECT code, physical_table
        FROM metadata.feature_classes
        WHERE physical_table IS NOT NULL
    LOOP
        tbl := split_part(r.physical_table, '.', 2);

        IF EXISTS (
            SELECT 1 FROM information_schema.columns
            WHERE table_schema = 'gis' AND table_name = tbl AND column_name = 'created_by'
        ) THEN
            cb_expr := 'created_by::text';
        ELSE
            cb_expr := 'NULL::text';
        END IF;

        IF EXISTS (
            SELECT 1 FROM information_schema.columns
            WHERE table_schema = 'gis' AND table_name = tbl AND column_name = 'published_at'
        ) THEN
            pub_expr := 'published_at';
        ELSE
            pub_expr := 'NULL::timestamptz';
        END IF;

        EXECUTE format(
            'UPDATE runtime.feature_registry AS fr '
            'SET created_by = src.cb, published_at = src.pub '
            'FROM (SELECT id, %s AS cb, %s AS pub FROM %s) AS src '
            'WHERE fr.feature_id = src.id',
            cb_expr, pub_expr, r.physical_table
        );
    END LOOP;
END;
$$;
