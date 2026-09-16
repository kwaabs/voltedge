-- seed/packaged_substation_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives under
-- /seed on purpose, same reasoning as the other seed files. Phase 56, Slice 1
-- (catalog). Models a PACKAGED (movable) SUBSTATION as an ASSEMBLY: a factory-
-- built enclosure that bundles, pre-wired, an MV switch + a distribution
-- transformer + an LV board, placed and moved as ONE physical unit.
--
-- Shaped exactly like the RMU (seed/rmu_example.sql): network_role 'container'
-- (it OWNS its internal kit, like MV_SWITCHBOARD owns its panels), a real map
-- footprint so geometry_type 'point' + srid (not 'none'), is_diagram_container
-- = true. It carries no topology itself -- the contained pieces do. UNLIKE
-- SUBSTATION (a drawn polygon site boundary with network_role 'none') a packaged
-- substation moves as one point-placed live object, which is why it is a NEW
-- class shaped like RMU rather than a SUBSTATION subtype.
--
-- Permanent vs temporary is the SUBTYPE axis (decision 2026-09-14):
--   * permanent -- a fixed compact/unit substation.
--   * mobile    -- a trailer/skid-mounted relocatable (temporary) unit.
-- Lifecycle (installed / isolated / retired) stays on the shared
-- operational_state field (seed/operational_state_example.sql), which applies to
-- every physical asset class including this one.
--
-- The internal kit is a series CHAIN (MV switch -> transformer -> LV board), NOT
-- the RMU's star, so its fan-out recipe lives in the chain-capable template
-- tables (migration 0092) seeded by seed/packaged_substation_templates.sql
-- (Slice 2). This catalog only adds the enclosure class, the containment
-- classes, and the two internal network legs the chain wires along -- everything
-- the MV_SWITCH / DIST_XFMR / LV_BUSBAR components need already ships with those
-- classes.
--
-- Internal chain the fan-out ESTABLISHES (this catalog PERMITS it):
--   (external MV cable) -> MV_SWITCH.T1     (MV_SWITCH's own rules already permit)
--   MV_SWITCH.T2  <-> DIST_XFMR.T1          (leg 1 -- this seed adds it)
--   DIST_XFMR.T2  <-> LV_BUSBAR.T1          (leg 2 -- this seed adds it)
--   LV_BUSBAR.T2  -> (external LV feeders)  (LV_BUSBAR's own rules already permit)
-- so a DOWNSTREAM trace can walk cable -> switch -> transformer -> LV board -> LV
-- feeders, and an OPEN MV switch halts that walk at the switch.
--
-- Depends on seed/mv_lv_switch_example.sql (MV_SWITCH -- T1 cable / T2 bus,
-- DEVICE_STATE + CONDITION_RATING, OPEN trace barrier), seed/connectivity_example
-- .sql (DIST_XFMR -- T1 primary / T2 secondary / T3 ground) and
-- seed/lv_metering_example.sql (LV_BUSBAR -- T1 incoming / T2 bar). Also expects
-- the VOLTAGE_CLASS + CONDITION_RATING domains (widely seeded) to exist.
--
-- Apply with psql after the dependencies above and migration 0092, then compile:
--   task compile CODE=PACKAGED_SUBSTATION
-- (the internal MV_SWITCH / DIST_XFMR / LV_BUSBAR are compiled by their own seeds.)

-- ---------------------------------------------------------------------------
-- Shared lookups (idempotent re-declaration -- see header).
-- ---------------------------------------------------------------------------
INSERT INTO metadata.field_groups (code, name, sort_order) VALUES
    ('electrical', 'Electrical', 1),
    ('maintenance', 'Maintenance', 2)
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domains (code, name, domain_type) VALUES
    ('PACKAGED_SUB_CONFIG', 'Packaged substation configuration', 'coded')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domain_values (domain_id, code, label, sort_order)
SELECT d.id, v.code, v.label, v.sort_order
FROM metadata.domains d
JOIN (VALUES
    -- Packaged-substation configurations. The value selects the fan-out template
    -- (Slice 2, metadata.packaged_substation_templates). COMPACT is the classic
    -- secondary substation: MV switch + one distribution transformer + LV board.
    ('PACKAGED_SUB_CONFIG', 'COMPACT', 'MV switch + transformer + LV board', 1)
) AS v(domain_code, code, label, sort_order) ON v.domain_code = d.code
ON CONFLICT (domain_id, code) DO NOTHING;

-- ===========================================================================
-- PACKAGED_SUBSTATION -- the map-placed movable enclosure. Container of its
-- internal kit (like RMU / MV_SWITCHBOARD), with a real footprint, so 'point'
-- + srid rather than 'none'.
-- ===========================================================================
INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status, is_diagram_container, is_diagram_placeable, network)
VALUES ('PACKAGED_SUBSTATION', 'Packaged Substation', 'point', 4326, 'container', 'draft', true, false, 'MV')
ON CONFLICT (code) DO NOTHING;

-- Subtypes carry the permanent-vs-temporary axis (decision 2026-09-14).
INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, v.code, v.name
FROM metadata.feature_classes fc
JOIN (VALUES
    (1, 'permanent'),
    (2, 'mobile')
) AS v(code, name) ON true
WHERE fc.code = 'PACKAGED_SUBSTATION'
ON CONFLICT (feature_class_id, code) DO NOTHING;

-- package_config -- the assembly configuration (PACKAGED_SUB_CONFIG domain). Its
-- coded value (COMPACT, ...) is what the Slice 2 fan-out reads to pick the chain
-- template, the same way an RMU's rmu_config picks its way template. Stored on
-- the unit so it records how it was built even after placement.
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, 'package_config', 'text', d.id, fg.id, false
FROM metadata.feature_classes fc
JOIN metadata.domains d ON d.code = 'PACKAGED_SUB_CONFIG'
JOIN metadata.field_groups fg ON fg.code = 'electrical'
WHERE fc.code = 'PACKAGED_SUBSTATION'
ON CONFLICT (feature_class_id, name) DO NOTHING;

-- Descriptive nameplate fields (nullable -- the crew fills them in after
-- placement). primary/secondary voltage are VOLTAGE_CLASS; condition_rating is
-- the shared CONDITION_RATING scale.
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, 'text', d.id, fg.id, true
FROM metadata.feature_classes fc
JOIN (VALUES
    ('primary_voltage',   'VOLTAGE_CLASS',    'electrical'),
    ('secondary_voltage', 'VOLTAGE_CLASS',    'electrical'),
    ('condition_rating',  'CONDITION_RATING', 'maintenance')
) AS v(name, domain_code, group_code) ON true
JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code = 'PACKAGED_SUBSTATION'
ON CONFLICT (feature_class_id, name) DO NOTHING;

-- package_config is required on every subtype -- the fan-out cannot build a unit
-- without it. The descriptive fields stay optional.
INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, true
FROM metadata.subtypes s
JOIN metadata.fields f ON f.feature_class_id = s.feature_class_id
WHERE s.feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'PACKAGED_SUBSTATION')
  AND f.name = 'package_config'
ON CONFLICT (subtype_id, field_id) DO NOTHING;

-- ---------------------------------------------------------------------------
-- Containment relationship classes -- the enclosure owns its MV switch, its
-- transformer and its LV board, the same one_to_many containment RMU uses for
-- its ways.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.relationship_classes (name, origin_feature_class_id, destination_feature_class_id, kind, cardinality)
SELECT 'PACKAGED_SUBSTATION contains ' || child.code, pkg.id, child.id, 'containment', 'one_to_many'
FROM metadata.feature_classes pkg
JOIN metadata.feature_classes child ON child.code IN ('MV_SWITCH', 'DIST_XFMR', 'LV_BUSBAR')
WHERE pkg.code = 'PACKAGED_SUBSTATION'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.relationship_classes rc
    WHERE rc.origin_feature_class_id = pkg.id AND rc.destination_feature_class_id = child.id AND rc.kind = 'containment'
  );

-- ---------------------------------------------------------------------------
-- network_rules -- the TWO internal legs this seed adds so the chain can wire:
--   leg 1: MV_SWITCH.T2 (bus/internal side) -> DIST_XFMR.T1 (HV/primary)
--   leg 2: DIST_XFMR.T2 (LV/secondary)      -> LV_BUSBAR.T1 (incoming)
-- Everything else the components need already ships with their own classes (the
-- external MV_CONDUCTOR -> MV_SWITCH.T1 leg, the LV_BUSBAR.T2 -> LV feeder legs,
-- the OPEN trace barrier on MV_SWITCH). phase_rule NULL on both -- an internal
-- factory leg is authorized structurally, not by matching voltage-class fields,
-- exactly as the RMU internal bus leg is (seed/rmu_example.sql). Connection
-- establishment matches a rule in either direction, so one rule per leg suffices.
-- ---------------------------------------------------------------------------

-- leg 1: MV switch internal side into the transformer HV primary.
INSERT INTO metadata.network_rules (name, from_feature_class_id, from_terminal_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'MV_SWITCH T2 -> DIST_XFMR T1', sw.id, swt2.id, xf.id, xft1.id, NULL, true
FROM metadata.feature_classes sw
JOIN metadata.feature_classes xf ON xf.code = 'DIST_XFMR'
JOIN metadata.terminals swt2 ON swt2.feature_class_id = sw.id AND swt2.code = 'T2'
JOIN metadata.terminals xft1 ON xft1.feature_class_id = xf.id AND xft1.code = 'T1'
WHERE sw.code = 'MV_SWITCH'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules nr
    WHERE nr.from_feature_class_id = sw.id AND nr.from_terminal_id = swt2.id
      AND nr.to_feature_class_id = xf.id AND nr.to_terminal_id = xft1.id
  );

-- leg 2: transformer LV secondary into the LV board incoming terminal.
INSERT INTO metadata.network_rules (name, from_feature_class_id, from_terminal_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'DIST_XFMR T2 -> LV_BUSBAR T1', xf.id, xft2.id, b.id, bt1.id, NULL, true
FROM metadata.feature_classes xf
JOIN metadata.feature_classes b ON b.code = 'LV_BUSBAR'
JOIN metadata.terminals xft2 ON xft2.feature_class_id = xf.id AND xft2.code = 'T2'
JOIN metadata.terminals bt1 ON bt1.feature_class_id = b.id AND bt1.code = 'T1'
WHERE xf.code = 'DIST_XFMR'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules nr
    WHERE nr.from_feature_class_id = xf.id AND nr.from_terminal_id = xft2.id
      AND nr.to_feature_class_id = b.id AND nr.to_terminal_id = bt1.id
  );

-- ---------------------------------------------------------------------------
-- CIM export mapping -- a packaged substation IS a self-contained mini-
-- substation, so it maps to CIM Substation (cim_kind 'container'), exactly like
-- the drawn SUBSTATION. Its contained MV switch / transformer / LV board then
-- reference it via Equipment.EquipmentContainer automatically (the CIM
-- containment loader reads the containment relationships this seed declared).
-- Class-default row (subtype_id NULL) via NOT EXISTS -- ON CONFLICT never fires
-- for NULL subtype_ids (UNIQUE treats them as DISTINCT).
-- ---------------------------------------------------------------------------
INSERT INTO metadata.cim_class_mappings (feature_class_id, subtype_id, cim_class, cim_kind)
SELECT fc.id, NULL, 'Substation', 'container'
FROM metadata.feature_classes fc
WHERE fc.code = 'PACKAGED_SUBSTATION'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.cim_class_mappings m
    WHERE m.feature_class_id = fc.id AND m.subtype_id IS NULL
  );
