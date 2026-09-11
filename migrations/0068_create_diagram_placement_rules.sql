-- 0068_create_diagram_placement_rules.sql
-- Purpose: Phase 9 metering cleanup -- an optional per-(container class ×
-- diagram type) allow-list over which feature classes may be placed on a
-- diagram. One row = one legal placement: on a diagram of type
-- `diagram_type_value_id` hosted by a container of class
-- `container_feature_class_id`, a feature of class `feature_class_id` may be
-- dropped. This is what turns a meter-chamber schematic from "any
-- is_diagram_placeable class" into "only LV_BUSBAR + METER", without
-- hardcoding any class in Go.
--
-- Keyed by (container × type), NOT type alone: the same DIAGRAM_TYPE value
-- (e.g. a generic schematic) can mean different legal contents under a
-- substation yard vs. a meter chamber, so the container is part of the key.
-- The user chose this over a type-only or container-only key.
--
-- Backward-compatible by ABSENCE -- the discipline domain_transitions
-- (migration 0065) already uses: a (container, type) pairing with NO rows is
-- unconstrained, so every is_diagram_placeable class may be placed on it.
-- That is every existing diagram (the one-line under a substation yard has no
-- rows here), so this table changes nothing until a seed populates a pairing.
-- Once a pairing has ANY rows, only the listed feature classes are legal on it
-- -- enforcement lives in api/internal/diagram placeFeature, alongside the
-- global is_diagram_placeable gate (opt-in, additive: a row can only ever
-- restrict, never widen, since a class must still be is_diagram_placeable to
-- appear here meaningfully).

CREATE TABLE metadata.diagram_placement_rules (
    id                        uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    container_feature_class_id uuid NOT NULL REFERENCES metadata.feature_classes(id) ON DELETE CASCADE,
    diagram_type_value_id      uuid NOT NULL REFERENCES metadata.domain_values(id) ON DELETE CASCADE,
    feature_class_id           uuid NOT NULL REFERENCES metadata.feature_classes(id) ON DELETE CASCADE,
    created_at                 timestamptz NOT NULL DEFAULT now()
);

-- One rule per (container, type, class) -- a duplicate is a data-entry mistake,
-- not a second distinct allowance. Doubles as the ON CONFLICT target the seed
-- relies on to stay idempotent.
CREATE UNIQUE INDEX diagram_placement_rules_key
    ON metadata.diagram_placement_rules (container_feature_class_id, diagram_type_value_id, feature_class_id);

-- placeFeature resolves the allow-list for a diagram by its (container class,
-- diagram type) pairing, so index the leading pair.
CREATE INDEX diagram_placement_rules_pairing_idx
    ON metadata.diagram_placement_rules (container_feature_class_id, diagram_type_value_id);

COMMENT ON TABLE metadata.diagram_placement_rules IS 'Optional per-(container class × diagram type) placement allow-list: one row per feature class allowed on that pairing. A pairing with no rows is unconstrained (any is_diagram_placeable class). Enforced in api/internal/diagram placeFeature; wired for METER_CHAMBER schematics by Phase 9.';
