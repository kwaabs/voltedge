-- seed/mv_bess_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives under
-- /seed on purpose, same reasoning as the other seed files. Assembly-backlog
-- Tier-2 #4 (part 1 of 2) -- the MV_BESS feature class. Part 2 is the assembly
-- kit seed/pad_mount_bess.sql; the storage-dispatch analysis integration lives in
-- api/internal/powerflow (resolveStorage + storage.go).
--
-- MV_BESS -- a grid battery-storage (BESS) unit: a bidirectional inverter-coupled
-- store that DISCHARGES (injects real power, like a generator) or CHARGES (draws
-- real power, an extra load) on command. Shaped like GENERATOR (a leaf injector:
-- one terminal T1, network_role 'junction', hanging off its point of common
-- coupling), NOT like the shunt cap bank -- a BESS moves real power, it is not an
-- earth-referenced VAr source. It is the concrete home for the Tier B "a BESS
-- discharging" case generator_example.sql called out, and the reason it is a
-- SEPARATE class from GENERATOR is the direction: a generator only ever injects
-- (resolveGeneration drops a non-positive value), whereas a battery must also
-- absorb, so it carries its OWN SIGNED dispatch field (storage_dispatch_kw) that
-- the engine nets on both real and reactive power (resolveStorage). A node is a
-- generator or a battery by which kW field it carries (rated_kw/dispatch_kw vs
-- rated_power_kw/storage_dispatch_kw), exactly the demand-vs-generation
-- convention, so the two never collide on the same feeder.
--
-- Fields (all electrical NULLABLE except the PCS rating + condition, the same
-- graceful-degradation contract GENERATOR uses -- an un-dispatched battery is an
-- inert junction, never an un-runnable study):
--   rated_power_kw       numeric -- PCS nameplate charge/discharge power (kW). The
--                        magnitude bound the storage-dispatch study sweeps over;
--                        NOT itself an injection. Required (a battery with no
--                        rating cannot be dispatched).
--   storage_dispatch_kw  numeric -- SIGNED live set-point (kW): + discharging
--                        (injecting), − charging (absorbing), 0 / absent idle.
--                        Authoritative when present, even at 0. This is what the
--                        engine nets; the dispatch study varies it.
--   energy_capacity_kwh  numeric -- usable energy size (kWh). Informational for a
--                        single power-flow snapshot; sizes the store.
--   state_of_charge_pct  numeric -- present charge (0..100). The dispatch study
--                        uses it only to gate the feasible window (an empty
--                        battery can only charge, a full one only discharge).
--   power_factor         numeric -- operating pf; Q follows P's sign at this pf,
--                        unset = unity (Q = 0), same rule the load/generator use.
--   condition_rating     text    -- maintenance state, as every field asset carries.
--
-- Subtypes:
--   1 standalone -- a stand-alone grid battery (front-of-meter storage).
--   2 hybrid     -- co-located with generation (a PV+storage hybrid inverter);
--                  still one dispatchable store to the power flow.
--
-- Depends on: seed/connectivity_example.sql (MV_CONDUCTOR, DIST_XFMR),
-- seed/lv_conductors_example.sql (LV_CONDUCTOR), and for the full footprint
-- seed/operational_state_example.sql, seed/snapping_rules_example.sql,
-- seed/ansi_stencils.sql (the ansi-battery glyph), seed/cim_mapping_example.sql
-- (cim_profiles), seed/symbology_icons.sql (the square map icon). Every JOIN
-- inserts nothing if a dependency is absent, so partial application is graceful.
--
-- Apply, then compile:
--   task seed-mv-bess
--   task compile CODE=MV_BESS
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL has
-- been reviewed statically -- a live psql apply + compile + a storage-dispatch
-- run are still owed to call it verified.

INSERT INTO metadata.field_groups (code, name, sort_order) VALUES
    ('electrical', 'Electrical', 1),
    ('maintenance', 'Maintenance', 2)
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domains (code, name, domain_type) VALUES
    ('CONDITION_RATING', 'Condition rating', 'coded')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domain_values (domain_id, code, label, sort_order)
SELECT d.id, v.code, v.label, v.sort_order
FROM metadata.domains d
JOIN (VALUES
    ('CONDITION_RATING', 'GOOD', 'Good', 1),
    ('CONDITION_RATING', 'FAIR', 'Fair', 2),
    ('CONDITION_RATING', 'POOR', 'Poor', 3)
) AS v(domain_code, code, label, sort_order) ON v.domain_code = d.code
ON CONFLICT (domain_id, code) DO NOTHING;

-- ===========================================================================
-- Class + subtypes + fields + terminal.
-- ===========================================================================
INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status, is_diagram_container, is_diagram_placeable, geometry_optional)
VALUES ('MV_BESS', 'MV Battery Storage', 'point', 4326, 'junction', 'draft', false, true, true)
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, v.code, v.name
FROM metadata.feature_classes fc
JOIN (VALUES (1, 'standalone'), (2, 'hybrid')) AS v(code, name) ON true
WHERE fc.code = 'MV_BESS'
ON CONFLICT (feature_class_id, code) DO NOTHING;

INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, v.nullable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('rated_power_kw',      'numeric', NULL,               'electrical',  false),
    ('storage_dispatch_kw', 'numeric', NULL,               'electrical',  true),
    ('energy_capacity_kwh', 'numeric', NULL,               'electrical',  true),
    ('state_of_charge_pct', 'numeric', NULL,               'electrical',  true),
    ('power_factor',        'numeric', NULL,               'electrical',  true),
    ('condition_rating',    'text',    'CONDITION_RATING', 'maintenance', false)
) AS v(name, data_type, domain_code, group_code, nullable) ON true
LEFT JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code = 'MV_BESS'
ON CONFLICT (feature_class_id, name) DO NOTHING;

-- Required fields (both subtypes): the PCS rating + a condition. The dispatch,
-- energy, SoC and pf are all optional -- an un-surveyed battery still places.
INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, true
FROM metadata.subtypes s
JOIN metadata.fields f ON f.feature_class_id = s.feature_class_id
WHERE s.feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'MV_BESS')
  AND f.name IN ('rated_power_kw', 'condition_rating')
ON CONFLICT (subtype_id, field_id) DO NOTHING;

-- Single terminal T1 (a battery is a network leaf at its point of common
-- coupling, never inline on a run) -- mirrors GENERATOR.T1 / SERVICE_POINT.T1.
INSERT INTO metadata.terminals (feature_class_id, subtype_id, code, role, phase_capable)
SELECT fc.id, NULL, 'T1', 'primary', true
FROM metadata.feature_classes fc
WHERE fc.code = 'MV_BESS'
ON CONFLICT (feature_class_id, subtype_id, code) DO NOTHING;

-- ===========================================================================
-- network_rules -- the three points of common coupling, identical to GENERATOR:
-- LV conductor, transformer LV bus, or MV conductor, all -> MV_BESS T1. The kit
-- adds an MV_SWITCH T2 -> MV_BESS T1 leg so the pad-mount unit sits behind its
-- own switch.
-- ===========================================================================

-- 1. MV_CONDUCTOR -> MV_BESS T1 -- the pad-mount / MV-connected case (the kit).
INSERT INTO metadata.network_rules (name, from_feature_class_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'MV_CONDUCTOR -> MV_BESS T1', mv.id, b.id, t1.id, NULL, true
FROM metadata.feature_classes mv
JOIN metadata.feature_classes b ON b.code = 'MV_BESS'
JOIN metadata.terminals t1 ON t1.feature_class_id = b.id AND t1.code = 'T1'
WHERE mv.code = 'MV_CONDUCTOR'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = mv.id AND to_feature_class_id = b.id AND to_terminal_id = t1.id
  );

-- 2. DIST_XFMR T2 -> MV_BESS T1 -- storage paralleled at a distribution
--    transformer's LV bus.
INSERT INTO metadata.network_rules (name, from_feature_class_id, from_terminal_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'DIST_XFMR T2 -> MV_BESS T1', xf.id, t2.id, b.id, t1.id, NULL, true
FROM metadata.feature_classes xf
JOIN metadata.feature_classes b ON b.code = 'MV_BESS'
JOIN metadata.terminals t2 ON t2.feature_class_id = xf.id AND t2.code = 'T2'
JOIN metadata.terminals t1 ON t1.feature_class_id = b.id AND t1.code = 'T1'
WHERE xf.code = 'DIST_XFMR'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = xf.id AND from_terminal_id = t2.id AND to_feature_class_id = b.id AND to_terminal_id = t1.id
  );

-- 3. LV_CONDUCTOR -> MV_BESS T1 -- smaller LV-connected behind-the-meter storage.
INSERT INTO metadata.network_rules (name, from_feature_class_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'LV_CONDUCTOR -> MV_BESS T1', lv.id, b.id, t1.id, NULL, true
FROM metadata.feature_classes lv
JOIN metadata.feature_classes b ON b.code = 'MV_BESS'
JOIN metadata.terminals t1 ON t1.feature_class_id = b.id AND t1.code = 'T1'
WHERE lv.code = 'LV_CONDUCTOR'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = lv.id AND to_feature_class_id = b.id AND to_terminal_id = t1.id
  );

-- ===========================================================================
-- Full footprint -- mirrors the MV_CAP_BANK seed for the single class.
-- ===========================================================================

-- 1. Circuit membership: MV network stamp + circuit_id field.
UPDATE metadata.feature_classes SET network = 'MV' WHERE code = 'MV_BESS';

INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, 'circuit_id', 'uuid', NULL, fg.id, true
FROM metadata.feature_classes fc
JOIN metadata.field_groups fg ON fg.code = 'electrical'
WHERE fc.code = 'MV_BESS'
ON CONFLICT (feature_class_id, name) DO NOTHING;

-- 2. operational_state lifecycle field.
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, 'operational_state', 'text', d.id, fg.id, true
FROM metadata.feature_classes fc
JOIN metadata.domains d       ON d.code  = 'OPERATIONAL_STATE'
JOIN metadata.field_groups fg ON fg.code = 'operational'
WHERE fc.code = 'MV_BESS'
ON CONFLICT (feature_class_id, name) DO NOTHING;

-- 3. Snapping: MV_CONDUCTOR -> MV_BESS (point_feature, either side).
INSERT INTO metadata.snapping_rules (from_feature_class_id, to_feature_class_id, to_terminal_id, target_type_value_id, tolerance_m, priority, is_required)
SELECT frm.id, tgt.id, NULL, tt.id, 0.5, 1, false
FROM metadata.feature_classes frm
CROSS JOIN metadata.feature_classes tgt
JOIN metadata.domain_values tt ON tt.code = 'point_feature'
  AND tt.domain_id = (SELECT id FROM metadata.domains WHERE code = 'SNAP_TARGET_TYPE')
WHERE frm.code = 'MV_CONDUCTOR' AND tgt.code = 'MV_BESS'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.snapping_rules sr
    WHERE sr.from_feature_class_id = frm.id AND sr.to_feature_class_id = tgt.id
      AND sr.to_terminal_id IS NULL
      AND sr.target_type_value_id = tt.id
  );

-- 4. Map symbology -- solid square, storage green.
INSERT INTO metadata.symbology_rules (feature_class_id, render_context, style_def)
SELECT fc.id, 'map', '{"icon_ref": "square", "color": "#16a34a"}'::jsonb
FROM metadata.feature_classes fc
WHERE fc.code = 'MV_BESS'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules
    WHERE feature_class_id = fc.id AND subtype_id IS NULL AND domain_value_id IS NULL AND render_context = 'map'
  );

-- 5. Diagram symbology -- the ansi-battery glyph (seed/ansi_stencils.sql).
INSERT INTO metadata.symbology_rules (feature_class_id, render_context, style_def)
SELECT fc.id, 'diagram', '{"icon_ref": "ansi-battery", "color": "#16a34a"}'::jsonb
FROM metadata.feature_classes fc
WHERE fc.code = 'MV_BESS'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules
    WHERE feature_class_id = fc.id AND subtype_id IS NULL AND domain_value_id IS NULL AND render_context = 'diagram'
  );

-- 6. CIM -- BatteryUnit under a PowerElectronicsConnection (the CIM home for an
--    inverter-coupled grid battery), conducting_junction; rated_power_kw maps to
--    PowerElectronicsConnection.ratedS (VA, so scale kW -> VA by 1e3, a nominal
--    unity-pf apparent-power stand-in).
INSERT INTO metadata.cim_class_mappings (feature_class_id, subtype_id, cim_class, cim_kind)
SELECT fc.id, NULL, 'BatteryUnit', 'conducting_junction'
FROM metadata.feature_classes fc
WHERE fc.code = 'MV_BESS'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.cim_class_mappings m
    WHERE m.feature_class_id = fc.id AND m.subtype_id IS NULL
  );

INSERT INTO metadata.cim_attribute_mappings (field_id, cim_attribute, transform, scale_factor, unit)
SELECT f.id, 'PowerElectronicsConnection.ratedS', 'scale', 1000, 'VA'
FROM metadata.fields f
JOIN metadata.feature_classes fc ON fc.id = f.feature_class_id
WHERE fc.code = 'MV_BESS' AND f.name = 'rated_power_kw'
ON CONFLICT (field_id, cim_attribute) DO NOTHING;
