-- 0108_create_feature_registry.sql
-- Purpose: a single cross-class index of every feature row -- one
-- (feature_class_code, feature_id) per feature in any gis.<code> table --
-- so a lookup by id (the asset-id picker, or a cross-subsystem reference
-- like faults/oms/wam that holds a bare (class, id)) resolves from ONE
-- indexed table instead of having to know which per-class gis.* table to
-- probe.
--
-- Kept in sync by a DATABASE TRIGGER that the compiler attaches to every
-- gis.<code> table (see api/internal/compiler), NOT by the Go CRUD layer.
-- Features are written through many paths in this system -- the REST API,
-- field-sync replay, MCP tools, and (heavily) raw-SQL seeds that INSERT
-- straight into gis.* -- and only a DB-level trigger catches all of them.
-- An app-level sync would silently drift the moment a seed ran.
--
-- feature_id is the PRIMARY KEY: gis ids are gen_random_uuid(), globally
-- unique across classes, so "type an id -> what is it?" is a single PK hit.
-- No FK to the per-class table -- feature_id spans every gis.* table and
-- Postgres has no cross-table FK (the same polymorphic-reference reason as
-- runtime.feature_connectivity, migration 0016). Existence is guaranteed by
-- the trigger, not a constraint.

CREATE TABLE runtime.feature_registry (
    feature_id         uuid PRIMARY KEY,
    feature_class_code text        NOT NULL,
    status             text,                       -- draft | published (mirrors the gis row)
    deleted_at         timestamptz,                -- non-null once soft-deleted; the picker filters on this
    created_at         timestamptz,
    territory_id       uuid,                        -- geometric classes only; NULL for geometry-less ones
    registered_at      timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE runtime.feature_registry IS 'One row per feature across every compiled gis.<code> table -- a cross-class (feature_class_code, feature_id) index for id lookups and asset pickers. Maintained by a per-class trigger the compiler attaches (runtime.sync_feature_registry); never written by hand.';
COMMENT ON COLUMN runtime.feature_registry.feature_id IS 'The gis row id (globally unique gen_random_uuid). PK, so a lookup by id alone identifies the class. No FK -- the target table varies by class.';
COMMENT ON COLUMN runtime.feature_registry.deleted_at IS 'Mirrors the gis row: a soft delete sets this (row still exists), so a picker excludes it via deleted_at IS NULL. A purge (hard delete) removes this registry row outright.';

-- The live-asset picker's access pattern: existing features of a class.
CREATE INDEX feature_registry_class_idx
    ON runtime.feature_registry (feature_class_code) WHERE deleted_at IS NULL;
-- Phase 58 spatial scoping of a picker to a user's territories.
CREATE INDEX feature_registry_territory_idx
    ON runtime.feature_registry (territory_id) WHERE territory_id IS NOT NULL;

-- The shared trigger function every gis.<code> table's registry trigger
-- calls. The class code is passed as the trigger argument (TG_ARGV[0])
-- because the gis row carries no code column. territory_id is read
-- generically via to_jsonb(NEW) -- geometry-less tables lack that column and
-- ->> yields NULL when the key is absent, so ONE function serves every table
-- shape without per-class branching.
CREATE FUNCTION runtime.sync_feature_registry() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        DELETE FROM runtime.feature_registry WHERE feature_id = OLD.id;
        RETURN OLD;
    END IF;
    INSERT INTO runtime.feature_registry
        (feature_id, feature_class_code, status, deleted_at, created_at, territory_id)
    VALUES (
        NEW.id, TG_ARGV[0], NEW.status, NEW.deleted_at, NEW.created_at,
        (to_jsonb(NEW) ->> 'territory_id')::uuid
    )
    ON CONFLICT (feature_id) DO UPDATE SET
        feature_class_code = EXCLUDED.feature_class_code,
        status             = EXCLUDED.status,
        deleted_at         = EXCLUDED.deleted_at,
        territory_id       = EXCLUDED.territory_id;
    RETURN NEW;
END;
$$;

COMMENT ON FUNCTION runtime.sync_feature_registry() IS 'AFTER INSERT/UPDATE/DELETE trigger on each gis.<code> table (attached by the compiler). Upserts / removes the matching runtime.feature_registry row. Class code arrives as TG_ARGV[0]; territory_id is read via to_jsonb so one function fits geometric and geometry-less tables alike.';

-- Attach the trigger to every already-compiled table and backfill its
-- existing rows, so the registry is complete the moment this migration lands
-- -- not only for classes compiled or recompiled afterward (the compiler
-- attaches + backfills each class from here on). The territory expression is
-- built per table because geometry-less tables have no territory_id column.
DO $$
DECLARE
    r         record;
    tbl       text;
    terr_expr text;
BEGIN
    FOR r IN
        SELECT code, physical_table
        FROM metadata.feature_classes
        WHERE physical_table IS NOT NULL
    LOOP
        tbl := split_part(r.physical_table, '.', 2);

        EXECUTE format('DROP TRIGGER IF EXISTS %I ON %s', tbl || '_registry_sync', r.physical_table);
        EXECUTE format(
            'CREATE TRIGGER %I AFTER INSERT OR UPDATE OR DELETE ON %s '
            'FOR EACH ROW EXECUTE FUNCTION runtime.sync_feature_registry(%L)',
            tbl || '_registry_sync', r.physical_table, r.code
        );

        IF EXISTS (
            SELECT 1 FROM information_schema.columns
            WHERE table_schema = 'gis' AND table_name = tbl AND column_name = 'territory_id'
        ) THEN
            terr_expr := 'territory_id';
        ELSE
            terr_expr := 'NULL::uuid';
        END IF;

        EXECUTE format(
            'INSERT INTO runtime.feature_registry '
            '(feature_id, feature_class_code, status, deleted_at, created_at, territory_id) '
            'SELECT id, %L, status, deleted_at, created_at, %s FROM %s '
            'ON CONFLICT (feature_id) DO NOTHING',
            r.code, terr_expr, r.physical_table
        );
    END LOOP;
END;
$$;
