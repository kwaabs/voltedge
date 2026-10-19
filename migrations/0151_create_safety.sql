-- 0151_create_safety.sql
-- The OPERATIONAL SAFETY layer (Oracle-NMS-style "safety documents"): the tags,
-- grounds and clearances a control operator applies to protect a crew working on
-- a de-energised section -- and, through them, the rule that BLOCKS operating a
-- protected device. It fills a real gap: VoltEdge can isolate a section and flip
-- device_state (an OMS switching step), but nothing stops a dispatcher from
-- re-closing a device a crew is working behind. These three records, and the gate
-- they feed (a later slice), are that interlock.
--
--   * safety.clearances -- a permit-to-work: a work area isolated, tagged and
--     earthed, handed to a crew. Issued, then surrendered when the work is done;
--     it OWNS the tags and grounds that protect it (they carry its clearance_id).
--   * safety.tags       -- a restriction placed on one device. 'danger' /
--     'out_of_service' BLOCK operation (blocks_operation); 'caution' /
--     'information' are advisory (warn only). A tag may be ad-hoc (clearance_id
--     null) or bound to a clearance.
--   * safety.grounds    -- an applied safety earth at a network point (the
--     de-energised feature). Must be removed before that section is re-energised.
--
-- Bare feature/device refs (class + id, no FK), the same convention
-- oms.incidents.predicted_device_* / faults.fault_events / ops.operations use,
-- because a feature lives in its own per-class gis.<code> table. The optional
-- incident_id / switching_order_id on a clearance are correlations (no FK).
-- actor columns (placed_by / issued_by / ...) are iam user ids, null for a
-- system/service write -- the same stamp crud, faults and ops use.

CREATE SCHEMA IF NOT EXISTS safety;

-- ---------------------------------------------------------------------------
-- clearances -- the permit-to-work a crew holds over an isolated work area.
-- ---------------------------------------------------------------------------
CREATE TABLE safety.clearances (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    reference          text NOT NULL,            -- human PTW reference (e.g. PTW-2026-001)
    incident_id        uuid,                     -- optional correlation (bare ref, no FK)
    switching_order_id uuid,                     -- optional correlation (bare ref, no FK)
    work_description   text,
    issued_to          text,                     -- the crew / person the permit is handed to
    status             text NOT NULL DEFAULT 'issued'
                           CHECK (status IN ('requested', 'issued', 'surrendered', 'cancelled')),
    requested_by       uuid,
    requested_at       timestamptz NOT NULL DEFAULT now(),
    issued_by          uuid,
    issued_at          timestamptz,
    surrendered_by     uuid,
    surrendered_at     timestamptz,
    notes              text,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX clearances_status_idx ON safety.clearances (status, created_at DESC);
CREATE INDEX clearances_incident_idx ON safety.clearances (incident_id);

-- ---------------------------------------------------------------------------
-- tags -- a restriction on one device. A blocking tag bars operation.
-- ---------------------------------------------------------------------------
CREATE TABLE safety.tags (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    device_class     text NOT NULL,
    device_id        uuid NOT NULL,
    tag_type         text NOT NULL
                         CHECK (tag_type IN ('danger', 'out_of_service', 'caution', 'information')),
    blocks_operation boolean NOT NULL DEFAULT true,  -- danger/out_of_service block; caution/information advise
    reason           text,
    clearance_id     uuid REFERENCES safety.clearances(id) ON DELETE SET NULL,
    status           text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'removed')),
    placed_by        uuid,
    placed_at        timestamptz NOT NULL DEFAULT now(),
    removed_by       uuid,
    removed_at       timestamptz,
    notes            text,
    created_at       timestamptz NOT NULL DEFAULT now()
);

-- "is this device barred from operation?" -- the hot path the operate gate runs
-- on every open/close step. Partial so it indexes only the live, blocking tags.
CREATE INDEX tags_device_blocking_idx ON safety.tags (device_class, device_id)
    WHERE status = 'active' AND blocks_operation = true;
CREATE INDEX tags_device_active_idx ON safety.tags (device_class, device_id) WHERE status = 'active';
CREATE INDEX tags_clearance_idx ON safety.tags (clearance_id);

-- ---------------------------------------------------------------------------
-- grounds -- an applied safety earth at a de-energised network point.
-- ---------------------------------------------------------------------------
CREATE TABLE safety.grounds (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    feature_class text NOT NULL,
    feature_id    uuid NOT NULL,
    reason        text,
    clearance_id  uuid REFERENCES safety.clearances(id) ON DELETE SET NULL,
    status        text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'removed')),
    applied_by    uuid,
    applied_at    timestamptz NOT NULL DEFAULT now(),
    removed_by    uuid,
    removed_at    timestamptz,
    notes         text,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX grounds_feature_active_idx ON safety.grounds (feature_class, feature_id) WHERE status = 'active';
CREATE INDEX grounds_clearance_idx ON safety.grounds (clearance_id);

COMMENT ON TABLE safety.clearances IS
    'Permit-to-work over an isolated work area: issued to a crew, surrendered when done. Owns the tags/grounds that protect it. See migration 0151.';
COMMENT ON TABLE safety.tags IS
    'A safety restriction on one device; a blocking tag (danger/out_of_service) bars the operate gate from flipping its device_state. See migration 0151.';
COMMENT ON TABLE safety.grounds IS
    'An applied safety earth at a de-energised network point; must be removed before re-energising. See migration 0151.';
