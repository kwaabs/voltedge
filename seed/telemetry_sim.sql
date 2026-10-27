-- telemetry_sim.sql
-- Live-state soak/demo seed (blocker #1): populate ops.device_telemetry for every
-- operable device so the reconciliation view (and its Apply action) can be
-- exercised WITHOUT a real SCADA/DMS gateway. It writes a realistic mix:
--   ~70% AGREE   (telemetry state = the model's device_state, fresh)
--   ~20% DISAGREE (state flipped OPEN<->CLOSED, so reconciliation flags it)
--   ~10% STALE    (measured_at an hour ago, so it reads as a quiet/dead device)
-- All tagged source='SIM' so the whole lot is trivial to remove:
--     DELETE FROM ops.device_telemetry WHERE source = 'SIM';   (or: task clean-telemetry-sim)
--
-- Discovers device classes from metadata (those with an active device_state +
-- physical table), so it covers every operable class without a hard-coded list --
-- the same derivation deviceops/reconciliation use. Table names come from the
-- server-controlled physical_table; the class code binds as a literal.

DO $$
DECLARE
    c RECORD;
BEGIN
    FOR c IN
        SELECT fc.code, fc.physical_table
        FROM metadata.feature_classes fc
        JOIN metadata.fields fld
             ON fld.feature_class_id = fc.id AND fld.name = 'device_state' AND fld.status = 'active'
        WHERE fc.status = 'active' AND fc.physical_table IS NOT NULL
    LOOP
        EXECUTE format($f$
            INSERT INTO ops.device_telemetry
                (feature_class_code, feature_id, state, quality, source, measured_at, ingested_at)
            SELECT %L,
                   g.id,
                   CASE WHEN random() < 0.2
                        THEN (CASE g.device_state WHEN 'OPEN' THEN 'CLOSED' WHEN 'CLOSED' THEN 'OPEN' ELSE g.device_state END)
                        ELSE g.device_state END,
                   'good',
                   'SIM',
                   CASE WHEN random() < 0.1
                        THEN now() - interval '1 hour'
                        ELSE now() - (random() * interval '20 seconds') END,
                   now()
            FROM %s g
            JOIN runtime.feature_registry r
                 ON r.feature_id = g.id AND r.deleted_at IS NULL AND r.feature_class_code = %L
            WHERE g.device_state IS NOT NULL
            ON CONFLICT (feature_class_code, feature_id) DO UPDATE SET
                state = EXCLUDED.state,
                quality = EXCLUDED.quality,
                source = EXCLUDED.source,
                measured_at = EXCLUDED.measured_at,
                ingested_at = now()
        $f$, c.code, c.physical_table, c.code);
    END LOOP;
END $$;
