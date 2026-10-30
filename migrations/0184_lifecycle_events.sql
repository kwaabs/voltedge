-- 0184: a filterable, network-wide asset-lifecycle events ledger.
--
-- Lifecycle transitions already land in gis.feature_history, but only as an opaque
-- "update" snapshot -- you can't filter it to "what got RETIRED this quarter"
-- without diffing consecutive snapshots. This purpose-built, append-only ledger --
-- written by crud.SetLifecycle alongside the history row -- turns that into a
-- direct, indexed read. One row per ACTUAL status change (from <> to).

CREATE TABLE ops.lifecycle_events (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    feature_class_code text NOT NULL,
    feature_id         uuid NOT NULL,
    from_status        text,            -- the prior lifecycle_status (null if unknown)
    to_status          text NOT NULL,   -- the new lifecycle_status
    actor_id           uuid,            -- who made the change; null for a system write
    occurred_at        timestamptz NOT NULL DEFAULT now()
);

-- The ledger board (newest first), and the common "to this status in this window".
CREATE INDEX lifecycle_events_recent_idx ON ops.lifecycle_events (occurred_at DESC);
CREATE INDEX lifecycle_events_to_status_idx ON ops.lifecycle_events (to_status, occurred_at DESC);
-- A single feature's own lifecycle timeline.
CREATE INDEX lifecycle_events_feature_idx ON ops.lifecycle_events (feature_class_code, feature_id, occurred_at DESC);

COMMENT ON TABLE ops.lifecycle_events IS
    'Append-only ledger of asset lifecycle_status transitions (from -> to), written by crud.SetLifecycle. Powers the network-wide "what got retired / commissioned this quarter" view. See migration 0184.';
