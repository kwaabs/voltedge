-- seed/cim_mapping_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives
-- under /seed on purpose, same reasoning as the other seed files. This is the
-- DEFAULT CDPSM/CIM16 mapping that drives Phase 16 CIM/XML export
-- (docs/phase-16-cim-export.md): which CIM class each feature class becomes,
-- which CIM attribute a handful of fields feed, and the one profile/config row
-- the RDF/XML header needs. The migration (0034) creates the table shapes; this
-- authors the content, the same split every other metadata catalog uses.
--
-- Depends on the feature classes it maps having been seeded already
-- (connectivity_example, switching_device_example, lv_*_example, pole_example,
-- substation_yard_example, service_point_example, protective_relay_example):
-- the class/field rows here JOIN those by code, and a class this file names
-- that isn't seeded simply produces no mapping row (the export then treats it
-- as unmapped and skips it -- no error).
--
-- Idempotent. The NULL-subtype class rows use NOT EXISTS rather than
-- ON CONFLICT because UNIQUE (feature_class_id, subtype_id) treats NULL
-- subtype_ids as DISTINCT in Postgres, so ON CONFLICT would never fire for the
-- class-default rows and re-running would duplicate them.
--
-- Apply with psql after the feature-class seeds above. No compile step -- these
-- are pure metadata rows, not a physical-table change.

-- The profile/config the RDF/XML header (md:FullModel) and the GL coordinates
-- need. One default profile in v1; the export request may name another later.
INSERT INTO metadata.cim_profiles
    (code, name, cim_namespace, profile_uri, model_authority, is_default)
VALUES
    ('CDPSM_CIM16', 'CDPSM (IEC 61968-13), CIM16',
     'http://iec.ch/TC57/2013/CIM-schema-cim16#',
     'http://iec.ch/TC57/61968-13/CDPSM/2013#',
     'https://voltedge.local/ModelAuthoritySet',
     true)
ON CONFLICT (code) DO NOTHING;

-- Class-default mappings (subtype_id NULL): one CIM class + kind per feature
-- class. cim_kind is the axis the encoder switches on -- conducting_edge (line
-- equipment, 2 synthesized terminals), conducting_junction (point equipment),
-- container (EquipmentContainer / Substation), asset (non-conducting, e.g.
-- Pole), protection (ProtectionEquipment, no map geometry). PROTECTIVE_RELAY is
-- geometry_type 'none' so the export path never reaches it, but its mapping row
-- is harmless and documents intent.
INSERT INTO metadata.cim_class_mappings (feature_class_id, subtype_id, cim_class, cim_kind)
SELECT fc.id, NULL, v.cim_class, v.cim_kind
FROM metadata.feature_classes fc
JOIN (VALUES
    ('MV_CONDUCTOR',     'ACLineSegment',      'conducting_edge'),
    ('LV_CONDUCTOR',     'ACLineSegment',      'conducting_edge'),
    ('DIST_XFMR',        'PowerTransformer',   'conducting_junction'),
    ('DIST_SWITCH',      'LoadBreakSwitch',    'conducting_junction'),
    ('SERVICE_CUTOUT',   'Fuse',               'conducting_junction'),
    ('LV_LINK_BOX',      'Junction',           'conducting_junction'),
    ('LV_BUSBAR',        'BusbarSection',      'conducting_junction'),
    ('SERVICE_POINT',    'EnergyConsumer',     'conducting_junction'),
    ('STREET_LIGHT',     'EnergyConsumer',     'conducting_junction'),
    ('METER',            'UsagePoint',         'asset'),
    ('METER_CHAMBER',    'EquipmentContainer', 'container'),
    ('SUBSTATION_YARD',  'Substation',         'container'),
    ('POLE',             'Pole',               'asset'),
    ('PROTECTIVE_RELAY', 'ProtectionEquipment', 'protection')
) AS v(fc_code, cim_class, cim_kind) ON v.fc_code = fc.code
WHERE NOT EXISTS (
    SELECT 1 FROM metadata.cim_class_mappings m
    WHERE m.feature_class_id = fc.id AND m.subtype_id IS NULL
);

-- Subtype override: an automatic_recloser DIST_SWITCH is a CIM Recloser
-- (a ProtectedSwitch), not the class-default LoadBreakSwitch. subtype_id is
-- non-NULL here, so this row is distinct from the class default above.
INSERT INTO metadata.cim_class_mappings (feature_class_id, subtype_id, cim_class, cim_kind)
SELECT fc.id, st.id, 'Recloser', 'conducting_junction'
FROM metadata.feature_classes fc
JOIN metadata.subtypes st ON st.feature_class_id = fc.id AND st.code = 2
WHERE fc.code = 'DIST_SWITCH'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.cim_class_mappings m
    WHERE m.feature_class_id = fc.id AND m.subtype_id = st.id
  );

-- Attribute mappings (field -> CIM attribute + coercion). Slice 1 seeds the two
-- transforms it can serialize without topology:
--   * BaseVoltage.nominalVoltage (domain_map): the coded voltage class ->
--     nominal volts; feeds the shared BaseVoltage object the equipment
--     references (the encoder special-cases this attribute).
--   * Switch.normalOpen (boolean): the coded device_state -> the CIM boolean
--     literal on the switch. Valid because LoadBreakSwitch/Recloser/Fuse are
--     all CIM Switch subclasses.
-- Impedance/length (material_spec, from_geometry) and other detail land in
-- Slices 2-3. UNIQUE (field_id, cim_attribute) is fully non-NULL, so
-- ON CONFLICT DO NOTHING is idempotent here.
INSERT INTO metadata.cim_attribute_mappings (field_id, cim_attribute, transform, value_map, unit)
SELECT f.id, v.cim_attribute, v.transform, v.value_map::jsonb, v.unit
FROM metadata.fields f
JOIN metadata.feature_classes fc ON fc.id = f.feature_class_id
JOIN (VALUES
    ('MV_CONDUCTOR',   'voltage_class',   'BaseVoltage.nominalVoltage', 'domain_map', '{"11KV":11000,"33KV":33000}', 'V'),
    ('DIST_XFMR',      'primary_voltage', 'BaseVoltage.nominalVoltage', 'domain_map', '{"11KV":11000,"33KV":33000}', 'V'),
    ('DIST_SWITCH',    'device_state',    'Switch.normalOpen',          'boolean',    '{"OPEN":true,"CLOSED":false}', NULL),
    ('SERVICE_CUTOUT', 'device_state',    'Switch.normalOpen',          'boolean',    '{"OPEN":true,"CLOSED":false}', NULL)
) AS v(fc_code, field_name, cim_attribute, transform, value_map, unit)
    ON v.fc_code = fc.code AND v.field_name = f.name
ON CONFLICT (field_id, cim_attribute) DO NOTHING;

-- Slice 3 attribute mappings (electrical detail). These carry a scale_factor,
-- so they are seeded through their own INSERT rather than folded into the block
-- above (which has no scale_factor column):
--   * Conductor.length (from_geometry): a placeholder field -- the value comes
--     from the LineString geometry, not the column -- so the encoder emits the
--     geodesic length. Both conductor classes are conducting_edge; MV and LV
--     each get a length. The field named here is just an anchor for the row.
--   * ACLineSegment.r / .x (material_spec): the coded conductor_spec value ->
--     its metadata.material_specs per-km resistance/reactance, x length in km.
--     Only LV_CONDUCTOR has seeded material specs (LV_CABLE_SPEC), so MV gets a
--     length but no impedance -- a known gap (docs/phase-16-cim-export.md).
--   * PowerTransformerEnd.ratedS (scale): nameplate kVA -> VA on every winding
--     of the transformer (the encoder emits it on each PowerTransformerEnd).
INSERT INTO metadata.cim_attribute_mappings (field_id, cim_attribute, transform, scale_factor, value_map, unit)
SELECT f.id, v.cim_attribute, v.transform, v.scale_factor, v.value_map::jsonb, v.unit
FROM metadata.fields f
JOIN metadata.feature_classes fc ON fc.id = f.feature_class_id
JOIN (VALUES
    ('MV_CONDUCTOR', 'voltage_class',  'Conductor.length',            'from_geometry', NULL,   NULL, 'm'),
    ('LV_CONDUCTOR', 'conductor_spec', 'Conductor.length',            'from_geometry', NULL,   NULL, 'm'),
    ('LV_CONDUCTOR', 'conductor_spec', 'ACLineSegment.r',             'material_spec', NULL,   NULL, 'ohm'),
    ('LV_CONDUCTOR', 'conductor_spec', 'ACLineSegment.x',             'material_spec', NULL,   NULL, 'ohm'),
    ('DIST_XFMR',    'rated_kva',      'PowerTransformerEnd.ratedS',  'scale',         1000.0, NULL, 'VA')
) AS v(fc_code, field_name, cim_attribute, transform, scale_factor, value_map, unit)
    ON v.fc_code = fc.code AND v.field_name = f.name
ON CONFLICT (field_id, cim_attribute) DO NOTHING;

-- Slice 4 attribute mappings (phasing). The 'phase' transform expands one coded
-- PHASE value (L1/L2/L3/N and the L1N/L2N/L3N/L1L2L3N composites) into per-phase
-- CIM objects: an ACLineSegmentPhase per energised phase on a conductor, an
-- EnergyConsumerPhase per phase on a load (STREET_LIGHT). Requires migration 0035
-- (the 'phase' value added to the transform CHECK). Only classes that actually
-- carry a `phase` field are mapped:
--   * MV_CONDUCTOR / LV_CONDUCTOR -> ACLineSegmentPhase (MV phase is nullable, so
--     an MV conductor with no phase set simply produces no per-phase objects).
--   * STREET_LIGHT -> EnergyConsumerPhase.
-- DIST_XFMR/LV_BUSBAR/METER carry a `phase` field too but have no simple CDPSM
-- per-phase object, so they are intentionally not mapped here (see the doc).
-- cim_attribute is the per-phase class's own `.phase` (a SinglePhaseKind); the
-- encoder derives the parent back-reference from the equipment's CIM class.
INSERT INTO metadata.cim_attribute_mappings (field_id, cim_attribute, transform)
SELECT f.id, v.cim_attribute, v.transform
FROM metadata.fields f
JOIN metadata.feature_classes fc ON fc.id = f.feature_class_id
JOIN (VALUES
    ('MV_CONDUCTOR', 'phase', 'ACLineSegmentPhase.phase',  'phase'),
    ('LV_CONDUCTOR', 'phase', 'ACLineSegmentPhase.phase',  'phase'),
    ('STREET_LIGHT', 'phase', 'EnergyConsumerPhase.phase', 'phase')
) AS v(fc_code, field_name, cim_attribute, transform)
    ON v.fc_code = fc.code AND v.field_name = f.name
ON CONFLICT (field_id, cim_attribute) DO NOTHING;
