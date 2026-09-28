-- 0125_create_webhook_entity_refs.sql
-- Purpose: extend inbound webhook ingestion (0124) beyond the feature target to
-- the work and fault targets. Those two adapters need the same idempotency /
-- correlation the feature adapter gets from ops.webhook_feature_refs -- map a
-- source's own id to the VoltEdge entity a delivery produced, so a repeat
-- delivery updates that entity instead of creating a duplicate -- but a work
-- order / fault event is identified by a single id (no polymorphic class to
-- carry, unlike a feature which lives in a compiled gis.<code> table). So rather
-- than widen webhook_feature_refs (whose feature_class_id column is meaningless
-- here), this adds one small sibling table keyed by target.
--
-- The reading target reuses the feature pipeline (a reading is modelled as a
-- feature row), so it keeps using webhook_feature_refs -- only work/fault land
-- here.

CREATE TABLE IF NOT EXISTS ops.webhook_entity_refs (
    endpoint_id uuid NOT NULL REFERENCES ops.webhook_endpoints(id) ON DELETE CASCADE,

    -- The external system's own id for the entity, as it arrived on the payload.
    external_id text NOT NULL,

    -- Which adapter produced entity_id -- 'work' (wam.work_orders) or 'fault'
    -- (faults.fault_events). Stored for clarity and so a mis-targeted lookup is
    -- self-describing; an endpoint has exactly one target, so external_id is
    -- already unique per endpoint (hence the PK below).
    target text NOT NULL CHECK (target IN ('work', 'fault')),

    -- The VoltEdge entity this external id currently maps to. Polymorphic, no FK
    -- (points into wam.work_orders / faults.fault_events) -- same convention as
    -- webhook_feature_refs / diagram_placements: an entity delete never cascades
    -- through here, a stale ref just misses and the next delivery re-creates.
    entity_id uuid NOT NULL,

    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (endpoint_id, external_id)
);

COMMENT ON TABLE ops.webhook_entity_refs IS 'Per-endpoint external-id -> VoltEdge work-order/fault-event correlation (0125). The non-feature sibling of webhook_feature_refs: lets a repeat delivery update the same entity instead of creating a duplicate. entity_id is polymorphic (no FK).';
