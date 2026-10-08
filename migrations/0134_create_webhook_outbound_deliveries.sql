-- 0134_create_webhook_outbound_deliveries.sql
-- Purpose: Slice 2 of the outbound event emitter (docs/outbound-webhooks-design.md).
-- Slice 1 (migration 0133) captures changes into the ops.webhook_outbound_events
-- OUTBOX. This slice turns those events into actual deliveries:
--   1. a subscription is an ops.webhook_endpoints row with the NEW direction
--      'outbound' (its config jsonb holds target_url + event_types + filters);
--   2. the fan-out stage reads an un-fanned outbox event and inserts one
--      ops.webhook_outbound_deliveries row per matching subscription;
--   3. the delivery worker claims those rows (FOR UPDATE SKIP LOCKED + lease),
--      POSTs the signed envelope through the SSRF-hardened client, and retries
--      with exponential backoff before dead-lettering.
-- This is the inbound ingestion machinery (0124: webhook_endpoints +
-- webhook_deliveries + the SKIP-LOCKED worker) inverted, plus the backoff the
-- inbound queue does not have.

-- 1. Allow an outbound subscription. The direction CHECK on webhook_endpoints
--    (migration 0124) is an unnamed inline constraint Postgres auto-named
--    webhook_endpoints_direction_check; widen it to admit 'outbound' alongside
--    the existing inbound 'push' (receive) and 'pull' (fetch).
ALTER TABLE ops.webhook_endpoints
    DROP CONSTRAINT IF EXISTS webhook_endpoints_direction_check;
ALTER TABLE ops.webhook_endpoints
    ADD CONSTRAINT webhook_endpoints_direction_check
    CHECK (direction IN ('push', 'pull', 'outbound'));

-- 2. One row per (event x subscription). A near-twin of ops.webhook_deliveries,
--    with the extra columns the outbound pipeline needs: a backoff schedule
--    (next_attempt_at) and the denormalised event identity (seq + entity) the
--    claim query reads to deliver a given entity's events in order.
CREATE TABLE ops.webhook_outbound_deliveries (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),

    -- The subscription this delivery targets (an outbound webhook_endpoints row)
    -- and the outbox event it carries. Both cascade: deleting a subscription or
    -- (hypothetically) an event discards its undelivered work.
    subscription_id uuid NOT NULL REFERENCES ops.webhook_endpoints(id) ON DELETE CASCADE,
    event_id        uuid NOT NULL REFERENCES ops.webhook_outbound_events(id) ON DELETE CASCADE,

    -- Denormalised from the event at fan-out time so the claim/ordering query
    -- never has to join back to the outbox. event_seq is the outbox's global
    -- order; (entity_kind, entity_id) scopes per-entity ordering; event_type is
    -- the X-VoltEdge-Event header and the admin log's label.
    event_seq       bigint NOT NULL,
    entity_kind     text   NOT NULL,
    entity_id       text   NOT NULL,
    event_type      text   NOT NULL,

    -- The fully-rendered envelope POSTed to the subscriber (event_id, event_type,
    -- occurred_at, actor, entity, data). Built once at fan-out so a retry re-sends
    -- byte-identical bytes -- the same signature and idempotency key every time.
    payload         jsonb NOT NULL,

    -- queued   : awaiting delivery (next_attempt_at gates a backing-off retry)
    -- running  : claimed by a worker, lease held
    -- succeeded : the subscriber returned 2xx
    -- dead     : retries exhausted or a permanent error (bad URL, no secret, 4xx)
    status          text NOT NULL DEFAULT 'queued'
                       CHECK (status IN ('queued', 'running', 'succeeded', 'dead')),

    attempts         integer NOT NULL DEFAULT 0,
    worker_id        text,
    lease_expires_at timestamptz,

    -- Exponential-backoff gate: a failed-but-retryable delivery returns to
    -- 'queued' with this set to the future, and the claim query ignores it until
    -- now() catches up. (The inbound queue requeues immediately; outbound backs
    -- off so a flapping subscriber is not hammered.)
    next_attempt_at  timestamptz NOT NULL DEFAULT now(),

    response_code    integer,
    error            text,

    created_at       timestamptz NOT NULL DEFAULT now(),
    started_at       timestamptz,
    finished_at      timestamptz
);

COMMENT ON TABLE ops.webhook_outbound_deliveries IS 'One row per (outbox event x matching outbound subscription), delivered by the webhookout worker (docs/outbound-webhooks-design.md). The outbound twin of ops.webhook_deliveries, with a backoff schedule and denormalised event identity for in-order delivery.';
COMMENT ON COLUMN ops.webhook_outbound_deliveries.next_attempt_at IS 'Exponential-backoff gate: a retryable failure returns the row to queued with this set to the future; the claim query ignores it until now() passes.';
COMMENT ON COLUMN ops.webhook_outbound_deliveries.payload IS 'The fully-rendered envelope, built once at fan-out so every retry re-sends identical bytes (stable signature + idempotency key).';

-- A given (event, subscription) is delivered at most once -- the idempotency
-- guard for the fan-out, so a fan-out that is retried after a crash cannot
-- double-insert deliveries.
CREATE UNIQUE INDEX IF NOT EXISTS webhook_outbound_deliveries_event_sub_idx
    ON ops.webhook_outbound_deliveries (event_id, subscription_id);

-- The worker's claim set: queued rows whose backoff has elapsed, oldest event
-- first.
CREATE INDEX IF NOT EXISTS webhook_outbound_deliveries_claim_idx
    ON ops.webhook_outbound_deliveries (status, next_attempt_at, event_seq);

-- The per-entity ordering probe (NOT EXISTS earlier-seq sibling still in flight).
CREATE INDEX IF NOT EXISTS webhook_outbound_deliveries_order_idx
    ON ops.webhook_outbound_deliveries (subscription_id, entity_kind, entity_id, event_seq);

-- The admin delivery log for one subscription, newest first.
CREATE INDEX IF NOT EXISTS webhook_outbound_deliveries_sub_idx
    ON ops.webhook_outbound_deliveries (subscription_id, created_at DESC);
