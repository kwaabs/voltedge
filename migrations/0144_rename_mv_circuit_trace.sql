-- 0144_rename_mv_circuit_trace.sql
--
-- "MV Circuit trace" is network-AGNOSTIC: it scopes a trace to the start
-- feature's circuit_id, and a circuit_id already identifies one MV *or* LV
-- circuit (see seed/trace_configurations_example.sql's own comment). The "MV" in
-- the display name is misleading -- on an LV feature (e.g. a service point) the
-- menu shows "MV Circuit trace" even though it would trace the LV circuit.
--
-- Rename the DISPLAY NAME to the accurate "Circuit trace". The code
-- (MV_CIRCUIT_TRACE) is unchanged -- the frontend resolves the config by its
-- direction/code, never its name, so nothing breaks.
UPDATE metadata.trace_configurations
SET name = 'Circuit trace'
WHERE code = 'MV_CIRCUIT_TRACE' AND name = 'MV Circuit trace';
