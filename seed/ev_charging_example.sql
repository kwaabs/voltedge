-- seed/ev_charging_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives under
-- /seed on purpose, same reasoning as the other seed files. Phase 54 Slice 1:
-- registers electric-vehicle charging as three new feature classes in a
-- Station -> charger -> connector containment hierarchy (the same shape the
-- substation already uses -- SUBSTATION_PANEL contains its devices). No
-- migration: every field is added as metadata seed (the additive-compile rule,
-- [[compiler-additive-nullable-required]]), and the containment edges reuse the
-- existing 'containment' relationship kind (seed/pnc_control_devices_example.sql
-- already uses it), so nothing here needs a schema change.
--
--   * EV_CHARGING_STATION -- the site and the POINT OF ELECTRICAL CONNECTION.
--       geometry_type 'point', network_role 'junction' with one terminal T1 --
--       modelled exactly like SERVICE_POINT (seed/service_point_example.sql): a
--       leaf load node the sweep flows power to. It carries demand_kw +
--       power_factor (the plain load fields resolve.go already reads), so it
--       participates in load-flow / fault the moment it is created -- BEFORE the
--       managed-charging resolver of Slice 2 exists. The extra managed fields
--       (connected_load_kw / diversity_factor / managed_demand_cap_kw) are what
--       Slice 2 will clamp the effective demand with:
--           effective_kw = min(managed_demand_cap_kw,
--                              connected_load_kw * diversity_factor)
--       They are inert until then -- a station modelled with a plain demand_kw
--       flows correctly today.
--   * EV_CHARGER (EVSE unit) -- a charging unit contained by a station.
--       geometry_type 'none' (inventory; its map/diagram presence IS the
--       station), network_role 'none' (NOT a separate connectivity node -- its
--       load rolls up to the station). Mirrors STATION_BATTERY et al.
--   * EV_CONNECTOR -- a physical port contained by a charger. geometry_type
--       'none', network_role 'none'. The leaf inventory item.
--
-- FIELDS are all nullable at the DB level (the compiler emits NOT NULL for
-- nullable=false, which would fail on a populated gis table -- see
-- [[compiler-additive-nullable-required]]). Requiredness is enforced at the app
-- layer via subtype_field_rules: the identifying coded field (charger_type /
-- connector_standard) is required, the rest optional -- the pnc-seed idiom.
--
-- CONTAINMENT (kind 'containment', one_to_many): EV_CHARGING_STATION contains
-- EV_CHARGER; EV_CHARGER contains EV_CONNECTOR. Rows are legal pairings only --
-- no instances (those live in runtime.feature_relationships via the API).
--
-- SYMBOLOGY: EV_CHARGING_STATION gets a 'map' rule (it is a point). The two
-- geometry-'none' classes get a 'diagram' rule -- a geometry-'none' class has NO
-- map rule to fall back to (diagram-canvas.tsx tries 'diagram' then 'map'), so
-- without one they would render with no symbol on an inventory/schematic view.
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL was
-- reviewed statically -- a live psql apply + `task compile CODE=EV_CHARGING_STATION`
-- + a create spot-check are still owed to call it verified. Only the station
-- needs compiling; the two 'none'-geometry classes carry no gis table (inventory
-- only, exactly like STATION_BATTERY).
--
-- Depends on: the core metadata migrations, plus seed/service_point_example.sql
-- (for the DIST_XFMR feed pattern) and the substation seed that creates
-- DIST_XFMR -- absent, the one network rule below just skips. Apply, then:
--   task seed-ev-charging
--   task compile CODE=EV_CHARGING_STATION

-- ===========================================================================
-- Field groups + domains
-- ===========================================================================

INSERT INTO metadata.field_groups (code, name, sort_order) VALUES
    ('electrical',  'Electrical',   1),
    ('ev_charging', 'EV Charging', 42)
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domains (code, name, domain_type) VALUES
    ('EV_ACCESS',             'EV charging access',     'coded'),
    ('EV_CHARGER_TYPE',       'EV charger type',        'coded'),
    ('EV_CONNECTOR_STANDARD', 'EV connector standard',  'coded')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domain_values (domain_id, code, label, sort_order)
SELECT d.id, v.code, v.label, v.sort_order
FROM metadata.domains d
JOIN (VALUES
    ('EV_ACCESS',             'public',      'Public',                 1),
    ('EV_ACCESS',             'depot',       'Depot / fleet',          2),
    ('EV_ACCESS',             'residential', 'Residential',            3),
    ('EV_ACCESS',             'workplace',   'Workplace',              4),
    ('EV_CHARGER_TYPE',       'ac_l2',       'AC Level 2',             1),
    ('EV_CHARGER_TYPE',       'dc_fast',     'DC fast',                2),
    ('EV_CHARGER_TYPE',       'dc_ultra',    'DC ultra-rapid',         3),
    ('EV_CONNECTOR_STANDARD', 'type2',       'Type 2 (IEC 62196)',     1),
    ('EV_CONNECTOR_STANDARD', 'ccs2',        'CCS Combo 2',            2),
    ('EV_CONNECTOR_STANDARD', 'chademo',     'CHAdeMO',                3),
    ('EV_CONNECTOR_STANDARD', 'nacs',        'NACS (J3400)',           4),
    ('EV_CONNECTOR_STANDARD', 'gbt',         'GB/T',                   5)
) AS v(domain_code, code, label, sort_order) ON v.domain_code = d.code
ON CONFLICT (domain_id, code) DO NOTHING;

-- ===========================================================================
-- Feature classes + subtypes
-- ===========================================================================

-- The station: a point, a network node (junction), like SERVICE_POINT.
INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status, is_diagram_container)
VALUES ('EV_CHARGING_STATION', 'EV Charging Station', 'point', 4326, 'junction', 'draft', false)
ON CONFLICT (code) DO NOTHING;

-- The two inventory classes: geometry none / network none, like STATION_BATTERY.
INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status, is_diagram_container, is_diagram_placeable)
SELECT v.code, v.name, 'none', NULL, 'none', 'draft', false, true
FROM (VALUES
    ('EV_CHARGER',   'EV Charger'),
    ('EV_CONNECTOR', 'EV Connector')
) AS v(code, name)
ON CONFLICT (code) DO NOTHING;

-- One 'standard' subtype each (the functional variant is a coded field, not a
-- subtype -- charger_type / connector_standard).
INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, 1, 'standard'
FROM metadata.feature_classes fc
WHERE fc.code IN ('EV_CHARGING_STATION', 'EV_CHARGER', 'EV_CONNECTOR')
ON CONFLICT (feature_class_id, code) DO NOTHING;

-- One terminal on the station so it can be the leaf of a feed (mirrors
-- SERVICE_POINT T1).
INSERT INTO metadata.terminals (feature_class_id, subtype_id, code, role, phase_capable)
SELECT fc.id, NULL, 'T1', 'primary', true
FROM metadata.feature_classes fc
WHERE fc.code = 'EV_CHARGING_STATION'
ON CONFLICT (feature_class_id, subtype_id, code) DO NOTHING;

-- ===========================================================================
-- Fields (all nullable at DB level; requiredness via subtype_field_rules below)
-- ===========================================================================

INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, true
FROM metadata.feature_classes fc
JOIN (VALUES
    -- Station: plain load fields (read by resolve.go today) + managed fields.
    ('EV_CHARGING_STATION', 'demand_kw',             'numeric', NULL,                     'electrical'),
    ('EV_CHARGING_STATION', 'power_factor',          'numeric', NULL,                     'electrical'),
    ('EV_CHARGING_STATION', 'connection_voltage_v',  'numeric', NULL,                     'electrical'),
    ('EV_CHARGING_STATION', 'connected_load_kw',     'numeric', NULL,                     'ev_charging'),
    ('EV_CHARGING_STATION', 'diversity_factor',      'numeric', NULL,                     'ev_charging'),
    ('EV_CHARGING_STATION', 'managed_demand_cap_kw', 'numeric', NULL,                     'ev_charging'),
    ('EV_CHARGING_STATION', 'operator',              'text',    NULL,                     'ev_charging'),
    ('EV_CHARGING_STATION', 'access',                'text',    'EV_ACCESS',              'ev_charging'),
    -- Charger (EVSE unit).
    ('EV_CHARGER',          'charger_type',          'text',    'EV_CHARGER_TYPE',        'ev_charging'),
    ('EV_CHARGER',          'rated_power_kw',        'numeric', NULL,                     'ev_charging'),
    ('EV_CHARGER',          'num_connectors',        'integer', NULL,                     'ev_charging'),
    ('EV_CHARGER',          'managed',               'boolean', NULL,                     'ev_charging'),
    ('EV_CHARGER',          'manufacturer',          'text',    NULL,                     'ev_charging'),
    -- Connector (physical port).
    ('EV_CONNECTOR',        'connector_standard',    'text',    'EV_CONNECTOR_STANDARD',  'ev_charging'),
    ('EV_CONNECTOR',        'rated_power_kw',        'numeric', NULL,                     'ev_charging')
) AS v(class_code, name, data_type, domain_code, group_code) ON v.class_code = fc.code
LEFT JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code IN ('EV_CHARGING_STATION', 'EV_CHARGER', 'EV_CONNECTOR')
ON CONFLICT (feature_class_id, name) DO NOTHING;

-- Attach every new field to its class's 'standard' subtype. The identifying
-- coded field (charger_type / connector_standard) is required; everything else
-- optional (a station with a plain demand_kw and no managed fields is valid).
INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, (f.name IN ('charger_type', 'connector_standard'))
FROM metadata.subtypes s
JOIN metadata.feature_classes fc ON fc.id = s.feature_class_id
JOIN metadata.fields f ON f.feature_class_id = fc.id
WHERE fc.code IN ('EV_CHARGING_STATION', 'EV_CHARGER', 'EV_CONNECTOR')
ON CONFLICT (subtype_id, field_id) DO NOTHING;

-- ===========================================================================
-- Network rule -- a station is LV-fed off a distribution transformer secondary,
-- exactly like a SERVICE_POINT (DIST_XFMR T2 -> station T1). Skipped if
-- DIST_XFMR is absent. (MV-fed ultra-rapid hubs are a later rule; this is the
-- proven LV feed.)
-- ===========================================================================

INSERT INTO metadata.network_rules (name, from_feature_class_id, from_terminal_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'DIST_XFMR T2 -> EV_CHARGING_STATION T1', xf.id, t2.id, ev.id, t1.id, NULL, true
FROM metadata.feature_classes xf CROSS JOIN metadata.feature_classes ev
JOIN metadata.terminals t2 ON t2.feature_class_id = xf.id AND t2.code = 'T2'
JOIN metadata.terminals t1 ON t1.feature_class_id = ev.id AND t1.code = 'T1'
WHERE xf.code = 'DIST_XFMR' AND ev.code = 'EV_CHARGING_STATION'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = xf.id AND from_terminal_id = t2.id AND to_feature_class_id = ev.id AND to_terminal_id = t1.id
  );

-- ===========================================================================
-- Symbology
-- ===========================================================================

-- Station -- a map point.
INSERT INTO metadata.symbology_rules (feature_class_id, render_context, style_def)
SELECT fc.id, 'map', jsonb_build_object('icon_ref', 'circle', 'color', '#16a34a')
FROM metadata.feature_classes fc
WHERE fc.code = 'EV_CHARGING_STATION'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules sr
    WHERE sr.feature_class_id = fc.id
      AND sr.subtype_id IS NULL AND sr.domain_value_id IS NULL
      AND sr.render_context = 'map'
  );

-- Charger / connector -- geometry 'none', so a diagram rule (no map fallback).
INSERT INTO metadata.symbology_rules (feature_class_id, render_context, style_def)
SELECT fc.id, 'diagram', jsonb_build_object('icon_ref', 'circle', 'color', v.color)
FROM metadata.feature_classes fc
JOIN (VALUES
    ('EV_CHARGER',   '#22c55e'),
    ('EV_CONNECTOR', '#4ade80')
) AS v(code, color) ON v.code = fc.code
WHERE NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules sr
    WHERE sr.feature_class_id = fc.id
      AND sr.subtype_id IS NULL AND sr.domain_value_id IS NULL
      AND sr.render_context = 'diagram'
);

-- ===========================================================================
-- Containment -- Station contains Charger; Charger contains Connector.
-- kind 'containment', one_to_many; guarded on (origin, destination, kind).
-- ===========================================================================

INSERT INTO metadata.relationship_classes (name, origin_feature_class_id, destination_feature_class_id, kind, cardinality)
SELECT v.name, origin.id, dst.id, 'containment', 'one_to_many'
FROM (VALUES
    ('EV_CHARGING_STATION contains EV_CHARGER',   'EV_CHARGING_STATION', 'EV_CHARGER'),
    ('EV_CHARGER contains EV_CONNECTOR',          'EV_CHARGER',          'EV_CONNECTOR')
) AS v(name, origin_code, dst_code)
JOIN metadata.feature_classes origin ON origin.code = v.origin_code
JOIN metadata.feature_classes dst ON dst.code = v.dst_code
WHERE NOT EXISTS (
    SELECT 1 FROM metadata.relationship_classes
    WHERE origin_feature_class_id = origin.id AND destination_feature_class_id = dst.id AND kind = 'containment'
  );
