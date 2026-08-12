-- 0016_create_runtime_schema.sql
-- Purpose: reserve the 'runtime' schema and create
-- runtime.feature_connectivity -- generic runtime infrastructure, not
-- compiled per feature class (docs/metadata-core.md calls this family
-- out explicitly: feature_connectivity/feature_relationships and,
-- implicitly, attachments and diagrams). Same separate-schema-per-concern
-- principle as ops/metadata/gis (migrations 0001, 0003, 0015).
--
-- One row here = one electrical connection that a person (or an agent
-- acting on a person's explicit confirmation) has actually established
-- between two real feature rows -- never written as a side effect of
-- digitizing or of a rule merely matching. See docs/metadata-core.md's
-- "Suggested vs. established" section: a matching network_rules row
-- produces a candidate, not a fact.
--
-- from_feature_id/to_feature_id are polymorphic (uuid, no FK) because the
-- target table varies by feature class -- gis.mv_conductor, gis.pole,
-- etc. -- and Postgres has no cross-table FK. from_feature_class_id/
-- to_feature_class_id are real FKs into metadata.feature_classes, and
-- the API is what enforces that a given feature_id actually exists in
-- that class's physical table (and isn't soft-deleted) before writing a
-- row here.

CREATE SCHEMA IF NOT EXISTS runtime;

COMMENT ON SCHEMA runtime IS 'Generic runtime data tables that are not compiled per feature class -- feature_connectivity today, feature_relationships/attachments/diagram tables as later slices land. Hand-written, unlike gis (compiler-generated).';

CREATE TABLE runtime.feature_connectivity (
    id                     uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    network_rule_id        uuid NOT NULL REFERENCES metadata.network_rules(id),
    from_feature_class_id  uuid NOT NULL REFERENCES metadata.feature_classes(id),
    from_feature_id        uuid NOT NULL,
    from_terminal_id       uuid REFERENCES metadata.terminals(id),
    to_feature_class_id    uuid NOT NULL REFERENCES metadata.feature_classes(id),
    to_feature_id           uuid NOT NULL,
    to_terminal_id          uuid REFERENCES metadata.terminals(id),
    established_at         timestamptz NOT NULL DEFAULT now(),
    established_by         text,
    UNIQUE NULLS NOT DISTINCT (from_feature_id, from_terminal_id, to_feature_id, to_terminal_id)
);

COMMENT ON TABLE runtime.feature_connectivity IS 'An electrical connection actually established between two feature rows, gated on a matching, active metadata.network_rules row at establish time. Absence of a matching rule -- or a matching rule with is_valid = false -- means the API rejects the establish outright; nothing is ever written speculatively.';
COMMENT ON COLUMN runtime.feature_connectivity.network_rule_id IS 'Which network_rules row authorized this connection. Kept even though rules can change later -- this row is a record of what was true at establish time, not a live re-evaluation.';
COMMENT ON COLUMN runtime.feature_connectivity.from_feature_id IS 'No FK -- the target table depends on from_feature_class_id and varies per feature class (gis.mv_conductor, gis.pole, ...). The API validates existence against the right physical table before writing here.';
COMMENT ON COLUMN runtime.feature_connectivity.from_terminal_id IS 'Null for feature classes with no terminals (edges, most containers) -- see metadata.terminals. Not every connection has a terminal on both ends.';
COMMENT ON COLUMN runtime.feature_connectivity.established_by IS 'Placeholder for a real identity once auth lands (see README) -- currently just a fixed literal identifying the write path (api vs. a future MCP agent).';

CREATE INDEX feature_connectivity_from_idx ON runtime.feature_connectivity (from_feature_id);
CREATE INDEX feature_connectivity_to_idx ON runtime.feature_connectivity (to_feature_id);
