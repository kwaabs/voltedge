-- seed/collapse_rmu_way_into_mv_switch.sql
-- Purpose: NOT a migration -- lives under /seed (apply.sh only scans
-- /migrations), same reasoning as the other seed scripts. One-off, DESTRUCTIVE,
-- single transaction -- run with psql -v ON_ERROR_STOP=1 so any failure rolls
-- the whole thing back and leaves the DB untouched.
--
-- Collapses RMU_WAY into MV_SWITCH. Decision 2026-09-07 (mirrors the RMU_BUSBAR
-- -> MV_BUSBAR collapse): an RMU "way" IS a switch -- byte-for-byte the DIST_SWITCH
-- pattern (junction, device_state CLOSED/OPEN + condition_rating, T1 cable side /
-- T2 bus side, OPEN = trace barrier). Its only real difference from the generic
-- MV_SWITCH was ROLE (a bus-tied, RMU-contained, fan-out-created switched
-- position), not electrical behaviour. So there is ONE MV switch class
-- (MV_SWITCH) and the RMU simply contains instances of it; the ring-vs-transformer
-- subtype folds onto MV_SWITCH's is_fused flag (its stated design: fused vs.
-- non-fused is the boolean, not a subtype):
--     RMU_WAY subtype 1 'ring'        -> MV_SWITCH standard, is_fused = false
--     RMU_WAY subtype 2 'transformer' -> MV_SWITCH standard, is_fused = true
--
-- MV_SWITCH is a strict superset of RMU_WAY (same base + device_state /
-- condition_rating, plus optional is_fused / curve_ref / fuse_rating_a), and it
-- already carries an OPEN trace barrier and MV_CONDUCTOR <-> T1/T2 rules. The
-- only NEW topology this collapse adds is the internal MV_SWITCH.T2 <-> MV_BUSBAR
-- leg (a legitimate generalization -- an MV switch may land on a busbar), grafted
-- onto MV_SWITCH by repointing the RMU's existing internal rules.
--
-- Four halves:
--   0. MIGRATE existing RMU_WAY features onto MV_SWITCH. uuid PKs, so each way
--      keeps its id; every runtime row that references it by feature_id stays
--      valid. Unlike the terminal-less bus, a way's connections reference its
--      T1/T2 TERMINAL uuids, so those are remapped by terminal CODE to MV_SWITCH's
--      terminals. is_fused is derived from the old subtype.
--   B. REPOINT the RMU CATALOG (template items, containment, internal network
--      rules) from RMU_WAY to MV_SWITCH, keeping the ONE connectivity-referenced
--      rule's id (FK: feature_connectivity.network_rule_id, ON DELETE RESTRICT).
--   C. Drop RMU_WAY's redundant trace barrier (MV_SWITCH has its own).
--   D. Drop the now-unused RMU_WAY class + physical table.
--
-- Non-destructive to real data: MV_SWITCH rows survive untouched, and every
-- RMU_WAY feature is MOVED (not deleted) onto MV_SWITCH with its topology intact.
-- Aborts before the drop if any RMU_WAY reference survived the repoint, or a way
-- row failed to copy.
--
-- Idempotent: re-running after success is a clean no-op (RMU_WAY is gone, the
-- copy is table-guarded, every repoint resolves RMU_WAY to NULL and matches
-- nothing).
--
-- Apply:  task collapse-rmu-way   (or psql -f directly). No compile step -- the
-- physical DDL is inline and the metadata is left consistent.

BEGIN;

-- ---------------------------------------------------------------------------
-- Guards: MV_SWITCH must exist (class + compiled table). It has a single T1 and
-- single T2 terminal -- the remap targets. RMU_WAY features are migrated, not a
-- blocker.
-- ---------------------------------------------------------------------------
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM metadata.feature_classes WHERE code = 'MV_SWITCH') THEN
        RAISE EXCEPTION 'MV_SWITCH class not found -- apply seed-mv-lv-switch + compile CODE=MV_SWITCH first';
    END IF;
    IF to_regclass('gis.mv_switch') IS NULL THEN
        RAISE EXCEPTION 'gis.mv_switch not found -- run task compile CODE=MV_SWITCH first';
    END IF;
    IF (SELECT count(*) FROM metadata.terminals t
        JOIN metadata.feature_classes fc ON fc.id = t.feature_class_id
        WHERE fc.code = 'MV_SWITCH' AND t.code IN ('T1','T2')) <> 2 THEN
        RAISE EXCEPTION 'MV_SWITCH must have exactly one T1 and one T2 terminal for the remap';
    END IF;
END $$;

-- ===========================================================================
-- 0. MIGRATE EXISTING RMU_WAY FEATURES ONTO MV_SWITCH
-- ===========================================================================

-- 0a. Copy the way row(s) into gis.mv_switch, same id. subtype is flattened to 1
--     ('standard', MV_SWITCH's only subtype); is_fused is derived from the old
--     subtype (2 'transformer' -> true, 1 'ring' -> false). curve_ref /
--     fuse_rating_a / circuit_id / operational_state stay NULL (RMU_WAY carried
--     no protection data -- state preserved). Table-guarded + ON CONFLICT so a
--     re-run is a clean no-op.
DO $$
BEGIN
    IF to_regclass('gis.rmu_way') IS NOT NULL THEN
        EXECUTE $mig$
            INSERT INTO gis.mv_switch
                (id, name, subtype, status, deleted_at, geom,
                 condition_rating, device_state, is_fused,
                 created_at, updated_at, created_by, updated_by, territory_id)
            SELECT id, name, 1, status, deleted_at, geom,
                   condition_rating, device_state, (subtype = 2),
                   created_at, updated_at, created_by, updated_by, territory_id
            FROM gis.rmu_way
            ON CONFLICT (id) DO NOTHING
        $mig$;
    END IF;
END $$;

-- 0b. Repoint runtime feature references from RMU_WAY to MV_SWITCH. feature_id is
--     unchanged. For connectivity the TERMINAL uuid is also remapped: a row's
--     RMU_WAY terminal (found by its id) contributes its CODE, and the MV_SWITCH
--     terminal of that same code replaces it. A NULL terminal (bus-side) stays
--     NULL. On a re-run RMU_WAY resolves to NULL and nothing matches.
UPDATE runtime.feature_connectivity c
SET from_feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'MV_SWITCH'),
    from_terminal_id = (
        SELECT mt.id FROM metadata.terminals mt
        JOIN metadata.feature_classes mfc ON mfc.id = mt.feature_class_id AND mfc.code = 'MV_SWITCH'
        WHERE mt.code = (SELECT rt.code FROM metadata.terminals rt WHERE rt.id = c.from_terminal_id)
    )
WHERE c.from_feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'RMU_WAY');

UPDATE runtime.feature_connectivity c
SET to_feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'MV_SWITCH'),
    to_terminal_id = (
        SELECT mt.id FROM metadata.terminals mt
        JOIN metadata.feature_classes mfc ON mfc.id = mt.feature_class_id AND mfc.code = 'MV_SWITCH'
        WHERE mt.code = (SELECT rt.code FROM metadata.terminals rt WHERE rt.id = c.to_terminal_id)
    )
WHERE c.to_feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'RMU_WAY');

UPDATE runtime.feature_relationships
SET origin_feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'MV_SWITCH')
WHERE origin_feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'RMU_WAY');

UPDATE runtime.feature_relationships
SET destination_feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'MV_SWITCH')
WHERE destination_feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'RMU_WAY');

UPDATE runtime.diagram_placements
SET feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'MV_SWITCH')
WHERE feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'RMU_WAY');

UPDATE runtime.diagram_instances
SET container_feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'MV_SWITCH')
WHERE container_feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'RMU_WAY');

UPDATE runtime.feature_external_links
SET feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'MV_SWITCH')
WHERE feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'RMU_WAY');

UPDATE files.attachments      SET feature_class      = 'MV_SWITCH' WHERE feature_class      = 'RMU_WAY';
UPDATE gis.feature_history    SET feature_class_code = 'MV_SWITCH' WHERE feature_class_code = 'RMU_WAY';

-- 0c. Every way row must now exist in gis.mv_switch. Abort before the table drop
--     if any RMU_WAY feature failed to copy.
DO $$
DECLARE
    n_missing integer;
BEGIN
    IF to_regclass('gis.rmu_way') IS NOT NULL THEN
        EXECUTE 'SELECT count(*) FROM gis.rmu_way r WHERE NOT EXISTS (SELECT 1 FROM gis.mv_switch m WHERE m.id = r.id)'
        INTO n_missing;
        IF n_missing > 0 THEN
            RAISE EXCEPTION '% RMU_WAY feature(s) did not copy into gis.mv_switch -- aborting before the table drop', n_missing;
        END IF;
    END IF;
END $$;

-- ===========================================================================
-- B. REPOINT THE RMU CATALOG FROM RMU_WAY TO MV_SWITCH
-- ===========================================================================

-- B1. Template items (migration 0073, way_feature_class_id FK ON DELETE CASCADE
--     -- repoint BEFORE the class delete). Flatten subtype_code to 1 ('standard')
--     and stamp is_fused into the default attributes (transformer -> true). RHS
--     reads the OLD subtype_code, so evaluate is_fused before flattening in the
--     same statement.
UPDATE metadata.rmu_template_items
SET way_feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'MV_SWITCH'),
    attributes = COALESCE(attributes, '{}'::jsonb) || jsonb_build_object('is_fused', (subtype_code = 2)),
    subtype_code = 1
WHERE way_feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'RMU_WAY');

-- B2. The "RMU contains <way>" containment relationship class.
UPDATE metadata.relationship_classes
SET destination_feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'MV_SWITCH'),
    name = 'RMU contains MV_SWITCH'
WHERE origin_feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'RMU')
  AND destination_feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'RMU_WAY')
  AND kind = 'containment';

-- B3. Internal network rules. The forward rule (RMU_WAY.T2 -> MV_BUSBAR) is the
--     one feature_connectivity.network_rule_id points at (ON DELETE RESTRICT), so
--     UPDATE IT IN PLACE -- keep its id, move the from-class to MV_SWITCH and the
--     from-terminal to MV_SWITCH.T2. Bus side stays TERMINAL-LESS. This single
--     rule is all the fresh-DB seed adds too: connection establishment matches a
--     rule in either direction (matchRuleEitherDirection), so no mirrored bus ->
--     T2 rule is needed.
UPDATE metadata.network_rules
SET from_feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'MV_SWITCH'),
    from_terminal_id = (
        SELECT mt.id FROM metadata.terminals mt
        JOIN metadata.feature_classes mfc ON mfc.id = mt.feature_class_id AND mfc.code = 'MV_SWITCH'
        WHERE mt.code = 'T2'
    ),
    name = 'MV_SWITCH T2 -> MV_BUSBAR'
WHERE from_feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'RMU_WAY')
  AND to_feature_class_id   = (SELECT id FROM metadata.feature_classes WHERE code = 'MV_BUSBAR');

-- Every OTHER RMU_WAY-referencing rule is now redundant (the reverse internal
-- rule, and the external MV_CONDUCTOR <-> RMU_WAY.T1 legs that MV_SWITCH already
-- provides via its own rules) and NONE are referenced by connectivity, so delete
-- them (this also clears the duplicate copies accumulated by earlier seeding).
-- Nothing is re-inserted: the one kept + repointed forward rule above is exactly
-- what a fresh seed produces.
DELETE FROM metadata.network_rules
WHERE from_feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'RMU_WAY')
   OR to_feature_class_id   = (SELECT id FROM metadata.feature_classes WHERE code = 'RMU_WAY');

-- ===========================================================================
-- C. Drop RMU_WAY's redundant OPEN trace barrier -- MV_SWITCH carries its own
--    (seed/mv_lv_switch_example.sql), so open MV_SWITCH ways still barrier. Done
--    explicitly so it can't block the class delete regardless of the FK's ON
--    DELETE behaviour.
-- ===========================================================================
DELETE FROM metadata.trace_barrier_rules
WHERE feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'RMU_WAY');

-- ===========================================================================
-- D. DROP RMU_WAY
-- ===========================================================================

-- D1. Post-repoint guard: nothing may still reference RMU_WAY before the class
--     delete -- not the catalog (template_items CASCADE would silently delete the
--     item; network_rules / relationship_classes have no ON DELETE and would
--     block) and not any runtime feature reference (would orphan a real way).
DO $$
DECLARE
    rw_id  uuid;
    n_item integer;
    n_rule integer;
    n_rel  integer;
    n_conn integer;
    n_frel integer;
    n_diag integer;
    n_inst integer;
    n_link integer;
    n_att  integer;
    n_bar  integer;
BEGIN
    SELECT id INTO rw_id FROM metadata.feature_classes WHERE code = 'RMU_WAY';
    IF rw_id IS NULL THEN
        RETURN; -- already collapsed on a prior run
    END IF;

    SELECT count(*) INTO n_item FROM metadata.rmu_template_items WHERE way_feature_class_id = rw_id;
    SELECT count(*) INTO n_rule FROM metadata.network_rules
    WHERE from_feature_class_id = rw_id OR to_feature_class_id = rw_id;
    SELECT count(*) INTO n_rel FROM metadata.relationship_classes
    WHERE origin_feature_class_id = rw_id OR destination_feature_class_id = rw_id;
    SELECT count(*) INTO n_conn FROM runtime.feature_connectivity
    WHERE from_feature_class_id = rw_id OR to_feature_class_id = rw_id;
    SELECT count(*) INTO n_frel FROM runtime.feature_relationships
    WHERE origin_feature_class_id = rw_id OR destination_feature_class_id = rw_id;
    SELECT count(*) INTO n_diag FROM runtime.diagram_placements WHERE feature_class_id = rw_id;
    SELECT count(*) INTO n_inst FROM runtime.diagram_instances WHERE container_feature_class_id = rw_id;
    SELECT count(*) INTO n_link FROM runtime.feature_external_links WHERE feature_class_id = rw_id;
    SELECT count(*) INTO n_att  FROM files.attachments WHERE feature_class = 'RMU_WAY';
    SELECT count(*) INTO n_bar  FROM metadata.trace_barrier_rules WHERE feature_class_id = rw_id;

    IF n_item > 0 OR n_rule > 0 OR n_rel > 0 OR n_conn > 0 OR n_frel > 0
       OR n_diag > 0 OR n_inst > 0 OR n_link > 0 OR n_att > 0 OR n_bar > 0 THEN
        RAISE EXCEPTION 'RMU_WAY still referenced (template_items=%, network_rules=%, relationship_classes=%, connectivity=%, relationships=%, diagram-placements=%, diagram-instances=%, external-links=%, attachments=%, trace-barriers=%). Aborting before the class delete.',
            n_item, n_rule, n_rel, n_conn, n_frel, n_diag, n_inst, n_link, n_att, n_bar;
    END IF;
END $$;

-- D2. Drop the class row (CASCADEs its terminals/subtypes/fields/subtype_field_
--     rules/symbology/CIM) and the physical table + its Martin tile function.
DELETE FROM metadata.feature_classes WHERE code = 'RMU_WAY';
DROP FUNCTION IF EXISTS gis.rmu_way(integer, integer, integer);
DROP TABLE IF EXISTS gis.rmu_way CASCADE;

COMMIT;
