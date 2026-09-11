-- seed/trace_barrier_resync.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives
-- under /seed on purpose, same reasoning as the other seed files.
--
-- WHY THIS EXISTS: the OPEN-device trace barrier is data, not code. Each
-- switching-device seed (seed/switching_device_example.sql for DIST_SWITCH,
-- seed/mv_switching_example.sql for MV_BREAKER/MV_ISOLATOR/MV_SECTIONALIZER,
-- seed/mv_protection_example.sql for MV_FUSE, seed/mv_lv_switch_example.sql for
-- MV_SWITCH/LV_SWITCH)
-- inserts its own trace_barrier_rules rows with a
--   JOIN metadata.trace_configurations tc ON tc.stop_at_open_devices = true
-- so the barrier attaches to every barrier-respecting config. But that JOIN
-- only sees the configs that EXIST at the moment the device seed runs: if a
-- device seed was ever applied before seed/trace_configurations_example.sql
-- (or before a later barrier-respecting config was added), it inserted ZERO
-- barrier rows, and because every such INSERT is guarded ON CONFLICT / WHERE
-- NOT EXISTS, re-running the device seed afterward is a silent no-op. The
-- symptom is a trace that walks straight THROUGH an open switch/fuse/breaker
-- because the engine has no barrier row to stop at (api/internal/trace's
-- checkBarrier finds nothing keyed for that class).
--
-- This file resynchronises the barrier set from first principles, order-
-- independently: for EVERY feature class that has a `device_state` field whose
-- coded domain carries an `OPEN` value, attach an OPEN barrier to EVERY
-- trace_configurations row with stop_at_open_devices = true. It is the union of
-- all the per-seed barrier inserts, so running it once after all the device and
-- trace-configuration seeds are in place guarantees the full set regardless of
-- the order those seeds were originally applied. Idempotent (WHERE NOT EXISTS),
-- so it is safe to run repeatedly.
--
-- Metadata-only: trace_barrier_rules has no physical footprint (the trace
-- engine reads it directly), so nothing needs compiling after this.
--
-- Apply with psql (or `task seed-trace-barrier-resync`) AFTER every switching-
-- device seed and seed/trace_configurations_example.sql. A no-op on a database
-- where every barrier row already exists.
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL has
-- been reviewed statically -- a live psql apply + a downstream trace across an
-- OPEN device (confirming it now stops) are still owed to call it verified.

INSERT INTO metadata.trace_barrier_rules (trace_configuration_id, feature_class_id, field_id, halts_on_value_id)
SELECT tc.id, f.feature_class_id, f.id, dv.id
FROM metadata.fields f
JOIN metadata.domain_values dv ON dv.domain_id = f.domain_id AND dv.code = 'OPEN'
JOIN metadata.trace_configurations tc ON tc.stop_at_open_devices = true
WHERE f.name = 'device_state'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.trace_barrier_rules r
    WHERE r.trace_configuration_id = tc.id
      AND r.feature_class_id = f.feature_class_id
      AND r.field_id = f.id
      AND r.halts_on_value_id = dv.id
  );
