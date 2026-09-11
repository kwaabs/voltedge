-- seed/rmu_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives
-- under /seed on purpose, same reasoning as the other seed files.
-- Phase RMU, Slice 1 (catalog). Models a Ring Main Unit as an ASSEMBLY of
-- independently-switched ways sharing one internal bus -- faithful to the
-- real unit, and the shape that makes isolation traces come out right:
-- open a tee way and only its transformer isolates while the ring keeps
-- feeding through the bus. Three classes:
--
--   * RMU         -- the map-placed unit itself. network_role 'container'
--     (it OWNS its ways, exactly like MV_SWITCHBOARD owns its panels in
--     seed/mv_switchgear_panel_example.sql), is_diagram_container = true.
--     UNLIKE the switchboard it has a real map footprint (an outdoor,
--     pad-mounted unit), so geometry_type 'point' + srid, not 'none'. It
--     carries no topology itself -- the ways do.
--
--   * (internal bus) -- the common internal bus every way's bus-side attaches
--     to is a plain MV_BUSBAR instance (seed/mv_busbar_example.sql), NOT a
--     dedicated class. A busbar's real classifier is voltage, so the RMU just
--     contains an MV_BUSBAR (collapse 2026-09-06 -- there used to be a separate
--     attribute-less RMU_BUSBAR class; it was folded into MV_BUSBAR). The bus
--     side is wired terminal-less, so many ways converge on the one bus node.
--     This node is what makes the ring a ring: the fan-out (Slice 2) wires every
--     way's T2 to it. So this seed now depends on seed/mv_busbar_example.sql.
--
--   * (switched way) -- each switched way is a plain MV_SWITCH instance
--     (seed/mv_lv_switch_example.sql), NOT a dedicated class. A way IS a switch --
--     junction, device_state (CLOSED/OPEN) + condition_rating, T1 (cable /
--     external side) + T2 (bus / internal side), OPEN = trace barrier -- so the
--     RMU just contains MV_SWITCHes (collapse 2026-09-07 -- there used to be a
--     dedicated RMU_WAY class with ring/transformer subtypes; it was folded into
--     MV_SWITCH). The ring-vs-transformer distinction folds onto MV_SWITCH's
--     is_fused flag (a fused tee way protecting the distribution transformer =
--     is_fused true; a plain ring way = is_fused false), set by the fan-out from
--     the per-way template (seed/rmu_templates.sql). So this seed depends on
--     seed/mv_lv_switch_example.sql.
--
-- Topology this catalog PERMITS (Slice 2's fan-out is what ESTABLISHES it):
--   MV_CONDUCTOR <-> MV_SWITCH.T1  -- external cable to a way's cable side
--                                     (MV_SWITCH's own rules already permit this)
--   MV_SWITCH.T2 <-> MV_BUSBAR     -- a way's bus side to the internal bus
--                                     (the ONE internal leg this seed adds)
-- so a DOWNSTREAM trace can walk cable -> way -> bus -> other ways -> their
-- cables, and an OPEN way halts that walk at exactly one way.
--
-- Depends on seed/connectivity_example.sql (MV_CONDUCTOR),
-- seed/mv_lv_switch_example.sql (MV_SWITCH -- the switched ways),
-- seed/mv_busbar_example.sql (MV_BUSBAR -- the RMU's internal bus) and
-- seed/trace_configurations_example.sql (the barrier-respecting trace
-- configs). MV_SWITCH already carries the DEVICE_STATE / CONDITION_RATING
-- domains, the electrical / maintenance / protection field groups, its own
-- MV_CONDUCTOR <-> T1/T2 rules and an OPEN trace barrier, so this seed only adds
-- the RMU container, the containment classes, and the internal bus leg.
--
-- Apply with psql after the dependencies above, then compile the RMU class
-- (the internal bus is a plain MV_BUSBAR, compiled by seed-mv-busbar; the ways
-- are plain MV_SWITCH, compiled by seed-mv-lv-switch):
--   task compile CODE=RMU

-- ---------------------------------------------------------------------------
-- Shared lookups (idempotent re-declaration -- see header).
-- ---------------------------------------------------------------------------
INSERT INTO metadata.field_groups (code, name, sort_order) VALUES
    ('electrical', 'Electrical', 1),
    ('maintenance', 'Maintenance', 2)
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domains (code, name, domain_type) VALUES
    ('DEVICE_STATE', 'Device state', 'coded'),
    ('CONDITION_RATING', 'Condition rating', 'coded'),
    ('RMU_CONFIG', 'RMU configuration', 'coded')
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
    -- RMU configurations: N ring ways + M transformer (tee) ways. The value is
    -- what selects the fan-out template (Slice 2, metadata.rmu_templates).
    ('RMU_CONFIG', '2W_1T', '2 ring + 1 transformer', 1),
    ('RMU_CONFIG', '2W_2T', '2 ring + 2 transformer', 2)
) AS v(domain_code, code, label, sort_order) ON v.domain_code = d.code
ON CONFLICT (domain_id, code) DO NOTHING;

-- ===========================================================================
-- RMU -- the map-placed unit. Container of its ways (like MV_SWITCHBOARD),
-- but with a real footprint, so 'point' + srid rather than 'none'.
-- ===========================================================================
INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status, is_diagram_container, is_diagram_placeable, network)
VALUES ('RMU', 'Ring Main Unit', 'point', 4326, 'container', 'draft', true, false, 'MV')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, v.code, v.name
FROM metadata.feature_classes fc
JOIN (VALUES (1, 'standard')) AS v(code, name) ON true
WHERE fc.code = 'RMU'
ON CONFLICT (feature_class_id, code) DO NOTHING;

-- rmu_config -- the assembly configuration (RMU_CONFIG domain). Its coded value
-- (2W_1T, 2W_2T, ...) is what the Slice 2 fan-out reads to pick the way template,
-- the same way a panel's panel_role picks its device template. Stored on the RMU
-- so the unit records how many ways it was built with even after placement.
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, 'rmu_config', 'text', d.id, fg.id, false
FROM metadata.feature_classes fc
LEFT JOIN metadata.domains d ON d.code = 'RMU_CONFIG'
JOIN metadata.field_groups fg ON fg.code = 'electrical'
WHERE fc.code = 'RMU'
ON CONFLICT (feature_class_id, name) DO NOTHING;

INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, true
FROM metadata.subtypes s
JOIN metadata.fields f ON f.feature_class_id = s.feature_class_id
WHERE s.feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'RMU')
  AND f.name = 'rmu_config'
ON CONFLICT (subtype_id, field_id) DO NOTHING;

-- The RMU's internal bus is a plain MV_BUSBAR instance -- there is no dedicated
-- bus class. MV_BUSBAR is subtype-less with nullable voltage_class /
-- condition_rating (seed/mv_busbar_example.sql), so the Slice 2 fan-out creates
-- the internal bus with an empty input exactly as it did for the old bare
-- RMU_BUSBAR node. It must be seeded + compiled before this file.

-- The RMU's switched ways are plain MV_SWITCH instances -- there is no dedicated
-- way class. MV_SWITCH (seed/mv_lv_switch_example.sql) already IS the switch a
-- way needs: junction, device_state (CLOSED/OPEN) + condition_rating, T1 cable
-- side / T2 bus side, OPEN trace barrier, MV_CONDUCTOR <-> T1/T2 rules, and the
-- is_fused flag that carries the ring-vs-transformer distinction. It must be
-- seeded + compiled before this file. The fan-out (Slice 2) creates each way as
-- an MV_SWITCH and stamps is_fused from the per-way template. The only topology
-- this seed adds is the internal MV_SWITCH.T2 <-> MV_BUSBAR bus leg below.

-- ---------------------------------------------------------------------------
-- Containment relationship classes -- RMU owns its bus and its ways, the
-- same one_to_many containment MV_SWITCHBOARD uses for its panels.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.relationship_classes (name, origin_feature_class_id, destination_feature_class_id, kind, cardinality)
SELECT 'RMU contains MV_BUSBAR', rmu.id, bus.id, 'containment', 'one_to_many'
FROM metadata.feature_classes rmu, metadata.feature_classes bus
WHERE rmu.code = 'RMU' AND bus.code = 'MV_BUSBAR'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.relationship_classes
    WHERE origin_feature_class_id = rmu.id AND destination_feature_class_id = bus.id AND kind = 'containment'
  );

INSERT INTO metadata.relationship_classes (name, origin_feature_class_id, destination_feature_class_id, kind, cardinality)
SELECT 'RMU contains MV_SWITCH', rmu.id, way.id, 'containment', 'one_to_many'
FROM metadata.feature_classes rmu, metadata.feature_classes way
WHERE rmu.code = 'RMU' AND way.code = 'MV_SWITCH'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.relationship_classes
    WHERE origin_feature_class_id = rmu.id AND destination_feature_class_id = way.id AND kind = 'containment'
  );

-- ---------------------------------------------------------------------------
-- network_rules -- the ONE leg this seed adds: a way's bus side (T2) into the
-- common internal bus. Everything else a way needs already ships with
-- MV_SWITCH: the external MV_CONDUCTOR <-> T1/T2 legs (its own rules) and the
-- OPEN trace barrier. And because connection establishment matches a rule in
-- either direction (matchRuleEitherDirection in api/internal/connectivity/
-- rules.go), a single T2 -> bus rule authorizes the leg whichever end the
-- caller names first -- no mirrored bus -> T2 rule is needed. No phase_rule
-- text -- nothing evaluates it for MV here, same as every other MV switch seed.
-- ---------------------------------------------------------------------------

-- Internal: a way's bus side (T2) into the common bus node (a plain MV_BUSBAR,
-- wired terminal-less on the bus side so many ways converge on the one node).
-- This is what the Slice 2 fan-out's EstablishConnection(way.T2 -> bus) matches.
INSERT INTO metadata.network_rules (name, from_feature_class_id, from_terminal_id, to_feature_class_id, phase_rule, is_valid)
SELECT 'MV_SWITCH T2 -> MV_BUSBAR', way.id, t2.id, bus.id, NULL, true
FROM metadata.feature_classes bus, metadata.feature_classes way
JOIN metadata.terminals t2 ON t2.feature_class_id = way.id AND t2.code = 'T2'
WHERE bus.code = 'MV_BUSBAR' AND way.code = 'MV_SWITCH'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = way.id AND from_terminal_id = t2.id AND to_feature_class_id = bus.id
  );
