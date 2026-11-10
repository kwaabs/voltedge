-- 0196_create_cim_sync_targets.sql
-- Named CIM sync targets for incremental (difference-model) export. Each row is
-- one external consumer / sync stream: a scope definition (which features, which
-- profile, which CRS) plus the BASELINE pointer -- the object key + identity of
-- the full model that target last received. A "run" produces the current full
-- model, diffs it against this baseline, emits a CIM difference model, and (on
-- success) advances the baseline. See api/internal/export/cimdiff.go + the
-- difference branch in runner.go, and docs on incremental CIM exchange.
--
-- Baselines live in a SEPARATE object-store bucket (export.NewObjectStoreForBucket)
-- so the 7-day export retention sweep (ops.export_jobs) never deletes them; this
-- table is the durable scope->baseline mapping the sweep never visits.

CREATE TABLE IF NOT EXISTS ops.cim_sync_targets (
    id   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL UNIQUE,
    description text,
    active boolean NOT NULL DEFAULT true,

    -- Scope definition (mirrors ExportScope's model-defining fields).
    feature_classes text[] NOT NULL DEFAULT '{}',
    bbox jsonb,                       -- [minX,minY,maxX,maxY] EPSG:4326, null = no bbox
    territory_ids uuid[],             -- null = no territory filter
    cim_profile text,                 -- null = the is_default metadata.cim_profiles row
    target_srid integer,              -- null = no reprojection (stored EPSG:4326)

    -- Baseline state (what this target last received); null until the first run.
    baseline_object_key text,
    baseline_model_mrid text,
    baseline_feature_count integer,
    last_exported_at timestamptz,
    last_job_id uuid,

    created_by uuid,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
