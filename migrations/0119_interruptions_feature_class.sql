-- 0119_interruptions_feature_class.sql
-- Phase 63 ("the network ends at the meter"): a customer interruption's supply
-- is no longer necessarily a SERVICE_POINT. Once load lives on the PRIMARY METER
-- (seed/meter_load_leaf.sql), a de-energized cone freezes both SERVICE_POINT and
-- PRIMARY-METER supplies as interruption rows -- so the fact table must record
-- WHICH class each frozen supply belongs to, for the map overlay to draw it back
-- and for the reliability denominator to stay on the same footing as the frozen
-- numerator.
--
-- Additive + backward-compatible: existing rows are all SERVICE_POINTs (that was
-- the only load leaf before this phase), so feature_class backfills to
-- 'SERVICE_POINT' and the column carries that as its default. service_point_id
-- keeps its name (a bare, unenforced uuid ref, per the schema's FK convention in
-- 0087) but its MEANING widens to "the supply feature's id, in feature_class's
-- table". The uniqueness key widens to include feature_class so an incident can
-- carry one row per (class, id) -- SP ids and meter ids are distinct uuids in
-- practice, but the class belongs in the key for correctness, not luck.

ALTER TABLE oms.customer_interruptions
    ADD COLUMN IF NOT EXISTS feature_class text NOT NULL DEFAULT 'SERVICE_POINT';

COMMENT ON COLUMN oms.customer_interruptions.feature_class IS
    'Feature class of the interrupted supply (SERVICE_POINT or a PRIMARY METER, Phase 63). service_point_id is that feature''s id within this class''s gis table.';

-- Widen the one-row-per-supply uniqueness to (incident, class, id). The original
-- inline UNIQUE (incident_id, service_point_id) from 0087 carries Postgres's
-- derived constraint name; drop it by that name if present (idempotent) before
-- adding the widened one, and guard the add so a re-run is a no-op.
ALTER TABLE oms.customer_interruptions
    DROP CONSTRAINT IF EXISTS customer_interruptions_incident_id_service_point_id_key;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'customer_interruptions_incident_class_supply_key'
          AND conrelid = 'oms.customer_interruptions'::regclass
    ) THEN
        ALTER TABLE oms.customer_interruptions
            ADD CONSTRAINT customer_interruptions_incident_class_supply_key
            UNIQUE (incident_id, feature_class, service_point_id);
    END IF;
END $$;
