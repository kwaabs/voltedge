-- seed/cim_mapping_coverage.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives under
-- /seed on purpose, same as cim_mapping_example.sql / mv_cim_mapping_example.sql.
-- Phase 55 Slice 3 (docs/phase-55-cim-mapping-admin.md): the coverage fill. The
-- editor (S1) and the warn-on-unmapped safety net (S2) surfaced ~25 feature
-- classes added since the original mapping seeds that have NO cim_class_mappings
-- row, so metadata.LoadCIMMapping's ClassFor returns ok=false and the CIM export
-- silently drops them (cim.go equipment pass). This seed closes that gap by
-- authoring a mapping decision for each one: either MAP it to a CIM class, or
-- record an explicit cim_kind='skip' so the drop is intentional, not accidental
-- (decision 2026-09-13: "electrical core + explicit skips").
--
-- Depends on the classes it names having been seeded already (power_transformer,
-- earthing/generator, ev_charging, mv_earth_switch, cable_joint, rmu, mv_metering,
-- switchgear/switchboard panels, substation panels, tower, pnc control devices,
-- cable-management civil, control building). A class this file names that isn't
-- seeded simply produces no row -- the export then keeps treating it as unmapped
-- (harmless; re-run after seeding it). JOINs by code, like the sibling seeds.
--
-- Idempotent. The NULL-subtype class rows use NOT EXISTS rather than ON CONFLICT
-- because UNIQUE (feature_class_id, subtype_id) treats NULL subtype_ids as
-- DISTINCT in Postgres, so ON CONFLICT would never fire for a class-default row
-- and re-running would duplicate it. Attribute rows use ON CONFLICT (the UNIQUE
-- there is fully non-NULL).
--
-- Apply with psql after the class seeds above. No compile step -- pure metadata.

-- Re-assert the default profile so this seed stands alone if applied before the
-- base cim_mapping_example.sql (idempotent -- ON CONFLICT on code).
INSERT INTO metadata.cim_profiles
    (code, name, cim_namespace, profile_uri, model_authority, is_default)
VALUES
    ('CDPSM_CIM16', 'CDPSM (IEC 61968-13), CIM16',
     'http://iec.ch/TC57/2013/CIM-schema-cim16#',
     'http://iec.ch/TC57/61968-13/CDPSM/2013#',
     'https://voltedge.local/ModelAuthoritySet',
     true)
ON CONFLICT (code) DO NOTHING;

-- Class-default mappings (subtype_id NULL). Two groups, one INSERT: the MAPPED
-- electrical core (a real CIM class + behavioural cim_kind) and the explicit
-- SKIPS (cim_kind='skip' -> the encoder excludes them; cim_class is required
-- NOT NULL but decorative for a skip, so a clear '(not exported)' sentinel).
--
-- MAP rationale:
--   * POWER_XFMR / EARTHING_XFMR -> PowerTransformer (conducting_junction), same
--     as DIST_XFMR. An earthing transformer is still a PowerTransformer in CDPSM.
--   * GENERATOR -> SynchronousMachine (conducting_junction): the concrete CDPSM
--     rotating-machine class for a generation injection point.
--   * EV_CHARGING_STATION -> EnergyConsumer (conducting_junction), same as
--     SERVICE_POINT -- a managed load at a connection point. The sub-equipment
--     (charger/connector) is skipped below; the station is the network node.
--   * MV_EARTH_SWITCH -> GroundDisconnector (conducting_junction): the CDPSM
--     earth switch, a Switch subclass (so Switch.normalOpen is valid below).
--   * MV_CABLE_JOINT / LV_CABLE_JOINT -> Junction (conducting_junction), same as
--     LV_LINK_BOX -- a connectivity node splicing two cable runs.
--   * RMU / MV_METERING_CUBICLE / MV_SWITCHGEAR_PANEL / MV_SWITCHBOARD /
--     SUBSTATION_PANEL -> EquipmentContainer (container), same as METER_CHAMBER:
--     equipment-grouping enclosures, not conducting equipment themselves.
--   * TOWER -> Tower (asset), the transmission-structure sibling of POLE->Pole.
--
-- SKIP rationale (intentionally out of the electrical network model):
--   * EV_CHARGER / EV_CONNECTOR -- sub-equipment of the station node above.
--   * STATION_BATTERY / BATTERY_CHARGER / CONTROL_IED / ANNUNCIATOR -- P&C DC
--     auxiliary / secondary-control devices, not primary plant.
--   * CABLE_TRENCH / CABLE_DUCT / CABLE_CHAMBER / DUCT_WAY -- civil cable-
--     management assets, no CDPSM electrical analog.
--   * CONTROL_BUILDING -- a site structure, not a container of conducting gear.
INSERT INTO metadata.cim_class_mappings (feature_class_id, subtype_id, cim_class, cim_kind)
SELECT fc.id, NULL, v.cim_class, v.cim_kind
FROM metadata.feature_classes fc
JOIN (VALUES
    -- Mapped electrical core
    ('POWER_XFMR',            'PowerTransformer',   'conducting_junction'),
    ('EARTHING_XFMR',         'PowerTransformer',   'conducting_junction'),
    ('GENERATOR',             'SynchronousMachine', 'conducting_junction'),
    ('EV_CHARGING_STATION',   'EnergyConsumer',     'conducting_junction'),
    ('MV_EARTH_SWITCH',       'GroundDisconnector', 'conducting_junction'),
    ('MV_CABLE_JOINT',        'Junction',           'conducting_junction'),
    ('LV_CABLE_JOINT',        'Junction',           'conducting_junction'),
    ('RMU',                   'EquipmentContainer', 'container'),
    ('MV_METERING_CUBICLE',   'EquipmentContainer', 'container'),
    ('MV_SWITCHGEAR_PANEL',   'EquipmentContainer', 'container'),
    ('MV_SWITCHBOARD',        'EquipmentContainer', 'container'),
    ('SUBSTATION_PANEL',      'EquipmentContainer', 'container'),
    ('TOWER',                 'Tower',              'asset'),
    -- Explicit skips (decided, not accidentally dropped)
    ('EV_CHARGER',            '(not exported)',     'skip'),
    ('EV_CONNECTOR',          '(not exported)',     'skip'),
    ('STATION_BATTERY',       '(not exported)',     'skip'),
    ('BATTERY_CHARGER',       '(not exported)',     'skip'),
    ('CONTROL_IED',           '(not exported)',     'skip'),
    ('ANNUNCIATOR',           '(not exported)',     'skip'),
    ('CABLE_TRENCH',          '(not exported)',     'skip'),
    ('CABLE_DUCT',            '(not exported)',     'skip'),
    ('CABLE_CHAMBER',         '(not exported)',     'skip'),
    ('DUCT_WAY',              '(not exported)',     'skip'),
    ('CONTROL_BUILDING',      '(not exported)',     'skip')
) AS v(fc_code, cim_class, cim_kind) ON v.fc_code = fc.code
WHERE NOT EXISTS (
    SELECT 1 FROM metadata.cim_class_mappings m
    WHERE m.feature_class_id = fc.id AND m.subtype_id IS NULL
);

-- Attribute mappings without a scale_factor (domain_map + boolean), mirroring the
-- established DIST_XFMR / DIST_SWITCH patterns:
--   * POWER_XFMR / EARTHING_XFMR primary_voltage -> BaseVoltage.nominalVoltage
--     (domain_map). These carry the VOLTAGE_CLASS domain, so the value_map spans
--     all four transmission/sub-transmission classes (11/33/66/132 kV), not just
--     the 11/33 pair the DIST_XFMR seed used.
--   * MV_EARTH_SWITCH device_state -> Switch.normalOpen (boolean). Valid because
--     GroundDisconnector is a CIM Switch subclass.
INSERT INTO metadata.cim_attribute_mappings (field_id, cim_attribute, transform, value_map, unit)
SELECT f.id, v.cim_attribute, v.transform, v.value_map::jsonb, v.unit
FROM metadata.fields f
JOIN metadata.feature_classes fc ON fc.id = f.feature_class_id
JOIN (VALUES
    ('POWER_XFMR',      'primary_voltage', 'BaseVoltage.nominalVoltage', 'domain_map', '{"11KV":11000,"33KV":33000,"66KV":66000,"132KV":132000}', 'V'),
    ('EARTHING_XFMR',   'primary_voltage', 'BaseVoltage.nominalVoltage', 'domain_map', '{"11KV":11000,"33KV":33000,"66KV":66000,"132KV":132000}', 'V'),
    ('MV_EARTH_SWITCH', 'device_state',    'Switch.normalOpen',          'boolean',    '{"OPEN":true,"CLOSED":false}', NULL)
) AS v(fc_code, field_name, cim_attribute, transform, value_map, unit)
    ON v.fc_code = fc.code AND v.field_name = f.name
ON CONFLICT (field_id, cim_attribute) DO NOTHING;

-- Attribute mapping with a scale_factor (its own INSERT, like the DIST_XFMR
-- rated_kva row):
--   * POWER_XFMR rated_mva -> PowerTransformerEnd.ratedS (scale). The nameplate
--     is in MVA here (DIST_XFMR was kVA), so the factor is 1e6 (MVA -> VA), NOT
--     the 1000.0 the kVA seed used. EARTHING_XFMR carries no rating field, so it
--     gets voltage only (above) and no ratedS row.
INSERT INTO metadata.cim_attribute_mappings (field_id, cim_attribute, transform, scale_factor, value_map, unit)
SELECT f.id, v.cim_attribute, v.transform, v.scale_factor, v.value_map::jsonb, v.unit
FROM metadata.fields f
JOIN metadata.feature_classes fc ON fc.id = f.feature_class_id
JOIN (VALUES
    ('POWER_XFMR', 'rated_mva', 'PowerTransformerEnd.ratedS', 'scale', 1000000.0, NULL, 'VA')
) AS v(fc_code, field_name, cim_attribute, transform, scale_factor, value_map, unit)
    ON v.fc_code = fc.code AND v.field_name = f.name
ON CONFLICT (field_id, cim_attribute) DO NOTHING;
