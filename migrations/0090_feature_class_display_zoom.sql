-- 0090_feature_class_display_zoom.sql
-- Purpose: per-feature-class map display-zoom band -- let a class declare a
-- min/max zoom outside which its features are not drawn on the geographic map
-- at all (e.g. "SERVICE_POINT only from zoom 16 up", "TOWER only up to zoom 12").
--
-- Two nullable integer columns on metadata.feature_classes:
--   min_display_zoom -- feature hidden BELOW this zoom (NULL = no lower bound)
--   max_display_zoom -- feature hidden ABOVE this zoom (NULL = no upper bound)
-- Both NULL (the default, and every existing row) = today's behaviour: the
-- class draws at every zoom.
--
-- Why columns, not the label_def-style jsonb: these are two plain integers
-- read straight through to the schema endpoint, like `network` (0064) and
-- `category` (0074) -- pure map-render metadata the compiler's DDL never
-- touches. No physical gis.* table change, no recompile. Purely additive and
-- idempotent (IF NOT EXISTS), so a re-run is a clean no-op.
--
-- Distinct from the existing symbology-rule zoom bands (metadata.symbology_rules
-- min_zoom/max_zoom, migration 0012): those pick WHICH STYLE applies at a zoom
-- and a feature outside every band still draws with a fallback style. This band
-- is a true visibility gate on the whole feature (glyph + label), evaluated in
-- the map style function (web/src/lib/map-layers.ts createSymbologyStyle).

ALTER TABLE metadata.feature_classes
    ADD COLUMN IF NOT EXISTS min_display_zoom integer,
    ADD COLUMN IF NOT EXISTS max_display_zoom integer;

-- Sanity: a band, when both bounds are set, must be non-empty (min <= max), and
-- each bound must be a plausible web-map zoom (0..24). NULLs pass (no bound).
-- Guarded add so a re-run doesn't error on the already-present constraint.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'feature_classes_display_zoom_band_check'
    ) THEN
        ALTER TABLE metadata.feature_classes
            ADD CONSTRAINT feature_classes_display_zoom_band_check CHECK (
                (min_display_zoom IS NULL OR (min_display_zoom BETWEEN 0 AND 24))
                AND (max_display_zoom IS NULL OR (max_display_zoom BETWEEN 0 AND 24))
                AND (min_display_zoom IS NULL OR max_display_zoom IS NULL OR min_display_zoom <= max_display_zoom)
            );
    END IF;
END $$;
