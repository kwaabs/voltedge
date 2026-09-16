-- 0096_create_wam.sql
-- Purpose: Phase 59 Slice 1 -- the Work & Asset Management (WAM) operational
-- store. Like OMS (migration 0087), the things this module tracks are not GIS
-- features drawn on the map; they are operational records ABOUT the assets that
-- are: a defect found on a transformer, a work order a crew executes to fix it,
-- an inspection run on a cycle. So WAM lives in its own `wam` schema with
-- hand-written tables and a Go package (api/internal/wam), NOT through the
-- metadata compiler -- the same arrangement as oms / circuit / powerflow.
--
-- Two tables in this slice (inspection templates + maintenance plans are later
-- slices), in dependency order:
--
--   wam.defects       one asset-health finding: a physical problem on a gis.*
--                     feature (corrosion, oil leak, cracked insulator, hot
--                     joint, vegetation encroachment...). Sourced from an
--                     inspection, an OMS outage's root-cause failure, condition
--                     monitoring, or an ad-hoc field report. A defect is the
--                     WHAT-IS-WRONG record; fixing it is a corrective work order.
--
--   wam.work_orders   the central, TYPED unit of work (the "one work order
--                     engine" decision): work_type in inspection / corrective /
--                     preventive / construction, all sharing one lifecycle
--                     (draft -> scheduled -> assigned -> in_progress ->
--                     completed | cancelled). A corrective order links back to
--                     the defect it addresses (defect_id).
--
-- Foreign-key convention (matches oms, migration 0087): a reference to a
-- per-class physical feature table (asset_id) or to another subsystem's record
-- (incident_id -> oms.incidents) is a bare uuid, NOT an enforced FK -- those
-- rows live in tables this schema is deliberately loosely coupled to (a compiled
-- per-class table we can't stably FK to; an OMS record we don't want to chain
-- migrations to). References WITHIN the wam schema (work_orders.defect_id) ARE
-- real FKs.

CREATE SCHEMA IF NOT EXISTS wam;

-- ---------------------------------------------------------------------------
-- wam.defects -- created first so work_orders.defect_id can FK to it.
-- ---------------------------------------------------------------------------
CREATE TABLE wam.defects (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    -- The asset the defect is on (feature class code + id). Required: a defect
    -- is always about a specific asset. Bare refs, no FK (compiled per-class
    -- table). asset_id may address a point, line, or polygon feature.
    asset_class    text NOT NULL,
    asset_id       uuid NOT NULL,
    -- Free-ish classification of the problem (corrosion, oil_leak,
    -- cracked_insulator, hot_joint, vegetation, ...). Kept as text in this slice
    -- rather than a coded catalog so field teams aren't blocked on a taxonomy;
    -- a domain can be layered on later without a schema change.
    defect_type    text,
    severity       text NOT NULL DEFAULT 'medium'
                       CHECK (severity IN ('low', 'medium', 'high', 'critical')),
    status         text NOT NULL DEFAULT 'open'
                       CHECK (status IN ('open', 'work_ordered', 'resolved', 'dismissed')),
    -- How the defect was discovered. 'outage' pairs with incident_id (the OMS
    -- link): an outage's root-cause asset failure can be booked as a defect.
    source         text NOT NULL DEFAULT 'ad_hoc'
                       CHECK (source IN ('inspection', 'outage', 'condition_monitoring', 'ad_hoc')),
    -- Optional link to the OMS incident this defect explains (source='outage').
    -- Bare ref, no FK -- loose coupling between subsystems (see header).
    incident_id    uuid,
    title          text NOT NULL,
    description    text,
    -- Optional pin: where on/near the asset the defect is (a spot on a long
    -- line asset, say). Null = the defect is simply "on the asset".
    location       geometry(Point, 4326),
    reported_by    text,
    reported_at    timestamptz NOT NULL DEFAULT now(),
    resolved_at    timestamptz,          -- null = not yet resolved
    notes          text,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    -- A resolved/dismissed defect carries a resolution time, and only those do.
    CHECK ((status IN ('resolved', 'dismissed')) = (resolved_at IS NOT NULL))
);

-- Open-defect board (the common "what still needs attention" query) and the
-- per-asset history lookup used to show a defect count on a feature.
CREATE INDEX defects_open_idx ON wam.defects (severity, reported_at) WHERE status = 'open';
CREATE INDEX defects_asset_idx ON wam.defects (asset_class, asset_id);
CREATE INDEX defects_status_idx ON wam.defects (status);
CREATE INDEX defects_incident_idx ON wam.defects (incident_id) WHERE incident_id IS NOT NULL;
CREATE INDEX defects_location_idx ON wam.defects USING gist (location);

-- ---------------------------------------------------------------------------
-- wam.work_orders -- the one typed unit of work.
-- ---------------------------------------------------------------------------
CREATE TABLE wam.work_orders (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    -- Human-facing work-order number. Optional in this slice (free text / null);
    -- an auto-numbering scheme can be added later without touching callers.
    reference       text,
    work_type       text NOT NULL
                        CHECK (work_type IN ('inspection', 'corrective', 'preventive', 'construction')),
    status          text NOT NULL DEFAULT 'draft'
                        CHECK (status IN ('draft', 'scheduled', 'assigned', 'in_progress', 'completed', 'cancelled')),
    priority        text NOT NULL DEFAULT 'medium'
                        CHECK (priority IN ('low', 'medium', 'high', 'urgent')),
    title           text NOT NULL,
    description     text,
    -- The primary asset the work targets (feature class code + id). Nullable:
    -- location-only work (e.g. a patrol of an area) may name no single asset.
    -- Bare refs, no FK (compiled per-class table).
    asset_class     text,
    asset_id        uuid,
    -- Where the work is, when it isn't pinned to a single asset's geometry.
    location        geometry(Point, 4326),
    -- The defect this order addresses (set on a corrective order raised from a
    -- defect). Real FK -- same schema; SET NULL so deleting a defect doesn't
    -- delete the historical work.
    defect_id       uuid REFERENCES wam.defects (id) ON DELETE SET NULL,
    -- Crew / person the order is assigned to. Free text in this slice (no crew
    -- table yet); a real crew reference can replace it later.
    assigned_to     text,
    scheduled_for   timestamptz,
    due_by          timestamptz,
    started_at      timestamptz,
    completed_at    timestamptz,          -- null = not yet completed
    completion_notes text,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    -- A completed order carries a completion time, and only a completed one does.
    CHECK ((status = 'completed') = (completed_at IS NOT NULL))
);

-- Work board (open orders by priority), per-asset work history, and the
-- crew's "my jobs" lookup.
CREATE INDEX work_orders_open_idx ON wam.work_orders (priority, scheduled_for)
    WHERE status NOT IN ('completed', 'cancelled');
CREATE INDEX work_orders_type_idx ON wam.work_orders (work_type);
CREATE INDEX work_orders_status_idx ON wam.work_orders (status);
CREATE INDEX work_orders_asset_idx ON wam.work_orders (asset_class, asset_id)
    WHERE asset_id IS NOT NULL;
CREATE INDEX work_orders_assigned_idx ON wam.work_orders (assigned_to) WHERE assigned_to IS NOT NULL;
CREATE INDEX work_orders_defect_idx ON wam.work_orders (defect_id) WHERE defect_id IS NOT NULL;
CREATE INDEX work_orders_location_idx ON wam.work_orders USING gist (location);
