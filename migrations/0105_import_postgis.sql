-- 0105_import_postgis.sql
-- Purpose: v2-S3 of the ETL import engine (docs/etl-import-engine.md) -- the
-- first LIVE-DATABASE connector, PostGIS. Unlike the file formats (v1 NDJSON,
-- v2 GeoPackage/SpatiaLite) a PostGIS import has no uploaded artifact: the worker
-- opens an outbound read-only connection to a remote Postgres/PostGIS server and
-- streams rows straight out of it. The mapping/validation/upsert spine downstream
-- is shared unchanged; the only new piece is the reader (api/internal/importer/
-- postgis.go) and the connection config it needs.
--
-- Per the locked v2 decisions (memory: etl-import-engine-design):
--   1. Connection config lives ON THE PROFILE (not reused from
--      metadata.external_systems) -- host/port/db/user in the clear, password
--      sealed with the existing secretbox AES-256-GCM cipher (write-only:
--      never returned to the browser, only unsealed server-side by the worker).
--   2. The source can be addressed EITHER by table (reuses source_layer, added
--      in 0103) OR by an optional custom read-only SQL query (source_query).
--
-- source_format already admits 'postgis' (pre-widened back in 0102), so no CHECK
-- widening is needed here -- only the new connection columns.

ALTER TABLE metadata.import_profiles
    ADD COLUMN IF NOT EXISTS db_host        text,
    ADD COLUMN IF NOT EXISTS db_port        integer,
    ADD COLUMN IF NOT EXISTS db_name        text,
    ADD COLUMN IF NOT EXISTS db_user        text,
    ADD COLUMN IF NOT EXISTS db_sslmode     text NOT NULL DEFAULT 'require',
    ADD COLUMN IF NOT EXISTS db_password_enc bytea,
    ADD COLUMN IF NOT EXISTS source_query   text;

-- db_sslmode is a small closed set the reader maps to a TLS posture:
--   disable      -> no TLS
--   require      -> TLS, encrypt but don't verify the server certificate
--   verify-full  -> TLS, verify the certificate chain AND the hostname
COMMENT ON COLUMN metadata.import_profiles.db_sslmode IS
    'TLS posture for a live-DB connection: disable | require | verify-full';

ALTER TABLE metadata.import_profiles
    DROP CONSTRAINT IF EXISTS import_profiles_db_sslmode_check;
ALTER TABLE metadata.import_profiles
    ADD CONSTRAINT import_profiles_db_sslmode_check
    CHECK (db_sslmode IN ('disable', 'require', 'verify-full'));

-- A live-DB profile must carry the addressing basics (host/db/user). The password
-- is deliberately NOT required here: a target DB may allow trust/peer auth, and
-- the secret is optional the same way an external system's is. Port defaults are
-- filled in by the reader (5432) when null. File formats never set these, so the
-- constraint only bites the two live-DB formats.
ALTER TABLE metadata.import_profiles
    DROP CONSTRAINT IF EXISTS import_profiles_livedb_conn_check;
ALTER TABLE metadata.import_profiles
    ADD CONSTRAINT import_profiles_livedb_conn_check
    CHECK (
        source_format NOT IN ('postgis', 'mssql')
        OR (db_host IS NOT NULL AND db_name IS NOT NULL AND db_user IS NOT NULL)
    );
