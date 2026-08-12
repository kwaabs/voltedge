-- 0014_create_trace_configurations.sql
-- Purpose: trace_configurations and trace_barrier_rules -- named,
-- reusable trace profiles (direction + barrier/crossing toggles) that
-- the Go graph service reads instead of hardcoding trace behavior per
-- network type. "Transformer-aware, voltage-aware, phase-aware, respects
-- barriers" is this one mechanism, not four separate ones.

CREATE TABLE metadata.trace_configurations (
    id                     uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code                   text NOT NULL UNIQUE,
    name                   text NOT NULL,
    direction_value_id     uuid NOT NULL REFERENCES metadata.domain_values(id),
    stop_at_open_devices   boolean NOT NULL DEFAULT true,
    cross_transformers     boolean NOT NULL DEFAULT false,
    respect_phase          boolean NOT NULL DEFAULT true,
    created_at             timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE metadata.trace_configurations IS 'A named, reusable trace profile, e.g. "Fault isolation - MV" (upstream+downstream, stop at open devices, do not cross transformers, respect phase). Isolation and loop-detection traces are not separate rows here -- the Go service composes two primitive traces (upstream + downstream, or connected run from two sources) and post-processes the result.';
COMMENT ON COLUMN metadata.trace_configurations.direction_value_id IS 'References a domain_value in the TRACE_DIRECTION domain: connected, downstream, upstream, or shortest_path. Only four raw values ever need to exist.';
COMMENT ON COLUMN metadata.trace_configurations.stop_at_open_devices IS 'Whether an open device (per trace_barrier_rules matching a DEVICE_STATE=open condition) halts traversal.';
COMMENT ON COLUMN metadata.trace_configurations.cross_transformers IS 'Whether the trace continues across a transformer boundary into the other voltage domain. False by default -- most traces should stop at a transformer unless explicitly told to continue.';
COMMENT ON COLUMN metadata.trace_configurations.respect_phase IS 'Whether phase compatibility is checked at each hop (intersecting available phases against each device''s phase capability) rather than ignored.';

CREATE TABLE metadata.trace_barrier_rules (
    id                       uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    trace_configuration_id  uuid NOT NULL REFERENCES metadata.trace_configurations(id) ON DELETE CASCADE,
    feature_class_id        uuid NOT NULL REFERENCES metadata.feature_classes(id),
    field_id                uuid NOT NULL REFERENCES metadata.fields(id),
    halts_on_value_id       uuid NOT NULL REFERENCES metadata.domain_values(id),
    created_at               timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE metadata.trace_barrier_rules IS 'One row = one condition that halts traversal for a given trace_configurations profile, e.g. "on feature_class DIST_SWITCH, if field device_state equals domain value OPEN, stop." Checked live at each hop against the feature''s current attribute value -- never baked into the connectivity graph itself.';
COMMENT ON COLUMN metadata.trace_barrier_rules.field_id IS 'Which attribute on feature_class_id to check (e.g. device_state).';
COMMENT ON COLUMN metadata.trace_barrier_rules.halts_on_value_id IS 'Which domain_value of that field''s domain triggers the halt (e.g. OPEN).';
