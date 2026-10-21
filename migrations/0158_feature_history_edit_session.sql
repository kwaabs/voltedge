-- 0158_feature_history_edit_session.sql
-- Purpose: group a batch of feature edits made in one edit-mode session back
-- together. The web client mints a uuid when the Edit toggle is turned on and
-- sends it on every mutating request (the X-Edit-Session header) until View; the
-- CRUD history writer stamps it onto each gis.feature_history row. An
-- edit-sessions report then GROUPs BY this id to show, per session: who, when it
-- started/ended (min/max of the stamped rows), how many changes, which classes.
--
-- Nullable, no default: a write made outside edit mode (a read-only viewer can't
-- write; a background/system write, the field-sync replay, or the MCP tools)
-- carries no session and leaves this NULL -- those rows simply never belong to an
-- edit session, which is the intended "ignore sessions where nothing changed"
-- shape (an empty session writes no history, so it never appears).

ALTER TABLE gis.feature_history
    ADD COLUMN IF NOT EXISTS edit_session_id uuid;

COMMENT ON COLUMN gis.feature_history.edit_session_id IS 'The client edit-mode session (X-Edit-Session) this change was made in, or NULL for a write made outside edit mode / by a non-UI caller. Groups a batch of edits into one session for the edit-sessions report.';

-- The report filters to non-null sessions and groups by (actor, session); a
-- partial index keeps that scan cheap as history grows without indexing the vast
-- majority of rows (system/legacy writes that carry no session).
CREATE INDEX IF NOT EXISTS feature_history_edit_session_idx
    ON gis.feature_history (edit_session_id, actor_id)
    WHERE edit_session_id IS NOT NULL;
