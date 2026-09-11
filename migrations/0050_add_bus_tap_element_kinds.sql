-- 0050_add_bus_tap_element_kinds.sql
-- Purpose: extend runtime.diagram_elements.kind (migrations 0046 + 0047) with
-- two new schematic-routing kinds -- 'bus' and 'tap' -- the Phase 24 Slice 5
-- delivery of the long-open "multi-segment bus routing" gap
-- (docs/phase-5-diagrams.md "What's still open", docs/open-items.md).
--
-- A real one-line/protection diagram routes wires off a shared busbar with
-- T-junction taps, not just point-to-point elbows between two devices. Both
-- new kinds are pure diagram furniture like the existing 'line' -- backed by
-- no feature, an ordered [[x,y],...] polyline in `path`, styled in `style`:
--   * bus  -- a heavy busbar drawn as a multi-segment orthogonal run.
--   * tap  -- a thin right-angle stub off a bus, rendered with a T-junction
--             dot at its bus end.
-- Neither needs new columns (both are line-shaped, path-carrying elements);
-- only the kind CHECK has to widen, exactly as 0047 did for 'symbol'.
--
-- Postgres auto-named 0046's inline CHECK diagram_elements_kind_check (0047
-- already dropped/recreated it once); drop and recreate again with the two
-- kinds added. Existing rows are unaffected (every current value is still
-- permitted).

ALTER TABLE runtime.diagram_elements DROP CONSTRAINT diagram_elements_kind_check;
ALTER TABLE runtime.diagram_elements ADD CONSTRAINT diagram_elements_kind_check
    CHECK (kind IN ('text', 'box', 'line', 'symbol', 'bus', 'tap', 'title_block', 'legend', 'slot'));

COMMENT ON COLUMN runtime.diagram_elements.kind IS 'text | box | line | symbol | bus | tap | title_block | legend | slot. text/box/line/symbol/bus/tap and title_block/legend/slot are all rendered. bus = a heavy multi-segment busbar; tap = a right-angle stub off a bus with a T-junction dot -- both are line-shaped furniture (path polyline), backed by no feature.';
