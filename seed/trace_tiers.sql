-- trace_tiers.sql
-- Stamp metadata.feature_classes.trace_tier (migration 0126) onto today's
-- electrical classes -- each class's RANK in the vertical source-to-load
-- hierarchy. Lower = closer to the grid infeed, higher = closer to the load. A
-- DOWNSTREAM trace may step to an equal-or-higher tier but never a strictly-
-- lower one (that would be climbing back toward the source); UPSTREAM mirrors
-- it. See the cross-layer guard in api/internal/trace's bfs.
--
-- Kept OUT of the migration so the scale can be retuned without a new migration;
-- run after applying 0126:  task seed-trace-tiers
--
-- Scale is in HUNDREDS, with gaps of 100 so a new layer can slot between two
-- existing ones later without renumbering:
--   0    source / grid infeed          400  DIST_XFMR (MV->LV boundary)
--   200  sub-transmission, POWER_XFMR   500  LV (busbar/pillar/conductor/fuse)
--   300  MV (busbar/switchgear/          600  service point / meter
--        conductor/switch/protection)
--
-- Idempotent: a set of UPDATE ... WHERE code IN (...) statements, so a re-run
-- just re-sets the same values. TOLERANT of a deployment that lacks some of
-- these classes -- an UPDATE whose code matches nothing is a clean no-op, so the
-- generous lists below never error on an absent class. Requires only the
-- metadata column (no gis.* table, no recompile).
--
-- UNTIERED classes stay trace_tier = NULL and are TRANSPARENT to the guard: a
-- step to or from them is never tier-blocked. Structural/containers (SUBSTATION,
-- BAY, panels-as-containers, RMU, poles, towers, ducts) and off-path devices
-- (relays, instrument transformers, DC-auxiliary) are deliberately left NULL --
-- they carry no rank on the traced power path. NETWORK_METER is off-path too.

BEGIN;

-- 0  source / grid infeed -- injects power into the network (embedded /
-- distributed generation). An endpoint at the top of the hierarchy: nothing is
-- "upstream" of it, so a downstream walk that reaches it stops naturally on tier
-- (0 is strictly-lower than any layer feeding it).
UPDATE metadata.feature_classes SET trace_tier = 0
 WHERE code IN ('GENERATOR');

-- 200  sub-transmission / HV->MV boundary -- the grid power transformer that
-- steps sub-transmission down to the MV bus. Sits just above the MV layer so a
-- downstream walk flows through it into MV but can't climb back up through it.
UPDATE metadata.feature_classes SET trace_tier = 200
 WHERE code IN ('POWER_XFMR');

-- 300  MV layer -- busbars, switchgear, conductors, switching/protection and
-- MV-side metering. One layer: within it direction is governed by the
-- connection's from/to (the tier guard only separates layers, it does not order
-- peers inside one).
UPDATE metadata.feature_classes SET trace_tier = 300
 WHERE code IN (
    'MV_BUSBAR', 'MV_SWITCHGEAR_PANEL', 'MV_CONDUCTOR',
    'MV_SWITCH', 'MV_ISOLATOR', 'MV_RECLOSER', 'MV_BREAKER', 'MV_FUSE',
    'MV_EARTH_SWITCH', 'MV_LV_SWITCH',
    'MV_CT', 'MV_VT', 'MV_METER', 'MV_METERING_POINT'
 );

-- 400  MV->LV boundary -- the distribution transformer (and station-service
-- auxiliary transformer) that steps MV down to the LV network. The far end of an
-- MV feeder and the head of the LV network below it.
UPDATE metadata.feature_classes SET trace_tier = 400
 WHERE code IN ('DIST_XFMR', 'AUX_TRANSFORMER');

-- 500  LV layer -- busbars, feeder pillars, conductors and LV switching/fusing
-- below a distribution transformer.
UPDATE metadata.feature_classes SET trace_tier = 500
 WHERE code IN (
    'LV_BUSBAR', 'LV_PILLAR', 'LV_CONDUCTOR', 'LV_FUSE',
    'LV_SWITCH', 'SERVICE_CUTOUT'
 );

-- 600  service / metering -- where power is delivered out of the network to the
-- customer. The bottom of the hierarchy (an endpoint for an upstream walk).
UPDATE metadata.feature_classes SET trace_tier = 600
 WHERE code IN ('SERVICE_POINT', 'METER');

COMMIT;
