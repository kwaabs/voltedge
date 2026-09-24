-- seed/circuit_ref_example.sql
-- Phase 35 (double-circuit depiction), Slice 1.
--
-- Adds a nullable `circuit_ref` identity tag to the conductor classes so two
-- circuits sharing one support structure (a double-circuit pole/tower/pylon --
-- e.g. 11 kV Feeder 1 on the left, Feeder 2 on the right) can be told apart:
--   * on the MAP, as the label ("offset annotation") that rides each offset
--     conductor run -- with the two runs digitized to their own side of the
--     pole line (Option B), F1's label sits on the left line and F2's on the
--     right, with no special annotation logic (OL placement:"line");
--   * on the pole-head SCHEMATIC (a later slice), as the block identity.
-- See docs/phase-35-double-circuit.md.
--
-- circuit_ref is nullable text (no domain). It is DERIVED, not hand-entered:
-- as of the circuit-membership consolidation it is is_computed = true and hidden
-- from both forms (visible_on_create/update = false), so the CRUD engine rejects
-- any attempt to set it directly (api/internal/crud/validate.go). The ONLY writer
-- is the Phase 37 assign-by-trace (api/internal/circuit/assign.go), which stamps
-- circuit_ref = the circuit's name onto every conductor a circuit reaches (and
-- NULLs it on the full-sync clear). The single source of truth for membership is
-- the circuit_id uuid (a proper catalog reference); circuit_ref is a denormalized
-- copy of that circuit's *name*, kept only because the two no-join read paths --
-- the map label below and the trace/analysis display (api/internal/trace/graph.go
-- enrichNode) -- read it straight off the row. A double circuit is told apart by
-- its two runs having distinct circuit_id (hence distinct names), NOT by a hand
-- tag; see docs/phase-35-double-circuit.md. A run with no circuit_id draws no
-- label. The physical left/right offset lives in the digitized geometry (Option
-- B), NOT in a field, so there is deliberately no `circuit_side` column here.
--
-- (Historical note: this field began life in Phase 35 as a hand-entered
-- free-text double-circuit tag. Phase 37's circuit_id + assign-by-trace made that
-- redundant and introduced a second writer that silently clobbered hand values on
-- re-sync; the fix was to make circuit_ref derive-only, leaving a single writer.)
--
-- Metadata-only field add + a guarded demo label_def. This is picked up by the
-- metadata-driven create form and generic CRUD automatically -- no API/UI code.
-- Recompile after applying so the physical tables gain the column:
--   task seed-circuit-ref
--   task compile CODE=MV_CONDUCTOR
--   task compile CODE=LV_CONDUCTOR
-- Adding a nullable text column is non-destructive (ALTER TABLE ADD COLUMN).
--
-- Depends on seed/connectivity_example.sql (MV_CONDUCTOR) and
-- seed/lv_conductors_example.sql (LV_CONDUCTOR) having been applied -- both
-- already declare the 'electrical' field_group this field reuses.

-- ---------------------------------------------------------------------------
-- circuit_ref field on both conductor classes (nullable text, no domain,
-- existing 'electrical' group). DERIVED: is_computed = true and hidden from both
-- forms so only assign-by-trace can write it. One set-generating INSERT over the
-- two edge classes; ON CONFLICT keeps it idempotent. No subtype_field_rules row.
-- Existing installs (where the row already exists) are flipped to derived by
-- seed/circuit_ref_derived.sql, since ON CONFLICT DO NOTHING won't touch them.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.fields
  (feature_class_id, name, data_type, domain_id, group_id, nullable,
   is_computed, visible_on_create, visible_on_update)
SELECT fc.id, 'circuit_ref', 'text', NULL, fg.id, true,
       true, false, false
FROM metadata.feature_classes fc
JOIN metadata.field_groups fg ON fg.code = 'electrical'
WHERE fc.code IN ('MV_CONDUCTOR', 'LV_CONDUCTOR')
ON CONFLICT (feature_class_id, name) DO NOTHING;

-- ---------------------------------------------------------------------------
-- Demo map label: draw circuit_ref as the conductor label so the two offset
-- runs self-annotate. Guarded on label_def IS NULL so an already-authored
-- label (Phase 30 admin dialog) is never clobbered. A blank circuit_ref simply
-- renders no text -- so a single-circuit network stays label-free, and only
-- the shared-structure runs that were actually tagged show F1/F2.
-- min_zoom 15 keeps it to pole scale (declutter); halo for legibility over the
-- line. Shape per metadata.feature_classes.label_def (migration 0055).
-- ---------------------------------------------------------------------------
UPDATE metadata.feature_classes
SET label_def = '{"template":"{circuit_ref}","size":11,"halo":true,"halo_color":"#ffffff","halo_width":3,"min_zoom":15}'::jsonb
WHERE code IN ('MV_CONDUCTOR', 'LV_CONDUCTOR')
  AND label_def IS NULL;
