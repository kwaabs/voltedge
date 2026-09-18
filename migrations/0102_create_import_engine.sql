-- 0102_create_import_engine.sql
-- Purpose: the admin ETL / import engine (docs/etl-import-engine.md). Admins
-- import external data one feature class at a time: map source columns to the
-- target class's fields, then insert or upsert the rows into the master gis.*
-- tables. This is the mirror image of the CIM export mapping (migration 0034)
-- -- an authored, DECLARATIVE mapping -- so it follows the same split:
--
--   * The mapping is CONFIG that versions with the app, so it lives in the
--     `metadata` schema beside cim_profiles / cim_attribute_mappings:
--       metadata.import_profiles         one reusable import per class+format
--       metadata.import_field_mappings   source column -> target field + transform
--
--   * A run is OPERATIONAL work with a lifecycle + lease, so it lives in the
--     `ops` schema beside export_jobs (migration 0036), claimed by a worker
--     with FOR UPDATE SKIP LOCKED:
--       ops.import_jobs        one row per import run (or dry-run)
--       ops.import_job_errors  per-row validation / load failures for the report
--
-- The BEHAVIORAL half -- parsing the source file, assembling geometry, coercing
-- values, and applying each row through the validated crud engine (the same
-- CreateFeature/UpdateFeature path field-pack sync uses) -- stays in Go
-- (api/internal/importer), since it can't be expressed as data.
--
-- v1 sources are file uploads (CSV, GeoJSON, Shapefile, KML, DXF), normalized in
-- the browser to a canonical newline-delimited GeoJSON (NDJSON) artifact and
-- uploaded to object storage; the worker streams that one artifact. Live DB
-- connectors (PostGIS/MSSQL) and FGDB/GeoPackage are a later phase; the shape
-- below deliberately keys off an object_key + a source_format enum so those slot
-- in without a schema change.
--
-- Permissions reuse the existing gates (no new RBAC): editing a profile is
-- metadata:write config (like the CIM mappings); running an import writes
-- features, so it is gated on features:write in the handler.

-- ---------------------------------------------------------------------------
-- metadata.import_profiles -- one reusable, named import definition for a
-- target feature class (optionally a specific subtype). Holds the write policy
-- (insert-only vs upsert + the natural-key fields to match on) and how geometry
-- is built for each row. The per-field column mapping hangs off it below.
-- ---------------------------------------------------------------------------
CREATE TABLE metadata.import_profiles (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code                text NOT NULL UNIQUE,
    name                text NOT NULL,

    -- Where rows are written. subtype_id NULL = the profile doesn't pin a
    -- subtype (the mapping may still set the subtype column via a field
    -- mapping); a non-null subtype_id fixes every imported row to that subtype,
    -- whose subtype_field_rules then drive required-field validation.
    feature_class_id    uuid NOT NULL REFERENCES metadata.feature_classes(id) ON DELETE CASCADE,
    subtype_id          uuid REFERENCES metadata.subtypes(id) ON DELETE CASCADE,

    -- The source format this profile expects. Informational + drives the browser
    -- parser; the server only ever sees the normalized NDJSON artifact. The enum
    -- is pre-widened for the v2 live-DB connectors so adding them needs no CHECK
    -- migration on a populated table.
    source_format       text NOT NULL DEFAULT 'csv'
                            CHECK (source_format IN (
                                'csv', 'geojson', 'shapefile', 'kml', 'dxf',
                                'postgis', 'mssql')),

    -- insert_only appends every row (a fresh UUID each). upsert matches an
    -- existing feature on match_fields and updates it, else inserts -- the one
    -- piece of genuinely new logic (there is no natural-key upsert elsewhere;
    -- the rest of the app only mints UUIDs).
    write_mode          text NOT NULL DEFAULT 'insert_only'
                            CHECK (write_mode IN ('insert_only', 'upsert')),

    -- Target field NAMES (= gis column names, stable per class) that form the
    -- natural key for upsert matching, e.g. {external_ref} or {feeder_no,pole_no}.
    -- The load does SELECT ... WHERE each column = the mapped source value.
    -- Empty for insert_only.
    match_fields        text[] NOT NULL DEFAULT '{}',

    -- How each row's geometry is assembled at load time (the profile is chosen
    -- at run time, so geometry assembly is a load-time decision, not baked into
    -- the artifact):
    --   none    geometry-none target class; no geom written.
    --   native  the NDJSON artifact already carries a GeoJSON geometry per row
    --           (GeoJSON / Shapefile / KML / DXF sources parsed in the browser).
    --   lonlat  two numeric source columns -> Point.
    --   wkt     one WKT/EWKT text source column -> geometry.
    geometry_mode       text NOT NULL DEFAULT 'native'
                            CHECK (geometry_mode IN ('none', 'native', 'lonlat', 'wkt')),
    geometry_lon_column text,   -- geometry_mode = lonlat
    geometry_lat_column text,   -- geometry_mode = lonlat
    geometry_wkt_column text,   -- geometry_mode = wkt

    status              text NOT NULL DEFAULT 'active'
                            CHECK (status IN ('active', 'deprecated')),
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    created_by          uuid REFERENCES iam.users(id) ON DELETE SET NULL,
    updated_by          uuid REFERENCES iam.users(id) ON DELETE SET NULL,

    -- Upsert needs at least one match field; the two coordinate modes need their
    -- column(s). Shape guards enforced here; that the referenced fields exist and
    -- are active is validated in the handler (cross-table, not expressible here).
    CHECK (write_mode <> 'upsert' OR cardinality(match_fields) > 0),
    CHECK (geometry_mode <> 'lonlat'
           OR (geometry_lon_column IS NOT NULL AND geometry_lat_column IS NOT NULL)),
    CHECK (geometry_mode <> 'wkt' OR geometry_wkt_column IS NOT NULL)
);

COMMENT ON TABLE metadata.import_profiles IS 'One reusable admin import definition for a target feature class (optionally a subtype): write policy (insert_only|upsert + match_fields), source format, and geometry assembly. Mirror of the CIM export mapping (0034). Drives the ETL import engine (docs/etl-import-engine.md).';
COMMENT ON COLUMN metadata.import_profiles.match_fields IS 'Target field names (= gis column names) forming the natural key the upsert matches an existing feature on. Empty for insert_only. The load runs SELECT ... WHERE each column = the mapped source value; 0 hits -> insert, 1 -> update, >1 -> ambiguous (row error).';
COMMENT ON COLUMN metadata.import_profiles.geometry_mode IS 'How each row''s geometry is built: none (geometry-none class), native (artifact already carries GeoJSON geometry), lonlat (two numeric columns -> Point), wkt (one WKT/EWKT column).';

-- ---------------------------------------------------------------------------
-- metadata.import_field_mappings -- one source column -> one target field, with
-- the coercion that bridges the (typed) source value to the field's data type.
-- The inverse of cim_attribute_mappings: there a field fed a CIM attribute; here
-- a source column feeds a field. A given field is written by at most one mapping
-- (UNIQUE below); a source column may feed more than one field.
-- ---------------------------------------------------------------------------
CREATE TABLE metadata.import_field_mappings (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    profile_id     uuid NOT NULL REFERENCES metadata.import_profiles(id) ON DELETE CASCADE,

    -- The target feature field this mapping writes.
    field_id       uuid NOT NULL REFERENCES metadata.fields(id) ON DELETE CASCADE,

    -- Source column name in the artifact's `properties`. Required for every
    -- transform except 'constant' (whose value is a literal, not a column).
    source_column  text,

    -- How the source value is coerced before it reaches crud validation:
    --   direct      use the source value as-is (crud type-checks/coerces it).
    --   constant    ignore the source; write constant_value verbatim.
    --   scale       numeric source * scale_factor (e.g. km -> m).
    --   domain_map  value_map lookup: source string -> our coded value.
    transform      text NOT NULL DEFAULT 'direct'
                       CHECK (transform IN ('direct', 'constant', 'scale', 'domain_map')),
    constant_value text,    -- transform = constant
    scale_factor   numeric, -- transform = scale
    value_map      jsonb,   -- transform = domain_map (source value -> coded value)

    created_at     timestamptz NOT NULL DEFAULT now(),

    UNIQUE (profile_id, field_id),

    -- Each transform needs its own input present.
    CHECK (transform = 'constant' OR source_column IS NOT NULL),
    CHECK (transform <> 'constant' OR constant_value IS NOT NULL),
    CHECK (transform <> 'scale'    OR scale_factor IS NOT NULL),
    CHECK (transform <> 'domain_map' OR value_map IS NOT NULL)
);

COMMENT ON TABLE metadata.import_field_mappings IS 'Maps one source column to one target feature field with a coercion transform (direct/constant/scale/domain_map). Inverse of metadata.cim_attribute_mappings. A field is written by at most one mapping; a source column may feed several fields.';
COMMENT ON COLUMN metadata.import_field_mappings.value_map IS 'JSONB lookup for transform = domain_map, keyed by the raw source value -> our coded value (e.g. {"11kV": "HV_11KV", "33kV": "HV_33KV"}).';

-- ---------------------------------------------------------------------------
-- ops.import_jobs -- one row per import run. Mirrors ops.export_jobs (0036):
-- queued -> running -> succeeded | failed | cancelled, claimed with FOR UPDATE
-- SKIP LOCKED, a heartbeat lease reclaims a crashed worker's job, and a scope
-- jsonb snapshots everything the worker needs so it never re-derives from the
-- (possibly later-edited) profile.
-- ---------------------------------------------------------------------------
CREATE TABLE ops.import_jobs (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),

    status             text NOT NULL DEFAULT 'queued'
                           CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'cancelled')),

    -- The profile that drives mapping + validation + upsert. ON DELETE SET NULL:
    -- keep the job record (an audit trail) even if the profile is later removed;
    -- the scope snapshot below carries the full run config regardless.
    profile_id         uuid REFERENCES metadata.import_profiles(id) ON DELETE SET NULL,

    -- Denormalized target class code for display/audit even if the profile is
    -- gone -- the worker also uses it to load class metadata.
    feature_class_code text NOT NULL,

    -- The requesting user (authz + audit). ON DELETE RESTRICT: a job's owner
    -- can't be deleted out from under the audit trail.
    requested_by       uuid NOT NULL REFERENCES iam.users(id) ON DELETE RESTRICT,

    -- The normalized newline-delimited GeoJSON (NDJSON) artifact in object
    -- storage (RustFS key). The browser uploads it; the worker streams it row by
    -- row. NOT NULL: every run has a source artifact.
    object_key         text NOT NULL,

    -- Resolved run + authz snapshot at enqueue: the write_mode, match_fields,
    -- geometry config, the field mappings, and the caller's territory scope. The
    -- worker applies exactly this and never re-derives permissions or re-reads
    -- the profile.
    scope              jsonb NOT NULL,

    -- dry_run validates + tallies but never writes (it backs the pre-flight
    -- report). A committed run has dry_run = false.
    dry_run            boolean NOT NULL DEFAULT false,

    -- Row tallies, filled as the worker streams. total_rows is null until the
    -- worker has counted the artifact.
    total_rows         integer,
    inserted_count     integer NOT NULL DEFAULT 0,
    updated_count      integer NOT NULL DEFAULT 0,
    skipped_count      integer NOT NULL DEFAULT 0,
    error_count        integer NOT NULL DEFAULT 0,

    -- Fatal job-level failure message (status = 'failed'); per-row failures go
    -- to ops.import_job_errors instead so one bad row never fails the job.
    error              text,

    -- Queue bookkeeping (same as export_jobs): attempts caps the crash-requeue
    -- loop; worker_id + lease_expires_at implement the heartbeat lease.
    attempts           integer NOT NULL DEFAULT 0,
    worker_id          text,
    lease_expires_at   timestamptz,

    created_at         timestamptz NOT NULL DEFAULT now(),
    started_at         timestamptz,
    finished_at        timestamptz
);

COMMENT ON TABLE ops.import_jobs IS 'Async import queue (ETL engine). One row per import run; workers claim with FOR UPDATE SKIP LOCKED. Mirrors ops.export_jobs (0036). See docs/etl-import-engine.md.';
COMMENT ON COLUMN ops.import_jobs.scope IS 'Resolved run + authz snapshot at enqueue: {write_mode, match_fields, geometry_mode/columns, field_mappings, territory scope}. The worker applies exactly this; it never re-derives permissions or re-reads the (possibly edited) profile.';
COMMENT ON COLUMN ops.import_jobs.object_key IS 'RustFS object key of the normalized newline-delimited GeoJSON (NDJSON) source artifact the browser uploaded; the worker streams it one line (one feature) at a time.';
COMMENT ON COLUMN ops.import_jobs.dry_run IS 'True = validate + tally only, never write (pre-flight report). False = committed run.';

-- Claim path: oldest queued (or lease-expired running) job first.
CREATE INDEX import_jobs_claim_idx ON ops.import_jobs (status, created_at);
-- History path: a user's own recent runs, newest first.
CREATE INDEX import_jobs_owner_idx ON ops.import_jobs (requested_by, created_at DESC);

-- ---------------------------------------------------------------------------
-- ops.import_job_errors -- per-row failures for the run's report. A row that
-- fails validation, geometry assembly, or matches ambiguously is recorded here
-- and skipped; the job proceeds (per-row skip-and-report, not all-or-nothing).
-- ---------------------------------------------------------------------------
CREATE TABLE ops.import_job_errors (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    job_id      uuid NOT NULL REFERENCES ops.import_jobs(id) ON DELETE CASCADE,
    -- 1-based line number in the NDJSON artifact (= source row order), so the
    -- admin can find the offending row in their file.
    row_number  integer NOT NULL,
    message     text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE ops.import_job_errors IS 'Per-row failures for one import run (validation, geometry, ambiguous upsert match). The row is skipped and the job continues; this backs the run''s error report.';

CREATE INDEX import_job_errors_job_idx ON ops.import_job_errors (job_id, row_number);
