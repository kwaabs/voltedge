-- 0012_create_symbology_rules.sql
-- Purpose: symbology_rules -- resolves a feature to a style. Martin never
-- reads this table; it's resolved client-side (or by a thin styling
-- service) from the published metadata cache, keyed by feature_class +
-- subtype + domain_value, most specific match wins.

CREATE TABLE metadata.symbology_rules (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    feature_class_id uuid NOT NULL REFERENCES metadata.feature_classes(id) ON DELETE CASCADE,
    subtype_id       uuid REFERENCES metadata.subtypes(id) ON DELETE CASCADE,
    domain_value_id  uuid REFERENCES metadata.domain_values(id) ON DELETE CASCADE,
    render_context   text NOT NULL DEFAULT 'map' CHECK (render_context IN ('map', 'diagram')),
    min_zoom         integer,
    max_zoom         integer,
    style_def        jsonb NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE metadata.symbology_rules IS 'Resolution order at read time is most-specific-wins: (feature_class + subtype + domain_value) > (feature_class + subtype) > (feature_class + domain_value) > (feature_class alone, the mandatory default). A feature class MUST have at least one rule with subtype_id and domain_value_id both null before it can be published -- the schema compiler''s validation pass enforces that, not a DB constraint.';
COMMENT ON COLUMN metadata.symbology_rules.subtype_id IS 'Optional match narrowing: applies only to this subtype if set.';
COMMENT ON COLUMN metadata.symbology_rules.domain_value_id IS 'Optional match narrowing: applies only when the matching field (implied by the domain) has this value. Which field is implied by which domain_value belongs to is resolved via that value''s domain, matched against whichever of the feature class''s fields uses that domain.';
COMMENT ON COLUMN metadata.symbology_rules.render_context IS 'map = cartographic icon/style for the geographic view. diagram = schematic symbol for a one-line diagram. The same feature can have separate rules for each context.';
COMMENT ON COLUMN metadata.symbology_rules.min_zoom IS 'Optional zoom-level floor this rule applies at. Null means no floor.';
COMMENT ON COLUMN metadata.symbology_rules.max_zoom IS 'Optional zoom-level ceiling this rule applies at. Null means no ceiling.';
COMMENT ON COLUMN metadata.symbology_rules.style_def IS 'The actual style, as JSON: icon_ref for a discrete pictogram, or color/scale/rotation/label modifiers computed against a shared base icon. Shape changes get a new icon_ref; everything else is a modifier here, not a new file.';
