-- 0013_create_snapping_rules.sql
-- Purpose: snapping_rules -- what a feature class is allowed to snap to
-- while digitizing, and how tightly. Purely geometric: a snap proves two
-- vertices are coincident, nothing more. It never implies connectivity
-- (metadata.network_rules) or structural support (metadata.relationship_classes)
-- -- those still require an explicit establish action in Phase 2.

CREATE TABLE metadata.snapping_rules (
    id                     uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    from_feature_class_id  uuid NOT NULL REFERENCES metadata.feature_classes(id),
    from_subtype_id        uuid REFERENCES metadata.subtypes(id),
    to_feature_class_id    uuid NOT NULL REFERENCES metadata.feature_classes(id),
    to_subtype_id          uuid REFERENCES metadata.subtypes(id),
    to_terminal_id         uuid REFERENCES metadata.terminals(id),
    target_type_value_id   uuid NOT NULL REFERENCES metadata.domain_values(id),
    tolerance_m            numeric NOT NULL CHECK (tolerance_m > 0),
    priority               integer NOT NULL DEFAULT 0,
    is_required            boolean NOT NULL DEFAULT false,
    created_at             timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE metadata.snapping_rules IS 'Feeds OpenLayers'' Snap interaction: while digitizing from_feature_class_id, which other feature classes/terminals are valid snap targets, at what tolerance. priority breaks ties when multiple targets fall within tolerance (a point feature should usually outrank a bare vertex). Purely spatial -- resolving a snap into an actual connection is a separate step (see network_rules and the Phase 2 establish flow).';
COMMENT ON COLUMN metadata.snapping_rules.target_type_value_id IS 'References a domain_value in the SNAP_TARGET_TYPE domain (point_feature / endpoint / edge / vertex) -- reuses the domains mechanism rather than a bespoke enum.';
COMMENT ON COLUMN metadata.snapping_rules.tolerance_m IS 'Snap tolerance in meters.';
COMMENT ON COLUMN metadata.snapping_rules.priority IS 'Lower number wins when multiple targets are within tolerance simultaneously. Not enforced by the DB -- the digitizing client resolves it.';
COMMENT ON COLUMN metadata.snapping_rules.is_required IS 'True for hard constraints where landing outside tolerance should fail validation rather than save as a near-miss (e.g. a conductor endpoint that must reach a transformer).';
