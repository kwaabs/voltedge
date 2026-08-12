-- 0011_create_network_rules.sql
-- Purpose: network_rules -- which feature-class/subtype/terminal pairs
-- may legally connect electrically. This is what the trace engine's
-- barrier logic and the MCP suggest_connections tool both read. A
-- missing row means "not valid" -- absence of a rule is itself the
-- rejection, not an exception case to special-case.

CREATE TABLE metadata.network_rules (
    id                     uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name                   text NOT NULL,
    from_feature_class_id  uuid NOT NULL REFERENCES metadata.feature_classes(id),
    from_subtype_id        uuid REFERENCES metadata.subtypes(id),
    from_terminal_id       uuid REFERENCES metadata.terminals(id),
    to_feature_class_id    uuid NOT NULL REFERENCES metadata.feature_classes(id),
    to_subtype_id          uuid REFERENCES metadata.subtypes(id),
    to_terminal_id         uuid REFERENCES metadata.terminals(id),
    phase_rule             text,
    is_valid               boolean NOT NULL DEFAULT true,
    created_at             timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE metadata.network_rules IS 'One row = one legal connectivity pattern, e.g. MV_CONDUCTOR may connect to DIST_XFMR terminal T1 when voltage_class matches. Matched at digitize time to produce a *candidate* connection (never a write); matched at trace time to decide whether an edge is traversable. No row for a given from/to pair means that connection is invalid -- absence is the rejection.';
COMMENT ON COLUMN metadata.network_rules.name IS 'Human-readable label for the rule, shown when a candidate connection is proposed (e.g. to a user confirming an establish_connection action).';
COMMENT ON COLUMN metadata.network_rules.from_subtype_id IS 'Optional: narrows the rule to a specific subtype of from_feature_class_id. Null applies to any subtype.';
COMMENT ON COLUMN metadata.network_rules.from_terminal_id IS 'Optional: narrows the rule to a specific terminal. Null applies to the feature class/subtype generally (relevant mainly for edges, which have no terminals of their own).';
COMMENT ON COLUMN metadata.network_rules.phase_rule IS 'Free-text description of the phase compatibility condition for this rule (e.g. "from.voltage_class = to.primary_voltage"). Not yet a structured expression language -- Phase 4''s trace engine work will formalize this if needed.';
COMMENT ON COLUMN metadata.network_rules.is_valid IS 'Almost always true -- rows are only created for legal connections. Kept as an explicit flag rather than relying purely on row-presence, so a rule can be temporarily disabled without deleting it.';
