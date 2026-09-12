-- 0087_create_oms.sql
-- Purpose: Phase 49 Slice 1a -- the Outage Management System (OMS) operational
-- store. Unlike feature classes, an outage is not a GIS feature drawn on the
-- map; it is an operational TRANSACTION about the live network -- a customer
-- rang in, the engine blamed a protective device, N customers went dark, the
-- crew restored it. So OMS lives in its own `oms` schema with hand-written
-- tables and a Go package (like api/internal/circuit / powerflow), NOT through
-- the metadata compiler.
--
-- Three tables, in dependency order:
--
--   oms.trouble_calls          one customer report of no/partial supply. May
--                              reference a known SERVICE_POINT, OR carry only a
--                              raw address / lat-lng (SP-optional intake); an
--                              unresolved call waits in a queue until associated
--                              (manually or snapped to the nearest SP). Only a
--                              call WITH a service_point_id feeds the upstream
--                              prediction rollup.
--
--   oms.incidents              the inferred outage. The prediction engine walks
--                              up from each resolved call to the deepest
--                              protective device common to a call cluster and
--                              writes ONE incident blaming that device (a batch
--                              of calls can yield several incidents when the
--                              calls sit under electrically-disjoint devices).
--                              status = predicted -> confirmed -> dispatched ->
--                              restored (or cancelled). A prediction touches
--                              NOTHING in the live network -- operational_state
--                              is only ever changed by a human, and this phase
--                              keeps OMS a pure overlay (no network coupling).
--
--   oms.customer_interruptions the per-SP interruption FACT the reliability
--                              indices (Slice 2: SAIDI/SAIFI/CAIDI/ENS) sum
--                              over. Materialized on CONFIRM -- it snapshots the
--                              customers-served count and the interruption
--                              window so a historical index stays correct even
--                              as the network is re-modelled later.
--
-- Foreign-key convention (matches gis.circuits, migration 0062): a reference to
-- a per-class physical feature table (service_point_id, predicted_device_id,
-- circuit_id) is a bare uuid, NOT an enforced FK -- the referenced row lives in
-- a compiled per-class table this schema can't stably FK to. References WITHIN
-- the oms schema (incident_id) ARE real FKs.

CREATE SCHEMA IF NOT EXISTS oms;

-- ---------------------------------------------------------------------------
-- oms.incidents -- created first so trouble_calls.incident_id can FK to it.
-- ---------------------------------------------------------------------------
CREATE TABLE oms.incidents (
    id                       uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    -- The protective device the rollup blamed (feature class code + id). Both
    -- nullable: a manually-raised incident may not name one yet. Bare refs, no FK.
    predicted_device_class   text,
    predicted_device_id      uuid,
    -- Optional grouping/reporting axis (gis.circuits). Bare ref, no FK.
    circuit_id               uuid,
    outage_type              text NOT NULL DEFAULT 'unplanned'
                                 CHECK (outage_type IN ('unplanned', 'planned')),
    cause_code               text,
    status                   text NOT NULL DEFAULT 'predicted'
                                 CHECK (status IN ('predicted', 'confirmed', 'dispatched', 'restored', 'cancelled')),
    -- Prediction confidence 0..1 (call count / cluster tightness); null when
    -- the incident was raised manually rather than predicted.
    confidence               numeric CHECK (confidence IS NULL OR (confidence >= 0 AND confidence <= 1)),
    started_at               timestamptz NOT NULL DEFAULT now(),
    etr                      timestamptz,          -- estimated restoration
    restored_at              timestamptz,          -- null = still out
    -- Denormalized impact snapshot (also stored per-SP in customer_interruptions
    -- once confirmed); handy for the incident list without a re-trace.
    affected_customers       integer,
    affected_service_points  integer,
    crew_ref                 text,
    notes                    text,
    created_at               timestamptz NOT NULL DEFAULT now(),
    updated_at               timestamptz NOT NULL DEFAULT now(),
    -- A restored incident must carry its restoration time, and vice-versa.
    CHECK ((status = 'restored') = (restored_at IS NOT NULL))
);

-- Active-outage board: the common query is "everything not yet restored".
CREATE INDEX incidents_open_idx ON oms.incidents (started_at) WHERE restored_at IS NULL;
CREATE INDEX incidents_status_idx ON oms.incidents (status);
CREATE INDEX incidents_circuit_idx ON oms.incidents (circuit_id) WHERE circuit_id IS NOT NULL;

-- ---------------------------------------------------------------------------
-- oms.trouble_calls
-- ---------------------------------------------------------------------------
CREATE TABLE oms.trouble_calls (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    -- SP-optional intake: at least one locator must be present so a call can
    -- ever be placed on the network. service_point_id is the resolved form the
    -- prediction rollup consumes; address/location are the raw forms that wait
    -- for association (location enables snap-to-nearest-SP).
    service_point_id uuid,
    address          text,
    location         geometry(Point, 4326),
    reported_at      timestamptz NOT NULL DEFAULT now(),
    caller_ref       text,                 -- phone / account, free text
    call_type        text NOT NULL DEFAULT 'no_supply'
                         CHECK (call_type IN ('no_supply', 'partial', 'flicker')),
    status           text NOT NULL DEFAULT 'open'
                         CHECK (status IN ('open', 'associated', 'cleared', 'cancelled')),
    -- Set when the call is rolled into an incident; cleared to null if the
    -- incident is cancelled. Real FK -- same schema.
    incident_id      uuid REFERENCES oms.incidents (id) ON DELETE SET NULL,
    notes            text,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    CHECK (service_point_id IS NOT NULL OR address IS NOT NULL OR location IS NOT NULL)
);

CREATE INDEX trouble_calls_incident_idx ON oms.trouble_calls (incident_id) WHERE incident_id IS NOT NULL;
CREATE INDEX trouble_calls_status_idx ON oms.trouble_calls (status);
CREATE INDEX trouble_calls_sp_idx ON oms.trouble_calls (service_point_id) WHERE service_point_id IS NOT NULL;
CREATE INDEX trouble_calls_location_idx ON oms.trouble_calls USING gist (location);

-- ---------------------------------------------------------------------------
-- oms.customer_interruptions -- the indices fact table (materialized on confirm)
-- ---------------------------------------------------------------------------
CREATE TABLE oms.customer_interruptions (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    incident_id      uuid NOT NULL REFERENCES oms.incidents (id) ON DELETE CASCADE,
    service_point_id uuid NOT NULL,        -- bare ref, no FK (per-class table)
    -- Snapshot of SERVICE_POINT.customers_served AT interruption time (null on
    -- the SP is treated as 1); frozen here so re-modelling the field later can't
    -- retro-change a historical SAIDI/SAIFI.
    customers        integer NOT NULL DEFAULT 1 CHECK (customers >= 0),
    interrupted_at   timestamptz NOT NULL,
    restored_at      timestamptz,          -- null = still interrupted
    created_at       timestamptz NOT NULL DEFAULT now(),
    -- One interruption row per (incident, service point).
    UNIQUE (incident_id, service_point_id)
);

CREATE INDEX customer_interruptions_incident_idx ON oms.customer_interruptions (incident_id);
CREATE INDEX customer_interruptions_sp_idx ON oms.customer_interruptions (service_point_id);
