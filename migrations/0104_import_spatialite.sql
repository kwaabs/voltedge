-- 0104_import_spatialite.sql
-- Purpose: v2-S2 of the ETL import engine (docs/etl-import-engine.md) -- add
-- SpatiaLite as a source format, the second SQLite-family format after GeoPackage
-- (0103). A SpatiaLite database is also a SQLite file the browser uploads RAW and
-- the server (api/internal/importer/spatialite.go) opens with the pure-Go SQLite
-- driver already in go.mod. It differs from GeoPackage only in where it catalogs
-- its feature tables (its own geometry_columns table) and how it wraps geometry
-- (the SpatiaLite BLOB, decoded to WKB server-side); everything downstream -- the
-- profile mapping, the source_layer picker (added in 0103), the validated crud
-- apply path -- is shared unchanged.
--
-- One change, additive: widen the import_profiles.source_format CHECK to admit
-- 'spatialite'. The source_layer column already exists (0103) and applies here
-- too (a SpatiaLite database is multi-layer like a GeoPackage).

-- Drop and re-add the named CHECK with the widened value set. IF EXISTS keeps this
-- re-runnable on a partially-migrated database.
ALTER TABLE metadata.import_profiles
    DROP CONSTRAINT IF EXISTS import_profiles_source_format_check;

ALTER TABLE metadata.import_profiles
    ADD CONSTRAINT import_profiles_source_format_check
    CHECK (source_format IN (
        'csv', 'geojson', 'shapefile', 'kml', 'dxf',
        'geopackage', 'spatialite',
        'postgis', 'mssql'));
