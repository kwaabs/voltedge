-- 0034_create_cim_mapping.sql
-- Purpose: the metadata-driven mapping that drives CIM/XML export
-- (Phase 16, docs/phase-16-cim-export.md). CIM (IEC 61970/61968) has a
-- fixed, typed class + attribute vocabulary, so there is no automatic 1:1
-- from this app's arbitrary per-class fields to CIM -- the mapping is
-- authored. Consistent with the rest of the app (feature classes, fields,
-- domains, subtypes, network rules are all metadata, not hardcoded), the
-- CIM mapping is data too: these tables hold the DECLARATIVE mapping (which
-- CIM class each feature class becomes, which CIM attribute each field
-- feeds, and the CIM/XML profile config). The BEHAVIORAL half -- the
-- ConnectivityNode synthesis, conductor-end geometry disambiguation, mRID
-- derivation, RDF/XML serialization -- stays in Go (api/internal/export),
-- since it can't be expressed as data.
--
-- Populated by seed/cim_mapping_example.sql (the default CDPSM/CIM16
-- mapping), NOT by this migration -- same split as the other metadata
-- catalogs (a migration creates the shape; a seed authors the content).
-- No admin UI yet (deferred): edited via SQL/seed for now.
--
-- Lean catalog style matching metadata.material_specs (0021): uuid PK,
-- created_at, CHECKs, thorough COMMENTs; no metadata_version_id -- the CIM
-- mapping versions with the application, not with a metadata version set.

-- cim_profiles -- one row per CIM target the export can emit. v1 seeds a
-- single CDPSM/CIM16 profile; the export request names one (defaulting to
-- the is_default row). Holds the namespace/authority URIs the RDF/XML
-- header (md:FullModel) needs, the coordinate CRS for the GL profile, and
-- the policy for feature attributes that have no CIM mapping.
CREATE TABLE metadata.cim_profiles (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code                 text NOT NULL UNIQUE,
    name                 text NOT NULL,
    cim_namespace        text NOT NULL,
    profile_uri          text NOT NULL,
    model_authority      text NOT NULL,
    coordinate_crs       text NOT NULL DEFAULT 'urn:ogc:def:crs:EPSG::4326',
    unmapped_attr_policy text NOT NULL DEFAULT 'drop'
                             CHECK (unmapped_attr_policy IN ('drop', 'carry')),
    is_default           boolean NOT NULL DEFAULT false,
    created_at           timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE metadata.cim_profiles IS 'A CIM/XML export target (e.g. CDPSM_CIM16) -- the namespace/authority URIs and config a CIM RDF/XML document needs. The export request names one; is_default is used when none is given.';
COMMENT ON COLUMN metadata.cim_profiles.cim_namespace IS 'The CIM schema namespace URI, e.g. http://iec.ch/TC57/2013/CIM-schema-cim16# -- the rdf namespace every CIM class in the output is qualified by.';
COMMENT ON COLUMN metadata.cim_profiles.profile_uri IS 'The profile identifier (e.g. the CDPSM/IEC 61968-13 profile URI) written into the md:FullModel header, declaring which CIM profile the file conforms to.';
COMMENT ON COLUMN metadata.cim_profiles.model_authority IS 'The Model Authority Set URI for md:FullModel.modelingAuthoritySet -- identifies who produced the model (this utility).';
COMMENT ON COLUMN metadata.cim_profiles.coordinate_crs IS 'CRS for the GL profile Location/PositionPoint coordinates. Data is stored EPSG:4326, so this defaults to the WGS84 URN.';
COMMENT ON COLUMN metadata.cim_profiles.unmapped_attr_policy IS 'What to do with a feature attribute that has no cim_attribute_mappings row: drop it (v1 default -- the mapping is the contract of what is carried) or carry it as a generic cim:Name/UserAttribute (not implemented yet).';
COMMENT ON COLUMN metadata.cim_profiles.is_default IS 'The profile used when an export request names none. At most one should be true (convention, not enforced -- v1 seeds exactly one profile).';

-- cim_class_mappings -- feature class (optionally per subtype) -> CIM class.
-- cim_kind is the behavioral axis the encoder switches on (an edge gets two
-- synthesized terminals; a junction draws terminals from metadata.terminals;
-- a container becomes an EquipmentContainer; asset/protection/skip are
-- non-conducting). A row with subtype_id = NULL is the class default; a row
-- with a subtype_id overrides it for that subtype (e.g. DIST_SWITCH ->
-- Breaker by default, LoadBreakSwitch for one subtype).
CREATE TABLE metadata.cim_class_mappings (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    feature_class_id  uuid NOT NULL REFERENCES metadata.feature_classes(id) ON DELETE CASCADE,
    subtype_id        uuid REFERENCES metadata.subtypes(id) ON DELETE CASCADE,
    cim_class         text NOT NULL,
    cim_kind          text NOT NULL CHECK (cim_kind IN (
                          'conducting_edge', 'conducting_junction',
                          'container', 'asset', 'protection', 'skip')),
    created_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (feature_class_id, subtype_id)
);

COMMENT ON TABLE metadata.cim_class_mappings IS 'Maps a feature class (optionally a specific subtype) to a CIM class + kind. subtype_id NULL is the class default; a subtype-scoped row overrides it. Drives Phase 16 CIM/XML export.';
COMMENT ON COLUMN metadata.cim_class_mappings.subtype_id IS 'Optional: overrides the mapping for one subtype (e.g. a DIST_SWITCH subtype that is a LoadBreakSwitch rather than the default Breaker). NULL = applies to every subtype of the class.';
COMMENT ON COLUMN metadata.cim_class_mappings.cim_class IS 'The CIM class name (unqualified, e.g. ACLineSegment, PowerTransformer, Breaker) the feature becomes; qualified by the profile''s cim_namespace at write time.';
COMMENT ON COLUMN metadata.cim_class_mappings.cim_kind IS 'How the encoder treats the feature: conducting_edge (line equipment, 2 synthesized terminals), conducting_junction (point equipment, terminals from metadata.terminals), container (EquipmentContainer), asset (non-conducting structure, e.g. Pole), protection (ProtectionEquipment), skip (not exported).';

-- cim_attribute_mappings -- a feature field -> a CIM attribute, with the
-- coercion needed to bridge this app's typed columns and CIM's fixed typed
-- vocabulary. Only mapped fields are carried (v1 drops the rest). A field
-- may map to more than one CIM attribute (e.g. a conductor spec feeding both
-- ACLineSegment.r and .x), hence the (field_id, cim_attribute) uniqueness.
CREATE TABLE metadata.cim_attribute_mappings (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    field_id       uuid NOT NULL REFERENCES metadata.fields(id) ON DELETE CASCADE,
    cim_attribute  text NOT NULL,
    transform      text NOT NULL DEFAULT 'direct'
                       CHECK (transform IN (
                           'direct', 'scale', 'domain_map',
                           'boolean', 'from_geometry', 'material_spec')),
    scale_factor   numeric,
    unit           text,
    value_map      jsonb,
    created_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (field_id, cim_attribute)
);

COMMENT ON TABLE metadata.cim_attribute_mappings IS 'Maps one feature field to one CIM attribute (dotted CIM class.attribute, e.g. BaseVoltage.nominalVoltage, Switch.normalOpen), with a transform bridging the type/unit gap. Only mapped fields are carried into CIM export; unmapped fields follow the profile''s unmapped_attr_policy (drop, v1).';
COMMENT ON COLUMN metadata.cim_attribute_mappings.cim_attribute IS 'Target CIM attribute as ClassName.attributeName (e.g. ACLineSegment.length). BaseVoltage.nominalVoltage is special: it feeds the shared BaseVoltage object the equipment references, not a literal on the equipment.';
COMMENT ON COLUMN metadata.cim_attribute_mappings.transform IS 'How the field value is coerced: direct (as-is), scale (x scale_factor, e.g. km->m), domain_map (value_map lookup, e.g. 11KV->11000), boolean (value_map coded->bool, e.g. OPEN->true), from_geometry (derive from the feature geometry, e.g. conductor length -- field is a placeholder), material_spec (resolve the field''s domain value to metadata.material_specs, Slice 3, for r/x/ampacity).';
COMMENT ON COLUMN metadata.cim_attribute_mappings.scale_factor IS 'Multiplier for transform = scale. NULL otherwise.';
COMMENT ON COLUMN metadata.cim_attribute_mappings.unit IS 'Informational unit of the resulting CIM value (e.g. m, V, ohm) -- documents the mapping; not written into the file (CIM attributes carry implicit SI units).';
COMMENT ON COLUMN metadata.cim_attribute_mappings.value_map IS 'JSONB lookup for transform = domain_map or boolean, keyed by the field''s coded value (e.g. {"11KV": 11000, "33KV": 33000} or {"OPEN": true, "CLOSED": false}).';
