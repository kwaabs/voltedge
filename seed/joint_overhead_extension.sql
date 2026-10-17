-- seed/joint_overhead_extension.sql
-- Purpose: NOT a migration -- lives under /seed (apply.sh only scans /migrations),
-- the same reasoning as seed/cable_joint_example.sql, which this EXTENDS.
--
-- Generalises the cable joint (seed/cable_joint_example.sql) into a single conductor
-- JOINT that covers OVERHEAD as well as underground, per the "one class, the subtype
-- carries the medium" design (Shape A). It is ADDITIVE and DATA-PRESERVING: there is
-- published cable-joint data, so this neither drops nor recreates the classes and it
-- KEEPS the class CODES (MV_CABLE_JOINT / LV_CABLE_JOINT) and their physical tables.
-- A code rename would have to chase the code through every CODE-keyed table that
-- holds live data -- runtime.feature_registry, runtime.asset_tags, diagram_placements,
-- files.attachments, faults.asset_class, wam.asset_class, gis.circuits.source_feature_
-- class, ... -- a risky sweep for no user-visible gain, since the user only sees the
-- NAME. The id/uuid-keyed graph (connectivity, relationships, symbology, CIM mapping,
-- trace_tiers, functional_role) is untouched precisely because the code is unchanged.
--
-- What changes (all idempotent -- guarded UPDATEs + ON CONFLICT inserts):
--   * display name "MV/LV cable joint" -> "MV/LV Joint".
--   * subtypes: existing codes 1-4 KEPT (no row remap) and relabelled; NEW overhead
--     subtypes 5-7 added. The MEDIUM is implied by the subtype:
--        1 Cable straight (UG)        2 Cable branch (tee) (UG)
--        3 Riser / pothead (OH<->UG transition, was 'Transition')
--        4 Cable termination (UG)     5 OH splice (OH)
--        6 OH tap (tee) (OH)          7 OH dead-end (OH)
--   * CONNECTOR_TYPE domain + a connector_type field (the OH analogue of
--     insulation_type). insulation_type is made NULLABLE so an OH row can omit it,
--     but stays REQUIRED on the cable subtypes (1-4) via subtype_field_rules;
--     connector_type is REQUIRED on the OH subtypes (5-7) only. NO existing published
--     row is invalidated: cable rows still satisfy their insulation rule, and the new
--     required connector_type applies ONLY to the brand-new OH subtypes.
--   * T3 (tee third leg) terminal added on subtype 6 (OH tap), mirroring subtype 2
--     (cable branch); the two tee network_rules are re-asserted so the new T3 gets a
--     rule row (the base T1/T2 feed-through rules are class-level and already cover
--     every subtype, overhead included).
--
-- NOT done here (by design): no CABLE_CHAMBER containment for the OH subtypes -- the
-- containment relationship CLASS already exists (chamber -> joint) and is optional, so
-- an OH splice simply never gets a chamber instance; an open-air splice has no box.
-- Symbology / CIM / trace_tiers / functional_role are unchanged (code unchanged), so
-- the overhead subtypes render + trace with the joint's existing class-level entries.
--
-- Recompile vs this seed -- who does what (the compiler is ADDITIVE-ONLY for
-- COLUMNS but DOES rebuild CHECK constraints every recompile via
-- syncCheckConstraints, api/internal/compiler):
--   * RECOMPILE handles: adding the connector_type column (nullable -> safe on a
--     populated table); widening the subtype CHECK from (1..4) to (1..7) so the OH
--     subtypes are insertable; adding connector_type's coded-domain CHECK.
--   * THIS SEED handles: dropping insulation_type's physical NOT NULL -- nullability
--     is NOT a CHECK, so syncCheckConstraints won't touch it, and the additive-only
--     compiler never rewrites an existing column (its only "fix" is DROP+recreate
--     the table, which would destroy the published rows). So we DROP NOT NULL here
--     directly (guarded, idempotent). A NULL always satisfies the domain CHECK, so
--     an OH row with no insulation_type still passes insulation_type's CHECK.
-- Apply, then RECOMPILE both classes:
--   task seed-joint-overhead
--   task compile CODE=MV_CABLE_JOINT
--   task compile CODE=LV_CABLE_JOINT
--
-- Depends on seed/cable_joint_example.sql (the base classes/subtypes/terminals/rules).
--
-- Static-review note: bash.exe EPERM here, so this is reviewed statically -- a live
-- psql apply + the two recompiles + creating an OH splice and an OH tap + a DOWNSTREAM
-- trace through an OH joint are still owed to call it verified.

-- ===========================================================================
-- 1. Display name: cable joint -> joint (code + physical table unchanged).
-- ===========================================================================
UPDATE metadata.feature_classes SET name = 'MV Joint' WHERE code = 'MV_CABLE_JOINT';
UPDATE metadata.feature_classes SET name = 'LV Joint' WHERE code = 'LV_CABLE_JOINT';

-- ===========================================================================
-- 2. CONNECTOR_TYPE domain -- the OH analogue of INSULATION_TYPE (how the splice
-- is made, not what the cable is insulated with).
-- ===========================================================================
INSERT INTO metadata.domains (code, name, domain_type) VALUES
    ('CONNECTOR_TYPE', 'Connector type', 'coded')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domain_values (domain_id, code, label, sort_order)
SELECT d.id, v.code, v.label, v.sort_order
FROM metadata.domains d
JOIN (VALUES
    ('CONNECTOR_TYPE', 'COMPRESSION',    'Compression splice',      1),
    ('CONNECTOR_TYPE', 'BOLTED',         'Bolted',                  2),
    ('CONNECTOR_TYPE', 'WEDGE',          'Wedge / H-tap',           3),
    ('CONNECTOR_TYPE', 'PG_CLAMP',       'Parallel-groove clamp',   4),
    ('CONNECTOR_TYPE', 'HOT_LINE_CLAMP', 'Hot-line clamp',          5),
    ('CONNECTOR_TYPE', 'DEAD_END_CLAMP', 'Dead-end / strain clamp', 6)
) AS v(domain_code, code, label, sort_order) ON v.domain_code = d.code
ON CONFLICT (domain_id, code) DO NOTHING;

-- ===========================================================================
-- 3. Fields. Make insulation_type NULLABLE (an OH joint has no cable insulation);
-- add the connector_type field (nullable -- a cable joint has no connector).
-- Requiredness is enforced PER-SUBTYPE below, not by the column, so both columns
-- are physically nullable and each subtype's rule decides which one it needs.
-- ===========================================================================
UPDATE metadata.fields f
SET nullable = true
FROM metadata.feature_classes fc
WHERE f.feature_class_id = fc.id
  AND fc.code IN ('MV_CABLE_JOINT', 'LV_CABLE_JOINT')
  AND f.name = 'insulation_type';

-- Drop the PHYSICAL NOT NULL on insulation_type now (the metadata flip above is not
-- enough -- the additive-only compiler won't rewrite the column on recompile, and
-- dropping the table to "fix" it would wipe the published rows). DROP NOT NULL on an
-- already-nullable column is a no-op, and the to_regclass guard skips a class that
-- has not been compiled yet (no physical table). Physical table name is gis.<lower
-- (code)>, the compiler's convention; the code is unchanged, so these names hold.
DO $$
BEGIN
  IF to_regclass('gis.mv_cable_joint') IS NOT NULL THEN
    ALTER TABLE gis.mv_cable_joint ALTER COLUMN insulation_type DROP NOT NULL;
  END IF;
  IF to_regclass('gis.lv_cable_joint') IS NOT NULL THEN
    ALTER TABLE gis.lv_cable_joint ALTER COLUMN insulation_type DROP NOT NULL;
  END IF;
END $$;

-- Compile blocker fix. Two failure modes the recompile hits on a POPULATED joint
-- table, both because the compiler can't rewrite data to fit the schema:
--   (1) NO subtype column at all -- the class was first compiled before it had
--       subtypes, so buildCreateTable never emitted the column; `ADD COLUMN subtype
--       integer NOT NULL` is then rejected on a table with rows (SQLSTATE 23502).
--   (2) a subtype value OUTSIDE the current set 1..7 -- a stale code left by an
--       earlier subtype scheme; syncCheckConstraints' CHECK (subtype IN (1..7)) is
--       then rejected by that row (SQLSTATE 23514).
-- Normalise both here so the recompile's column-add is skipped (column exists) and
-- its CHECK rebuild succeeds: ensure the column exists (nullable), set any NULL or
-- out-of-range subtype to 1 (Cable straight -- the commonest inline splice), then
-- SET NOT NULL. Idempotent + a no-op on an already-correct table. NOTE: an existing
-- joint carrying a stale/invalid subtype is reset to Cable straight -- reclassify any
-- such rows afterward (find them first with: SELECT id, subtype FROM gis.<table>
-- WHERE subtype IS NULL OR subtype NOT IN (1,2,3,4,5,6,7); run BEFORE this seed).
DO $$
DECLARE tbl text;
BEGIN
  FOREACH tbl IN ARRAY ARRAY['gis.mv_cable_joint', 'gis.lv_cable_joint'] LOOP
    IF to_regclass(tbl) IS NULL THEN
      CONTINUE;
    END IF;
    IF NOT EXISTS (
         SELECT 1 FROM information_schema.columns
         WHERE table_schema = split_part(tbl, '.', 1)
           AND table_name = split_part(tbl, '.', 2)
           AND column_name = 'subtype') THEN
      EXECUTE format('ALTER TABLE %s ADD COLUMN subtype integer', tbl);
    END IF;
    EXECUTE format('UPDATE %s SET subtype = 1 WHERE subtype IS NULL OR subtype NOT IN (1,2,3,4,5,6,7)', tbl);
    EXECUTE format('ALTER TABLE %s ALTER COLUMN subtype SET NOT NULL', tbl);
  END LOOP;
END $$;

INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, 'connector_type', 'text', d.id, fg.id, true
FROM metadata.feature_classes fc
JOIN metadata.domains d ON d.code = 'CONNECTOR_TYPE'
JOIN metadata.field_groups fg ON fg.code = 'general'
WHERE fc.code IN ('MV_CABLE_JOINT', 'LV_CABLE_JOINT')
ON CONFLICT (feature_class_id, name) DO NOTHING;

-- ===========================================================================
-- 4. Subtypes. UPSERT all 7 (insert if missing, relabel if present). Codes 1-4
-- are KEPT (no row remap) and relabelled; 5-7 are the new overhead ones. Medium is
-- implied by the subtype. This is an UPSERT, not a relabel-then-insert, because the
-- base seed's 1-4 did not necessarily land on every class (MV_CABLE_JOINT was found
-- in the field with ONLY 5-7 present -- its 1-4 were missing, so a relabel UPDATE
-- was a no-op and the subtype CHECK compiled to IN (5,6,7), which the existing
-- subtype-1 rows then violated). Inserting 1-4 here makes the class self-consistent
-- regardless of what the base seed left behind.
-- ===========================================================================
INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, v.code, v.name
FROM metadata.feature_classes fc
JOIN (VALUES
    (1, 'Cable straight'),
    (2, 'Cable branch (tee)'),
    (3, 'Riser / pothead'),
    (4, 'Cable termination'),
    (5, 'OH splice'),
    (6, 'OH tap (tee)'),
    (7, 'OH dead-end')
) AS v(code, name) ON true
WHERE fc.code IN ('MV_CABLE_JOINT', 'LV_CABLE_JOINT')
ON CONFLICT (feature_class_id, code) DO UPDATE SET name = EXCLUDED.name;

-- ===========================================================================
-- 5. Per-subtype required fields. Cable subtypes (1-4) require insulation_type +
-- condition_rating; overhead subtypes (5-7) require connector_type + condition_rating.
-- condition_rating is thus required on every subtype; insulation_type only on cable,
-- connector_type only on OH -- so neither column's NULLs invalidate a row of the
-- other medium. The 1-4 rules are re-asserted here (not left to the base seed) for
-- the same reason the subtypes are upserted above -- a class that was missing 1-4
-- was missing their rules too. (A rule whose field doesn't exist on the class is
-- simply not produced by the JOIN, so this is safe even if a field is absent.)
-- ===========================================================================
INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, true
FROM metadata.subtypes s
JOIN metadata.feature_classes fc ON fc.id = s.feature_class_id
JOIN metadata.fields f ON f.feature_class_id = s.feature_class_id
WHERE fc.code IN ('MV_CABLE_JOINT', 'LV_CABLE_JOINT')
  AND (
    (s.code IN (1, 2, 3, 4) AND f.name IN ('insulation_type', 'condition_rating'))
    OR (s.code IN (5, 6, 7) AND f.name IN ('connector_type', 'condition_rating'))
  )
ON CONFLICT (subtype_id, field_id) DO NOTHING;

-- ===========================================================================
-- 6. Terminals. T1/T2 are generic (subtype_id null) and already cover every
-- subtype incl. overhead. Add T3 (the tee's third leg) on BOTH tee subtypes --
-- 2 (cable branch) and 6 (OH tap) -- role 'other' (a tap, not a winding). Subtype
-- 2's T3 is re-asserted (not left to the base seed) in case the class was missing
-- subtype 2 and thus its T3, same robustness reasoning as the subtypes/rules above.
-- ===========================================================================
INSERT INTO metadata.terminals (feature_class_id, subtype_id, code, role, phase_capable)
SELECT s.feature_class_id, s.id, 'T3', 'other', true
FROM metadata.subtypes s
JOIN metadata.feature_classes fc ON fc.id = s.feature_class_id
WHERE fc.code IN ('MV_CABLE_JOINT', 'LV_CABLE_JOINT') AND s.code IN (2, 6)  -- tee subtypes
ON CONFLICT (feature_class_id, subtype_id, code) DO NOTHING;

-- ===========================================================================
-- 7. network_rules for the new tee terminal. The base T1/T2 feed-through rules
-- are class-level (keyed to the conductor + the generic terminals) and already
-- apply to every subtype including overhead -- NOTHING to add there. Only the tee
-- needs rules for its T3, and the joint now has TWO T3 terminals (subtype 2 and
-- subtype 6), so re-asserting the two tee rules adds the subtype-6 T3 rows while
-- the NOT EXISTS guard skips the subtype-2 ones that already exist. Keyed to the
-- joint's own network conductor, exactly as seed/cable_joint_example.sql.
-- ===========================================================================

-- Conductor into the joint's T3 (per T3 terminal: subtype 2 already has a rule,
-- subtype 6 gets a fresh one).
INSERT INTO metadata.network_rules (name, from_feature_class_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT cond.code || ' -> ' || j.code || ' T3', cond.id, j.id, t3.id, NULL, true
FROM (VALUES ('MV_CABLE_JOINT', 'MV_CONDUCTOR'), ('LV_CABLE_JOINT', 'LV_CONDUCTOR')) AS pair(joint_code, cond_code)
JOIN metadata.feature_classes j ON j.code = pair.joint_code
JOIN metadata.feature_classes cond ON cond.code = pair.cond_code
JOIN metadata.terminals t3 ON t3.feature_class_id = j.id AND t3.code = 'T3'
WHERE NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = cond.id AND to_feature_class_id = j.id AND to_terminal_id = t3.id
  );

-- Joint T3 onward to more conductor (the tee's second downstream leg).
INSERT INTO metadata.network_rules (name, from_feature_class_id, from_terminal_id, to_feature_class_id, phase_rule, is_valid)
SELECT j.code || ' T3 -> ' || cond.code, j.id, t3.id, cond.id, NULL, true
FROM (VALUES ('MV_CABLE_JOINT', 'MV_CONDUCTOR'), ('LV_CABLE_JOINT', 'LV_CONDUCTOR')) AS pair(joint_code, cond_code)
JOIN metadata.feature_classes j ON j.code = pair.joint_code
JOIN metadata.feature_classes cond ON cond.code = pair.cond_code
JOIN metadata.terminals t3 ON t3.feature_class_id = j.id AND t3.code = 'T3'
WHERE NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = j.id AND from_terminal_id = t3.id AND to_feature_class_id = cond.id
  );
