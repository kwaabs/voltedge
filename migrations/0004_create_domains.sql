-- 0004_create_domains.sql
-- Purpose: domains and domain_values -- the coded-value and range
-- vocabularies that fields, symbology_rules, snapping_rules, and
-- trace_configurations all key off. Reused everywhere rather than
-- inventing a new lookup table per concept (diagram_type, trace
-- direction, etc all live here too).

CREATE TABLE metadata.domains (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code        text NOT NULL UNIQUE,
    name        text NOT NULL,
    domain_type text NOT NULL CHECK (domain_type IN ('coded', 'range')),
    created_at  timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE metadata.domains IS 'A reusable vocabulary a field''s value must belong to -- coded-value list (e.g. PHASE_CODE) or a numeric range. One row per domain, values live in metadata.domain_values.';
COMMENT ON COLUMN metadata.domains.code IS 'Stable machine identifier, e.g. PHASE_CODE, VOLTAGE_CLASS, TRACE_DIRECTION. Referenced by fields.domain_id and others.';
COMMENT ON COLUMN metadata.domains.name IS 'Human-readable name shown in admin UI.';
COMMENT ON COLUMN metadata.domains.domain_type IS 'coded = enumerated values in domain_values; range = a numeric min/max, values table unused.';

CREATE TABLE metadata.domain_values (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    domain_id  uuid NOT NULL REFERENCES metadata.domains(id) ON DELETE CASCADE,
    code       text NOT NULL,
    label      text NOT NULL,
    sort_order integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (domain_id, code)
);

COMMENT ON TABLE metadata.domain_values IS 'One legal value within a coded domain, e.g. domain PHASE_CODE has rows A, B, C, ABC. Also doubles as the lookup for things that would otherwise need their own small table -- diagram_type, trace direction, snapping target_type.';
COMMENT ON COLUMN metadata.domain_values.code IS 'The stored value, e.g. A, ABC, OPEN, UPSTREAM. Unique within its domain, not globally.';
COMMENT ON COLUMN metadata.domain_values.label IS 'Display label, e.g. "Phase A", "Open".';
COMMENT ON COLUMN metadata.domain_values.sort_order IS 'Display order within the domain -- not enforced, just a UI hint.';
