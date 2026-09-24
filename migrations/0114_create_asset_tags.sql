-- 0114_create_asset_tags.sql
-- Purpose: the asset-tagging module's store. Every feature gets a short,
-- scannable tag code (printed as a QR + Code128 + human-readable label and
-- affixed on the physical asset in the field); a scan resolves the code back to
-- the GIS record. A feature may carry MANY codes -- the system-minted one plus
-- any pre-existing/legacy asset numbers registered against it -- with exactly
-- one marked primary (the one that gets printed).
--
-- WHY MINT OFF runtime.feature_registry (migration 0108), NOT a per-class
-- trigger the compiler attaches:
--   The registry is ALREADY the one table that sees every feature the instant
--   it is written, by any path -- the REST API, MCP tools, field-sync replay,
--   and (heavily) raw-SQL seeds that INSERT straight into gis.* -- because the
--   compiler attaches a per-class AFTER INSERT/UPDATE/DELETE trigger to every
--   gis.<code> table that upserts the registry row. So a SINGLE trigger on
--   runtime.feature_registry inherits that total coverage for free: one new
--   feature -> one registry INSERT -> one minted tag. No compiler change, no
--   per-class attachment to keep in step. (A registry re-sync is an UPDATE, not
--   an INSERT, so re-syncs never re-mint.)
--
-- No FK from asset_tags to the gis row: feature_id spans every gis.<code> table
-- and Postgres has no cross-table FK -- the same polymorphic-reference reason as
-- runtime.feature_registry and runtime.feature_connectivity. A tag whose feature
-- has been purged simply stops resolving (the resolve path joins through the
-- registry, which the purge removed), and the DELETE branch below tidies it up.

-- ---------------------------------------------------------------------------
-- Code generation. A tag code is  VE-<base32 body><check char>  where the body
-- is a global sequence value in Crockford base32 (the alphabet drops I, L, O, U
-- so a human reading a printed label can't confuse them) and the trailing check
-- character catches a single mis-keyed/mis-read character.
--
-- The check character is a position-weighted sum of the body's digits mod 32,
-- mapped back through the SAME 32-character alphabet -- deliberately NOT
-- Crockford's mod-37 check, whose extra symbols (* ~ $ = U) would be ugly on a
-- label and need escaping in the QR deep-link URL. Keeping every character in
-- the clean base32 set makes the whole code URL- and label-safe by construction.
-- ---------------------------------------------------------------------------

CREATE SEQUENCE runtime.asset_tag_seq;

CREATE FUNCTION runtime.encode_base32_crockford(n bigint) RETURNS text
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE
    alphabet constant text := '0123456789ABCDEFGHJKMNPQRSTVWXYZ';  -- no I L O U
    out text := '';
    v   bigint := n;
BEGIN
    IF v < 0 THEN
        RAISE EXCEPTION 'encode_base32_crockford: negative input %', n;
    END IF;
    IF v = 0 THEN
        RETURN '0';
    END IF;
    WHILE v > 0 LOOP
        out := substr(alphabet, (v % 32)::int + 1, 1) || out;
        v := v / 32;
    END LOOP;
    RETURN out;
END;
$$;

CREATE FUNCTION runtime.asset_tag_check_char(body text) RETURNS text
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE
    alphabet constant text := '0123456789ABCDEFGHJKMNPQRSTVWXYZ';
    total int := 0;
    i     int;
    pos   int;
BEGIN
    FOR i IN 1 .. length(body) LOOP
        pos := strpos(alphabet, substr(body, i, 1)) - 1;  -- 0-based digit value
        total := total + pos * i;                          -- weight by position
    END LOOP;
    RETURN substr(alphabet, (total % 32) + 1, 1);
END;
$$;

-- The one function the mint trigger and any reissue call: sequence value -> code.
-- 'VE-' is the default prefix; a later configurable label-template phase can
-- supersede it, but every code minted here carries it.
CREATE FUNCTION runtime.encode_asset_tag(n bigint) RETURNS text
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE
    body text := runtime.encode_base32_crockford(n);
BEGIN
    RETURN 'VE-' || body || runtime.asset_tag_check_char(body);
END;
$$;

-- ---------------------------------------------------------------------------
-- The store.
-- ---------------------------------------------------------------------------

CREATE TABLE runtime.asset_tags (
    tag_code           text PRIMARY KEY,          -- globally unique across all features and statuses; a voided code is never reused
    feature_class_code text        NOT NULL,      -- the gis.<code> class the feature lives in
    feature_id         uuid        NOT NULL,      -- the gis row id (globally unique gen_random_uuid); no FK (polymorphic)
    source             text        NOT NULL DEFAULT 'generated'
                       CHECK (source IN ('generated', 'external')),  -- system-minted vs a registered legacy/external number
    is_primary         boolean     NOT NULL DEFAULT false,           -- the one printed on the asset; at most one per feature
    status             text        NOT NULL DEFAULT 'active'
                       CHECK (status IN ('active', 'void', 'superseded')),
    print_count        integer     NOT NULL DEFAULT 0,               -- how many times a label was rendered (S3)
    created_by         uuid,                                          -- the acting user for a registered/reissued tag; NULL for a trigger-minted one
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE runtime.asset_tags IS 'Scannable asset tags (QR/Code128/human-readable) affixed on physical plant. One feature may carry many codes (system-minted + registered legacy numbers); exactly one is primary. Trigger-minted off runtime.feature_registry; resolved back to the feature through it. No FK to the gis row (polymorphic feature_id).';
COMMENT ON COLUMN runtime.asset_tags.is_primary IS 'The single code that gets printed for this feature. Enforced unique-per-feature by asset_tags_one_primary.';
COMMENT ON COLUMN runtime.asset_tags.status IS 'active = live; void = retired/wrong (does not resolve); superseded = replaced by a reissue but still resolves (old labels linger in the field).';

-- Resolve / list-for-feature access patterns.
CREATE INDEX asset_tags_feature_idx  ON runtime.asset_tags (feature_class_code, feature_id);
CREATE INDEX asset_tags_feature_id_idx ON runtime.asset_tags (feature_id);
-- At most one primary tag per feature. Partial so it constrains only the flagged
-- rows; a feature with a voided ex-primary and a new primary is fine.
CREATE UNIQUE INDEX asset_tags_one_primary ON runtime.asset_tags (feature_id) WHERE is_primary;

-- ---------------------------------------------------------------------------
-- The single mint/cleanup trigger on the registry.
-- ---------------------------------------------------------------------------

CREATE FUNCTION runtime.sync_asset_tag() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        -- A registry row is removed only by a hard delete (purge) of the gis
        -- row -- a soft delete is an UPDATE that sets deleted_at and keeps the
        -- registry row, so a soft-deleted asset KEEPS the label already on it in
        -- the field. On a purge, drop the now-danglings tags.
        DELETE FROM runtime.asset_tags WHERE feature_id = OLD.feature_id;
        RETURN OLD;
    END IF;
    -- INSERT: a feature just entered the registry -> mint its one primary tag.
    -- Guard so a re-appearing id (belt-and-braces; the UPDATE path this trigger
    -- is NOT attached to already handles re-syncs) never double-mints.
    IF EXISTS (SELECT 1 FROM runtime.asset_tags WHERE feature_id = NEW.feature_id) THEN
        RETURN NEW;
    END IF;
    INSERT INTO runtime.asset_tags
        (tag_code, feature_class_code, feature_id, source, is_primary, status)
    VALUES (runtime.encode_asset_tag(nextval('runtime.asset_tag_seq')),
            NEW.feature_class_code, NEW.feature_id, 'generated', true, 'active')
    ON CONFLICT (tag_code) DO NOTHING;
    RETURN NEW;
END;
$$;

COMMENT ON FUNCTION runtime.sync_asset_tag() IS 'AFTER INSERT/DELETE trigger on runtime.feature_registry. On INSERT mints one primary generated tag for the new feature; on DELETE (feature purge) removes its tags. Rides the registry''s total write coverage, so no per-class trigger is needed.';

CREATE TRIGGER feature_registry_tag_sync
    AFTER INSERT OR DELETE ON runtime.feature_registry
    FOR EACH ROW EXECUTE FUNCTION runtime.sync_asset_tag();

-- ---------------------------------------------------------------------------
-- Backfill: every feature that already exists (the registry is complete as of
-- migration 0108 + the compiler's per-class backfill) gets its primary tag now,
-- so the module is populated the moment this migration lands, not only for
-- features created afterward. Reads the registry, the canonical "every feature
-- across every class" list -- exactly the substrate the trigger mints from.
-- ---------------------------------------------------------------------------

INSERT INTO runtime.asset_tags
    (tag_code, feature_class_code, feature_id, source, is_primary, status)
SELECT runtime.encode_asset_tag(nextval('runtime.asset_tag_seq')),
       fr.feature_class_code, fr.feature_id, 'generated', true, 'active'
FROM runtime.feature_registry fr
WHERE NOT EXISTS (
    SELECT 1 FROM runtime.asset_tags t WHERE t.feature_id = fr.feature_id
);
