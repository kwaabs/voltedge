-- 0170_create_oms_crews.sql
-- Purpose: real CREW / resource management for the OMS (blocker #2). Until now a
-- crew was a free-text `crew_ref` string on the incident and "dispatched" was a
-- board flag with nobody behind it. This adds crew ENTITIES with a status, and an
-- ASSIGNMENT with a lifecycle (dispatched → on-site → cleared) tracked per crew
-- per incident, so the board can answer "who is where, and when did they arrive".
--
-- Bare refs (uuid, no FK) to oms.incidents / gis.territories, matching the rest
-- of oms's loose coupling. A crew's `status` is denormalised for cheap listing and
-- kept in step by the assignment transitions; `unavailable` is an operator-set
-- off-shift state (only when the crew has no active assignment).

CREATE TABLE IF NOT EXISTS oms.crews (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name         text NOT NULL,
    kind         text,                 -- line / cable / substation / contractor (free text)
    status       text NOT NULL DEFAULT 'available'
                 CHECK (status IN ('available', 'dispatched', 'on_site', 'unavailable')),
    territory_id uuid,                 -- home base (bare ref)
    phone        text,
    active       boolean NOT NULL DEFAULT true,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE oms.crews IS 'Field crews / resources (0170): a dispatchable entity with a status, replacing the incident''s free-text crew_ref.';

CREATE TABLE IF NOT EXISTS oms.crew_assignments (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    crew_id       uuid NOT NULL,       -- bare ref → oms.crews
    incident_id   uuid NOT NULL,       -- bare ref → oms.incidents
    status        text NOT NULL DEFAULT 'dispatched'
                  CHECK (status IN ('dispatched', 'on_site', 'cleared')),
    dispatched_at timestamptz NOT NULL DEFAULT now(),
    on_site_at    timestamptz,
    cleared_at    timestamptz,
    note          text,
    assigned_by   uuid,                -- the acting user (bare ref → iam.users)
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE oms.crew_assignments IS 'A crew''s engagement on one incident (0170): dispatched → on-site → cleared, each with its timestamp.';

CREATE INDEX IF NOT EXISTS oms_crew_assignments_incident_idx ON oms.crew_assignments (incident_id);
-- One ACTIVE (non-cleared) assignment per crew is the common lookup (a crew's
-- current job); a partial index keeps it tight.
CREATE INDEX IF NOT EXISTS oms_crew_assignments_active_crew_idx
    ON oms.crew_assignments (crew_id) WHERE status <> 'cleared';
