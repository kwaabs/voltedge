-- 0085_rename_substation_yard_to_substation.sql
-- Purpose: Phase 47 Slice 2 (docs/phase-47-substation-site-model.md) -- rename the
-- SUBSTATION_YARD feature class to SUBSTATION, data-preserving, on live databases.
--
-- Why a rename (not a new class): "yard" was semantically overloaded -- at once the
-- site identity, the outdoor switchyard, AND the top-level container for indoor
-- control-building panels. The site boundary is a SUBSTATION; the outdoor switchyard
-- and the control building are tiers BELOW it (the building arrives in Slice 1's
-- CONTROL_BUILDING; the switchyard/voltage grouping in Slice 3's VOLTAGE_LEVEL). This
-- also aligns the top container with the CIM `Substation` the export already emits
-- (docs/phase-16-cim-export.md).
--
-- Blast radius is small and this migration is the whole of it on the DB side: NOTHING
-- in the Go API or web frontend hardcodes the code 'SUBSTATION_YARD' (only comments),
-- and every dependent metadata row -- containment relationship_classes, symbology,
-- fields, subtypes, cim_class_mappings, diagram_placement_rules -- references the
-- feature class by ROW-ID, not by code, so a code rename leaves them correctly wired
-- with zero re-linking. The physical table is named strings.ToLower(code)
-- (compiler.go), so the table must be renamed in lock-step with the code.
--
-- Idempotent: guarded on the old table existing and the new one not, so a re-run (or
-- a fresh DB that was seeded straight as SUBSTATION) is a clean no-op. The matching
-- seed edits (substation_yard_example.sql et al. now insert code 'SUBSTATION') cover
-- fresh installs; this migration covers databases already carrying live yard rows.
--
-- Collision self-heal: an earlier (pre-guard) run of the seed against a not-yet
-- migrated DB could insert a SECOND, empty class row with code 'SUBSTATION' beside
-- the live 'SUBSTATION_YARD', which would make the rename UPDATE collide on
-- feature_classes' UNIQUE(code). The DO block detects that duplicate and, after
-- PROVING it holds no feature rows (else it raises, refusing to guess), deletes it
-- and its dependents first, then proceeds with the rename. The data-bearing yard row
-- is always the one kept.
--
-- After applying, recompile to reconcile the derived objects under the new name
-- (check constraints renamed by column introspection, the Martin tile function
-- recreated as gis.substation(z,x,y), physical_table re-set):
--   task compile CODE=SUBSTATION

DO $$
DECLARE
    dup_id uuid;
    new_pt text;
    cnt    bigint;
BEGIN
    -- Collision self-heal (must run FIRST -- before the table-rename guard below,
    -- whose `gis.substation IS NULL` condition can only become true once a
    -- duplicate's empty table is gone). A pre-guard run of substation_yard_example.sql
    -- against a not-yet-migrated DB inserted a SECOND, empty class row with code
    -- 'SUBSTATION' alongside the live 'SUBSTATION_YARD' (the seed's INSERT is now
    -- guarded; this repairs a DB where it already happened). Left in place it makes
    -- the metadata rename UPDATE below violate feature_classes' UNIQUE(code). The
    -- data-bearing row is ALWAYS the old SUBSTATION_YARD one (its feature instances
    -- live in gis.substation_yard); the fresh SUBSTATION row has none -- but we prove
    -- that before deleting, never guess which row holds the data.
    IF EXISTS (SELECT 1 FROM metadata.feature_classes WHERE code = 'SUBSTATION_YARD')
       AND EXISTS (SELECT 1 FROM metadata.feature_classes WHERE code = 'SUBSTATION') THEN

        SELECT id, physical_table INTO dup_id, new_pt
          FROM metadata.feature_classes WHERE code = 'SUBSTATION';

        -- Prove the duplicate holds no feature rows. An uncompiled duplicate has no
        -- physical table (physical_table NULL, or to_regclass NULL) -> trivially
        -- empty. If it was compiled AND someone drew features under it, this is a
        -- genuine two-populated-classes situation we must NOT silently resolve: bail
        -- with a clear message so it is merged by hand.
        cnt := 0;
        IF new_pt IS NOT NULL AND to_regclass(new_pt) IS NOT NULL THEN
            EXECUTE format('SELECT count(*) FROM %s', new_pt) INTO cnt;
        END IF;
        IF cnt > 0 THEN
            RAISE EXCEPTION 'migration 0085: both SUBSTATION_YARD and a POPULATED SUBSTATION class exist (% rows in %); this is a real merge -- resolve by hand before re-running', cnt, new_pt;
        END IF;

        -- Clear the duplicate's non-cascade metadata dependents. The edited seeds
        -- create containment relationship_classes against it (origin side); network/
        -- snapping are cleared defensively though a none/container class seeds none.
        -- subtypes, fields, subtype_field_rules, symbology_rules, cim_class_mappings,
        -- terminals all FK ON DELETE CASCADE, so the final row delete sweeps them; the
        -- fresh class has no runtime instances (diagram/connectivity/relationship rows).
        DELETE FROM metadata.relationship_classes
         WHERE origin_feature_class_id = dup_id OR destination_feature_class_id = dup_id;
        DELETE FROM metadata.network_rules
         WHERE from_feature_class_id = dup_id OR to_feature_class_id = dup_id;
        DELETE FROM metadata.snapping_rules
         WHERE from_feature_class_id = dup_id OR to_feature_class_id = dup_id;

        -- If the duplicate was compiled, drop its (proven-empty) physical artifacts so
        -- the table guard below can move gis.substation_yard into gis.substation.
        IF new_pt IS NOT NULL AND to_regclass(new_pt) IS NOT NULL THEN
            DROP FUNCTION IF EXISTS gis.substation(integer, integer, integer);
            EXECUTE format('DROP TABLE IF EXISTS %s CASCADE', new_pt);
        END IF;

        DELETE FROM metadata.feature_classes WHERE id = dup_id;
    END IF;

    -- Only act on a database that still has the old physical table and has not
    -- already gained the new one -- makes the whole migration re-runnable.
    IF to_regclass('gis.substation_yard') IS NOT NULL
       AND to_regclass('gis.substation') IS NULL THEN

        -- Drop the old Martin tile function first (gis.substation_yard(z,x,y) ->
        -- bytea, migration 0038): its body references the old table name, so it
        -- would dangle after the rename. Recompile recreates gis.substation(z,x,y).
        -- SUBSTATION_YARD is a polygon class, so it always had one; IF EXISTS keeps
        -- this safe regardless.
        DROP FUNCTION IF EXISTS gis.substation_yard(integer, integer, integer);

        -- Data-preserving table rename (rows, geom, territory_id all carried over).
        ALTER TABLE gis.substation_yard RENAME TO substation;

        -- The spatial index follows the table but keeps its old name; rename it so a
        -- recompile does not leave a mismatched substation_yard_geom_idx behind.
        ALTER INDEX IF EXISTS gis.substation_yard_geom_idx RENAME TO substation_geom_idx;
    END IF;

    -- Metadata rename -- kept in the same guard-free position so it also fixes a DB
    -- where the table was renamed but the metadata lagged. WHERE code = old makes it
    -- a no-op once already renamed. physical_table is set so the metadata is
    -- consistent even before the recompile (recompile would re-set it anyway).
    UPDATE metadata.feature_classes
       SET code = 'SUBSTATION',
           name = 'Substation',
           physical_table = 'gis.substation'
     WHERE code = 'SUBSTATION_YARD';

    -- Containment relationship_classes reference the class by row-id, so they stay
    -- correctly wired through the rename -- only their human-readable NAME still
    -- shows the old code ('SUBSTATION_YARD contains BAY' etc.). Normalize the label
    -- so the UI reads consistently. The yard is always the origin (top-level
    -- container), never a destination, so a prefix REPLACE is unambiguous. Idempotent.
    UPDATE metadata.relationship_classes
       SET name = REPLACE(name, 'SUBSTATION_YARD contains ', 'SUBSTATION contains ')
     WHERE name LIKE 'SUBSTATION_YARD contains %';
END $$;
