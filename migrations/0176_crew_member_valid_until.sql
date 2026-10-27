-- 0176_crew_member_valid_until.sql
-- Purpose: temporary crew membership. A crew member (0173) was permanent until
-- someone removed the row; this adds an optional expiry so a user can be seconded
-- to a crew for a shift/storm and drop off automatically when it lapses -- no
-- manual cleanup, and no routing work to a member who has rotated out.
--
-- NULL valid_until = permanent member (the common case). A non-null value is the
-- instant the membership stops counting: CrewsForUser and the field-pack fan-out
-- treat a member as effective only while (valid_until IS NULL OR valid_until >
-- now()), and the roster member_count counts only currently-effective members. An
-- expired row is kept (shown with an "expired" chip) so an admin can see and tidy
-- it; it simply no longer routes work.

ALTER TABLE oms.crew_members ADD COLUMN IF NOT EXISTS valid_until timestamptz;

COMMENT ON COLUMN oms.crew_members.valid_until IS 'Temporary-membership expiry (0176): NULL = permanent; past = no longer an effective member (kept for audit, routes no work).';
