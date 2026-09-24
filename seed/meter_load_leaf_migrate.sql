-- seed/meter_load_leaf_migrate.sql
-- Phase 63 -- the DESTRUCTIVE half of "the network ends at the meter".
-- seed/meter_load_leaf.sql made the METER able to carry load; this script MOVES
-- existing load from each trailing SERVICE_POINT onto the meter that feeds it,
-- retires that now-redundant SP, and finally retires the METER->SERVICE_POINT
-- wiring itself (the T2 terminal + its rule). After this, a metered supply is a
-- meter leaf and an unmetered supply is a SERVICE_POINT leaf -- never both.
--
-- !!! THIS IS DESTRUCTIVE AND IRREVERSIBLE (soft-deletes SERVICE_POINT rows,
-- !!! deletes connectivity, drops a terminal + rule). Review the NOTICE output
-- !!! against a backup / dev copy BEFORE running it on anything real. It cannot
-- !!! be verified from the dev shell here (bash.exe EPERM) -- a human must apply
-- !!! it against a real database and read the reported counts.
--
-- PRECONDITIONS (in order):
--   1. seed/meter_load_leaf.sql applied AND `task compile CODE=METER` run, so
--      gis.meter carries demand_kw/power_factor/customers_served/load_priority
--      and meter_role. (This script assumes those columns exist; it errors
--      loudly if not, which is the correct "you skipped step 1" signal.)
--   2. seed/service_point_load_priority(_backfill) applied, so each SP's
--      load_priority is set (the backfill already folds the legacy is_critical
--      flag into it: true -> P2). This script reads load_priority as the single
--      source of truth; any SP still NULL is copied as Standard P4.
--
-- SAFETY MODEL -- it migrates ONLY clean, unambiguous, dependency-free supplies
-- and NOTICEs everything it skips, so a partial/odd topology degrades to "did
-- less" not "did wrong":
--   * Only a 1:1 METER<->SERVICE_POINT pair is migrated (the SP has exactly one
--     meter neighbour AND that meter has exactly one SP neighbour). Multi-meter
--     or multi-SP tangles (check-meter arrangements, shared SPs) are LEFT as-is
--     for a human to resolve.
--   * A SERVICE_POINT with an UNRESTORED interruption or an OPEN trouble call is
--     left in place (retiring it would orphan live OMS state).
--   * A meter that already carries load (meter_role IS NOT NULL) is treated as
--     already migrated and skipped -- so re-running this script is safe (idempotent).
--   * The METER.T2 terminal + the `METER T2 -> SERVICE_POINT T1` rule are dropped
--     ONLY when NO METER<->SERVICE_POINT connectivity remains at all; if anything
--     was skipped above, they are kept (dropping T2 would strand those links) and
--     a NOTICE says so. Re-run after resolving the skips to finish the retirement.
--
-- Wrapped in a single transaction: all-or-nothing. A temp table (ON COMMIT DROP)
-- holds the vetted pairs so every step acts on exactly the same set.
--
-- Apply:  task seed-meter-load-leaf-migrate   (or psql -f directly)
--         (no recompile -- this touches data + one terminal/rule, no columns)

BEGIN;

-- Vetted 1:1 pairs to migrate: normalize connectivity to (meter_id, sp_id),
-- keep only clean 1:1 links whose SP has no live OMS dependency and whose meter
-- has not already been given load.
CREATE TEMP TABLE _mll_go ON COMMIT DROP AS
WITH mcls AS (SELECT id FROM metadata.feature_classes WHERE code = 'METER'),
     scls AS (SELECT id FROM metadata.feature_classes WHERE code = 'SERVICE_POINT'),
     links AS (
        SELECT c.from_feature_id AS meter_id, c.to_feature_id AS sp_id, c.id AS conn_id
        FROM runtime.feature_connectivity c
        WHERE c.from_feature_class_id = (SELECT id FROM mcls)
          AND c.to_feature_class_id   = (SELECT id FROM scls)
        UNION
        SELECT c.to_feature_id, c.from_feature_id, c.id
        FROM runtime.feature_connectivity c
        WHERE c.to_feature_class_id   = (SELECT id FROM mcls)
          AND c.from_feature_class_id = (SELECT id FROM scls)
     ),
     -- Fan-out counts, to keep only clean 1:1 relationships.
     sp_deg AS (SELECT sp_id, count(DISTINCT meter_id) AS n FROM links GROUP BY sp_id),
     m_deg  AS (SELECT meter_id, count(DISTINCT sp_id) AS n FROM links GROUP BY meter_id)
SELECT l.meter_id, l.sp_id
FROM links l
JOIN sp_deg ON sp_deg.sp_id = l.sp_id AND sp_deg.n = 1
JOIN m_deg  ON m_deg.meter_id = l.meter_id AND m_deg.n = 1
JOIN gis.service_point sp ON sp.id = l.sp_id AND sp.deleted_at IS NULL
JOIN gis.meter m          ON m.id = l.meter_id AND m.deleted_at IS NULL
WHERE m.meter_role IS NULL                                     -- not already migrated
  AND NOT EXISTS (                                             -- no unrestored interruption
        SELECT 1 FROM oms.customer_interruptions ci            -- (match by id; class-independent,
        WHERE ci.service_point_id = l.sp_id                    --  so robust to 0119 apply-order)
          AND ci.restored_at IS NULL)
  AND NOT EXISTS (                                             -- no active trouble call
        SELECT 1 FROM oms.trouble_calls tc                     -- (terminal = cleared/cancelled)
        WHERE tc.service_point_id = l.sp_id
          AND tc.status IN ('open', 'associated'));

-- Report what we're about to do vs. what stays behind.
DO $$
DECLARE
    go_ct      int;
    metered_sp int;
BEGIN
    SELECT count(*) INTO go_ct FROM _mll_go;
    SELECT count(DISTINCT c.to_feature_id) INTO metered_sp
    FROM runtime.feature_connectivity c
    WHERE c.to_feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'SERVICE_POINT')
      AND c.from_feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'METER');
    RAISE NOTICE 'meter-load-leaf migrate: % clean 1:1 pair(s) will be migrated', go_ct;
    RAISE NOTICE 'meter-load-leaf migrate: ~% metered SP(s) currently wired (any excess over migrated are ambiguous/dependent skips, left in place)', metered_sp;
END $$;

-- 1. Lift each SP's load onto its meter. load_priority is THE canonical tier:
--    the Phase 62 backfill (service_point_load_priority_backfill.sql) already
--    folded the legacy is_critical flag INTO load_priority (true -> P2, else P4),
--    so we read that column alone -- is_critical is not even guaranteed to exist
--    as a physical column (the backfill itself probes information_schema for it).
--    Any SP still NULL here never had a tier set -> Standard P4. meter_role=PRIMARY.
UPDATE gis.meter m
SET demand_kw        = sp.demand_kw,
    power_factor     = sp.power_factor,
    customers_served = sp.customers_served,
    load_priority    = COALESCE(sp.load_priority, 'P4'),
    meter_role       = 'PRIMARY',
    updated_at       = now()
FROM _mll_go g
JOIN gis.service_point sp ON sp.id = g.sp_id
WHERE m.id = g.meter_id;

-- 2. Retire the now-redundant SERVICE_POINT (soft delete -- history/audit keep it).
UPDATE gis.service_point sp
SET deleted_at = now()
FROM _mll_go g
WHERE sp.id = g.sp_id
  AND sp.deleted_at IS NULL;

-- 3. Remove the meter<->SP connectivity rows for the migrated pairs (both directions).
DELETE FROM runtime.feature_connectivity c
USING _mll_go g
WHERE (c.from_feature_id = g.meter_id AND c.to_feature_id   = g.sp_id)
   OR (c.from_feature_id = g.sp_id    AND c.to_feature_id   = g.meter_id);

-- 4. Retire the METER->SERVICE_POINT wiring ITSELF, but only when the network no
--    longer holds any such connection (i.e. nothing was skipped). Dropping the
--    T2 terminal while an ambiguous meter->SP link still references it would fail
--    the FK anyway -- so we guard and NOTICE rather than error.
DO $$
DECLARE
    remaining int;
    mcls uuid := (SELECT id FROM metadata.feature_classes WHERE code = 'METER');
    scls uuid := (SELECT id FROM metadata.feature_classes WHERE code = 'SERVICE_POINT');
BEGIN
    SELECT count(*) INTO remaining
    FROM runtime.feature_connectivity c
    WHERE (c.from_feature_class_id = mcls AND c.to_feature_class_id = scls)
       OR (c.from_feature_class_id = scls AND c.to_feature_class_id = mcls);

    IF remaining = 0 THEN
        DELETE FROM metadata.network_rules
        WHERE name = 'METER T2 -> SERVICE_POINT T1'
           OR (from_feature_class_id = mcls AND to_feature_class_id = scls);
        DELETE FROM metadata.terminals
        WHERE feature_class_id = mcls AND code = 'T2';
        RAISE NOTICE 'meter-load-leaf migrate: no meter<->SP links remain -- dropped METER.T2 terminal + the METER->SERVICE_POINT rule';
    ELSE
        RAISE NOTICE 'meter-load-leaf migrate: % meter<->SP link(s) still remain (ambiguous/dependent) -- KEEPING METER.T2 + rule; resolve those and re-run to finish retirement', remaining;
    END IF;
END $$;

COMMIT;
