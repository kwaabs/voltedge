-- seed/mv_cim_mapping_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives under
-- /seed on purpose, same reasoning as seed/cim_mapping_example.sql (the base
-- CDPSM/CIM16 mapping this extends). Phase 27, Slice 6: add the CDPSM/CIM16
-- mapping rows for the eight new MV plant classes (Slices 1-4) so they export to
-- CIM/XML (docs/phase-16-cim-export.md) instead of being dropped as unmapped.
-- Migration 0034 created the table shapes; seed/cim_mapping_example.sql authored
-- the base content; this authors the Phase 27 additions -- the same catalog
-- split every other metadata table uses.
--
-- Depends on the Phase 27 class seeds having been applied (the rows here JOIN
-- feature_classes / fields by code; a class this file names that isn't seeded
-- simply produces no row -- the export then treats it as unmapped and skips it,
-- no error). Also assumes seed/cim_mapping_example.sql already ran for the
-- cim_profiles default row -- but that INSERT is repeated here idempotently so
-- this seed is self-sufficient.
--
-- Idempotent. Class rows use NOT EXISTS (not ON CONFLICT) because
-- UNIQUE (feature_class_id, subtype_id) treats NULL subtype_ids as DISTINCT in
-- Postgres, so ON CONFLICT would never fire for a class-default row and a re-run
-- would duplicate it. Attribute rows use ON CONFLICT (field_id, cim_attribute)
-- DO NOTHING (that key is fully non-NULL).
--
-- No compile step -- these are pure metadata rows, not a physical-table change.

-- Repeat the default profile idempotently so this seed stands alone.
INSERT INTO metadata.cim_profiles
    (code, name, cim_namespace, profile_uri, model_authority, is_default)
VALUES
    ('CDPSM_CIM16', 'CDPSM (IEC 61968-13), CIM16',
     'http://iec.ch/TC57/2013/CIM-schema-cim16#',
     'http://iec.ch/TC57/61968-13/CDPSM/2013#',
     'https://voltedge.local/ModelAuthoritySet',
     true)
ON CONFLICT (code) DO NOTHING;

-- Class-default mappings (subtype_id NULL). Every new class is a point junction,
-- so cim_kind = 'conducting_junction' (point equipment, 2 synthesized terminals)
-- for all eight -- the same kind LV_BUSBAR / DIST_SWITCH use. The distinct CIM
-- classes are exactly why Phase 27 gave each device its own feature class:
--   MV_BREAKER       -> Breaker              (ProtectedSwitch)
--   MV_ISOLATOR      -> Disconnector         (off-load isolator)
--   MV_SECTIONALIZER -> Sectionaliser        (Switch subclass)
--   MV_FUSE          -> Fuse                 (Switch subclass)
--   MV_ARRESTER      -> SurgeArrester        (shunt protection equipment)
--   MV_CT            -> CurrentTransformer   (measuring)
--   MV_VT            -> PotentialTransformer (measuring)
--   MV_BUSBAR        -> BusbarSection        (matches LV_BUSBAR)
INSERT INTO metadata.cim_class_mappings (feature_class_id, subtype_id, cim_class, cim_kind)
SELECT fc.id, NULL, v.cim_class, v.cim_kind
FROM metadata.feature_classes fc
JOIN (VALUES
    ('MV_BREAKER',       'Breaker',              'conducting_junction'),
    ('MV_ISOLATOR',      'Disconnector',         'conducting_junction'),
    ('MV_SECTIONALIZER', 'Sectionaliser',        'conducting_junction'),
    ('MV_FUSE',          'Fuse',                 'conducting_junction'),
    ('MV_ARRESTER',      'SurgeArrester',        'conducting_junction'),
    ('MV_CT',            'CurrentTransformer',   'conducting_junction'),
    ('MV_VT',            'PotentialTransformer', 'conducting_junction'),
    ('MV_BUSBAR',        'BusbarSection',        'conducting_junction')
) AS v(fc_code, cim_class, cim_kind) ON v.fc_code = fc.code
WHERE NOT EXISTS (
    SELECT 1 FROM metadata.cim_class_mappings m
    WHERE m.feature_class_id = fc.id AND m.subtype_id IS NULL
);

-- Attribute mappings.
--   * Switch.normalOpen (boolean): the coded device_state -> the CIM boolean on
--     the switch. Valid because Breaker / Disconnector / Sectionaliser / Fuse
--     are all CIM Switch subclasses. MV_ARRESTER / MV_CT / MV_VT / MV_BUSBAR
--     carry no device_state and are not mapped here.
--   * BaseVoltage.nominalVoltage (domain_map): MV_BUSBAR's coded voltage_class
--     -> nominal volts, feeding the shared BaseVoltage the BusbarSection
--     references (encoder special-cases this attribute) -- same transform the
--     base seed uses for MV_CONDUCTOR / DIST_XFMR.
-- The instrument-transformer ratios (ct_ratio / vt_ratio) and condition_rating
-- have no simple CDPSM scalar to derive without inventing units, so they are
-- deliberately left unmapped rather than seeded speculatively (cf. the base
-- seed's impedance gap note).
INSERT INTO metadata.cim_attribute_mappings (field_id, cim_attribute, transform, value_map, unit)
SELECT f.id, v.cim_attribute, v.transform, v.value_map::jsonb, v.unit
FROM metadata.fields f
JOIN metadata.feature_classes fc ON fc.id = f.feature_class_id
JOIN (VALUES
    ('MV_BREAKER',       'device_state',  'Switch.normalOpen',          'boolean',    '{"OPEN":true,"CLOSED":false}', NULL),
    ('MV_ISOLATOR',      'device_state',  'Switch.normalOpen',          'boolean',    '{"OPEN":true,"CLOSED":false}', NULL),
    ('MV_SECTIONALIZER', 'device_state',  'Switch.normalOpen',          'boolean',    '{"OPEN":true,"CLOSED":false}', NULL),
    ('MV_FUSE',          'device_state',  'Switch.normalOpen',          'boolean',    '{"OPEN":true,"CLOSED":false}', NULL),
    ('MV_BUSBAR',        'voltage_class', 'BaseVoltage.nominalVoltage', 'domain_map', '{"11KV":11000,"33KV":33000}',  'V')
) AS v(fc_code, field_name, cim_attribute, transform, value_map, unit)
    ON v.fc_code = fc.code AND v.field_name = f.name
ON CONFLICT (field_id, cim_attribute) DO NOTHING;
