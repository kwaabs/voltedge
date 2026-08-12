-- 0009_create_terminals.sql
-- Purpose: terminals -- named connection points on a junction feature
-- class (e.g. DIST_XFMR has T1 primary, T2 secondary, T3 ground). A
-- junction is still one point in the geodatabase; terminals are logical,
-- not separate geometries. network_rules references terminals to say
-- which edge types may legally attach to which terminal.

CREATE TABLE metadata.terminals (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    feature_class_id  uuid NOT NULL REFERENCES metadata.feature_classes(id) ON DELETE CASCADE,
    subtype_id        uuid REFERENCES metadata.subtypes(id) ON DELETE CASCADE,
    code              text NOT NULL,
    role              text NOT NULL CHECK (role IN ('primary', 'secondary', 'ground', 'other')),
    phase_capable     boolean NOT NULL DEFAULT false,
    created_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (feature_class_id, subtype_id, code)
);

COMMENT ON TABLE metadata.terminals IS 'A named, logical connection point on a junction feature class. Only feature_classes with network_role = junction should have rows here (not enforced by a constraint yet -- the schema compiler''s validation pass owns that).';
COMMENT ON COLUMN metadata.terminals.subtype_id IS 'Optional: scopes this terminal to one subtype. Null means it applies to every subtype of the feature class.';
COMMENT ON COLUMN metadata.terminals.code IS 'Short terminal identifier, e.g. T1, T2, T3.';
COMMENT ON COLUMN metadata.terminals.role IS 'primary/secondary/ground describe the terminal''s electrical role, used by network_rules and trace barrier logic.';
COMMENT ON COLUMN metadata.terminals.phase_capable IS 'Whether current/phase can flow through this terminal. False for a ground terminal, for example -- excludes it from phase-aware tracing.';
