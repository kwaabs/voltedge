-- boundary_role_backfill.sql
-- Stamp metadata.feature_classes.boundary_role (migration 0129) onto today's
-- classes -- the NETWORK-SECTIONING taxonomy that underpins network labelling /
-- subnetworks. A "section" is the span between boundary devices; this says which
-- classes ARE boundary devices, and what kind of section each carves. Grounded in
-- distribution practice (see the migration header). Idempotent; re-runnable.
--
-- NOT a migration (apply.sh only scans /migrations). Pure metadata about classes
-- -- no gis.* change, no recompile. Apply with:
--   task seed-boundary-roles
--
-- A class the IN-list names but that isn't seeded on this database is simply
-- skipped (the UPDATE matches nothing). Static-review note: shell here can't
-- spawn (bash.exe EPERM) -- a live psql apply is still owed to call it verified.

BEGIN;

-- source -- a class that is itself a network origin (generation / grid infeed).
-- The MV FEEDER head is NOT here: which breaker is the station CB is an INSTANCE
-- fact carried by a circuit's source feature, not a class fact (a breaker is
-- 'protective' by class). Same caveat functional_role 'source' carries.
UPDATE metadata.feature_classes SET boundary_role = 'source'
 WHERE code IN ('GENERATOR');

-- protective -- interrupts fault current, carving a PROTECTION ZONE (bounds what
-- de-energises on a fault): breakers, reclosers, fuses, cut-outs. MV_BREAKER is
-- here (it becomes a feeder source by circuit designation, stays protective by
-- class); LV_FUSE is the feeder-pillar fuse the user named as an LV controller.
UPDATE metadata.feature_classes SET boundary_role = 'protective'
 WHERE code IN (
    'MV_BREAKER', 'MV_RECLOSER', 'MV_FUSE',
    'SERVICE_CUTOUT', 'LV_FUSE'
 );

-- switching -- isolates / transfers without being primarily protective, carving
-- a SWITCHING / ISOLATION SECTION (bounds what you can isolate to work on it):
-- disconnectors, isolators, load-break switches, link boxes. RMU ways collapse to
-- MV_SWITCH, so they ride this row. MV_EARTH_SWITCH is deliberately NOT a
-- sectioning boundary (it grounds for safety, it doesn't section the network).
UPDATE metadata.feature_classes SET boundary_role = 'switching'
 WHERE code IN (
    'MV_SWITCH', 'MV_ISOLATOR', 'MV_SECTIONALIZER',
    'LV_SWITCH', 'LV_LINK_BOX'
 );

-- transformer -- a voltage boundary that originates the next-voltage network at
-- its secondary (the transformer is the LV network's "station breaker").
UPDATE metadata.feature_classes SET boundary_role = 'transformer'
 WHERE code IN ('DIST_XFMR', 'POWER_XFMR');

COMMIT;
