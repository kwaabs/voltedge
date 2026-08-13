-- 0018_add_trace_target_class.sql
-- Purpose: add an optional target feature class to trace_configurations,
-- generalizing the trace engine to support "walk until you reach a node
-- of this class" (e.g. trace to nearest transformer) alongside the
-- existing direction + barrier mechanism, instead of only ever walking
-- until the graph runs out or a specific target feature is named
-- (shortest_path). Nullable -- every trace_configurations row seeded
-- before this migration keeps working exactly as before with no
-- target class at all.

ALTER TABLE metadata.trace_configurations
    ADD COLUMN target_class_id uuid REFERENCES metadata.feature_classes(id);

COMMENT ON COLUMN metadata.trace_configurations.target_class_id IS 'Optional. When set, the trace stops expanding through (but still records) any node of this feature class, other than the trace''s own start node -- e.g. an UPSTREAM trace with target_class_id set to DIST_XFMR finds the nearest transformer, whether starting from a consuming feature (finds the nearest one) or from a transformer itself (finds the next one upstream, i.e. its source). cross_transformers governs whether the walk continues past a matched node (true) or stops there (false, the default) -- the one place that previously-stubbed column now has real, enforced meaning.';
