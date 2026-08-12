-- 0010_create_relationship_classes.sql
-- Purpose: relationship_classes -- the metadata definition of a
-- structural or spatial relationship between two feature classes (e.g.
-- POLE supports MV_CONDUCTOR). Distinct from network_rules: this is
-- never traced electrically. The actual established instances (Phase 2's
-- feature_relationships) are a separate, explicit-confirm data table --
-- nothing here implies an instance exists, only that one is *legal*.

CREATE TABLE metadata.relationship_classes (
    id                            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name                          text NOT NULL UNIQUE,
    origin_feature_class_id       uuid NOT NULL REFERENCES metadata.feature_classes(id),
    destination_feature_class_id  uuid NOT NULL REFERENCES metadata.feature_classes(id),
    kind                          text NOT NULL CHECK (kind IN ('spatial_support', 'containment', 'composite')),
    cardinality                   text NOT NULL CHECK (cardinality IN ('one_to_one', 'one_to_many', 'many_to_many')),
    created_at                    timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE metadata.relationship_classes IS 'Defines a legal structural/spatial relationship between two feature classes, e.g. POLE (origin) supports MV_CONDUCTOR (destination). Never used for electrical tracing -- that is network_rules'' job. This table only says what kind of relationship is allowed to exist; Phase 2''s feature_relationships records which specific instances actually have been.';
COMMENT ON COLUMN metadata.relationship_classes.kind IS 'spatial_support (e.g. pole holding up a conductor span) or containment (e.g. substation area containing equipment) or composite (a stronger, ownership-like relationship, not yet used by any sample).';
COMMENT ON COLUMN metadata.relationship_classes.cardinality IS 'How many origin features can relate to how many destination features.';
