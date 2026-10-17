-- seed/terminal_cardinality.sql
-- Terminal-cardinality flag: metadata.terminals.single_connection marks a terminal
-- that may hold exactly ONE connection -- a one-cable-per-port terminal like a cable
-- joint's T1/T2/T3. The connect path (api/internal/connectivity) refuses a SECOND
-- distinct feature on such a terminal, and the connectivity audit flags any existing
-- over-subscribed ones.
--
-- A BUSBAR terminal is deliberately left UNFLAGGED (false): a bus is a common node
-- where many ways meet, so it must stay many-to-one. The column defaults to false,
-- so NOTHING is guarded until a class is explicitly stamped here -- this seed stamps
-- the cable joints, the motivating case (a conductor split that re-wired BOTH halves
-- onto one joint terminal, silently breaking the supply-path trace).
--
-- Metadata-only, NO recompile (terminals are read live by the connect path and the
-- audit). Idempotent (ADD COLUMN IF NOT EXISTS + an UPDATE that re-asserts the flag).
-- Apply:  task seed-terminal-cardinality

ALTER TABLE metadata.terminals
    ADD COLUMN IF NOT EXISTS single_connection boolean NOT NULL DEFAULT false;

COMMENT ON COLUMN metadata.terminals.single_connection IS 'true = this terminal holds exactly one connection (a one-cable-per-port terminal, e.g. a cable joint T1/T2). The connect path refuses a second distinct feature on it; the connectivity audit flags over-subscribed ones. false (default) = many-to-one, e.g. a busbar common node.';

-- Stamp every terminal of the cable-joint classes (both the original MV_CABLE_JOINT/
-- LV_CABLE_JOINT codes and the renamed MV_JOINT/LV_JOINT, so it holds regardless of
-- whether the generic rename was applied). A joint splices one cable per terminal.
UPDATE metadata.terminals t
   SET single_connection = true
  FROM metadata.feature_classes fc
 WHERE t.feature_class_id = fc.id
   AND fc.code IN ('MV_JOINT', 'LV_JOINT', 'MV_CABLE_JOINT', 'LV_CABLE_JOINT');

DO $$
BEGIN
    RAISE NOTICE 'seed/terminal_cardinality.sql applied: terminals.single_connection added (default false) + stamped true on the cable-joint classes. No recompile needed.';
END $$;
