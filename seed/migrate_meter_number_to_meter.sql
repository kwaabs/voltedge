-- seed/migrate_meter_number_to_meter.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, and this
-- backfill depends on the METER feature class + the SERVICE_POINT-owns-METER
-- relationship_class existing, which live in seed/lv_metering_example.sql (a
-- fresh DB running migrations alone would not have them). So it lives under
-- /seed and is applied by hand, once, on a database that already carries real
-- SERVICE_POINT rows with legacy meter_number values.
--
-- WHAT IT DOES
-- ------------
-- The Phase 9 metering cleanup dropped the SERVICE_POINT.meter_number scalar in
-- favour of the first-class METER feature + the optional `SERVICE_POINT owns
-- METER` composite relationship (see docs/open-items.md's 2026-09-02 entry). The
-- seed edits only change *new* installs; this script migrates the *existing*
-- data: for every non-deleted service point that still carries a meter_number,
-- it creates one real METER feature co-located with the service point and links
-- them with the ownership relationship. After this runs, meter_number is pure
-- redundancy and is deprecated separately (see the UPDATE at the bottom).
--
-- IDEMPOTENT: re-running skips any service point that already owns a METER under
-- the composite relationship, so a second run is a no-op (it will only pick up
-- service points added since, or ones whose meter creation previously failed).
--
-- HEURISTIC MAPPINGS (review before running -- these are assumptions, not facts
-- carried in the old data, and are the one place you may want to hand-tune):
--   * METER.meter_serial  <- service_point.meter_number  (the device identity;
--                            this is the faithful 1:1 carry-over).
--   * METER.subtype       <- residential -> whole_current (1);
--                            commercial/industrial -> ct_metered (2).
--   * METER.phase (required, no phase data existed on the SP) <-
--                            residential -> 'L1N' (1-phase);
--                            commercial/industrial -> 'L1L2L3N' (3-phase).
--   * METER.status / geom / territory_id are copied verbatim from the SP, so a
--     published SP yields a published meter at the same location in the same
--     territory. name is set to 'Meter <meter_number>'. account_ref is left
--     NULL (the CIS/MDM billing link is populated later, not derivable here).
-- If those defaults are wrong for your fleet, edit the two CASE expressions
-- below (or run this on a subset by narrowing the FOR ... LOOP query's WHERE).
--
-- WHAT IT DELIBERATELY DOES NOT DO
--   * It does NOT rewire connectivity. The migrated meter is created as an
--     owned asset + relationship only; it is not spliced *inline* into the
--     electrical path (SERVICE_CUTOUT/LV -> METER.T1, METER.T2 -> SERVICE_POINT).
--     Inline re-wiring is a topological change per site, out of scope for a bulk
--     backfill -- do it deliberately, feature by feature, where you want the
--     meter to be a traced node.
--   * It does NOT write gis.feature_history rows. This is a raw bulk backfill
--     that bypasses the app-level CRUD write path (Phase 41), so these
--     system-created meters have no 'create' audit row. That's intentional for a
--     one-off migration; every subsequent edit through the API audits normally.
--   * It does NOT clear or drop meter_number. Deprecating the field is the
--     separate, non-destructive UPDATE at the end (the physical column and its
--     data stay; the field just goes invisible to the compiler/CRUD). That
--     UPDATE is paired with a DROP NOT NULL on the physical column, because a DB
--     that compiled meter_number as a required column would otherwise reject
--     every NEW service_point insert (CRUD stops supplying the deprecated field,
--     leaving a NULL against a NOT NULL constraint the compiler never relaxes --
--     see api/internal/compiler/ddl.go: deprecation is a status flip, never a
--     physical DDL change).
--
-- Apply with psql AFTER seed/lv_metering_example.sql (and its dependencies) have
-- been applied and the three classes compiled:
--   psql "$DATABASE_URL" -f seed/migrate_meter_number_to_meter.sql
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL has
-- been reviewed statically against the live physical schema (gis.meter /
-- gis.service_point column shape from api/internal/compiler/ddl.go,
-- runtime.feature_relationships from migrations/0017) -- a live psql apply +
-- a spot-check of the created meters and relationships is still owed.

DO $$
DECLARE
    sp             RECORD;
    new_meter_id   uuid;
    meter_class_id uuid;
    sp_class_id    uuid;
    rel_class_id   uuid;
    m_subtype      integer;
    m_phase        text;
    migrated       integer := 0;
BEGIN
    SELECT id INTO meter_class_id FROM metadata.feature_classes WHERE code = 'METER';
    SELECT id INTO sp_class_id    FROM metadata.feature_classes WHERE code = 'SERVICE_POINT';
    SELECT id INTO rel_class_id   FROM metadata.relationship_classes
        WHERE name = 'SERVICE_POINT owns METER (composite)';

    -- Fail loudly rather than silently doing nothing if the prerequisites are
    -- missing -- the most likely mistake is running this before the metering
    -- seed, and a clean error names exactly what to apply first.
    IF meter_class_id IS NULL OR sp_class_id IS NULL OR rel_class_id IS NULL THEN
        RAISE EXCEPTION 'migrate_meter_number_to_meter: METER / SERVICE_POINT / ownership relationship_class not found -- apply seed/lv_metering_example.sql (and compile the classes) first';
    END IF;

    FOR sp IN
        SELECT s.id, s.subtype, s.status, s.geom, s.territory_id, s.meter_number
        FROM gis.service_point s
        WHERE s.meter_number IS NOT NULL
          AND btrim(s.meter_number) <> ''
          AND s.deleted_at IS NULL
          -- Skip service points that already own a meter under this composite
          -- relationship -- this is what makes the whole script re-runnable.
          AND NOT EXISTS (
              SELECT 1 FROM runtime.feature_relationships fr
              WHERE fr.relationship_class_id = rel_class_id
                AND fr.origin_feature_id = s.id
          )
    LOOP
        -- Heuristic mappings (documented in the header) -- edit here to change.
        m_subtype := CASE sp.subtype WHEN 1 THEN 1 ELSE 2 END;              -- residential -> whole_current(1), else ct_metered(2)
        m_phase   := CASE sp.subtype WHEN 1 THEN 'L1N' ELSE 'L1L2L3N' END;  -- residential -> 1-phase, else 3-phase

        INSERT INTO gis.meter
            (name, subtype, status, geom, meter_serial, account_ref, phase, territory_id)
        VALUES
            ('Meter ' || sp.meter_number, m_subtype, sp.status, sp.geom,
             sp.meter_number, NULL, m_phase, sp.territory_id)
        RETURNING id INTO new_meter_id;

        INSERT INTO runtime.feature_relationships
            (relationship_class_id, origin_feature_class_id, origin_feature_id,
             destination_feature_class_id, destination_feature_id)
        VALUES
            (rel_class_id, sp_class_id, sp.id, meter_class_id, new_meter_id)
        ON CONFLICT (relationship_class_id, origin_feature_id, destination_feature_id) DO NOTHING;

        migrated := migrated + 1;
    END LOOP;

    RAISE NOTICE 'migrate_meter_number_to_meter: created % METER feature(s) + ownership relationship(s)', migrated;
END $$;

-- With every meter_number now carried by a real METER feature, deprecate the
-- legacy scalar field. status <> 'active' makes it invisible to the compiler and
-- CRUD engine (api/internal/metadata/load.go's active-only filter); the physical
-- gis.service_point.meter_number column and its data stay put, harmlessly. Run
-- `task compile CODE=SERVICE_POINT` afterwards so the writable/loadable field set
-- drops it.
UPDATE metadata.fields
   SET status = 'deprecated'
 WHERE name = 'meter_number'
   AND feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'SERVICE_POINT');

-- Relax the physical constraint. On a DB that compiled meter_number as required
-- (NOT NULL, no default), deprecating the field alone breaks new inserts: CRUD
-- no longer supplies the value, so the NOT NULL constraint rejects the row. The
-- compiler never relaxes a deprecated column (ddl.go: deprecation is a metadata
-- status flip, not a physical DROP), so we relax it here. Guarded on column
-- existence (ALTER COLUMN has no IF EXISTS) so a fresh DB -- which never created
-- the legacy column -- is skipped; DROP NOT NULL is itself a no-op if the column
-- is already nullable, so a re-run is harmless.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'gis' AND table_name = 'service_point'
          AND column_name = 'meter_number'
    ) THEN
        ALTER TABLE gis.service_point ALTER COLUMN meter_number DROP NOT NULL;
    END IF;
END $$;
