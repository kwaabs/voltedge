-- 0124_create_webhook_ingestion.sql
-- Purpose: inbound webhook ingestion -- external systems push (or VoltEdge pulls) feature/reading/work/fault changes in through a configured endpoint, every delivery lands in an async queue, and a per-endpoint external-id map correlates a source's own ids to the VoltEdge features it creates so repeat deliveries update in place.
--
-- Three tables, all in the ops schema reserved (0001) for operational/queue
-- state:
--
--   webhook_endpoints   the admin-configured integration sources. Each binds a
--                       target adapter (feature/reading/work/fault), an auth
--                       mode (hmac signature and/or bearer token), an optional
--                       sealed HMAC secret, and -- crucially -- a service_user
--                       whose permissions + territory scope every ingested write
--                       runs under. There is no ambient "system" writer: an
--                       endpoint can only ever write what its service user could.
--
--   webhook_deliveries  one row per received (push) or fetched (pull) or
--                       admin-initiated (manual/replay) payload. Mirrors
--                       ops.export_jobs (0036): status + lease + attempts drive a
--                       FOR UPDATE SKIP LOCKED worker that applies the payload
--                       through the normal crud/package path. Store-then-apply,
--                       so a delivery is durable, retryable, replayable, and
--                       auditable even if the apply later fails.
--
--   webhook_feature_refs  the idempotency/correlation map: (endpoint, external_id)
--                       -> the (class, feature) it produced. On the next delivery
--                       carrying the same external_id the adapter updates that
--                       feature instead of creating a duplicate. Keyed per
--                       endpoint so two integrations may reuse the same id space
--                       without colliding. feature_class_id/feature_id carry no
--                       FK -- they are polymorphic references into the compiled
--                       gis.* tables, exactly like diagram_placements and
--                       feature_connectivity, so a feature delete never cascades
--                       through here (a stale ref just misses and the next
--                       delivery re-creates).

CREATE TABLE IF NOT EXISTS ops.webhook_endpoints (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),

    name               text NOT NULL,
    active             boolean NOT NULL DEFAULT true,

    -- push: external systems POST to /webhooks/{id}. pull: VoltEdge fetches from
    -- a configured URL (config.pull_url) on demand. Both feed the same queue.
    direction          text NOT NULL DEFAULT 'push'
                           CHECK (direction IN ('push', 'pull')),

    -- The ingestion-target adapter this endpoint drives. feature ships first;
    -- reading/work/fault land in a later slice against the same pipeline.
    target             text NOT NULL DEFAULT 'feature'
                           CHECK (target IN ('feature', 'reading', 'work', 'fault')),

    -- How a push sender proves itself. hmac: X-VoltEdge-Signature over the raw
    -- body + timestamp against signing_secret. bearer: an iam.api_tokens bearer
    -- that must resolve to service_user_id. both: either is accepted. (pull
    -- endpoints ignore this -- there is no inbound sender to authenticate.)
    auth_mode          text NOT NULL DEFAULT 'hmac'
                           CHECK (auth_mode IN ('hmac', 'bearer', 'both')),

    -- AES-256-GCM sealed HMAC signing secret (secretbox, same at-rest scheme as
    -- metadata.external_systems.credential_enc). NULL when auth_mode='bearer'.
    signing_secret_enc bytea,

    -- The principal every write from this endpoint acts as. ON DELETE RESTRICT:
    -- a service user backing a live integration can't be deleted out from under
    -- it. Ingested writes inherit this user's permissions AND territory scope.
    service_user_id    uuid NOT NULL REFERENCES iam.users(id) ON DELETE RESTRICT,

    -- Optional field-mapping spec transforming the sender's own JSON shape into
    -- VoltEdge's native change form. NULL = native passthrough (the sender
    -- already speaks VoltEdge's feature JSON).
    mapping            jsonb,

    -- Free-form endpoint settings: pull url/auth/cursor, per-endpoint replay
    -- window override, etc. Defaulted so code never has to null-check it.
    config             jsonb NOT NULL DEFAULT '{}'::jsonb,

    created_by         uuid REFERENCES iam.users(id) ON DELETE SET NULL,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE ops.webhook_endpoints IS 'Inbound webhook ingestion sources (0124). One row per integration; every ingested write runs under service_user_id''s permissions + territory scope.';
COMMENT ON COLUMN ops.webhook_endpoints.signing_secret_enc IS 'AES-256-GCM sealed HMAC signing secret (secretbox). NULL for bearer-only endpoints.';
COMMENT ON COLUMN ops.webhook_endpoints.service_user_id IS 'The principal ingested writes act as. Territory scope + RBAC are inherited from this user -- an endpoint can never write what its service user could not.';
COMMENT ON COLUMN ops.webhook_endpoints.mapping IS 'Optional external-shape -> VoltEdge-native change mapping. NULL = native passthrough.';

CREATE TABLE IF NOT EXISTS ops.webhook_deliveries (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),

    endpoint_id      uuid NOT NULL REFERENCES ops.webhook_endpoints(id) ON DELETE CASCADE,

    -- queued -> running -> succeeded | failed. dead is the terminal state for a
    -- delivery that exhausted its attempts (a dead-letter, held for an admin to
    -- inspect/replay). CHECK-enforced so no bad state is written by hand.
    status           text NOT NULL DEFAULT 'queued'
                         CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'dead')),

    -- Where the delivery came from -- push (received), pull (fetched), manual
    -- (admin paste-and-test), replay (admin re-ran a prior delivery).
    source           text NOT NULL DEFAULT 'push'
                         CHECK (source IN ('push', 'pull', 'manual', 'replay')),

    -- Caller-supplied dedup key (Idempotency-Key header / mapped field). A
    -- unique partial index below makes a retried delivery a no-op collision the
    -- receiver can answer 200 to without re-queuing.
    idempotency_key  text,

    -- The raw received body (push/pull) or the admin-supplied test payload.
    payload          jsonb NOT NULL,

    -- Selected request headers captured for audit/debug (signature, timestamp,
    -- content-type). Never the raw Authorization value.
    headers          jsonb,

    -- Queue bookkeeping, identical shape to ops.export_jobs: attempts caps the
    -- crash-requeue loop; worker_id + lease_expires_at implement the heartbeat
    -- lease that reclaims a delivery whose worker died mid-apply.
    attempts         integer NOT NULL DEFAULT 0,
    worker_id        text,
    lease_expires_at timestamptz,

    -- Apply outcome once terminal: [{op, feature_class, feature_id, external_id}].
    result           jsonb,
    -- Failure message on 'failed'/'dead'.
    error            text,

    created_at       timestamptz NOT NULL DEFAULT now(),
    started_at       timestamptz,
    finished_at      timestamptz
);

COMMENT ON TABLE ops.webhook_deliveries IS 'Async ingestion queue (0124). One row per received/fetched/manual payload; workers claim with FOR UPDATE SKIP LOCKED and apply through the normal crud path. Store-then-apply so every delivery is durable, retryable, replayable, auditable.';
COMMENT ON COLUMN ops.webhook_deliveries.result IS 'Per-item apply outcome once terminal: [{op, feature_class, feature_id, external_id}].';
COMMENT ON COLUMN ops.webhook_deliveries.lease_expires_at IS 'Heartbeat lease. A running delivery whose lease has lapsed is treated as a crashed worker''s and re-claimable.';

-- Claim path: oldest queued (or lease-expired running) delivery.
CREATE INDEX IF NOT EXISTS webhook_deliveries_claim_idx ON ops.webhook_deliveries (status, created_at);
-- History path: one endpoint's recent deliveries, newest first (the admin log).
CREATE INDEX IF NOT EXISTS webhook_deliveries_endpoint_idx ON ops.webhook_deliveries (endpoint_id, created_at DESC);
-- Idempotency: at most one delivery per (endpoint, idempotency_key). Partial so
-- the common no-key case is unconstrained.
CREATE UNIQUE INDEX IF NOT EXISTS webhook_deliveries_idem_idx
    ON ops.webhook_deliveries (endpoint_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;

CREATE TABLE IF NOT EXISTS ops.webhook_feature_refs (
    endpoint_id      uuid NOT NULL REFERENCES ops.webhook_endpoints(id) ON DELETE CASCADE,

    -- The external system's own id for the entity, as it arrived on the payload.
    external_id      text NOT NULL,

    -- The VoltEdge feature this external id currently maps to. Polymorphic, no
    -- FK (points into a compiled gis.* table) -- same convention as
    -- diagram_placements / feature_connectivity.
    feature_class_id uuid NOT NULL,
    feature_id       uuid NOT NULL,

    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (endpoint_id, external_id)
);

COMMENT ON TABLE ops.webhook_feature_refs IS 'Per-endpoint external-id -> VoltEdge feature correlation (0124). Lets a repeat delivery update the same feature instead of creating a duplicate. feature_class_id/feature_id are polymorphic (no FK).';
