-- Phase 30: per-feature-class map label configuration.
--
-- One label config per feature class (not per symbology rule), authored in
-- its own admin dialog and drawn on the geographic map. Free-form jsonb so
-- the label expression/font tuning can evolve with no further schema churn,
-- exactly like symbology_rules.style_def. Nullable: NULL (or an empty
-- template) means the class draws no labels, which is the default for every
-- existing class. Published untouched on /feature-classes/{code}/schema, the
-- same response the map already reads for symbology.
--
-- Shape (all keys optional except template, whose presence is the on-switch):
--   { "template": "{name} ({voltage} kV)", "size": 12, "color": "#1f2937",
--     "bold": false, "italic": false, "halo": true, "halo_color": "#ffffff",
--     "halo_width": 3, "min_zoom": 14 }

ALTER TABLE metadata.feature_classes ADD COLUMN label_def jsonb;

COMMENT ON COLUMN metadata.feature_classes.label_def IS
  'Phase 30 map label config (jsonb): template with {field} placeholders + font/halo/min_zoom tuning. NULL or empty template = no labels. Map only; the diagram toggles labels per placement.';
