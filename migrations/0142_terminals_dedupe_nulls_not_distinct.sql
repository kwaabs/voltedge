-- 0142_terminals_dedupe_nulls_not_distinct.sql
--
-- Remove duplicate terminal rows and stop them ever recurring.
--
-- ROOT CAUSE: metadata.terminals had UNIQUE (feature_class_id, subtype_id, code),
-- but a plain UNIQUE treats NULL subtype_id as DISTINCT -- so a generic
-- (subtype-less) terminal could be inserted over and over, and the seeds'
-- `ON CONFLICT (feature_class_id, subtype_id, code) DO NOTHING` never matched one
-- either. Re-applying a class seed therefore accumulated duplicate T1/T2/... rows
-- (e.g. MV_RECLOSER showing T1 six times).
--
-- Terminal ids are referenced by metadata.network_rules, metadata.snapping_rules
-- and runtime.feature_connectivity, so duplicates can't simply be deleted -- each
-- reference is first RE-POINTED to the surviving (canonical) terminal of its
-- group, which is safe because duplicates are byte-for-byte the same terminal.
--
-- Wrapped in an explicit transaction so the whole dedupe is atomic AND the
-- ON COMMIT DROP temp table survives across statements (the migration runner
-- autocommits each statement otherwise, which drops it immediately).

BEGIN;

-- 1. Canonical (keep) terminal per (class, subtype[null-aware], code) group:
--    the earliest-created, lowest-id row. Every other row in the group is a dup.
CREATE TEMP TABLE _terminal_canon ON COMMIT DROP AS
SELECT t.id AS dup_id,
       first_value(t.id) OVER (
           PARTITION BY t.feature_class_id, COALESCE(t.subtype_id::text, '~null~'), t.code
           ORDER BY t.created_at, t.id
       ) AS keep_id
FROM metadata.terminals t;

-- 2. Re-point every terminal reference from a duplicate to its canonical.
UPDATE metadata.network_rules r SET from_terminal_id = c.keep_id
  FROM _terminal_canon c WHERE r.from_terminal_id = c.dup_id AND c.dup_id <> c.keep_id;
UPDATE metadata.network_rules r SET to_terminal_id = c.keep_id
  FROM _terminal_canon c WHERE r.to_terminal_id = c.dup_id AND c.dup_id <> c.keep_id;
UPDATE metadata.snapping_rules r SET to_terminal_id = c.keep_id
  FROM _terminal_canon c WHERE r.to_terminal_id = c.dup_id AND c.dup_id <> c.keep_id;
UPDATE runtime.feature_connectivity fc SET from_terminal_id = c.keep_id
  FROM _terminal_canon c WHERE fc.from_terminal_id = c.dup_id AND c.dup_id <> c.keep_id;
UPDATE runtime.feature_connectivity fc SET to_terminal_id = c.keep_id
  FROM _terminal_canon c WHERE fc.to_terminal_id = c.dup_id AND c.dup_id <> c.keep_id;

-- 3. Delete the now-unreferenced duplicates.
DELETE FROM metadata.terminals t
USING _terminal_canon c
WHERE t.id = c.dup_id AND c.dup_id <> c.keep_id;

-- 4. Swap the NULL-distinct unique for a NULLS NOT DISTINCT one, so a NULL
--    subtype_id row now conflicts like any other and re-seeding is idempotent.
--    Drop whatever the single existing unique constraint is named (robust to the
--    Postgres auto-name), then add the strict one.
DO $$
DECLARE cname text;
BEGIN
    SELECT conname INTO cname
    FROM pg_constraint
    WHERE conrelid = 'metadata.terminals'::regclass AND contype = 'u'
    LIMIT 1;
    IF cname IS NOT NULL THEN
        EXECUTE format('ALTER TABLE metadata.terminals DROP CONSTRAINT %I', cname);
    END IF;
END $$;

ALTER TABLE metadata.terminals
    ADD CONSTRAINT terminals_feature_class_id_subtype_id_code_key
    UNIQUE NULLS NOT DISTINCT (feature_class_id, subtype_id, code);

COMMIT;
