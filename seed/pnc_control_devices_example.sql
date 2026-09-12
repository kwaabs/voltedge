-- seed/pnc_control_devices_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives under
-- /seed on purpose, same reasoning as the other seed files. Registers the
-- secondary / DC-auxiliary & control layer of a substation P&C scheme as real
-- feature classes -- the layer that was missing while primary plant, protection
-- (PROTECTIVE_RELAY + curves), instrument transformers (MV_CT/MV_VT) and
-- metering were all already modelled. Four new classes:
--
--   * STATION_BATTERY  -- the substation DC battery bank (trip/close/control
--                          supply). The thing that actually operates a breaker's
--                          trip coil when a relay picks up.
--   * BATTERY_CHARGER  -- rectifier/charger feeding the DC bus + float-charging
--                          the battery.
--   * CONTROL_IED       -- the control/automation intelligent electronic device:
--                          bay controller, RTU, SCADA gateway, station HMI or
--                          protocol converter (ied_role coded field, not one
--                          class each -- these are the same asset in different
--                          roles, like SUBSTATION_PANEL's panel_type).
--   * ANNUNCIATOR       -- alarm annunciator / facia window panel.
--
-- MODELLING -- all four mirror PROTECTIVE_RELAY (seed/protective_relay_example.sql)
-- deliberately:
--   * geometry_type = 'none' -- these are never a point/line on the map; their
--     only visual form is a diagram (panel schematic) placement. The compiler
--     skips the geom column (ddl.go buildCreateTable) and CRUD rejects any
--     create carrying geometry (validate.go).
--   * network_role  = 'none' -- NOT in the electrical connectivity graph. A DC
--     battery / charger / IED / annunciator carries no AC power path and no
--     terminals; their link to the primary plant is the schematic control/trip
--     wiring (annotation), not runtime.feature_connectivity. Same stance the
--     relay took -- protection/control wiring is a separate, larger open item
--     (docs/open-items.md "Dual line semantics").
--   * is_diagram_placeable = true -- so they can be stamped on a panel schematic.
--   * one 'standard' subtype each -- the functional variant is carried by a coded
--     field (battery_technology / charger_type / ied_role), not by subtypes,
--     exactly as the relay's ANSI device_number does.
--
-- FIELDS are all nullable at the DB level (additive-compile rule: the compiler
-- emits NOT NULL for nullable=false, which would fail on a populated table --
-- see [[compiler-additive-nullable-required]]). Requiredness is enforced at the
-- app layer via subtype_field_rules: the identifying coded field is is_required,
-- the rest are optional. New classes have zero rows so this is safe either way;
-- the pattern is kept for consistency and so later re-seeds stay additive.
--
-- CONTAINMENT: SUBSTATION_PANEL contains all four (kind 'containment',
-- one_to_many) -- the control-building enclosure is their physical home, the
-- same way it already contains PROTECTIVE_RELAY + METER
-- (seed/substation_panel_containment_example.sql). Seeded here so this file is
-- self-contained and need not re-run the panel seed. Rows are legal pairings
-- only -- no instances (those live in runtime.feature_relationships via the API).
--
-- SYMBOLOGY: each gets a render_context = 'diagram' rule. A geometry 'none' class
-- has NO map rule to fall back to (diagram-canvas.tsx tries 'diagram' then 'map'),
-- so without this it would render with no symbol at all. Distinct colours from
-- the existing palette; icon_ref 'circle' (a dedicated battery/RTU/annunciator
-- glyph is a nice-to-have follow-up -- see one_line_stencils_extra.sql for the
-- authoring convention).
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL was
-- reviewed statically -- a live psql apply + `task compile` for each of the four
-- classes + a create spot-check are still owed to call it verified.
--
-- Depends on: the core metadata migrations, plus seed/substation_panel_example.sql
-- (SUBSTATION_PANEL) for containment -- absent, the containment JOIN just skips.
-- Apply, then compile each new class so its gis table lands:
--   task seed-pnc-control-devices
--   task compile CODE=STATION_BATTERY
--   task compile CODE=BATTERY_CHARGER
--   task compile CODE=CONTROL_IED
--   task compile CODE=ANNUNCIATOR

-- ===========================================================================
-- Field groups + domains
-- ===========================================================================

INSERT INTO metadata.field_groups (code, name, sort_order) VALUES
    ('dc_auxiliary', 'DC Auxiliary', 40),
    ('control',      'Control & Automation', 41)
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domains (code, name, domain_type) VALUES
    ('BATTERY_TECHNOLOGY', 'Battery technology',      'coded'),
    ('CHARGER_TYPE',       'Battery charger type',    'coded'),
    ('IED_ROLE',           'Control IED role',        'coded'),
    ('COMMS_PROTOCOL',     'Communications protocol', 'coded')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domain_values (domain_id, code, label, sort_order)
SELECT d.id, v.code, v.label, v.sort_order
FROM metadata.domains d
JOIN (VALUES
    ('BATTERY_TECHNOLOGY', 'lead_acid_vented', 'Lead-acid (vented)',       1),
    ('BATTERY_TECHNOLOGY', 'lead_acid_vrla',   'Lead-acid (VRLA)',         2),
    ('BATTERY_TECHNOLOGY', 'nicd',             'Nickel-cadmium',           3),
    ('BATTERY_TECHNOLOGY', 'li_ion',           'Lithium-ion',              4),
    ('CHARGER_TYPE',       'smps',             'Switch-mode (SMPS)',       1),
    ('CHARGER_TYPE',       'thyristor',        'Thyristor-controlled',     2),
    ('CHARGER_TYPE',       'ferroresonant',    'Ferroresonant',            3),
    ('IED_ROLE',           'bay_controller',   'Bay controller',           1),
    ('IED_ROLE',           'rtu',              'RTU',                      2),
    ('IED_ROLE',           'scada_gateway',    'SCADA gateway',            3),
    ('IED_ROLE',           'station_hmi',      'Station HMI',              4),
    ('IED_ROLE',           'protocol_converter','Protocol converter',      5),
    ('COMMS_PROTOCOL',     'iec61850',         'IEC 61850',                1),
    ('COMMS_PROTOCOL',     'dnp3',             'DNP3',                     2),
    ('COMMS_PROTOCOL',     'iec60870_5_104',   'IEC 60870-5-104',          3),
    ('COMMS_PROTOCOL',     'modbus',           'Modbus',                   4)
) AS v(domain_code, code, label, sort_order) ON v.domain_code = d.code
ON CONFLICT (domain_id, code) DO NOTHING;

-- ===========================================================================
-- Feature classes + subtypes
-- ===========================================================================

INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status, is_diagram_container, is_diagram_placeable)
SELECT v.code, v.name, 'none', NULL, 'none', 'draft', false, true
FROM (VALUES
    ('STATION_BATTERY', 'Station Battery'),
    ('BATTERY_CHARGER', 'Battery Charger'),
    ('CONTROL_IED',     'Control IED'),
    ('ANNUNCIATOR',     'Annunciator')
) AS v(code, name)
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, 1, 'standard'
FROM metadata.feature_classes fc
WHERE fc.code IN ('STATION_BATTERY', 'BATTERY_CHARGER', 'CONTROL_IED', 'ANNUNCIATOR')
ON CONFLICT (feature_class_id, code) DO NOTHING;

-- ===========================================================================
-- Fields (all nullable at DB level; requiredness via subtype_field_rules below)
-- ===========================================================================

INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, v.nullable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('STATION_BATTERY', 'battery_technology', 'text',    'BATTERY_TECHNOLOGY', 'dc_auxiliary', false),
    ('STATION_BATTERY', 'nominal_voltage_v',  'numeric', NULL,                 'dc_auxiliary', true),
    ('STATION_BATTERY', 'capacity_ah',        'numeric', NULL,                 'dc_auxiliary', true),
    ('STATION_BATTERY', 'string_count',       'integer', NULL,                 'dc_auxiliary', true),
    ('BATTERY_CHARGER', 'charger_type',       'text',    'CHARGER_TYPE',       'dc_auxiliary', false),
    ('BATTERY_CHARGER', 'rated_current_a',    'numeric', NULL,                 'dc_auxiliary', true),
    ('BATTERY_CHARGER', 'output_voltage_v',   'numeric', NULL,                 'dc_auxiliary', true),
    ('CONTROL_IED',     'ied_role',           'text',    'IED_ROLE',           'control',      false),
    ('CONTROL_IED',     'protocol',           'text',    'COMMS_PROTOCOL',     'control',      true),
    ('CONTROL_IED',     'manufacturer',       'text',    NULL,                 'control',      true),
    ('ANNUNCIATOR',     'window_count',       'integer', NULL,                 'control',      true),
    ('ANNUNCIATOR',     'manufacturer',       'text',    NULL,                 'control',      true)
) AS v(class_code, name, data_type, domain_code, group_code, nullable) ON v.class_code = fc.code
LEFT JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code IN ('STATION_BATTERY', 'BATTERY_CHARGER', 'CONTROL_IED', 'ANNUNCIATOR')
ON CONFLICT (feature_class_id, name) DO NOTHING;

-- Attach every new field to its class's 'standard' subtype. The identifying
-- coded field (battery_technology / charger_type / ied_role) is required; the
-- rest are optional. ANNUNCIATOR has no required field -- both its fields are
-- optional, which is fine.
INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, (f.name IN ('battery_technology', 'charger_type', 'ied_role'))
FROM metadata.subtypes s
JOIN metadata.feature_classes fc ON fc.id = s.feature_class_id
JOIN metadata.fields f ON f.feature_class_id = fc.id
WHERE fc.code IN ('STATION_BATTERY', 'BATTERY_CHARGER', 'CONTROL_IED', 'ANNUNCIATOR')
ON CONFLICT (subtype_id, field_id) DO NOTHING;

-- ===========================================================================
-- Diagram symbology (no map rule to fall back to -- geometry 'none')
-- ===========================================================================

INSERT INTO metadata.symbology_rules (feature_class_id, render_context, style_def)
SELECT fc.id, 'diagram', jsonb_build_object('icon_ref', 'circle', 'color', v.color)
FROM metadata.feature_classes fc
JOIN (VALUES
    ('STATION_BATTERY', '#0d9488'),
    ('BATTERY_CHARGER', '#0891b2'),
    ('CONTROL_IED',     '#6366f1'),
    ('ANNUNCIATOR',     '#d97706')
) AS v(code, color) ON v.code = fc.code
WHERE NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules sr
    WHERE sr.feature_class_id = fc.id
      AND sr.subtype_id IS NULL AND sr.domain_value_id IS NULL
      AND sr.render_context = 'diagram'
);

-- ===========================================================================
-- Containment -- SUBSTATION_PANEL houses the DC & control secondary devices
-- (kind 'containment', one_to_many). Guarded on (origin, destination, kind);
-- skipped if SUBSTATION_PANEL is absent.
-- ===========================================================================

INSERT INTO metadata.relationship_classes (name, origin_feature_class_id, destination_feature_class_id, kind, cardinality)
SELECT 'SUBSTATION_PANEL contains ' || dst.code, panel.id, dst.id, 'containment', 'one_to_many'
FROM metadata.feature_classes panel
JOIN metadata.feature_classes dst ON dst.code IN ('STATION_BATTERY', 'BATTERY_CHARGER', 'CONTROL_IED', 'ANNUNCIATOR')
WHERE panel.code = 'SUBSTATION_PANEL'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.relationship_classes
    WHERE origin_feature_class_id = panel.id AND destination_feature_class_id = dst.id AND kind = 'containment'
  );
