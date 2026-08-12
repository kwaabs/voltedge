-- 0017_create_feature_relationships.sql
-- Purpose: runtime.feature_relationships -- the established-instance
-- counterpart to metadata.relationship_classes, same relationship
-- migration 0010 already promises: "the actual established instances
-- ... are a separate, explicit-confirm data table -- nothing here
-- implies an instance exists, only that one is *legal*." Lives in the
-- runtime schema reserved by migration 0016, alongside
-- feature_connectivity -- same family of generic runtime infrastructure,
-- not compiled per feature class.
--
-- No is_valid flag here, unlike network_rules -- relationship_classes
-- has no such column (see migration 0010): legality is simply "does a
-- relationship_classes row exist for this origin/destination
-- feature-class pairing," not a separately-toggleable flag on top of
-- that. api/internal/relationship enforces it by requiring a matching
-- row before ever writing here, same zero-trust discipline as
-- api/internal/connectivity.

CREATE TABLE runtime.feature_relationships (
    id                             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    relationship_class_id          uuid NOT NULL REFERENCES metadata.relationship_classes(id),
    origin_feature_class_id        uuid NOT NULL REFERENCES metadata.feature_classes(id),
    origin_feature_id              uuid NOT NULL,
    destination_feature_class_id   uuid NOT NULL REFERENCES metadata.feature_classes(id),
    destination_feature_id         uuid NOT NULL,
    established_at                 timestamptz NOT NULL DEFAULT now(),
    established_by                 text,
    UNIQUE (relationship_class_id, origin_feature_id, destination_feature_id)
);

COMMENT ON TABLE runtime.feature_relationships IS 'A structural/spatial relationship actually established between two feature rows, gated on a matching metadata.relationship_classes row existing for the origin/destination feature-class pairing and on that class''s cardinality not already being exhausted. Never written speculatively -- see api/internal/relationship.';
COMMENT ON COLUMN runtime.feature_relationships.origin_feature_id IS 'No FK -- same polymorphic reasoning as feature_connectivity.from_feature_id: the target table depends on origin_feature_class_id and varies per feature class. The API validates existence against the right physical table before writing here.';
COMMENT ON COLUMN runtime.feature_relationships.established_by IS 'Placeholder for a real identity once auth lands, same as feature_connectivity.established_by.';

CREATE INDEX feature_relationships_origin_idx ON runtime.feature_relationships (origin_feature_id);
CREATE INDEX feature_relationships_destination_idx ON runtime.feature_relationships (destination_feature_id);
