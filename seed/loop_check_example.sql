-- seed/loop_check_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this
-- lives under /seed on purpose, same reasoning as the other seed files.
-- A second SHORTEST_PATH trace_configurations row, distinct from
-- SHORTEST_PATH_TRACE in exactly one setting: stop_at_open_devices is
-- true here. SHORTEST_PATH_TRACE deliberately leaves barriers off
-- ("a path query between two points is about topology, not live
-- state" -- seed/trace_configurations_example.sql's own comment); a
-- loop check needs the opposite answer -- whether a second path is
-- *currently live*, not merely whether one exists on paper. Closing a
-- switch between two points that are only connected topologically
-- through an already-open device wouldn't actually create a loop right
-- now.
--
-- No new Go code needed for this at all -- api/internal/trace's bfs
-- already supports a barrier-respecting target search (that's exactly
-- what SHORTEST_PATH_TRACE itself uses, just with barriers off); this
-- is a second, differently-configured row through the same code path.
-- The UI side (connect-confirm-panel.tsx's "Check for loop") is what
-- actually uses it, before establishing a new connectivity edge.
--
-- Depends on seed/switching_device_example.sql having already been
-- applied (reuses its DIST_SWITCH feature class and device_state field
-- for the barrier rule below, same as the other trace_configurations
-- seed files).
--
-- Apply with psql after seed/switching_device_example.sql. Nothing to
-- compile -- trace_configurations rows have no physical footprint.

INSERT INTO metadata.trace_configurations (code, name, direction_value_id, stop_at_open_devices, cross_transformers, respect_phase)
SELECT 'LOOP_CHECK_TRACE', 'Loop check', dv.id, true, false, true
FROM metadata.domain_values dv
JOIN metadata.domains d ON d.id = dv.domain_id
WHERE d.code = 'TRACE_DIRECTION' AND dv.code = 'SHORTEST_PATH'
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.trace_barrier_rules (trace_configuration_id, feature_class_id, field_id, halts_on_value_id)
SELECT tc.id, sw.id, f.id, dv.id
FROM metadata.trace_configurations tc
JOIN metadata.feature_classes sw ON sw.code = 'DIST_SWITCH'
JOIN metadata.fields f ON f.feature_class_id = sw.id AND f.name = 'device_state'
JOIN metadata.domain_values dv ON dv.domain_id = f.domain_id AND dv.code = 'OPEN'
WHERE tc.code = 'LOOP_CHECK_TRACE'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.trace_barrier_rules
    WHERE trace_configuration_id = tc.id AND feature_class_id = sw.id AND field_id = f.id AND halts_on_value_id = dv.id
  );
