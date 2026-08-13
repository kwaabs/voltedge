-- seed/trace_target_class_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this
-- lives under /seed on purpose, same reasoning as the other seed files.
-- Two trace_configurations rows exercising target_class_id
-- (migrations/0018), the generalization that lets a trace stop at the
-- first node of a given feature class instead of only ever walking
-- until the graph runs out or a specific target feature is named:
--
-- - TRACE_TO_TRANSFORMER (UPSTREAM, target_class DIST_XFMR): started
--   from a consuming feature (a SERVICE_POINT), finds the nearest
--   transformer upstream of it -- a service connection trace. Started
--   from a DIST_XFMR itself, finds the *next* transformer upstream
--   (excluding the start node), which in a radial network is exactly
--   its supplying source -- the same config and mechanism serves both
--   framings, see docs/phase-4-utility-traces.md.
-- - LV_NETWORK_TRACE (DOWNSTREAM, target_class DIST_XFMR,
--   cross_transformers false): started from a transformer, walks its
--   downstream/secondary footprint, halting at (but still recording)
--   any other transformer it would otherwise cross into.
--
-- Depends on seed/service_point_example.sql having already been
-- applied (SERVICE_POINT is what makes TRACE_TO_TRANSFORMER meaningful
-- to run from).
--
-- Apply with psql after seed/service_point_example.sql. Nothing to
-- compile -- trace_configurations rows have no physical footprint.

INSERT INTO metadata.trace_configurations (code, name, direction_value_id, stop_at_open_devices, cross_transformers, respect_phase, target_class_id)
SELECT 'TRACE_TO_TRANSFORMER', 'Trace to transformer', dv.id, true, false, true, xf.id
FROM metadata.domain_values dv
JOIN metadata.domains d ON d.id = dv.domain_id
CROSS JOIN metadata.feature_classes xf
WHERE d.code = 'TRACE_DIRECTION' AND dv.code = 'UPSTREAM' AND xf.code = 'DIST_XFMR'
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.trace_configurations (code, name, direction_value_id, stop_at_open_devices, cross_transformers, respect_phase, target_class_id)
SELECT 'LV_NETWORK_TRACE', 'LV network trace', dv.id, true, false, true, xf.id
FROM metadata.domain_values dv
JOIN metadata.domains d ON d.id = dv.domain_id
CROSS JOIN metadata.feature_classes xf
WHERE d.code = 'TRACE_DIRECTION' AND dv.code = 'DOWNSTREAM' AND xf.code = 'DIST_XFMR'
ON CONFLICT (code) DO NOTHING;

-- Same barrier rule the other two barrier-respecting configs use
-- (DIST_SWITCH open halts traversal) -- both new configs set
-- stop_at_open_devices = true, same reasoning as DOWNSTREAM_TRACE/
-- UPSTREAM_TRACE: you can't trace to a transformer, or trace its LV
-- footprint, through a switch that's deliberately open.

INSERT INTO metadata.trace_barrier_rules (trace_configuration_id, feature_class_id, field_id, halts_on_value_id)
SELECT tc.id, sw.id, f.id, dv.id
FROM metadata.trace_configurations tc
JOIN metadata.feature_classes sw ON sw.code = 'DIST_SWITCH'
JOIN metadata.fields f ON f.feature_class_id = sw.id AND f.name = 'device_state'
JOIN metadata.domain_values dv ON dv.domain_id = f.domain_id AND dv.code = 'OPEN'
WHERE tc.code = 'TRACE_TO_TRANSFORMER'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.trace_barrier_rules
    WHERE trace_configuration_id = tc.id AND feature_class_id = sw.id AND field_id = f.id AND halts_on_value_id = dv.id
  );

INSERT INTO metadata.trace_barrier_rules (trace_configuration_id, feature_class_id, field_id, halts_on_value_id)
SELECT tc.id, sw.id, f.id, dv.id
FROM metadata.trace_configurations tc
JOIN metadata.feature_classes sw ON sw.code = 'DIST_SWITCH'
JOIN metadata.fields f ON f.feature_class_id = sw.id AND f.name = 'device_state'
JOIN metadata.domain_values dv ON dv.domain_id = f.domain_id AND dv.code = 'OPEN'
WHERE tc.code = 'LV_NETWORK_TRACE'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.trace_barrier_rules
    WHERE trace_configuration_id = tc.id AND feature_class_id = sw.id AND field_id = f.id AND halts_on_value_id = dv.id
  );
