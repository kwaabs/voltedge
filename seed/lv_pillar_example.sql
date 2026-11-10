-- seed/lv_pillar_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives
-- under /seed on purpose, same reasoning as the other seed files.
--
-- Phase 9, Slice 7 (LV feeder pillar as an assembly). Brings the LV feeder
-- pillar up to the RMU pattern: a map-placed CONTAINER that OWNS an internal
-- LV busbar and a set of fused outgoing ways, so opening ONE way isolates ONE
-- feeder -- the per-way isolation the flat LV_LINK_BOX junction
-- (seed/lv_devices_example.sql) cannot express (its OPEN barrier is
-- feature-level, halting the whole box). This is the LV sibling of the RMU
-- (seed/rmu_example.sql: an RMU is a container of MV_SWITCH ways on one
-- MV_BUSBAR); here the pillar is a container of LV_FUSE ways on one LV_BUSBAR.
--
-- ADDITIVE, non-destructive: LV_LINK_BOX is left exactly as it is (still the
-- simple 4-terminal junction the PAD_MOUNT_URD kit and the existing LV rules
-- use). This file adds two NEW classes and wires only new legs.
--
-- Two classes:
--
--   * LV_PILLAR -- the map-placed unit. network_role 'container' (it OWNS its
--     bus and ways, carrying no topology itself), is_diagram_container = true.
--     Like the RMU it has a real footprint (a ground-mounted LV cabinet), so
--     geometry_type 'point' + srid, not 'none'. network 'LV'. No terminals.
--
--   * LV_FUSE -- one fused outgoing way. geometry_type 'none' (its only visual
--     representation is a diagram placement, exactly like PROTECTIVE_RELAY --
--     seed/protective_relay_example.sql -- a way lives INSIDE the pillar's
--     footprint, never as its own point on the map). network_role 'junction':
--     it IS a node in the connectivity graph (bus side T1, outgoing side T2),
--     with device_state (CLOSED/OPEN) as an OPEN trace barrier so opening a
--     single fuse isolates a single feeder. Carries a fuse_rating (new
--     FUSE_RATING domain) -- a fused way is a real, sized protective device.
--
-- The internal bus is a plain LV_BUSBAR instance (seed/lv_metering_example.sql:
-- T1 incoming, T2 the equipotential bar that fans out) -- NOT a dedicated
-- class, the same way the RMU reuses MV_BUSBAR. The fan-out (the generic
-- assembly engine, api/internal/assembly, migration 0093, driven by
-- seed/lv_pillar_templates.sql) creates the bus + N fuses, contains them in the
-- pillar, and wires each fuse's bus side (T1) to the bus's fan-out bar (T2).
--
-- Topology this catalog PERMITS (the fan-out is what ESTABLISHES it):
--   LV_CONDUCTOR      -> LV_BUSBAR.T1   -- the incoming LV main into the pillar
--   LV_BUSBAR.T2      -> LV_FUSE.T1     -- the internal bar into each fuse way
--   LV_FUSE.T2        -> LV_CONDUCTOR   -- a fused way out to its feeder
-- so a DOWNSTREAM trace can walk main -> bus -> fuse -> feeder, and an OPEN
-- fuse halts that walk at exactly one way.
--
-- Depends on seed/lv_metering_example.sql (LV_BUSBAR), seed/lv_conductors_example.sql
-- (LV_CONDUCTOR) and seed/trace_configurations_example.sql (the barrier-respecting
-- trace configs -- the barrier INSERT is a no-op if they are absent).
--
-- Apply with psql after the dependencies above, then compile. Besides the two
-- NEW classes, LV_BUSBAR is RE-compiled: this seed flips it to geometry_optional
-- (see the promotion block below) and that flag only takes effect on a recompile
-- (migration 0052):
--   task seed-lv-pillar
--   task compile CODE=LV_PILLAR
--   task compile CODE=LV_FUSE
--   task compile CODE=LV_BUSBAR
-- Then seed/lv_pillar_templates.sql (task seed-lv-pillar-templates) adds the
-- fan-out recipes. No compile step for the templates -- pure metadata rows.

-- ---------------------------------------------------------------------------
-- Shared lookups (idempotent re-declaration -- see the other seeds' headers).
-- ---------------------------------------------------------------------------
INSERT INTO metadata.field_groups (code, name, sort_order) VALUES
    ('electrical', 'Electrical', 1),
    ('maintenance', 'Maintenance', 2),
    ('protection', 'Protection', 3)
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domains (code, name, domain_type) VALUES
    ('DEVICE_STATE', 'Device state', 'coded'),
    ('CONDITION_RATING', 'Condition rating', 'coded'),
    ('FUSE_RATING', 'LV fuse rating', 'coded')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domain_values (domain_id, code, label, sort_order)
SELECT d.id, v.code, v.label, v.sort_order
FROM metadata.domains d
JOIN (VALUES
    ('DEVICE_STATE', 'CLOSED', 'Closed', 1),
    ('DEVICE_STATE', 'OPEN', 'Open', 2),
    ('CONDITION_RATING', 'GOOD', 'Good', 1),
    ('CONDITION_RATING', 'FAIR', 'Fair', 2),
    ('CONDITION_RATING', 'POOR', 'Poor', 3),
    -- Common BS88 LV pillar fuse ratings (amps). The value sizes the way's
    -- protection; the fan-out stamps a default the operator edits per way.
    ('FUSE_RATING', '100A', '100 A', 1),
    ('FUSE_RATING', '160A', '160 A', 2),
    ('FUSE_RATING', '200A', '200 A', 3),
    ('FUSE_RATING', '250A', '250 A', 4),
    ('FUSE_RATING', '315A', '315 A', 5),
    ('FUSE_RATING', '400A', '400 A', 6)
) AS v(domain_code, code, label, sort_order) ON v.domain_code = d.code
ON CONFLICT (domain_id, code) DO NOTHING;

-- ===========================================================================
-- LV_PILLAR -- the map-placed unit. Container of its bus + ways (like RMU),
-- with a real footprint, so 'point' + srid rather than 'none'.
-- is_diagram_container = true: the pillar hosts its own single-line schematic.
-- ===========================================================================
INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status, is_diagram_container, is_diagram_placeable, network)
VALUES ('LV_PILLAR', 'LV Feeder Pillar', 'point', 4326, 'container', 'draft', true, false, 'LV')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, v.code, v.name
FROM metadata.feature_classes fc
JOIN (VALUES (1, 'standard')) AS v(code, name) ON true
WHERE fc.code = 'LV_PILLAR'
ON CONFLICT (feature_class_id, code) DO NOTHING;

-- condition_rating -- the cabinet's asset condition. Nullable and NOT required
-- (no subtype_field_rule): a container needs no mandatory attribute, so the
-- fan-out creates the pillar cleanly. Unlike the RMU there is no config field:
-- the generic assembly engine takes the kit config as the POST /assemblies
-- Config parameter, never as an anchor attribute (api/internal/assembly:
-- CreateAssemblyInput.Config), the same way POLE / DIST_XFMR anchors carry none.
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, 'condition_rating', 'text', d.id, fg.id, true
FROM metadata.feature_classes fc
LEFT JOIN metadata.domains d ON d.code = 'CONDITION_RATING'
JOIN metadata.field_groups fg ON fg.code = 'maintenance'
WHERE fc.code = 'LV_PILLAR'
ON CONFLICT (feature_class_id, name) DO NOTHING;

-- ===========================================================================
-- LV_FUSE -- one fused outgoing way. geometry_type 'none' (diagram-only, like
-- PROTECTIVE_RELAY): a way has no independent map location, it lives inside the
-- pillar. network_role 'junction': a real node with a bus side (T1) and an
-- outgoing side (T2), device_state as an OPEN barrier for per-way isolation.
-- ===========================================================================
INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status, is_diagram_container, is_diagram_placeable)
VALUES ('LV_FUSE', 'LV Fuse Way', 'none', NULL, 'junction', 'draft', false, true)
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, v.code, v.name
FROM metadata.feature_classes fc
JOIN (VALUES (1, 'standard')) AS v(code, name) ON true
WHERE fc.code = 'LV_FUSE'
ON CONFLICT (feature_class_id, code) DO NOTHING;

INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, v.nullable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('device_state', 'text', 'DEVICE_STATE', 'electrical', false),
    ('condition_rating', 'text', 'CONDITION_RATING', 'maintenance', false),
    ('fuse_rating', 'text', 'FUSE_RATING', 'protection', false)
) AS v(name, data_type, domain_code, group_code, nullable) ON true
LEFT JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code = 'LV_FUSE'
ON CONFLICT (feature_class_id, name) DO NOTHING;

INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, true
FROM metadata.subtypes s
JOIN metadata.fields f ON f.feature_class_id = s.feature_class_id
WHERE s.feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'LV_FUSE')
  AND f.name IN ('device_state', 'condition_rating', 'fuse_rating')
ON CONFLICT (subtype_id, field_id) DO NOTHING;

-- T1 the bus side (primary), T2 the outgoing side (secondary) -- the same
-- line/load asymmetry SERVICE_CUTOUT uses. phase_capable so a way can be
-- single-phase-tapped off a 3-phase board.
INSERT INTO metadata.terminals (feature_class_id, subtype_id, code, role, phase_capable)
SELECT fc.id, NULL, v.code, v.role, v.phase_capable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('T1', 'primary', true),
    ('T2', 'secondary', true)
) AS v(code, role, phase_capable) ON true
WHERE fc.code = 'LV_FUSE'
ON CONFLICT (feature_class_id, subtype_id, code) DO NOTHING;

-- ---------------------------------------------------------------------------
-- Promote LV_BUSBAR to the RMU-style fan-out bus. Its class seed
-- (seed/lv_metering_example.sql) created it for the meter-chamber case only:
-- geometry_type 'point', geometry_optional DEFAULT false, no diagram_geometry_type.
-- Two flags must change for the pillar fan-out, both mirroring MV_BUSBAR
-- (seed/mv_busbar_example.sql), which the RMU already relies on:
--
--   1. geometry_optional = true. The generic assembly engine
--      (api/internal/assembly/fanout.go) creates every kit component WITHOUT map
--      geometry; a non-optional geometric class makes crud.CreateFeature reject
--      that ("geometry is required for feature class \"LV_BUSBAR\"",
--      api/internal/crud/validate.go), so the fan-out would fail creating the bus.
--   2. diagram_geometry_type = 'line'. The auto single-line
--      (seed/assembly_diagram_templates.sql "Feeder pillar" templates) binds the
--      bus slot with a 2-point path, and validatePlacementPath
--      (api/internal/diagram/sql.go) accepts a path ONLY for a class whose DIAGRAM
--      shape is 'line'. Without it the bus slot is silently skipped and the
--      single-line generates with no busbar.
--
-- Both are idempotent and BACKWARD-COMPATIBLE: geometry_optional only RELAXES
-- (existing map-placed busbars are unaffected), and a 'line' diagram class can
-- still be placed as a point (validatePlacementPath allows a nil path for any
-- class), so the existing meter-chamber LV_BUSBAR placements keep working.
--
-- geometry_optional needs a RECOMPILE (task compile CODE=LV_BUSBAR) to make the
-- geom column nullable and relax the create validator (migration 0052);
-- diagram_geometry_type is a pure runtime flag (migration 0053), no recompile.
-- ---------------------------------------------------------------------------
UPDATE metadata.feature_classes
SET geometry_optional = true
WHERE code = 'LV_BUSBAR' AND geometry_optional IS DISTINCT FROM true;

UPDATE metadata.feature_classes
SET diagram_geometry_type = 'line'
WHERE code = 'LV_BUSBAR' AND diagram_geometry_type IS DISTINCT FROM 'line';

-- Relax LV_PILLAR to geometry-optional so it can RIDE in a kit as a component, not
-- only anchor its own (a feeder pillar inside a larger packaged assembly). Same reason
-- as LV_BUSBAR above: the generic assembly engine (fanout.go) creates every kit
-- component WITHOUT map geometry, which crud.CreateFeature rejects for a non-optional
-- geometric class ("geometry is required for feature class \"LV_PILLAR\""). Idempotent
-- and BACKWARD-COMPATIBLE -- geometry_optional only RELAXES, so an LV_PILLAR placed as
-- its own map unit (with a point) is unaffected. Needs a RECOMPILE
-- (task compile CODE=LV_PILLAR) to make the geom column nullable + relax the validator.
UPDATE metadata.feature_classes
SET geometry_optional = true
WHERE code = 'LV_PILLAR' AND geometry_optional IS DISTINCT FROM true;

-- ---------------------------------------------------------------------------
-- Containment relationship classes -- the pillar owns its bus and its ways, the
-- same one_to_many containment the RMU uses for its bus and switches.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.relationship_classes (name, origin_feature_class_id, destination_feature_class_id, kind, cardinality)
SELECT 'LV_PILLAR contains LV_BUSBAR', p.id, bus.id, 'containment', 'one_to_many'
FROM metadata.feature_classes p, metadata.feature_classes bus
WHERE p.code = 'LV_PILLAR' AND bus.code = 'LV_BUSBAR'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.relationship_classes
    WHERE origin_feature_class_id = p.id AND destination_feature_class_id = bus.id AND kind = 'containment'
  );

INSERT INTO metadata.relationship_classes (name, origin_feature_class_id, destination_feature_class_id, kind, cardinality)
SELECT 'LV_PILLAR contains LV_FUSE', p.id, wy.id, 'containment', 'one_to_many'
FROM metadata.feature_classes p, metadata.feature_classes wy
WHERE p.code = 'LV_PILLAR' AND wy.code = 'LV_FUSE'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.relationship_classes
    WHERE origin_feature_class_id = p.id AND destination_feature_class_id = wy.id AND kind = 'containment'
  );

-- ---------------------------------------------------------------------------
-- network_rules -- the three legs a fused-way pillar needs. All phase_rule NULL
-- (device hops, not the phase-matched edge-to-edge splice LV_CONDUCTOR ->
-- LV_CONDUCTOR gates). Guarded with WHERE NOT EXISTS, as the other seeds are.
-- ---------------------------------------------------------------------------

-- The incoming LV main into the pillar's internal bus (LV_BUSBAR.T1). LV_BUSBAR
-- had no LV_CONDUCTOR leg before (its meter-chamber use fans T2 out to METERs),
-- so this is a new rule.
INSERT INTO metadata.network_rules (name, from_feature_class_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'LV_CONDUCTOR -> LV_BUSBAR T1', lv.id, bus.id, t1.id, NULL, true
FROM metadata.feature_classes lv CROSS JOIN metadata.feature_classes bus
JOIN metadata.terminals t1 ON t1.feature_class_id = bus.id AND t1.code = 'T1'
WHERE lv.code = 'LV_CONDUCTOR' AND bus.code = 'LV_BUSBAR'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = lv.id AND to_feature_class_id = bus.id AND to_terminal_id = t1.id
  );

-- Internal: the bus's fan-out bar (T2) into each fuse way's bus side (T1). This
-- is what the fan-out's EstablishConnection(bus.T2 -> fuse.T1) matches; many
-- ways converge on the one bar node.
INSERT INTO metadata.network_rules (name, from_feature_class_id, from_terminal_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'LV_BUSBAR T2 -> LV_FUSE T1', bus.id, t2.id, wy.id, t1.id, NULL, true
FROM metadata.feature_classes bus CROSS JOIN metadata.feature_classes wy
JOIN metadata.terminals t2 ON t2.feature_class_id = bus.id AND t2.code = 'T2'
JOIN metadata.terminals t1 ON t1.feature_class_id = wy.id AND t1.code = 'T1'
WHERE bus.code = 'LV_BUSBAR' AND wy.code = 'LV_FUSE'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = bus.id AND from_terminal_id = t2.id AND to_feature_class_id = wy.id AND to_terminal_id = t1.id
  );

-- A fused way's outgoing side (T2) out to its feeder conductor.
INSERT INTO metadata.network_rules (name, from_feature_class_id, from_terminal_id, to_feature_class_id, phase_rule, is_valid)
SELECT 'LV_FUSE T2 -> LV_CONDUCTOR', wy.id, t2.id, lv.id, NULL, true
FROM metadata.feature_classes wy CROSS JOIN metadata.feature_classes lv
JOIN metadata.terminals t2 ON t2.feature_class_id = wy.id AND t2.code = 'T2'
WHERE wy.code = 'LV_FUSE' AND lv.code = 'LV_CONDUCTOR'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = wy.id AND from_terminal_id = t2.id AND to_feature_class_id = lv.id
  );

-- ---------------------------------------------------------------------------
-- trace_barrier_rules -- halt an isolation trace at an OPEN fuse way, exactly
-- like the SERVICE_CUTOUT / LV_LINK_BOX barriers (seed/lv_devices_example.sql).
-- Attached only to the two barrier-respecting configs; a no-op if they are not
-- seeded yet. This is what gives the pillar its per-way isolation: an OPEN
-- LV_FUSE stops the trace at that one feeder, leaving the rest energised.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.trace_barrier_rules (trace_configuration_id, feature_class_id, field_id, halts_on_value_id)
SELECT tc.id, wy.id, f.id, dv.id
FROM metadata.feature_classes wy
JOIN metadata.fields f ON f.feature_class_id = wy.id AND f.name = 'device_state'
JOIN metadata.domain_values dv ON dv.domain_id = f.domain_id AND dv.code = 'OPEN'
JOIN metadata.trace_configurations tc ON tc.code IN ('DOWNSTREAM_TRACE', 'UPSTREAM_TRACE')
WHERE wy.code = 'LV_FUSE'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.trace_barrier_rules
    WHERE trace_configuration_id = tc.id AND feature_class_id = wy.id AND field_id = f.id AND halts_on_value_id = dv.id
  );
