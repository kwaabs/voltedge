-- 0145_create_external_feature_refs.sql
-- External-system references, the JSONB-on-the-feature design (both directions,
-- pull + push). A feature carries its ids in OTHER systems (SCADA / AMI / CIS) in
-- a single `external_ref` jsonb SYSTEM COLUMN (added by the compiler, like
-- deleted_at / name / territory_id -- see api/internal/compiler), shaped:
--
--   external_ref = { "<system_code>": <id> }
--     <system_code> references metadata.external_systems.code
--     <id> is EITHER a scalar string (the external id, e.g. "48213")
--          OR an object (e.g. {"id":"48213","point":"FDR7.CB1"}) whose "id"
--          field is the correlation id and whose other fields fill the URL
--          template (external_systems.path_template, e.g. /meters/{id}/consumption).
--
-- OUTBOUND (pull / display / build-url) reads external_ref straight off the feature
-- row (co-located, queryable, flexible). INBOUND (an external system PUSHES "device
-- 48213 ...": which feature?) needs a fast cross-class reverse lookup, which a jsonb
-- column spread over ~60 gis tables can't give cheaply -- so this migration adds a
-- TRIGGER-MAINTAINED reverse index, runtime.external_feature_refs, derived from
-- external_ref on every write. Exact same pattern as runtime.feature_registry
-- (migration 0108): one shared trigger function the compiler attaches per class, a
-- to_jsonb(NEW) read so one function fits every table (and is a safe no-op on a table
-- that doesn't have the external_ref column yet -- it is added by the compiler slice
-- that follows). external_ref is the SOURCE OF TRUTH; this table is just its index.

-- ---------------------------------------------------------------------------
-- 1. external_systems: the URL template filled from a feature's external_ref to
-- form the live-fetch request path (appended to the system's base_url by the
-- fetch proxy). Placeholders {name} are filled from the ref value's fields (or
-- {id}/the-scalar for a string ref). Nullable -- a system used only for inbound
-- correlation needs no outbound template.
-- ---------------------------------------------------------------------------
ALTER TABLE metadata.external_systems
    ADD COLUMN IF NOT EXISTS path_template text;

COMMENT ON COLUMN metadata.external_systems.path_template IS 'For outbound live-fetch: a path (relative to base_url) with {placeholder} fields filled from a feature''s external_ref value for this system, e.g. /meters/{id}/consumption. The fetch proxy validates the FILLED path is still relative (SSRF). Null for push-only systems.';

-- ---------------------------------------------------------------------------
-- 2. The reverse index: (system_code, external_id) -> the VoltEdge feature. PK on
-- (system_code, external_id) so a pushed external id resolves in one hit, across
-- every class. feature_class_code/feature_id are polymorphic (no FK into gis.*),
-- same convention as feature_registry / feature_connectivity.
-- ---------------------------------------------------------------------------
CREATE TABLE runtime.external_feature_refs (
    system_code        text        NOT NULL,
    external_id        text        NOT NULL,
    feature_class_code text        NOT NULL,
    feature_id         uuid        NOT NULL,
    updated_at         timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (system_code, external_id)
);

COMMENT ON TABLE runtime.external_feature_refs IS 'Trigger-maintained reverse index of external_ref: (external system, that system''s id) -> the VoltEdge feature it currently maps to. Lets an INBOUND push (SCADA/AMI/CIS sends its own id) resolve the feature in one PK hit across all classes. Derived from each gis row''s external_ref jsonb by runtime.sync_external_feature_refs; never written by hand. feature_class_code/feature_id are polymorphic (no FK).';

-- A feature's own refs (e.g. "clear every ref for this purged feature") and the
-- class-scoped backfill both read by (class, id).
CREATE INDEX external_feature_refs_feature_idx
    ON runtime.external_feature_refs (feature_class_code, feature_id);

-- ---------------------------------------------------------------------------
-- 3. The shared trigger function. Re-derives this feature's rows in the index from
-- its current external_ref on every INSERT/UPDATE, and clears them on DELETE. Reads
-- external_ref via to_jsonb(NEW) so ONE function fits every table shape -- including
-- a table that doesn't have the column yet (the key is absent -> NULL -> no-op),
-- exactly how 0108 reads territory_id. Class code arrives as TG_ARGV[0] (the gis row
-- has no code column). The external id for a system is the scalar ref, else its
-- "id" field; a ref with neither contributes no index row (outbound-only, e.g. a
-- system whose template needs no correlation).
-- ---------------------------------------------------------------------------
CREATE FUNCTION runtime.sync_external_feature_refs() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    refs jsonb;
    sys  text;
    val  jsonb;
    ext  text;
BEGIN
    IF TG_OP = 'DELETE' THEN
        DELETE FROM runtime.external_feature_refs
         WHERE feature_class_code = TG_ARGV[0] AND feature_id = OLD.id;
        RETURN OLD;
    END IF;

    -- Clear this feature's existing index rows, then rebuild from the current jsonb.
    -- (Simplest correct sync: a removed/renamed system key leaves no stale row.)
    DELETE FROM runtime.external_feature_refs
     WHERE feature_class_code = TG_ARGV[0] AND feature_id = NEW.id;

    -- A SOFT-DELETED feature must not stay in the index -- a deleted feature is hidden
    -- everywhere, and inbound correlation must not adopt one. The DELETE above already
    -- cleared its rows, so skip the re-insert when deleted_at is set (a later RESTORE
    -- re-runs this with deleted_at NULL and re-indexes). deleted_at via to_jsonb so
    -- the one function fits every table.
    IF (to_jsonb(NEW) ->> 'deleted_at') IS NOT NULL THEN
        RETURN NEW;
    END IF;

    refs := to_jsonb(NEW) -> 'external_ref';
    IF refs IS NOT NULL AND jsonb_typeof(refs) = 'object' THEN
        FOR sys, val IN SELECT key, value FROM jsonb_each(refs) LOOP
            IF jsonb_typeof(val) = 'string' THEN
                ext := val #>> '{}';
            ELSIF jsonb_typeof(val) = 'object' THEN
                ext := val ->> 'id';
            ELSE
                ext := NULL;
            END IF;
            IF ext IS NOT NULL AND ext <> '' THEN
                INSERT INTO runtime.external_feature_refs
                    (system_code, external_id, feature_class_code, feature_id)
                VALUES (sys, ext, TG_ARGV[0], NEW.id)
                ON CONFLICT (system_code, external_id) DO UPDATE SET
                    feature_class_code = EXCLUDED.feature_class_code,
                    feature_id         = EXCLUDED.feature_id,
                    updated_at         = now();
            END IF;
        END LOOP;
    END IF;
    RETURN NEW;
END;
$$;

COMMENT ON FUNCTION runtime.sync_external_feature_refs() IS 'AFTER INSERT/UPDATE/DELETE trigger on each gis.<code> table (attached by the compiler, beside the registry trigger). Rebuilds the feature''s runtime.external_feature_refs rows from its external_ref jsonb. Class code is TG_ARGV[0]; external_ref read via to_jsonb so one function fits every table and is a no-op where the column is absent.';

-- ---------------------------------------------------------------------------
-- 4. Attach to every already-compiled table (idempotent) so the index is live the
-- moment the compiler slice adds the external_ref column + recompiles. No backfill
-- needed: external_ref starts NULL on every existing row, so there is nothing to
-- index until a ref is set. The compiler attaches this trigger to each class from
-- here on (DROP IF EXISTS keeps re-attachment idempotent).
-- ---------------------------------------------------------------------------
DO $$
DECLARE
    r   record;
    tbl text;
BEGIN
    FOR r IN
        SELECT code, physical_table
        FROM metadata.feature_classes
        WHERE physical_table IS NOT NULL
    LOOP
        tbl := split_part(r.physical_table, '.', 2);
        EXECUTE format('DROP TRIGGER IF EXISTS %I ON %s', tbl || '_external_ref_sync', r.physical_table);
        EXECUTE format(
            'CREATE TRIGGER %I AFTER INSERT OR UPDATE OR DELETE ON %s '
            'FOR EACH ROW EXECUTE FUNCTION runtime.sync_external_feature_refs(%L)',
            tbl || '_external_ref_sync', r.physical_table, r.code
        );
    END LOOP;
END;
$$;
