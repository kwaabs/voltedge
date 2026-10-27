-- 0173_create_oms_crew_members.sql
-- Purpose: de-silo the crew (blocker #2 follow-up). Until now a crew (0170) was a
-- name/phone/territory with no PEOPLE behind it -- disconnected from iam.users. A
-- crew is a team; this adds its MEMBERSHIP: which internal users make up the crew,
-- and an optional role (lead / member). That membership is the join everything
-- downstream keys off -- a crew-assigned work order ships to every member's field
-- pack, and "which crews is this user on" drives that fan-out.
--
-- Bare refs (uuid, no FK) to oms.crews / iam.users, matching the rest of oms's
-- loose coupling. A user is on a crew at most once (unique crew_id+user_id); a
-- user may be on several crews. Removing a member is a hard delete (membership is
-- current-state, not history -- the assignment timeline on oms.crew_assignments is
-- where "who worked what" lives).

CREATE TABLE IF NOT EXISTS oms.crew_members (
    id        uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    crew_id   uuid NOT NULL,       -- bare ref → oms.crews
    user_id   uuid NOT NULL,       -- bare ref → iam.users
    role      text,                -- lead / member (free text)
    added_at  timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE oms.crew_members IS 'Crew membership (0173): the internal users that make up a crew, the join between oms.crews and iam.users.';

-- A user is on a given crew at most once.
CREATE UNIQUE INDEX IF NOT EXISTS oms_crew_members_unique
    ON oms.crew_members (crew_id, user_id);
-- "which crews is this user on" (the field-pack fan-out lookup) wants the user side.
CREATE INDEX IF NOT EXISTS oms_crew_members_user_idx
    ON oms.crew_members (user_id);
