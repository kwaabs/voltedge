-- functional_role_backfill.sql
-- Stamp metadata.feature_classes.functional_role (migration 0091) onto today's
-- electrical classes -- the class->role assignments for the electrical-function
-- taxonomy (source/consumer/passthrough/transformer/controller/node/sensor/
-- ground). Kept OUT of the migration so the taxonomy can evolve without a new
-- migration; run after applying 0091:  task seed-functional-roles
--
-- Idempotent: it's a set of UPDATE ... WHERE code IN (...) statements, so a
-- re-run just re-sets the same values. TOLERANT of a deployment that lacks some
-- of these classes -- an UPDATE whose code matches nothing is a clean no-op, so
-- the generous lists below never error on an absent class.
--
-- Non-electrical classes (SUBSTATION, BAY, panels, RMU, poles, towers, cable
-- ducts/trenches/chambers, DC-auxiliary devices like STATION_BATTERY) are left
-- with functional_role = NULL: they have no function on the traced power
-- network. Requires only the metadata column (no gis.* table, no recompile).
--
-- SOURCE note: the GRID INFEED is still an INSTANCE, not a class -- a slack
-- DIST_XFMR (GRID-SRC / SUB-SOURCE) carrying source_r_ohm -- and supply-path
-- finds it topologically (api/internal/trace/supply.go). But GENERATOR (Tier A
-- embedded generation, seed/generator_example.sql) IS now a first-class 'source'
-- class: it injects power into the network (modelled as negative demand by
-- powerflow/resolveGeneration), which is exactly the source function. This is
-- the "future generation class" the reservation note used to point at.

BEGIN;

-- source -- injects power into the network (embedded / distributed generation:
-- diesel, solar PV, wind, hydro, battery, gas CHP). The engine treats it as a
-- fixed negative-demand injection at its node during a grid-connected sweep.
UPDATE metadata.feature_classes SET functional_role = 'source'
 WHERE code IN ('GENERATOR');

-- consumer -- loads / sinks: where power is delivered out of the network.
-- STREET_LIGHT (unmetered LV leaf, Phase 9 Slice 5) and EV_CHARGING_STATION (a leaf
-- load node modelled exactly like SERVICE_POINT, Phase 54) are both consumers: the
-- point power is delivered out of the network. They post-date the original list and
-- were missed; classifying them here makes supply-path treat them as LV leaves and
-- the trace menu scope them like any other dead-end delivery point.
UPDATE metadata.feature_classes SET functional_role = 'consumer'
 WHERE code IN ('SERVICE_POINT', 'METER', 'MV_METERING_POINT', 'MV_METER', 'STREET_LIGHT', 'EV_CHARGING_STATION');

-- passthrough -- carries flow with no state (conductors / cables).
UPDATE metadata.feature_classes SET functional_role = 'passthrough'
 WHERE code IN ('MV_CONDUCTOR', 'LV_CONDUCTOR');

-- transformer -- carries flow but changes voltage level (a voltage/circuit
-- boundary). The supply-path trace latches its nearest upstream one as the
-- serving transformer.
UPDATE metadata.feature_classes SET functional_role = 'transformer'
 WHERE code IN ('DIST_XFMR', 'POWER_XFMR');

-- controller -- has an open/closed STATE that gates flow. The only role whose
-- state can stop a trace; supply-path calls each one out as a switching device.
UPDATE metadata.feature_classes SET functional_role = 'controller'
 WHERE code IN (
    'LV_SWITCH', 'SERVICE_CUTOUT',
    'MV_SWITCH', 'MV_ISOLATOR', 'MV_RECLOSER', 'MV_BREAKER', 'MV_FUSE',
    'MV_SECTIONALIZER', 'MV_EARTH_SWITCH'
 );

-- node -- a many-way meeting point (busbars, and cable joints: a splice/tee that
-- joins two or more cables at a point, electrically a node like a busbar).
UPDATE metadata.feature_classes SET functional_role = 'node'
 WHERE code IN ('MV_BUSBAR', 'LV_BUSBAR', 'MV_CABLE_JOINT', 'LV_CABLE_JOINT');

-- sensor -- observes but never carries or gates through-flow, off-path
-- (instrument transformers, relays, and secondary-side control/indication IEDs).
UPDATE metadata.feature_classes SET functional_role = 'sensor'
 WHERE code IN ('MV_CT', 'MV_VT', 'PROTECTIVE_RELAY', 'CONTROL_IED', 'ANNUNCIATOR');

-- ground -- provides a return / reference path, not power delivery
-- (neutral earthing resistor, earthing transformer).
UPDATE metadata.feature_classes SET functional_role = 'ground'
 WHERE code IN ('NER', 'EARTHING_XFMR');

COMMIT;
