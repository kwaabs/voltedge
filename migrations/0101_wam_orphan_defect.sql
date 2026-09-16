-- 0101_wam_orphan_defect.sql
-- Purpose: Phase 59 -- allow an "orphan" defect: one a field worker raises against
-- no catalogued asset (an unmapped pole, a stray bit of kit found in the field) but
-- pins with a location instead. Until now wam.defects.asset_class/asset_id were
-- NOT NULL (migration 0096), so every defect had to name a packed feature, and the
-- discovery of an un-inventoried problem could not be logged from the field.
--
-- This mirrors wam.work_orders, which already permits a null asset alongside a
-- nullable location (0096). A defect must still be anchored to SOMETHING -- either
-- an asset (class + id) or a location point -- so it is never a floating note; the
-- CHECK below enforces that. The `location geometry(Point, 4326)` column and its
-- GiST index already exist (0096), so no geometry is added here.

ALTER TABLE wam.defects ALTER COLUMN asset_class DROP NOT NULL;
ALTER TABLE wam.defects ALTER COLUMN asset_id   DROP NOT NULL;

COMMENT ON COLUMN wam.defects.asset_class IS 'Feature class of the asset the defect is on. NULL = an orphan defect not tied to a catalogued asset (anchored by location instead).';
COMMENT ON COLUMN wam.defects.asset_id IS 'Feature id of the asset the defect is on (bare ref, no FK). NULL = an orphan defect (see asset_class).';

-- The composite asset index now covers only defects that HAVE an asset (partial),
-- matching wam.work_orders' asset index. Orphan defects are found by location
-- (defects_location_idx GiST) or the open board, not by this index.
DROP INDEX IF EXISTS wam.defects_asset_idx;
CREATE INDEX defects_asset_idx ON wam.defects (asset_class, asset_id)
    WHERE asset_id IS NOT NULL;

-- Anchor rule: every defect names an asset OR carries a location (or both), never
-- neither -- so a defect is always locatable. Existing rows all have asset_id
-- NOT NULL, so this constraint validates immediately.
ALTER TABLE wam.defects
    ADD CONSTRAINT defects_anchor_ck
    CHECK (asset_id IS NOT NULL OR location IS NOT NULL);
