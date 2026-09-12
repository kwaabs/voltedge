-- 0091_feature_class_functional_role.sql
-- Purpose: declare each feature class's ELECTRICAL FUNCTION -- what it does in
-- the power network -- as a first-class metadata axis, distinct from the
-- existing `network_role` (edge/junction/container/none), which describes only
-- how a class sits in the CONNECTIVITY GRAPH (a link, a node, a grouping, or
-- off-graph). The two are orthogonal: a DIST_XFMR and an MV_CONDUCTOR are both
-- network_role='edge' but functionally a transformer versus a wire.
--
-- One nullable text column on metadata.feature_classes:
--   functional_role -- one of the values below, or NULL for a class with no
--                      electrical function (containers, civil assets, furniture)
--
-- Values (the electrical-function taxonomy, see memory functional-role-taxonomy):
--   source       -- injects power (grid infeed, generation, storage discharge)
--   consumer     -- a load / sink (service point, meter, metering point)
--   passthrough  -- carries flow with no state (conductors / cables)
--   transformer  -- carries flow but changes voltage level (a passthrough that
--                   is also a voltage/circuit boundary)
--   controller   -- has an open/closed STATE that gates flow (switches,
--                   isolators, reclosers, breakers, fuses, cut-outs, earth
--                   switches) -- the only role whose state stops a trace
--   node         -- a many-way meeting point (busbars)
--   sensor       -- observes but never carries or gates through-flow, off-path
--                   (CTs, VTs, relays, the meter measurement element)
--   ground       -- provides a return / reference path, not power delivery
--                   (neutral earthing resistors, earthing devices)
--
-- NOTE the SOURCE caveat: in the current data model the network source is an
-- INSTANCE, not a class -- GRID-SRC / SUB-SOURCE are DIST_XFMR rows acting as a
-- unity-ratio slack (they carry source_r_ohm). So no existing class maps to
-- 'source'; the value exists for a future dedicated source class (generation /
-- grid-infeed). Supply-path source detection therefore stays topological, with
-- a role='source' preference layered in for when such a class exists.
--
-- Why a column, not the label_def-style jsonb: it's a single coded value read
-- straight through to the schema endpoint, exactly like `network` (0064),
-- `category` (0074) and the display-zoom band (0090) -- pure metadata the
-- compiler's DDL never touches. No physical gis.* table change, no recompile.
-- Purely additive and idempotent (IF NOT EXISTS), so a re-run is a clean no-op.
-- The class->role assignments for today's classes are applied separately by
-- seed/functional_role_backfill.sql (task seed-functional-roles), kept out of
-- the migration so the taxonomy can evolve without a new migration.

ALTER TABLE metadata.feature_classes
    ADD COLUMN IF NOT EXISTS functional_role text;

-- Constrain to the taxonomy (NULL always allowed = unclassified / non-electrical).
-- Guarded add so a re-run doesn't error on the already-present constraint.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'feature_classes_functional_role_check'
    ) THEN
        ALTER TABLE metadata.feature_classes
            ADD CONSTRAINT feature_classes_functional_role_check CHECK (
                functional_role IS NULL OR functional_role IN (
                    'source', 'consumer', 'passthrough', 'transformer',
                    'controller', 'node', 'sensor', 'ground'
                )
            );
    END IF;
END $$;
