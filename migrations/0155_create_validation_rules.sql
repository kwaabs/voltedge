-- 0155_create_validation_rules.sql
-- Purpose: a per-feature-class COMPLETENESS / business-rule catalog for the
-- attribute-QA audit. The compiler enforces schema-level required/domain/range at
-- write time; this catalog captures the softer "this field is EXPECTED to be
-- populated" rules that aren't schema constraints -- e.g. a DIST_XFMR should carry
-- a rating, a conductor should carry a material -- optionally conditioned on the
-- feature's subtype. The attribute audit (api/internal/connectivity) scans
-- existing rows against these rules and feeds violations into the same
-- ops.validation_issues queue (kind attr_completeness) as the drift checks.
--
-- Lives in ops (with validation_issues / validation_cursor), not metadata: these
-- are quality POLICY an admin tunes, not schema the compiler reads.

CREATE TABLE ops.validation_rules (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),

    -- The class and the field the rule is about (bare refs to metadata codes/names
    -- -- the audit skips a rule whose field is not a current field of the class, so
    -- a stale rule is a harmless no-op rather than a scan error).
    feature_class_code text NOT NULL,
    field_name         text NOT NULL,

    -- NULL = the field is expected on EVERY feature of the class; set = expected
    -- only when the feature's subtype matches (the "required if subtype X" rule).
    subtype_code       integer,

    severity           text NOT NULL DEFAULT 'warning'
                           CHECK (severity IN ('error', 'warning')),
    -- Optional custom message; NULL lets the audit auto-phrase one.
    message            text,

    sort_order         integer NOT NULL DEFAULT 0,
    active             boolean NOT NULL DEFAULT true,

    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),

    -- One rule per (class, field, subtype-condition). NULLS NOT DISTINCT so the
    -- always-applies rule (subtype_code NULL) is unique too (Postgres 15+, same as
    -- the terminals dedupe constraint, migration 0142).
    CONSTRAINT validation_rules_uq UNIQUE NULLS NOT DISTINCT (feature_class_code, field_name, subtype_code)
);

CREATE INDEX validation_rules_class_idx ON ops.validation_rules (feature_class_code, sort_order) WHERE active;

COMMENT ON TABLE ops.validation_rules IS 'Per-class completeness / business rules for the attribute-QA audit: fields that are EXPECTED to be populated (optionally per subtype), beyond the schema-enforced required/domain/range. Violations flow into ops.validation_issues (kind attr_completeness). Admin-editable at /admin/validation-rules.';
