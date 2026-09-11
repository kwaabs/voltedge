-- seed/substation_panel_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives under
-- /seed on purpose, same reasoning as seed/bay_example.sql and the other seeds.
--
-- Phase 45 Slice 1 -- the SUBSTATION_PANEL feature class: a control-building
-- panel modelled as a FIRST-CLASS, INVENTORIABLE ASSET (a tagged enclosure you
-- can count and give nameplate data), NOT the old Phase 34 simplification where
-- "a BAY is its panel" (1:1). A panel is the PHYSICAL enclosure; a BAY is the
-- LOGICAL circuit. They are orthogonal: one shared protection panel can serve
-- several bays, and one bay's kit spans a protection + a control + a metering
-- panel -- which is why panel<->bay is a many_to_many association
-- (seed/substation_panel_containment_example.sql, kind 'panel_bay',
-- migration 0083), not containment. See docs/phase-45-substation-panels.md.
--
-- Shape decisions, all grounded in existing classes:
--   * geometry_type = 'none' -- a panel has no map footprint; it exists as an
--     inventoried asset placed on a control-scheme diagram, exactly like BAY
--     (seed/bay_example.sql) and PROTECTIVE_RELAY
--     (seed/protective_relay_example.sql). The compiler skips the geom +
--     territory_id columns for 'none' (api/internal/compiler/ddl.go
--     buildCreateTable) and never reads network_role, so 'none' + 'container'
--     compiles cleanly. (The user chose NOT to add a mappable CONTROL_BUILDING;
--     panels are parked under the yard -- see the containment seed.)
--   * network_role = 'container' -- the panel exists to CONTAIN its secondary
--     devices (relays, meters), the same honest label BAY / METER_CHAMBER carry.
--     It is not a traced network node and has no terminals/network_rules.
--   * one 'standard' subtype -- the functional variant (protection vs control vs
--     metering vs RTU/SCADA vs auxiliary vs marshalling) is the panel_type
--     FIELD, not a subtype, the same call BAY made with bay_role and
--     PROTECTIVE_RELAY with device_number. Diagram symbology can still key on
--     panel_type's domain_value_id for a role glyph (a later slice).
--
-- Apply with psql after migrations 0001-0083, then compile:
--   task seed-substation-panel      (then: task compile CODE=SUBSTATION_PANEL)

INSERT INTO metadata.field_groups (code, name, sort_order) VALUES
    ('panel', 'Panel', 1)
ON CONFLICT (code) DO NOTHING;

-- PANEL_TYPE and CONTROL_MODE are new here. VOLTAGE_CLASS and CONDITION_RATING
-- already exist (seed/connectivity_example.sql, seed/substation_yard_example.sql);
-- they are re-asserted defensively so this file is self-contained and
-- order-independent against the other seeds, the same pattern bay_example.sql
-- uses.
INSERT INTO metadata.domains (code, name, domain_type) VALUES
    ('PANEL_TYPE', 'Panel type', 'coded'),
    ('CONTROL_MODE', 'Control mode', 'coded'),
    ('VOLTAGE_CLASS', 'Voltage class', 'coded'),
    ('CONDITION_RATING', 'Condition rating', 'coded')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domain_values (domain_id, code, label, sort_order)
SELECT d.id, v.code, v.label, v.sort_order
FROM metadata.domains d
JOIN (VALUES
    -- The functional variants of a control-building panel. PROTECTION and
    -- CONTROL are the two the user named; METERING/RTU_SCADA/AUXILIARY_DC/
    -- MARSHALLING round out the real control building. RTU_SCADA + the
    -- CONTROL_MODE field together capture the "remote relay panels" the user
    -- asked about (a telecontrolled panel).
    ('PANEL_TYPE', 'PROTECTION', 'Protection', 1),
    ('PANEL_TYPE', 'CONTROL', 'Control', 2),
    ('PANEL_TYPE', 'METERING', 'Metering', 3),
    ('PANEL_TYPE', 'RTU_SCADA', 'RTU / SCADA', 4),
    ('PANEL_TYPE', 'AUXILIARY_DC', 'Auxiliary / DC', 5),
    ('PANEL_TYPE', 'MARSHALLING', 'Marshalling', 6),
    -- The Local/Remote posture of the panel's control -- the standard L/R
    -- selector. REMOTE (or LOCAL_REMOTE) is a "remote relay panel".
    ('CONTROL_MODE', 'LOCAL', 'Local', 1),
    ('CONTROL_MODE', 'REMOTE', 'Remote', 2),
    ('CONTROL_MODE', 'LOCAL_REMOTE', 'Local + Remote', 3),
    ('VOLTAGE_CLASS', '11KV', '11 kV', 1),
    ('VOLTAGE_CLASS', '33KV', '33 kV', 2),
    ('CONDITION_RATING', 'GOOD', 'Good', 1),
    ('CONDITION_RATING', 'FAIR', 'Fair', 2),
    ('CONDITION_RATING', 'POOR', 'Poor', 3)
) AS v(domain_code, code, label, sort_order) ON v.domain_code = d.code
ON CONFLICT (domain_id, code) DO NOTHING;

INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status, is_diagram_container)
VALUES ('SUBSTATION_PANEL', 'Substation Panel', 'none', NULL, 'container', 'draft', false)
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, 1, 'standard'
FROM metadata.feature_classes fc
WHERE fc.code = 'SUBSTATION_PANEL'
ON CONFLICT (feature_class_id, code) DO NOTHING;

-- panel_type is the one required identifier (what kind of panel it is).
-- panel_ref is the asset/panel tag (nullable -- may be unlabelled), matching
-- BAY.bay_ref / METER_CHAMBER.chamber_ref. manufacturer/model are nameplate
-- data (an inventoried asset carries these). control_mode captures the Local/
-- Remote posture (the "remote relay panel" hook). voltage_class + condition are
-- optional context.
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, v.nullable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('panel_type', 'text', 'PANEL_TYPE', 'panel', false),
    ('panel_ref', 'text', NULL, 'panel', true),
    ('control_mode', 'text', 'CONTROL_MODE', 'panel', true),
    ('manufacturer', 'text', NULL, 'panel', true),
    ('model', 'text', NULL, 'panel', true),
    ('voltage_class', 'text', 'VOLTAGE_CLASS', 'panel', true),
    ('condition_rating', 'text', 'CONDITION_RATING', 'panel', true)
) AS v(name, data_type, domain_code, group_code, nullable) ON true
LEFT JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code = 'SUBSTATION_PANEL'
ON CONFLICT (feature_class_id, name) DO NOTHING;

INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, true
FROM metadata.subtypes s
JOIN metadata.fields f ON f.feature_class_id = s.feature_class_id
WHERE s.feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'SUBSTATION_PANEL')
  AND f.name IN ('panel_type')
ON CONFLICT (subtype_id, field_id) DO NOTHING;
