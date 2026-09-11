-- 0070_add_tertiary_terminal_role.sql
-- Purpose: widen metadata.terminals.role to admit 'tertiary'. Until now a
-- terminal's role was primary/secondary/ground/other (migrations/0009) -- fine
-- for two-winding equipment where "primary" and "secondary" name the two
-- windings. A three-winding POWER_XFMR (seed/power_transformer_example.sql) has
-- a genuine third winding (the tertiary, TV), electrically distinct from the HV
-- primary and LV secondary, so its terminal needs a role of its own rather than
-- being flattened into 'other' (which would lose the winding's meaning for any
-- future phase/impedance logic that keys off role).
--
-- The original CHECK was an inline, unnamed column constraint, so Postgres
-- auto-named it metadata.terminals_role_check (<table>_<column>_check). Drop and
-- re-add it with the wider value set; IF EXISTS keeps this safe on a database
-- where the constraint was ever renamed or is absent. Purely additive to the
-- allowed set -- every existing row (primary/secondary/ground/other) still
-- satisfies the new constraint, so no data migration is needed.

ALTER TABLE metadata.terminals DROP CONSTRAINT IF EXISTS terminals_role_check;
ALTER TABLE metadata.terminals ADD CONSTRAINT terminals_role_check
    CHECK (role IN ('primary', 'secondary', 'tertiary', 'ground', 'other'));

COMMENT ON COLUMN metadata.terminals.role IS 'primary/secondary/tertiary/ground/other describe the terminal''s electrical role, used by network_rules and trace barrier logic. tertiary names the third winding of a three-winding power transformer.';
