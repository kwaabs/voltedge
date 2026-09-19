-- 0106_create_faults.sql
-- Purpose: the Faults module -- a first-class record of an ELECTRICAL FAULT
-- EVENT on the network (an earth fault, a phase-to-phase short, a protection
-- operation), the causal hinge that sits BETWEEN the two subsystems that already
-- exist:
--
--   * OMS (migration 0087) tracks the customer-facing OUTAGE an incident
--     represents (who is off supply, ETR, SAIDI/SAIFI). Its cause is free text.
--   * WAM (migration 0096) tracks a slow-degrading asset CONDITION (a defect)
--     and the corrective work that fixes it.
--
-- Neither models the fault event itself: the short/earth fault, the device that
-- tripped and cleared it, the fault current, the clearing time. A fault EXPLAINS
-- an incident (the outage it caused) and TRIGGERS a defect (the asset failure
-- booked for repair). So faults gets its own `faults` schema and Go package
-- (api/internal/faults), the same arrangement oms / wam / circuit use -- NOT the
-- metadata compiler, because a fault is an operational record ABOUT the assets
-- on the map, not a GIS feature drawn on it.
--
-- Foreign-key convention (matches oms 0087 and wam 0096): a reference to a
-- per-class physical feature table (asset_id, protective_device_id) or to
-- another subsystem's record (incident_id -> oms.incidents, defect_id ->
-- wam.defects) is a bare uuid, NOT an enforced FK -- those rows live in tables
-- this schema is deliberately loosely coupled to (a compiled per-class table we
-- can't stably FK to; records in sibling schemas we don't want to chain
-- migrations to). There are no within-schema references in this slice, so this
-- table carries no real FKs at all.

CREATE SCHEMA IF NOT EXISTS faults;

-- ---------------------------------------------------------------------------
-- faults.fault_events -- one electrical fault event on the network.
-- ---------------------------------------------------------------------------
CREATE TABLE faults.fault_events (
    id                       uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    -- Optional human-facing reference (a control-room fault number), like a work
    -- order's reference. Null lets the id stand alone.
    reference                text,
    -- The asset the fault occurred on (feature class code + id). BOTH nullable --
    -- a fault on a span between assets, or one known only by the incident it
    -- caused, may carry no asset; it is anchored by its location or its incident
    -- instead (see the anchor CHECK below). Bare refs, no FK.
    asset_class              text,
    asset_id                 uuid,
    -- Optional pin: where the fault occurred. For an asset-less fault this may be
    -- the only spatial anchor a crew has.
    location                 geometry(Point, 4326),
    -- Electrical classification of the fault. Kept in a small closed set (the
    -- vocabulary the fault-current / protection engines already speak); 'unknown'
    -- covers a fault not yet classified.
    fault_type               text
                                 CHECK (fault_type IS NULL OR fault_type IN (
                                     'SLG',    -- single line-to-ground
                                     'LL',     -- line-to-line
                                     'LLG',    -- double line-to-ground
                                     'LLL',    -- three-phase
                                     'LLLG',   -- three-phase-to-ground
                                     'earth',  -- generic earth fault
                                     'unknown'
                                 )),
    -- Free-ish root cause (vegetation, animal, equipment_failure, dig_in,
    -- lightning, third_party, unknown...). Text, not a coded catalog -- the same
    -- "don't block field teams on a taxonomy" stance wam.defects.defect_type
    -- takes; a domain can be layered on later without a schema change.
    cause                    text,
    -- Measured or estimated fault current at the point of fault, in amperes;
    -- links a recorded event back to what the fault-current analysis predicts.
    fault_current_a          numeric CHECK (fault_current_a IS NULL OR fault_current_a >= 0),
    -- The protective device that operated to clear the fault (feature class code
    -- + id). Both nullable -- unknown until investigated. Bare refs, no FK; this
    -- mirrors oms.incidents' predicted_device_{class,id}.
    protective_device_class  text,
    protective_device_id     uuid,
    -- Protection performance: how long the fault stood before it cleared, in
    -- milliseconds. Feeds a coordination review.
    clearing_time_ms         numeric CHECK (clearing_time_ms IS NULL OR clearing_time_ms >= 0),
    -- Lifecycle: detected -> investigating -> cleared -> closed. 'cleared' means
    -- the fault is off the network; 'closed' means the follow-up (incident +
    -- defect) is wrapped up.
    status                   text NOT NULL DEFAULT 'detected'
                                 CHECK (status IN ('detected', 'investigating', 'cleared', 'closed')),
    -- How the fault came to light (scada, protection_relay, field_crew,
    -- customer_call, model...). Free text.
    detected_by              text,
    detected_at              timestamptz NOT NULL DEFAULT now(),
    cleared_at               timestamptz,          -- null = still on the network
    -- The OMS incident this fault caused (source of the outage). Bare ref, no FK.
    incident_id              uuid,
    -- The WAM defect this fault booked for repair (the asset failure). Bare ref,
    -- no FK -- loose coupling to the sibling wam schema.
    defect_id                uuid,
    title                    text NOT NULL,
    description              text,
    notes                    text,
    created_at               timestamptz NOT NULL DEFAULT now(),
    updated_at               timestamptz NOT NULL DEFAULT now(),

    -- An asset reference is all-or-nothing (both columns or neither) -- never a
    -- half-named asset.
    CONSTRAINT fault_events_asset_ck
        CHECK ((asset_class IS NULL) = (asset_id IS NULL)),
    -- A protective device reference is likewise all-or-nothing.
    CONSTRAINT fault_events_device_ck
        CHECK ((protective_device_class IS NULL) = (protective_device_id IS NULL)),
    -- A fault must be locatable by SOMETHING: the asset it hit, a location pin,
    -- or the incident it caused. It never floats with none of the three.
    CONSTRAINT fault_events_anchor_ck
        CHECK (asset_id IS NOT NULL OR location IS NOT NULL OR incident_id IS NOT NULL),
    -- A cleared/closed fault carries a clearing time, and only those do -- the
    -- same status/timestamp discipline oms.incidents and wam.defects keep.
    CONSTRAINT fault_events_cleared_ck
        CHECK ((status IN ('cleared', 'closed')) = (cleared_at IS NOT NULL))
);

-- Active-fault board: the common query is "everything not yet cleared".
CREATE INDEX fault_events_open_idx ON faults.fault_events (detected_at)
    WHERE status NOT IN ('cleared', 'closed');
CREATE INDEX fault_events_status_idx ON faults.fault_events (status);
-- Per-asset history (a fault count / list on a selected feature).
CREATE INDEX fault_events_asset_idx ON faults.fault_events (asset_class, asset_id)
    WHERE asset_id IS NOT NULL;
-- The OMS link (find the fault behind an incident).
CREATE INDEX fault_events_incident_idx ON faults.fault_events (incident_id)
    WHERE incident_id IS NOT NULL;
-- Spatial index on the pin for map overlays.
CREATE INDEX fault_events_location_idx ON faults.fault_events USING gist (location)
    WHERE location IS NOT NULL;
