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
-- circuit_ref is free text (no domain), nullable -- a single-circuit run just
-- leaves it blank (and then draws no circuit label). The physical left/right
-- offset lives in the digitized geometry (Option B), NOT in a field, so there
-- is deliberately no `circuit_side` column here (see the doc for why -- a side
-- field would only be needed if the map ever switched to render-time offset).
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
-- existing 'electrical' group). One set-generating INSERT over the two edge
-- classes; ON CONFLICT keeps it idempotent. No subtype_field_rules row -- the
-- tag is optional for every subtype (a run need not belong to a named circuit).
-- ---------------------------------------------------------------------------
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, 'circuit_ref', 'text', NULL, fg.id, true
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
