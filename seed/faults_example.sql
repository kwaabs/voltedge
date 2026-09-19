-- seed/faults_example.sql
-- Purpose: Faults module demo data -- a few fault_events you can see on the
-- fault board immediately, without hand-typing. It is a SEED (psql), NOT a
-- migration: apply.sh only scans /migrations, and although this file touches
-- nothing but the faults schema, it is demo content, not structure (same
-- reasoning as every other file under /seed).
--
-- DELIBERATELY PORTABLE: each fault is anchored by a LOCATION PIN (not by a real
-- gis.* feature id, which varies per environment, nor by an OMS incident id,
-- which is minted at prediction time). So this seed depends on migration 0106
-- ONLY -- no compiled feature tables, no prior network seed. The two richer
-- links (incident_id from RaiseFaultFromIncident, defect_id from
-- BookDefectFromFault) are meant to be exercised live through the API, not
-- seeded.
--
-- Fixed uuids + ON CONFLICT (id) DO NOTHING => idempotent (a re-run is a no-op).
--
-- HARD PREREQUISITE (fails LOUDLY if missing):
--   * migration 0106 applied -> `task migrate`

DO $$
BEGIN
    IF to_regclass('faults.fault_events') IS NULL THEN
        RAISE EXCEPTION 'faults.fault_events is missing -- apply migration 0106 first (task migrate)';
    END IF;
END $$;

-- Three demo fault events near central London (arbitrary pins), spanning the
-- lifecycle so the board shows an open one, one under investigation, and a
-- cleared one.
INSERT INTO faults.fault_events
    (id, reference, fault_type, cause, fault_current_a, clearing_time_ms,
     status, detected_by, detected_at, cleared_at, location, title, description)
VALUES
    ('f0000000-0000-0000-0000-000000000001', 'FLT-1001', 'SLG', 'vegetation',
     3200, 120, 'detected', 'protection_relay', now() - interval '2 hours', NULL,
     ST_SetSRID(ST_MakePoint(-0.1276, 51.5072), 4326),
     'Single line-to-ground fault on Feeder 1',
     'Relay picked up an earth fault; vegetation suspected. Investigating.'),

    ('f0000000-0000-0000-0000-000000000002', 'FLT-1002', 'LL', 'equipment_failure',
     8100, 85, 'investigating', 'scada', now() - interval '6 hours', NULL,
     ST_SetSRID(ST_MakePoint(-0.1290, 51.5090), 4326),
     'Phase-to-phase fault near pad-mount transformer',
     'High fault current; likely cable termination failure under investigation.'),

    ('f0000000-0000-0000-0000-000000000003', 'FLT-1003', 'LLL', 'third_party',
     12500, 60, 'cleared', 'field_crew', now() - interval '1 day',
     now() - interval '22 hours',
     ST_SetSRID(ST_MakePoint(-0.1250, 51.5060), 4326),
     'Three-phase fault from third-party dig-in',
     'Contractor struck an MV cable; supply restored after isolation and repair.')
ON CONFLICT (id) DO NOTHING;
