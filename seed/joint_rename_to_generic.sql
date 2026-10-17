-- seed/joint_rename_to_generic.sql
-- ONE-TIME, MANUALLY-APPLIED rename of the joint feature classes to a GENERIC code:
--   MV_CABLE_JOINT -> MV_JOINT,  LV_CABLE_JOINT -> LV_JOINT
-- The classes now cover overhead AS WELL AS underground joints, so "CABLE" in the
-- code is wrong (see seed/joint_overhead_extension.sql). Lives under /seed (NOT
-- /migrations) on purpose: apply.sh auto-runs /migrations against every environment,
-- and a class-code rename across published data is deliberate surgery you run by hand
-- once, not an automatic step. Promote to a numbered migration later if you need it
-- replayed on other environments.
--
-- WHY A HAND RENAME: a feature-class CODE is a PERMANENT identifier here -- the admin
-- layer refuses to rename it (api/internal/admin/types.go), and only files.attachments
-- FKs the code with ON UPDATE CASCADE. Every other subsystem stores the class as a
-- bare TEXT code with no FK, so each must be hand-updated. This script does that for
-- every code-keyed column found in the schema as of migration 0144.
--
-- SAFETY: edits PUBLISHED data across ~20 tables, authored statically (bash.exe EPERM
-- here -- UNTESTED against a live DB). >>> TAKE A BACKUP / run on a copy first. <<<
-- Idempotent (old-code -> new-code; a re-run finds no old codes and is a no-op) and
-- wrapped in a single transaction (all-or-nothing). Each per-table update is guarded
-- by to_regclass + information_schema, so a table/column absent from this deployment
-- is skipped, never an error.
--
-- Apply, then RECOMPILE both classes under the NEW codes (regenerates the registry
-- trigger + tile function + re-syncs the subtype/domain CHECKs):
--   task seed-joint-rename       (or: psql "$DATABASE_URL" -f seed/joint_rename_to_generic.sql)
--   task compile CODE=MV_JOINT
--   task compile CODE=LV_JOINT

BEGIN;

-- ---------------------------------------------------------------------------
-- 1. Pre-existing subtype gap: ensure cable subtypes 1-4 exist on both joint
-- classes (the OH subtypes 5-7 came from seed/joint_overhead_extension.sql).
-- MV_CABLE_JOINT was found missing 1-4 -- only 5-7 present -- so a recompile built
-- CHECK (subtype IN (5,6,7)) and its subtype-1 rows failed it. Upsert so a class
-- missing them gains them; a class that has them is relabelled. Done BEFORE the
-- rename, by the current code.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, v.code, v.name
FROM metadata.feature_classes fc
JOIN (VALUES
    (1, 'Cable straight'),
    (2, 'Cable branch (tee)'),
    (3, 'Riser / pothead'),
    (4, 'Cable termination')
) AS v(code, name) ON true
WHERE fc.code IN ('MV_CABLE_JOINT', 'LV_CABLE_JOINT')
ON CONFLICT (feature_class_id, code) DO UPDATE SET name = EXCLUDED.name;

-- ---------------------------------------------------------------------------
-- 2. Rename the class codes. files.attachments.feature_class FKs this ON UPDATE
-- CASCADE (migration 0041) so its rows follow automatically. Every uuid-keyed child
-- (subtypes, fields, terminals, network_rules, symbology_rules, cim mappings) is
-- unaffected -- they key feature_class_id, not the code.
-- ---------------------------------------------------------------------------
UPDATE metadata.feature_classes SET code = 'MV_JOINT' WHERE code = 'MV_CABLE_JOINT';
UPDATE metadata.feature_classes SET code = 'LV_JOINT' WHERE code = 'LV_CABLE_JOINT';

-- ---------------------------------------------------------------------------
-- 3. Rename the physical tables + point physical_table at the new names. The
-- registry-sync trigger is table-bound so it rides the rename (its embedded old code
-- is corrected on recompile). Guarded: a never-compiled class (no table) is skipped.
-- ---------------------------------------------------------------------------
DO $$
BEGIN
  IF to_regclass('gis.mv_cable_joint') IS NOT NULL THEN
    ALTER TABLE gis.mv_cable_joint RENAME TO mv_joint;
  END IF;
  IF to_regclass('gis.lv_cable_joint') IS NOT NULL THEN
    ALTER TABLE gis.lv_cable_joint RENAME TO lv_joint;
  END IF;
END $$;

UPDATE metadata.feature_classes SET physical_table = 'gis.mv_joint' WHERE code = 'MV_JOINT';
UPDATE metadata.feature_classes SET physical_table = 'gis.lv_joint' WHERE code = 'LV_JOINT';

-- ---------------------------------------------------------------------------
-- 4. Rewrite every bare-text code reference across the system. One guarded loop over
-- (schema, table, column) so a table/column absent here is skipped (never an error),
-- and the whole set is auditable in one place. attachments is OMITTED (it cascaded
-- in step 2).
-- ---------------------------------------------------------------------------
DO $$
DECLARE
  pairs text[][] := ARRAY[
    ['runtime',  'feature_registry',       'feature_class_code'],
    ['runtime',  'feature_pending_edits',  'feature_class_code'],
    ['runtime',  'asset_tags',             'feature_class_code'],
    ['gis',      'feature_history',        'feature_class_code'],
    ['gis',      'feature_gps_captures',   'feature_class_code'],
    ['faults',   'fault_events',           'asset_class'],
    ['faults',   'fault_events',           'protective_device_class'],
    ['wam',      'defects',                'asset_class'],
    ['wam',      'work_orders',            'asset_class'],
    ['wam',      'inspections',            'asset_class'],
    ['gis',      'circuits',               'source_feature_class'],
    ['ops',      'sync_inbox',             'feature_class'],
    ['ops',      'sync_policies',          'feature_class'],
    ['runtime',  'diagram_elements',       'slot_class'],
    ['runtime',  'diagram_elements',       'bound_class'],
    ['oms',      'incidents',              'predicted_device_class'],
    ['oms',      'customer_interruptions', 'feature_class'],
    ['oms',      'switching_steps',        'device_class'],
    ['metadata', 'cost_rates',             'feature_class_code'],
    ['metadata', 'feature_cost_overrides', 'feature_class_code'],
    ['ops',      'validation_issues',      'from_class'],
    ['ops',      'validation_issues',      'to_class'],
    ['ops',      'import_jobs',            'feature_class_code']
  ];
  i int;
BEGIN
  FOR i IN 1 .. array_length(pairs, 1) LOOP
    CONTINUE WHEN to_regclass(format('%I.%I', pairs[i][1], pairs[i][2])) IS NULL;
    CONTINUE WHEN NOT EXISTS (
      SELECT 1 FROM information_schema.columns
      WHERE table_schema = pairs[i][1] AND table_name = pairs[i][2] AND column_name = pairs[i][3]
    );
    EXECUTE format(
      'UPDATE %I.%I SET %I = CASE %I '
      || 'WHEN ''MV_CABLE_JOINT'' THEN ''MV_JOINT'' '
      || 'WHEN ''LV_CABLE_JOINT'' THEN ''LV_JOINT'' END '
      || 'WHERE %I IN (''MV_CABLE_JOINT'', ''LV_CABLE_JOINT'')',
      pairs[i][1], pairs[i][2], pairs[i][3], pairs[i][3], pairs[i][3]);
  END LOOP;
END $$;

-- ---------------------------------------------------------------------------
-- 5. Drop the stale per-class tile functions (migration 0038 named them
-- gis.<code>(z,x,y)). After the table rename their bodies point at a table that no
-- longer exists; the recompile under the new code makes gis.mv_joint(...) /
-- gis.lv_joint(...). The Phase-58 Go tile router reads physical_table (updated
-- above), so map tiles are unaffected. IF EXISTS -> no-op if absent.
-- ---------------------------------------------------------------------------
DROP FUNCTION IF EXISTS gis.mv_cable_joint(integer, integer, integer);
DROP FUNCTION IF EXISTS gis.lv_cable_joint(integer, integer, integer);

COMMIT;
