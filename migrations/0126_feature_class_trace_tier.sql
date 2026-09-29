-- 0126_feature_class_trace_tier.sql
-- Purpose: declare each feature class's NETWORK TIER -- its rank in the vertical
-- source-to-load hierarchy -- as a first-class metadata axis, so a trace can tell
-- "downstream" from "upstream" by what a feature IS, not only by the from/to
-- orientation its connections happened to be authored in. Lower tier = closer to
-- the source (grid infeed = 0); higher = closer to the load. A DOWNSTREAM walk may
-- step to an equal-or-higher tier but never to a strictly-lower one (that would be
-- walking back toward the source); UPSTREAM mirrors it. See api/internal/trace's
-- bfs cross-layer guard.
--
-- One nullable int column on metadata.feature_classes:
--   trace_tier -- a non-negative rank, or NULL for an untiered class. NULL is
--                 TRANSPARENT: a step to or from an untiered class is never
--                 tier-blocked, so the guard is a clean no-op until tiers are
--                 seeded and only ever tightens the classes that carry one.
--
-- Suggested scale (seeded separately, gaps of 100 so classes slot in later):
--   0    source / grid infeed        300  MV (busbar/switchgear/conductor/switch)
--   200  sub-transmission, POWER_XFMR 400  DIST_XFMR (MV->LV boundary)
--   500  LV (busbar/pillar/conductor/fuse)   600  service point / meter
--
-- Why a column, not the label_def-style jsonb: it's a single scalar read straight
-- through to the schema endpoint, exactly like `functional_role` (0091),
-- `network` (0064) and `category` (0074) -- pure metadata the compiler's DDL never
-- touches. No physical gis.* table change, no recompile. Purely additive and
-- idempotent (IF NOT EXISTS), so a re-run is a clean no-op. The class->tier
-- assignments for today's classes are applied separately by seed/trace_tiers.sql
-- (task seed-trace-tiers), kept out of the migration so the scale can be retuned
-- without a new migration.

ALTER TABLE metadata.feature_classes
    ADD COLUMN IF NOT EXISTS trace_tier integer;

-- Constrain to a non-negative rank (NULL always allowed = untiered / transparent).
-- Guarded add so a re-run doesn't error on the already-present constraint.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'feature_classes_trace_tier_check'
    ) THEN
        ALTER TABLE metadata.feature_classes
            ADD CONSTRAINT feature_classes_trace_tier_check CHECK (
                trace_tier IS NULL OR trace_tier >= 0
            );
    END IF;
END $$;
