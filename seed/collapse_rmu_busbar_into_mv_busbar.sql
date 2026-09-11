-- seed/collapse_rmu_busbar_into_mv_busbar.sql
-- Purpose: NOT a migration -- lives under /seed (apply.sh only scans
-- /migrations), same reasoning as the other seed scripts. One-off, DESTRUCTIVE,
-- single transaction -- run with psql -v ON_ERROR_STOP=1 so any failure rolls
-- the whole thing back and leaves the DB untouched.
--
-- Collapses the RMU internal bus into MV_BUSBAR. Decision 2026-09-06: a busbar's
-- real classifier is VOLTAGE (MV vs LV), not "standalone substation asset vs
-- internal assembly node" -- so there is ONE MV bus class (MV_BUSBAR) and the RMU
-- simply contains an instance of it. RMU_BUSBAR (a second, attribute-less MV bus
-- class) goes away.
--
-- Three halves:
--   A. RELAX MV_BUSBAR so a bare internal bus is a legal create. MV_BUSBAR
--      requires a subtype (air/gas-insulated) + voltage_class + condition_rating;
--      the RMU fan-out creates its bus with an EMPTY input. Drop the subtype
--      (nothing references it -- symbology keys on class code, the CIM mapping and
--      terminals use subtype_id NULL, no rule filters on it) and make the two
--      fields nullable. After this an empty-input create passes validateSubtype
--      (no subtypes -> nil) and validateRequired (short-circuits when subtype is
--      nil, api/internal/crud/validate.go), and the physical table takes the NULLs.
--   0. MIGRATE any existing RMU_BUSBAR feature onto MV_BUSBAR. gis PKs are uuids,
--      so each bus row keeps its id when copied into gis.mv_busbar -- every
--      runtime row that references it by feature_id (connectivity, relationships,
--      placements, links, history) stays valid and only needs its class reference
--      flipped from RMU_BUSBAR to MV_BUSBAR. Runs AFTER A so gis.mv_busbar accepts
--      a bare bus (no subtype, nullable voltage_class/condition_rating).
--   B. REPOINT the RMU CATALOG from RMU_BUSBAR to MV_BUSBAR -- the template's bus
--      class, the "RMU contains" relationship, and the two internal network rules
--      (kept TERMINAL-LESS on the bus side, exactly as the bare RMU_BUSBAR node
--      was, so idMatches still matches and neither the fan-out nor matchRule need
--      changing) -- then drop the now-unused RMU_BUSBAR class + physical table.
--
-- Non-destructive to real data: existing MV_BUSBAR rows survive (DROP COLUMN
-- subtype only removes a descriptive label; DROP NOT NULL only loosens
-- constraints), and any RMU_BUSBAR feature is MOVED (not deleted) onto MV_BUSBAR
-- with its topology intact. The script aborts before dropping the class/table if
-- any RMU_BUSBAR reference survived the repoint, or if a bus row failed to copy.
--
-- Idempotent: re-running after success is a clean no-op (RMU_BUSBAR is gone, the
-- ALTERs are IF EXISTS / already-satisfied, the copy is table-guarded, the
-- repoints resolve RMU_BUSBAR to NULL and match nothing).
--
-- Apply:  task collapse-rmu-busbar   (or psql -f directly). No compile step -- the
-- physical DDL is inline here and the metadata is left consistent, so a later
-- `task compile CODE=MV_BUSBAR` is a no-op.

BEGIN;

-- ---------------------------------------------------------------------------
-- Guards: MV_BUSBAR must exist (class + table). RMU_BUSBAR features are no
-- longer a blocker -- Section 0 migrates them.
-- ---------------------------------------------------------------------------
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM metadata.feature_classes WHERE code = 'MV_BUSBAR') THEN
        RAISE EXCEPTION 'MV_BUSBAR class not found -- apply seed-mv-busbar first';
    END IF;
    IF to_regclass('gis.mv_busbar') IS NULL THEN
        RAISE EXCEPTION 'gis.mv_busbar not found -- run task compile CODE=MV_BUSBAR first';
    END IF;
END $$;

-- ===========================================================================
-- A. RELAX MV_BUSBAR
-- ===========================================================================

-- A1. Physical: drop the air/gas subtype column (removes only that label from
--     every row), and loosen the two required fields to nullable. The compiler
--     is additive-only so it never does this itself; done here in place so no
--     table rebuild / row loss is needed.
ALTER TABLE gis.mv_busbar DROP COLUMN IF EXISTS subtype;
ALTER TABLE gis.mv_busbar ALTER COLUMN voltage_class DROP NOT NULL;
ALTER TABLE gis.mv_busbar ALTER COLUMN condition_rating DROP NOT NULL;

-- A2. Metadata: delete the subtype_field_rules that hang the required-ness off
--     the subtypes, then the subtypes themselves (MV_BUSBAR's terminals + CIM
--     mapping use subtype_id NULL, so nothing else references them), then flip
--     the two fields to nullable so the field pack / admin / publish checks all
--     agree with the physical table.
DELETE FROM metadata.subtype_field_rules
WHERE subtype_id IN (
    SELECT s.id FROM metadata.subtypes s
    JOIN metadata.feature_classes fc ON fc.id = s.feature_class_id
    WHERE fc.code = 'MV_BUSBAR'
);

DELETE FROM metadata.subtypes
WHERE feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'MV_BUSBAR');

UPDATE metadata.fields
SET nullable = true
WHERE feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'MV_BUSBAR')
  AND name IN ('voltage_class', 'condition_rating');

-- ===========================================================================
-- 0. MIGRATE EXISTING RMU_BUSBAR FEATURES ONTO MV_BUSBAR
--    Runs AFTER Section A so gis.mv_busbar accepts a bare bus (subtype dropped,
--    voltage_class/condition_rating nullable). uuid PKs mean each row keeps its
--    id, so the runtime rows that reference it stay valid and only need their
--    class reference flipped. If gis.rmu_busbar is already gone (a prior run),
--    the whole section is a no-op.
-- ===========================================================================

-- 0a. Copy the bus row(s) into gis.mv_busbar, same id. Only the columns both
--     tables share (rmu_busbar is attribute-less: no subtype/voltage_class/
--     condition_rating/circuit_id/operational_state); those MV_BUSBAR-only
--     columns are left NULL (all nullable now / dropped). Table-guarded so a
--     re-run after the drop is a clean no-op; ON CONFLICT keeps it idempotent.
DO $$
BEGIN
    IF to_regclass('gis.rmu_busbar') IS NOT NULL THEN
        EXECUTE $mig$
            INSERT INTO gis.mv_busbar
                (id, name, status, deleted_at, geom,
                 created_at, updated_at, created_by, updated_by, territory_id)
            SELECT id, name, status, deleted_at, geom,
                   created_at, updated_at, created_by, updated_by, territory_id
            FROM gis.rmu_busbar
            ON CONFLICT (id) DO NOTHING
        $mig$;
    END IF;
END $$;

-- 0b. Repoint every runtime reference from the RMU_BUSBAR class to MV_BUSBAR.
--     feature_id is unchanged (same uuid). On a re-run RMU_BUSBAR is gone, the
--     subquery is NULL, and every UPDATE matches nothing.
--     uuid-class references:
UPDATE runtime.feature_connectivity
SET from_feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'MV_BUSBAR')
WHERE from_feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'RMU_BUSBAR');

UPDATE runtime.feature_connectivity
SET to_feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'MV_BUSBAR')
WHERE to_feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'RMU_BUSBAR');

UPDATE runtime.feature_relationships
SET origin_feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'MV_BUSBAR')
WHERE origin_feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'RMU_BUSBAR');

UPDATE runtime.feature_relationships
SET destination_feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'MV_BUSBAR')
WHERE destination_feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'RMU_BUSBAR');

UPDATE runtime.diagram_placements
SET feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'MV_BUSBAR')
WHERE feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'RMU_BUSBAR');

UPDATE runtime.diagram_instances
SET container_feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'MV_BUSBAR')
WHERE container_feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'RMU_BUSBAR');

UPDATE runtime.feature_external_links
SET feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'MV_BUSBAR')
WHERE feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'RMU_BUSBAR');

--     text-code class references (no FK; keyed by the class code string):
UPDATE files.attachments      SET feature_class      = 'MV_BUSBAR' WHERE feature_class      = 'RMU_BUSBAR';
UPDATE gis.feature_history    SET feature_class_code = 'MV_BUSBAR' WHERE feature_class_code = 'RMU_BUSBAR';

-- 0c. Every bus row must now exist in gis.mv_busbar. If any RMU_BUSBAR feature
--     failed to copy, abort before we drop its home table (Section B5).
DO $$
DECLARE
    n_missing integer;
BEGIN
    IF to_regclass('gis.rmu_busbar') IS NOT NULL THEN
        EXECUTE 'SELECT count(*) FROM gis.rmu_busbar r WHERE NOT EXISTS (SELECT 1 FROM gis.mv_busbar m WHERE m.id = r.id)'
        INTO n_missing;
        IF n_missing > 0 THEN
            RAISE EXCEPTION '% RMU_BUSBAR feature(s) did not copy into gis.mv_busbar -- aborting before the table drop', n_missing;
        END IF;
    END IF;
END $$;

-- ===========================================================================
-- B. REPOINT THE RMU CATALOG FROM RMU_BUSBAR TO MV_BUSBAR, THEN DROP RMU_BUSBAR
-- ===========================================================================

-- B1. The template's bus class (migration 0073, FK is ON DELETE CASCADE -- so
--     this MUST happen before the class delete, or the template rows vanish).
UPDATE metadata.rmu_templates
SET busbar_feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'MV_BUSBAR')
WHERE busbar_feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'RMU_BUSBAR');

-- B2. The "RMU contains <bus>" containment relationship class.
UPDATE metadata.relationship_classes
SET destination_feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'MV_BUSBAR'),
    name = 'RMU contains MV_BUSBAR'
WHERE origin_feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'RMU')
  AND destination_feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'RMU_BUSBAR')
  AND kind = 'containment';

-- B3. The two internal network rules (RMU_WAY.T2 <-> bus). UPDATE IN PLACE -- NOT
--     delete+reinsert -- so each rule keeps its id. feature_connectivity rows
--     record which rule established them via network_rule_id (FK, ON DELETE
--     RESTRICT: a way's bus-side connection points at these rules), so deleting
--     would violate that FK. Repointing the bus-side class and renaming leaves
--     those connectivity rows valid. Bus side stays TERMINAL-LESS, exactly as the
--     bare RMU_BUSBAR node was -- idMatches (a NULL rule terminal matches a
--     terminal-less connection) means the fan-out's terminal-less bus connection
--     still matches, so no Go change. Idempotent: on a re-run RMU_BUSBAR resolves
--     to NULL and neither UPDATE matches.

-- RMU_WAY T2 -> MV_BUSBAR (a way's bus side into the shared bus node).
UPDATE metadata.network_rules
SET to_feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'MV_BUSBAR'),
    name = 'RMU_WAY T2 -> MV_BUSBAR'
WHERE from_feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'RMU_WAY')
  AND to_feature_class_id   = (SELECT id FROM metadata.feature_classes WHERE code = 'RMU_BUSBAR');

-- MV_BUSBAR -> RMU_WAY T2 (reverse -- the bus reaches every way hanging off it).
UPDATE metadata.network_rules
SET from_feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'MV_BUSBAR'),
    name = 'MV_BUSBAR -> RMU_WAY T2'
WHERE from_feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'RMU_BUSBAR')
  AND to_feature_class_id   = (SELECT id FROM metadata.feature_classes WHERE code = 'RMU_WAY');

-- B4. Post-repoint guard: NOTHING must still reference RMU_BUSBAR before the
--     class delete -- not the catalog (rmu_templates CASCADE would silently
--     delete the template; network_rules / relationship_classes have no ON DELETE
--     and would block), and not any runtime feature reference (would orphan a
--     real bus's topology). If any survive, abort.
DO $$
DECLARE
    rb_id  uuid;
    n_tmpl integer;
    n_rule integer;
    n_rel  integer;
    n_conn integer;
    n_frel integer;
    n_diag integer;
    n_inst integer;
    n_link integer;
    n_att  integer;
BEGIN
    SELECT id INTO rb_id FROM metadata.feature_classes WHERE code = 'RMU_BUSBAR';
    IF rb_id IS NULL THEN
        RETURN; -- already collapsed on a prior run
    END IF;

    SELECT count(*) INTO n_tmpl FROM metadata.rmu_templates WHERE busbar_feature_class_id = rb_id;
    SELECT count(*) INTO n_rule FROM metadata.network_rules
    WHERE from_feature_class_id = rb_id OR to_feature_class_id = rb_id;
    SELECT count(*) INTO n_rel FROM metadata.relationship_classes
    WHERE origin_feature_class_id = rb_id OR destination_feature_class_id = rb_id;
    SELECT count(*) INTO n_conn FROM runtime.feature_connectivity
    WHERE from_feature_class_id = rb_id OR to_feature_class_id = rb_id;
    SELECT count(*) INTO n_frel FROM runtime.feature_relationships
    WHERE origin_feature_class_id = rb_id OR destination_feature_class_id = rb_id;
    SELECT count(*) INTO n_diag FROM runtime.diagram_placements WHERE feature_class_id = rb_id;
    SELECT count(*) INTO n_inst FROM runtime.diagram_instances WHERE container_feature_class_id = rb_id;
    SELECT count(*) INTO n_link FROM runtime.feature_external_links WHERE feature_class_id = rb_id;
    SELECT count(*) INTO n_att  FROM files.attachments WHERE feature_class = 'RMU_BUSBAR';

    IF n_tmpl > 0 OR n_rule > 0 OR n_rel > 0 OR n_conn > 0 OR n_frel > 0
       OR n_diag > 0 OR n_inst > 0 OR n_link > 0 OR n_att > 0 THEN
        RAISE EXCEPTION 'RMU_BUSBAR still referenced (templates=%, network_rules=%, relationship_classes=%, connectivity=%, relationships=%, diagram-placements=%, diagram-instances=%, external-links=%, attachments=%). Aborting before the class delete.',
            n_tmpl, n_rule, n_rel, n_conn, n_frel, n_diag, n_inst, n_link, n_att;
    END IF;
END $$;

-- B5. Drop the class row (CASCADEs its terminals/subtypes/fields/symbology/CIM/
--     placement-rules -- RMU_BUSBAR had none beyond the class row) and the
--     physical table + its Martin tile function.
DELETE FROM metadata.feature_classes WHERE code = 'RMU_BUSBAR';
DROP FUNCTION IF EXISTS gis.rmu_busbar(integer, integer, integer);
DROP TABLE IF EXISTS gis.rmu_busbar CASCADE;

COMMIT;
