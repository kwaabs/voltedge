-- 0103_import_geopackage.sql
-- Purpose: v2 of the ETL import engine (docs/etl-import-engine.md) -- the first
-- non-file-normalized source: an uploaded OGC GeoPackage (.gpkg). Unlike the v1
-- formats (CSV/GeoJSON/Shapefile/KML/DXF), which the browser normalizes to NDJSON
-- before upload, a GeoPackage is a SQLite database the browser uploads RAW; the
-- server (api/internal/importer/geopackage.go) opens it with the pure-Go SQLite
-- driver already in go.mod, reads the chosen feature table, and decodes each
-- row's GeoPackage geometry blob to GeoJSON via PostGIS. The load then applies
-- every row through the exact same profile mapping + validated crud path the v1
-- formats use (one apply spine, two ingest front-ends).
--
-- Two changes, both additive:
--
--   1. Widen the import_profiles.source_format CHECK to admit 'geopackage'. Note
--      the 0102 comment said the enum was "pre-widened" -- but only for the v2
--      live-DB connectors (postgis/mssql), not the file formats, so this format
--      really is new to the constraint. (SpatiaLite lands with its own reader +
--      a further widening in the next slice.)
--
--   2. Add import_profiles.source_layer: which feature table inside the source to
--      import. A GeoPackage can hold many layers; a v1 file format has exactly
--      one, so the column is nullable and stays NULL for them. It is a REUSABLE
--      default -- the run wizard offers the file's layers and may override it per
--      run (the override rides in the job's scope snapshot, not here).

-- 1. Admit the new source format. The 0102 CHECK is an inline column constraint,
--    so Postgres named it <table>_<column>_check; drop and re-add it with the
--    widened value set. IF EXISTS keeps this re-runnable on a partially-migrated
--    database.
ALTER TABLE metadata.import_profiles
    DROP CONSTRAINT IF EXISTS import_profiles_source_format_check;

ALTER TABLE metadata.import_profiles
    ADD CONSTRAINT import_profiles_source_format_check
    CHECK (source_format IN (
        'csv', 'geojson', 'shapefile', 'kml', 'dxf',
        'geopackage',
        'postgis', 'mssql'));

-- 2. The feature table (layer) to import from a multi-layer source. NULL for the
--    single-layer file formats; set (or overridden at run time) for GeoPackage.
ALTER TABLE metadata.import_profiles
    ADD COLUMN IF NOT EXISTS source_layer text;

COMMENT ON COLUMN metadata.import_profiles.source_layer IS 'For a multi-layer source (GeoPackage), which feature table to import. NULL for the single-layer file formats (CSV/GeoJSON/Shapefile/KML/DXF). A reusable default the run wizard may override per run (the override is snapshotted into the job scope, not written back here).';
