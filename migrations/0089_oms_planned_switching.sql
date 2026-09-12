-- 0089_oms_planned_switching.sql
-- Purpose: Phase 49 Slice 3 -- planned outages + switching orders.
--
-- Two additions on top of the Slice 1 OMS core (migration 0087):
--
--   1. PLANNED OUTAGES. The incidents table already carries outage_type
--      ('unplanned' | 'planned'), but every creation path so far only ever
--      produced unplanned, engine-PREDICTED incidents. A planned outage is
--      different: it is scheduled in advance around operating a chosen device,
--      not inferred from trouble calls. So this migration:
--        - adds a 'scheduled' lifecycle status (a planned outage sits scheduled
--          until it is actually executed, at which point confirm materialises
--          its interruptions -- same fact table, same reliability maths);
--        - adds planned_start / planned_end (the booked window), null on an
--          unplanned incident.
--      Reliability indices (SAIDI/SAIFI/CAIDI) count only UNPLANNED outages by
--      convention (IEEE 1366); the engine filters on outage_type, so a planned
--      outage's interruptions are recorded but excluded from those indices.
--
--   2. SWITCHING ORDERS. The ordered open/close program a crew executes to
--      isolate a work area and later restore it -- the formalisation of the
--      isolate trace into a step list (DU backlog #2). One switching_order per
--      incident (planned or, later, an unplanned restoration plan); an ordered
--      set of switching_steps under it. Device references are bare uuids (no FK
--      to the per-class compiled tables), the same convention as
--      incidents.predicted_device_id.

-- ---------------------------------------------------------------------------
-- 1. Planned-outage columns + the 'scheduled' status
-- ---------------------------------------------------------------------------

-- Widen the status domain to admit 'scheduled' (planned, not yet executed).
-- The original inline column CHECK is named oms.incidents' status check by
-- Postgres convention; drop and re-add it with the extra value.
ALTER TABLE oms.incidents DROP CONSTRAINT IF EXISTS incidents_status_check;
ALTER TABLE oms.incidents ADD CONSTRAINT incidents_status_check
    CHECK (status IN ('predicted', 'scheduled', 'confirmed', 'dispatched', 'restored', 'cancelled'));

-- The booked outage window; null on an unplanned incident.
ALTER TABLE oms.incidents ADD COLUMN IF NOT EXISTS planned_start timestamptz;
ALTER TABLE oms.incidents ADD COLUMN IF NOT EXISTS planned_end   timestamptz;

-- ---------------------------------------------------------------------------
-- 2. Switching orders + steps
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS oms.switching_orders (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    -- The incident this program serves. Real FK -- same schema.
    incident_id  uuid NOT NULL REFERENCES oms.incidents (id) ON DELETE CASCADE,
    reference    text,                       -- human ref / title (free text)
    status       text NOT NULL DEFAULT 'draft'
                     CHECK (status IN ('draft', 'issued', 'in_progress', 'completed', 'cancelled')),
    notes        text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS switching_orders_incident_idx ON oms.switching_orders (incident_id);

CREATE TABLE IF NOT EXISTS oms.switching_steps (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    switching_order_id uuid NOT NULL REFERENCES oms.switching_orders (id) ON DELETE CASCADE,
    -- 1-based execution order within the order.
    step_no            integer NOT NULL CHECK (step_no >= 1),
    -- Which half of the program: isolate the work area, then restore it.
    phase              text NOT NULL DEFAULT 'isolate'
                           CHECK (phase IN ('isolate', 'restore')),
    -- The switching action. 'verify' / 'earth' carry no device toggle; 'note'
    -- is a free instruction with no device.
    action             text NOT NULL
                           CHECK (action IN ('open', 'close', 'earth', 'verify', 'note')),
    -- The device operated (bare refs, no FK); null for a verify/note step.
    device_class       text,
    device_id          uuid,
    instruction        text NOT NULL,        -- the human-readable step text
    -- Set when the operator marks the step done (null = outstanding).
    executed_at        timestamptz,
    created_at         timestamptz NOT NULL DEFAULT now(),
    UNIQUE (switching_order_id, step_no)
);

CREATE INDEX IF NOT EXISTS switching_steps_order_idx ON oms.switching_steps (switching_order_id, step_no);
