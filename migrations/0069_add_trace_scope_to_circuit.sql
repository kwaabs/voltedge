-- 0069_add_trace_scope_to_circuit.sql
-- Purpose: Phase 37 -- give a trace_configurations row an optional
-- **circuit scope**, so a trace can be confined to features that share the
-- start feature's stored circuit membership (the circuit_id stamped by
-- api/internal/circuit's AssignCircuit, Phase 37 Slice 3). This is the
-- "trace the circuit" primitive: from any feature on a circuit, walk the
-- connected network but only through/onto features carrying the same
-- circuit_id, so the result is exactly that one circuit.
--
-- Why a flag and not a whole new direction: the walk is still one of the four
-- raw directions (CONNECTED for a whole-circuit trace), plus barriers, plus
-- target class -- circuit-scoping is an orthogonal gate layered on top, the
-- same way target_class_id (migration 0018) generalized the engine without a
-- new direction. When true, api/internal/trace reads the start's live
-- circuit_id once and admits a neighbour only when its own circuit_id matches;
-- a feature with no circuit_id field or an unset one is never on the circuit,
-- so it bounds the walk (the circuit boundary is membership, not live switch
-- state -- so a circuit trace typically leaves stop_at_open_devices off).
--
-- Network-agnostic: circuit_id is a uuid pointing at one gis.circuits row,
-- which is itself MV or LV (migration 0062), so matching by circuit_id keeps
-- the walk on a single circuit of a single network automatically -- one flagged
-- config serves an MV or an LV start alike. NOT NULL DEFAULT false: every
-- trace_configurations row seeded before this keeps its exact behaviour.

ALTER TABLE metadata.trace_configurations
    ADD COLUMN scope_to_circuit boolean NOT NULL DEFAULT false;

COMMENT ON COLUMN metadata.trace_configurations.scope_to_circuit IS 'Optional. When true, the trace is confined to the start feature''s stored circuit: api/internal/trace reads the start''s live circuit_id and only expands through / includes neighbours whose own circuit_id equals it (a feature with no circuit_id field, or an unset one, is treated as off-circuit and bounds the walk). Network-agnostic -- circuit_id already identifies one MV or LV circuit. Errors when the start feature carries no circuit membership. Off by default; the four seeded direction primitives leave it false.';
