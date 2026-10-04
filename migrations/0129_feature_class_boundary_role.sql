-- 0129_feature_class_boundary_role.sql
-- Purpose: declare each feature class's NETWORK-SECTIONING function -- what kind
-- of boundary it forms in the distribution network -- as a first-class metadata
-- axis. This is the backbone of network labelling / subnetworks: a "section" is
-- the span between boundary devices, and the KIND of section depends on the KIND
-- of device that bounds it. It refines `functional_role` (migration 0091), which
-- lumps every switching/protection device as 'controller'; sectioning needs the
-- finer distribution distinction between a device that interrupts fault current
-- and one that merely isolates.
--
-- One nullable text column on metadata.feature_classes:
--   boundary_role -- one of the values below, or NULL for a class that does not
--                    bound the network (conductors, busbars, meters, poles, ...)
--
-- Values (the sectioning taxonomy, grounded in distribution practice):
--   source       -- a class that originates a (sub)network: generation / grid
--                   infeed. NOTE the feeder HEAD is an INSTANCE fact (which
--                   breaker sits off the station bus) carried by a circuit's
--                   source feature, not a class fact -- a breaker is 'protective'
--                   by class and becomes a feeder source by being a circuit's
--                   source. Same caveat functional_role 'source' carries.
--   protective   -- interrupts fault current, carving a PROTECTION ZONE: breaker,
--                   recloser, fuse, cut-out. Bounds "what de-energises on a fault".
--   switching    -- isolates / transfers without being primarily protective,
--                   carving a SWITCHING / ISOLATION SECTION: disconnector,
--                   isolator, load-break switch, sectionaliser, tie, link box.
--                   Bounds "what I can isolate to work on it".
--   transformer  -- a voltage boundary that originates the next-voltage network
--                   at its secondary (DIST_XFMR / POWER_XFMR -> the LV network).
--
-- Why a column, not jsonb: a single coded value read straight through to the
-- schema endpoint and the trace/section engines, exactly like `functional_role`
-- (0091), `network` (0064) and `trace_tier` (0126). No physical gis.* change, no
-- recompile. Purely additive and idempotent. The per-class assignments are
-- applied separately by seed/boundary_role_backfill.sql (task seed-boundary-roles),
-- kept out of the migration so the taxonomy can evolve without a new migration.

ALTER TABLE metadata.feature_classes
    ADD COLUMN IF NOT EXISTS boundary_role text;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'feature_classes_boundary_role_check'
    ) THEN
        ALTER TABLE metadata.feature_classes
            ADD CONSTRAINT feature_classes_boundary_role_check CHECK (
                boundary_role IS NULL OR boundary_role IN (
                    'source', 'protective', 'switching', 'transformer'
                )
            );
    END IF;
END $$;
