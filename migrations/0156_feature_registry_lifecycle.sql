-- 0156_feature_registry_lifecycle.sql
-- Purpose: mirror each feature's asset-lifecycle status into the cross-class
-- registry (0108) so the network gate -- trace & power flow -- can drop a
-- non-operational asset without knowing which gis.<code> table holds it. The
-- lifecycle_status axis is a system column the compiler puts on every gis.*
-- table (api/internal/crud/lifecycle.go, buildCreateTable); the conduction
-- queries walk runtime.feature_connectivity and probe runtime.feature_registry
-- for each endpoint's liveness (deleted_at) already -- adding lifecycle_status
-- there lets the same correlated-EXISTS probe also require the endpoint be in an
-- OPERATIONAL state (IN_SERVICE or DERATED), exactly as the soft-delete gate
-- requires deleted_at IS NULL.
--
-- Like 0118, this needs NO compiler change and NO recompile-all: every per-class
-- trigger calls the ONE shared runtime.sync_feature_registry(), and that function
-- reads columns generically via to_jsonb(NEW). Replacing the body updates the
-- sync for every attached class at once; a table that lacks the column (one not
-- yet recompiled for S1) yields NULL through ->>, defaulted to 'IN_SERVICE' here
-- so an un-recompiled class stays fully operational rather than silently dropping
-- out of the network.

ALTER TABLE runtime.feature_registry
    ADD COLUMN IF NOT EXISTS lifecycle_status text NOT NULL DEFAULT 'IN_SERVICE';

COMMENT ON COLUMN runtime.feature_registry.lifecycle_status IS 'Mirrors the gis row lifecycle_status (asset-lifecycle axis). The network gate (trace & power flow) treats IN_SERVICE/DERATED as operational -- a feature in any other state does not conduct. Defaults to IN_SERVICE for a row/class without the column (un-recompiled), the fail-operational direction.';

-- Replace the shared maintenance function so it also mirrors lifecycle_status.
-- Read generically via to_jsonb(NEW), coalesced to 'IN_SERVICE' so a table
-- without the column (not yet recompiled) stays operational. Every existing
-- per-class trigger calls this by name, so the change takes effect for all
-- classes immediately -- no re-attach, no recompile.
CREATE OR REPLACE FUNCTION runtime.sync_feature_registry() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        DELETE FROM runtime.feature_registry WHERE feature_id = OLD.id;
        RETURN OLD;
    END IF;
    INSERT INTO runtime.feature_registry
        (feature_id, feature_class_code, status, deleted_at, created_at, territory_id, created_by, published_at, lifecycle_status)
    VALUES (
        NEW.id, TG_ARGV[0], NEW.status, NEW.deleted_at, NEW.created_at,
        (to_jsonb(NEW) ->> 'territory_id')::uuid,
        to_jsonb(NEW) ->> 'created_by',
        (to_jsonb(NEW) ->> 'published_at')::timestamptz,
        COALESCE(to_jsonb(NEW) ->> 'lifecycle_status', 'IN_SERVICE')
    )
    ON CONFLICT (feature_id) DO UPDATE SET
        feature_class_code = EXCLUDED.feature_class_code,
        status             = EXCLUDED.status,
        deleted_at         = EXCLUDED.deleted_at,
        territory_id       = EXCLUDED.territory_id,
        created_by         = EXCLUDED.created_by,
        published_at       = EXCLUDED.published_at,
        lifecycle_status   = EXCLUDED.lifecycle_status;
    RETURN NEW;
END;
$$;

-- Backfill lifecycle_status for every existing registry row from its gis table.
-- Built per table because a table may lack the column (a class not yet
-- recompiled for S1); an absent column contributes 'IN_SERVICE' -- the
-- fail-operational direction, so no asset drops out of the network until its
-- real status is known. Idempotent: re-runnable, and the trigger keeps it fresh
-- from the next write on.
DO $$
DECLARE
    r        record;
    ls_expr  text;
BEGIN
    FOR r IN
        SELECT code, physical_table
        FROM metadata.feature_classes
        WHERE physical_table IS NOT NULL
    LOOP
        IF EXISTS (
            SELECT 1 FROM information_schema.columns
            WHERE table_schema = 'gis'
              AND table_name = split_part(r.physical_table, '.', 2)
              AND column_name = 'lifecycle_status'
        ) THEN
            ls_expr := 'COALESCE(lifecycle_status, ''IN_SERVICE'')';
        ELSE
            ls_expr := '''IN_SERVICE''::text';
        END IF;

        EXECUTE format(
            'UPDATE runtime.feature_registry AS fr '
            'SET lifecycle_status = src.ls '
            'FROM (SELECT id, %s AS ls FROM %s) AS src '
            'WHERE fr.feature_id = src.id',
            ls_expr, r.physical_table
        );
    END LOOP;
END;
$$;
