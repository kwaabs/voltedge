-- seed/trace_configurations_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this
-- lives under /seed on purpose, same reasoning as the other seed files.
-- One trace_configurations row per raw direction
-- (docs/metadata-core.md's "Network tracing" section: "only four raw
-- direction values ever need to exist" -- connected, downstream,
-- upstream, shortest_path). Isolation and loop/mesh detection compose
-- two of these primitive traces in the Go service rather than needing
-- rows of their own -- not built this pass (see
-- docs/phase-4-trace-engine.md), so this file only seeds the four
-- primitives.
--
-- Depends on seed/switching_device_example.sql having already been
-- applied (this file's trace_barrier_rules rows reference DIST_SWITCH
-- and its device_state field).
--
-- Apply with psql after seed/switching_device_example.sql. Nothing to
-- compile -- trace_configurations/trace_barrier_rules have no physical
-- footprint, they're read directly by api/internal/trace.

INSERT INTO metadata.domains (code, name, domain_type) VALUES
    ('TRACE_DIRECTION', 'Trace direction', 'coded')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domain_values (domain_id, code, label, sort_order)
SELECT d.id, v.code, v.label, v.sort_order
FROM metadata.domains d
JOIN (VALUES
    ('TRACE_DIRECTION', 'CONNECTED', 'Connected', 1),
    ('TRACE_DIRECTION', 'DOWNSTREAM', 'Downstream', 2),
    ('TRACE_DIRECTION', 'UPSTREAM', 'Upstream', 3),
    ('TRACE_DIRECTION', 'SHORTEST_PATH', 'Shortest path', 4)
) AS v(domain_code, code, label, sort_order) ON v.domain_code = d.code
ON CONFLICT (domain_id, code) DO NOTHING;

-- Downstream/upstream both stop at an open device by default -- the
-- common case (don't trace load or supply through an intentionally
-- opened switch). Connected deliberately does not (matches
-- docs/metadata-core.md's own "Feeder connectivity audit" sample:
-- stop_at_open = false) -- an audit of what's wired to what shouldn't
-- silently stop at a switch just because it happens to be open right
-- now. Shortest path also leaves barriers off, for the same reason: a
-- path query between two points is about topology, not live state.
-- cross_transformers/respect_phase are accepted here but not enforced
-- by this pass's trace engine (see "Decisions made" in
-- docs/phase-4-trace-engine.md) -- left at their table defaults
-- (false/true) rather than tuned per row, since nothing reads them yet.

INSERT INTO metadata.trace_configurations (code, name, direction_value_id, stop_at_open_devices, cross_transformers, respect_phase)
SELECT 'CONNECTED_TRACE', 'Connected trace', dv.id, false, false, true
FROM metadata.domain_values dv
JOIN metadata.domains d ON d.id = dv.domain_id
WHERE d.code = 'TRACE_DIRECTION' AND dv.code = 'CONNECTED'
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.trace_configurations (code, name, direction_value_id, stop_at_open_devices, cross_transformers, respect_phase)
SELECT 'DOWNSTREAM_TRACE', 'Downstream trace', dv.id, true, false, true
FROM metadata.domain_values dv
JOIN metadata.domains d ON d.id = dv.domain_id
WHERE d.code = 'TRACE_DIRECTION' AND dv.code = 'DOWNSTREAM'
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.trace_configurations (code, name, direction_value_id, stop_at_open_devices, cross_transformers, respect_phase)
SELECT 'UPSTREAM_TRACE', 'Upstream trace', dv.id, true, false, true
FROM metadata.domain_values dv
JOIN metadata.domains d ON d.id = dv.domain_id
WHERE d.code = 'TRACE_DIRECTION' AND dv.code = 'UPSTREAM'
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.trace_configurations (code, name, direction_value_id, stop_at_open_devices, cross_transformers, respect_phase)
SELECT 'SHORTEST_PATH_TRACE', 'Shortest path trace', dv.id, false, false, true
FROM metadata.domain_values dv
JOIN metadata.domains d ON d.id = dv.domain_id
WHERE d.code = 'TRACE_DIRECTION' AND dv.code = 'SHORTEST_PATH'
ON CONFLICT (code) DO NOTHING;

-- MV Circuit trace (Phase 37, migration 0069's scope_to_circuit). Walks the
-- CONNECTED graph but stays on the start feature's stored circuit: the trace
-- engine reads the start's circuit_id and admits a neighbour only when its own
-- circuit_id matches, so the result is exactly that one circuit's connected
-- members (the membership api/internal/circuit.AssignCircuit stamped). No
-- barriers and stop_at_open_devices = false on purpose: the circuit boundary is
-- *membership*, not live switch state -- a feature past an open switch simply
-- carries a different circuit_id (or none), so the scope gate already stops
-- there. Depends on the Phase 37 Slice 2 seed having added circuit_id to the
-- electrical apparatus classes and those classes being recompiled; a start with
-- no circuit assigned returns a clear 400. Named "MV Circuit" per the common
-- MV use, but the scope_to_circuit gate is network-agnostic (circuit_id already
-- identifies one MV *or* LV circuit) -- an LV variant would be an identical row
-- under a different code/name.
INSERT INTO metadata.trace_configurations (code, name, direction_value_id, stop_at_open_devices, cross_transformers, respect_phase, scope_to_circuit)
SELECT 'MV_CIRCUIT_TRACE', 'MV Circuit trace', dv.id, false, false, true, true
FROM metadata.domain_values dv
JOIN metadata.domains d ON d.id = dv.domain_id
WHERE d.code = 'TRACE_DIRECTION' AND dv.code = 'CONNECTED'
ON CONFLICT (code) DO NOTHING;

-- trace_barrier_rules -- no unique constraint on this table (same
-- reasoning as network_rules: a rule set is authored deliberately),
-- guarded with WHERE NOT EXISTS. One row: on DIST_SWITCH, halt when
-- device_state = OPEN. Only attached to the two configs that actually
-- respect barriers (stop_at_open_devices = true above) -- attaching it
-- to CONNECTED_TRACE or SHORTEST_PATH_TRACE too would be inert (the Go
-- engine only ever consults trace_barrier_rules when
-- stop_at_open_devices is true), so leaving it off makes the two
-- tables agree instead of one silently overriding the other.

INSERT INTO metadata.trace_barrier_rules (trace_configuration_id, feature_class_id, field_id, halts_on_value_id)
SELECT tc.id, sw.id, f.id, dv.id
FROM metadata.trace_configurations tc
JOIN metadata.feature_classes sw ON sw.code = 'DIST_SWITCH'
JOIN metadata.fields f ON f.feature_class_id = sw.id AND f.name = 'device_state'
JOIN metadata.domain_values dv ON dv.domain_id = f.domain_id AND dv.code = 'OPEN'
WHERE tc.code = 'DOWNSTREAM_TRACE'
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
WHERE tc.code = 'UPSTREAM_TRACE'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.trace_barrier_rules
    WHERE trace_configuration_id = tc.id AND feature_class_id = sw.id AND field_id = f.id AND halts_on_value_id = dv.id
  );
