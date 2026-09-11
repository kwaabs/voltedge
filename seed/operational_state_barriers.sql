-- seed/operational_state_barriers.sql
-- Phase 38 (operational lifecycle), Slice 3.
--
-- Makes operational_state = ISOLATED or RETIRED a live trace barrier, exactly
-- like device_state = OPEN already is (seed/trace_configurations_example.sql). A
-- feature that's tagged out (ISOLATED) or decommissioned (RETIRED) is not
-- passing power, so a downstream/upstream trace should stop AT it rather than
-- walk through it -- the same "stop at an open device" logic, generalised to the
-- declared lifecycle state.
--
-- Depends on:
--   * seed/operational_state_example.sql (the operational_state field + the
--     OPERATIONAL_STATE domain values ISOLATED/RETIRED), and
--   * seed/trace_configurations_example.sql (the DOWNSTREAM_TRACE / UPSTREAM_TRACE
--     configs this attaches to -- the two that respect barriers,
--     stop_at_open_devices = true; CONNECTED_TRACE and SHORTEST_PATH_TRACE
--     deliberately don't, so attaching here would be inert).
--
-- This is the seed the engine change in Slice 3 exists for: DIST_SWITCH already
-- carries a device_state = OPEN barrier, and these rows add operational_state
-- barriers on top -- so a class now has MORE than one barrier check per config.
-- api/internal/trace resolves a *slice* of checks per class and halts if ANY
-- matches (graph.go's barrierCheck / checkBarrier), which is what lets
-- device_state = OPEN and operational_state = ISOLATED/RETIRED coexist as two
-- independent reasons the same DIST_SWITCH stops a trace.
--
-- Scope: every class that (a) carries an active operational_state field and (b)
-- actually participates in connectivity traversal -- network_role edge or
-- junction. That excludes structural / non-traced classes (POLE, PROTECTIVE_RELAY,
-- container yards/chambers, network_role none/container): they carry
-- operational_state too, but a trace never passes *through* them, so a barrier
-- rule there would only ever be inert. One set-generating INSERT across
-- {DOWNSTREAM, UPSTREAM} x {matching classes} x {ISOLATED, RETIRED}, guarded by
-- WHERE NOT EXISTS (trace_barrier_rules has no unique constraint, same authoring
-- convention as network_rules) so re-applying is a no-op.
--
-- Metadata-only, no compile step -- trace_barrier_rules is read directly by the
-- trace engine, it has no physical-table footprint.

INSERT INTO metadata.trace_barrier_rules (trace_configuration_id, feature_class_id, field_id, halts_on_value_id)
SELECT tc.id, fc.id, f.id, dv.id
FROM metadata.trace_configurations tc
JOIN metadata.feature_classes fc
    ON fc.network_role IN ('edge', 'junction')
JOIN metadata.fields f
    ON f.feature_class_id = fc.id AND f.name = 'operational_state' AND f.status = 'active'
JOIN metadata.domain_values dv
    ON dv.domain_id = f.domain_id AND dv.code IN ('ISOLATED', 'RETIRED')
WHERE tc.code IN ('DOWNSTREAM_TRACE', 'UPSTREAM_TRACE')
  AND NOT EXISTS (
    SELECT 1 FROM metadata.trace_barrier_rules
    WHERE trace_configuration_id = tc.id
      AND feature_class_id = fc.id
      AND field_id = f.id
      AND halts_on_value_id = dv.id
  );
