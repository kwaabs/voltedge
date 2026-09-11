-- 0084_add_control_element_kind.sql
-- Purpose: extend runtime.diagram_elements.kind (migrations 0046 + 0047 + 0050)
-- with one new schematic kind -- 'control' -- the Phase 46 Slice 1 delivery of
-- the last long-open diagram gap, "dual line semantics (power wiring vs. dashed
-- control/trip lines)" (docs/phase-5-diagrams.md "What's still open",
-- docs/open-items.md).
--
-- Every wire the canvas draws today is either a real feature_connectivity power
-- edge (a conductor) or a decorative 'line'/'bus'/'tap'. A protection scheme
-- also needs SIGNAL lines -- a relay's dashed, arrowheaded trip line to a
-- breaker -- which are NOT power connectivity at all. 'control' is that
-- schematic-only signal line: pure diagram furniture like 'line', backed by no
-- feature, an ordered [[x,y],...] polyline in `path`, but rendered dashed with
-- an arrowhead so it reads as a control/trip signal rather than a power wire.
-- (Phase 46 Slice 2 additionally DERIVES the same dashed-arrow lines from real
-- protection_trip / protection_sensing relationships between placed features;
-- that is a pure client render over the existing GET /relationships and needs
-- no schema change -- this migration only adds the hand-drawn furniture kind.)
--
-- Neither a new column nor anything else is needed (a control line is
-- line-shaped, path-carrying, styled in `style`); only the kind CHECK has to
-- widen, exactly as 0047 ('symbol') and 0050 ('bus'/'tap') did.
--
-- Postgres auto-named 0046's inline CHECK diagram_elements_kind_check (0047 and
-- 0050 already dropped/recreated it); drop and recreate again with 'control'
-- added. Existing rows are unaffected (every current value is still permitted).

ALTER TABLE runtime.diagram_elements DROP CONSTRAINT diagram_elements_kind_check;
ALTER TABLE runtime.diagram_elements ADD CONSTRAINT diagram_elements_kind_check
    CHECK (kind IN ('text', 'box', 'line', 'symbol', 'bus', 'tap', 'control', 'title_block', 'legend', 'slot'));

COMMENT ON COLUMN runtime.diagram_elements.kind IS 'text | box | line | symbol | bus | tap | control | title_block | legend | slot. All rendered. bus = a heavy multi-segment busbar; tap = a right-angle stub off a bus with a T-junction dot; control = a dashed, arrowheaded schematic-only control/trip signal line -- all line-shaped furniture (path polyline), backed by no feature.';
