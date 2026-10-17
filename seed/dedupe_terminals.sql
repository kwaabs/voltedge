-- seed/dedupe_terminals.sql
-- Purpose: a REUSABLE, idempotent cleanup you can run anytime to remove duplicate
-- terminal rows -- the same dedupe migration 0142 did once, minus the constraint
-- swap (0142 already made the UNIQUE NULLS NOT DISTINCT, which normally prevents
-- any new duplicate from forming). Safe to run on a clean database: it simply
-- finds nothing and changes nothing.
--
-- NOT a migration (lives under /seed). Run it by hand when you want to be sure:
--   psql "$DATABASE_URL" -f seed/dedupe_terminals.sql
-- or, after wiring the Taskfile entry:  task dedupe-terminals
--
-- It keeps the earliest-created (lowest-id) terminal per
-- (feature_class_id, subtype_id[null-aware], code) group, RE-POINTS every
-- reference to it (network_rules, snapping_rules, runtime.feature_connectivity --
-- duplicates are identical, so this is lossless), then deletes the extras.
-- Wrapped in a transaction: atomic, and the ON COMMIT DROP temp table survives
-- across statements (the runner autocommits each statement otherwise).

BEGIN;

CREATE TEMP TABLE _terminal_canon ON COMMIT DROP AS
SELECT t.id AS dup_id,
       first_value(t.id) OVER (
           PARTITION BY t.feature_class_id, COALESCE(t.subtype_id::text, '~null~'), t.code
           ORDER BY t.created_at, t.id
       ) AS keep_id
FROM metadata.terminals t;

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

DELETE FROM metadata.terminals t
USING _terminal_canon c
WHERE t.id = c.dup_id AND c.dup_id <> c.keep_id;

COMMIT;
