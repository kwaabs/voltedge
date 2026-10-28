-- 0181_incident_mirror.sql
-- Purpose: consume an AUTHORITATIVE external OMS as a READ-ONLY, PUSH-ONLY mirror
-- (external-mirror-integration design). An external OMS that OWNS its incidents
-- pushes them to VoltEdge, which reflects them beside its own locally-raised
-- incidents. Two columns tag a mirrored incident:
--
--   origin_system — NULL = a locally-owned incident (raised from a signal or by an
--                   operator; editable; pushed out on change as before). A
--                   system_code = MIRRORED from that external system: read-only in
--                   VoltEdge (its lifecycle is theirs), and NEVER re-emitted outbound
--                   (echo suppression — no loop back to its source).
--   external_id   — the external system's own id for the incident, the upsert key so
--                   a re-pushed delta updates the same shadow row.
--
-- A mirrored incident freezes NO interruptions and so never enters VoltEdge's
-- SAIDI/SAIFI (their system owns reliability); it carries the external system's own
-- affected counts for display. The device/circuit it references resolve to VoltEdge
-- features through external_ref (0145), so a mirrored incident still draws on the map.

ALTER TABLE oms.incidents ADD COLUMN IF NOT EXISTS origin_system text;
ALTER TABLE oms.incidents ADD COLUMN IF NOT EXISTS external_id   text;

COMMENT ON COLUMN oms.incidents.origin_system IS 'Read-only mirror origin (0181): NULL = local; a system_code = mirrored from that external OMS (read-only, not re-emitted).';
COMMENT ON COLUMN oms.incidents.external_id IS 'The external OMS''s own incident id (0181), the upsert key for a mirrored incident.';

-- One shadow per external incident, the mirror upsert key.
CREATE UNIQUE INDEX IF NOT EXISTS oms_incidents_origin_external_idx
    ON oms.incidents (origin_system, external_id)
    WHERE origin_system IS NOT NULL AND external_id IS NOT NULL;
