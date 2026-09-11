-- 0081_seed_google_hybrid_basemap.sql
-- Purpose: add Google Hybrid (satellite imagery + street/label overlay) to the
-- admin-managed basemap catalog (iam.basemaps, migration 0077), alongside the
-- five keyless raster sources that migration seeded (OSM, three Carto styles,
-- Esri satellite). Requested to give the map a labelled aerial view.
--
-- URL: https://mt0.google.com/vt/lyrs=y&x={x}&y={y}&z={z}
--   * Standard slippy XYZ scheme ({x}/{y}/{z}), so tms stays false.
--   * lyrs=y is Google's HYBRID layer (imagery WITH roads + labels). The bare
--     imagery layer is lyrs=s (no overlay); m = roads, p = terrain. Hybrid was
--     asked for, hence y.
--   * mt0 is one of Google's tile hosts (mt0..mt3 round-robin). A single host is
--     fine here since OpenLayers' XYZ source hits one URL; the {x}/{y}/{z}
--     placeholders are what OL substitutes.
--   * max_zoom 21: Google serves hybrid tiles to ~z21 in most areas (deeper than
--     Esri's 19); past it OpenLayers over-zooms the last level.
--
-- CAVEAT (operational, not enforced here): mt0.google.com/vt is Google's
-- undocumented internal tile endpoint. Consuming it outside the Google Maps
-- JavaScript API is contrary to Google's Terms of Service. It is seeded because
-- it was explicitly requested; the properly-licensed 'esri-imagery' row is the
-- ToS-clean satellite alternative. An admin can disable it from the Map settings
-- page if that matters.
--
-- is_builtin = true to match the convention that migration-seeded basemaps are
-- protected from accidental API deletion (an admin may still disable/edit/reorder
-- it). sort_order 6 places it after the five shipped built-ins.
-- ON CONFLICT keeps a re-apply idempotent.

INSERT INTO iam.basemaps (code, label, url_template, attribution, max_zoom, sort_order, is_builtin) VALUES
    ('google-hybrid', 'Google Hybrid',
     'https://mt0.google.com/vt/lyrs=y&x={x}&y={y}&z={z}',
     '[{"label":"© Google","href":"https://www.google.com/help/terms_maps/"}]'::jsonb,
     21, 6, true)
ON CONFLICT (code) DO NOTHING;
