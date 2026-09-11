-- seed/operational_state_backfill.sql
-- Phase 38 (operational lifecycle), Slice 1 -- the backfill.
--
-- Run this AFTER seed/operational_state_example.sql AND after recompiling every
-- stamped class (the `operational_state` column has to physically exist first).
--
-- ALTER TABLE ADD COLUMN left the new column NULL on every existing row. This
-- maps the old world onto the new axis so nothing is stranded at NULL, and
-- directly answers "how do I get all my features off the draft default":
--   * a published (real, in-the-ground) asset  -> IN_SERVICE
--   * a still-draft asset                       -> PLANNED
--
-- Generic + idempotent: it loops over every class that carries an active
-- `operational_state` field and a compiled table, and only touches rows still
-- NULL, so re-running it is a no-op and it needs no hardcoded table list.
-- physical_table is already schema-qualified (e.g. gis.pole), usable directly.

DO $$
DECLARE
    r record;
BEGIN
    FOR r IN
        SELECT fc.physical_table AS tbl
        FROM metadata.feature_classes fc
        WHERE fc.physical_table IS NOT NULL
          AND EXISTS (
              SELECT 1 FROM metadata.fields f
              WHERE f.feature_class_id = fc.id
                AND f.name = 'operational_state'
                AND f.status = 'active'
          )
    LOOP
        EXECUTE format(
            'UPDATE %s SET operational_state = ''IN_SERVICE'' '
            'WHERE operational_state IS NULL AND status = ''published''',
            r.tbl
        );
        EXECUTE format(
            'UPDATE %s SET operational_state = ''PLANNED'' '
            'WHERE operational_state IS NULL AND status = ''draft''',
            r.tbl
        );
    END LOOP;
END $$;
