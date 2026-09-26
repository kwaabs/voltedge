-- 0120_create_cim_reference_dictionary.sql
-- Purpose: a machine-readable REFERENCE dictionary of the CIM vocabulary, so the
-- CIM export mapping (Phase 16, migration 0034) is no longer limited to
-- hand-typed cim_class / cim_attribute strings with no validation and no
-- discoverability. Until now those two columns were free text: an admin editing
-- metadata.cim_class_mappings / cim_attribute_mappings could type any string,
-- valid CDPSM/CIM16 or not, and had no list of what CIM even offers.
--
-- These tables hold the FULL CIM class + attribute complement (the reference
-- layer), parsed from an authoritative machine-readable schema file (NOT authored
-- by hand -- see seed/cim_reference_*.sql, generated from schema/cim/). On top of
-- that, a PROFILE-membership layer marks which classes/attributes belong to a
-- given profile (e.g. CDPSM/CIM16), so the mapping editor can DEFAULT to the safe
-- in-profile subset and let an admin opt into the wider CIM knowingly. This is the
-- "both, layered" model: full CIM as reference, CDPSM/CIM16 flagged within it.
--
-- Read-only reference data. It does NOT change what the encoder emits (that is
-- still driven by cim_class_mappings + the behavioral Go in api/internal/export);
-- it validates and guides the mappings that feed the encoder. Same catalog style
-- as metadata.cim_profiles (0034): uuid PK, created_at, thorough COMMENTs, no
-- metadata_version_id -- the CIM dictionary versions with the application.
--
-- Populated by seed/cim_reference_classes.sql / cim_reference_attributes.sql /
-- cim_reference_profile_members.sql, each GENERATED from the schema file(s) under
-- schema/cim/ -- NOT by this migration (a migration creates the shape; a seed
-- authors the content). Every row is traceable to its source file via `source`.

-- cim_ref_classes -- one row per CIM class in the reference schema (the full CIM
-- complement, a superset of any single profile). Self-referential by `parent_name`
-- to carry the subClassOf hierarchy (e.g. ACLineSegment -> Conductor ->
-- ConductingEquipment -> Equipment -> PowerSystemResource -> IdentifiedObject).
CREATE TABLE metadata.cim_ref_classes (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name         text NOT NULL UNIQUE,
    parent_name  text,
    namespace    text NOT NULL,
    is_abstract  boolean NOT NULL DEFAULT false,
    description  text,
    source       text NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE metadata.cim_ref_classes IS 'Reference dictionary of CIM classes (the full CIM complement), parsed from an authoritative schema under schema/cim/. Read-only; validates and guides the cim_class_mappings that drive CIM export. Not authored by hand -- see seed/cim_reference_classes.sql.';
COMMENT ON COLUMN metadata.cim_ref_classes.name IS 'CIM class name, unqualified (e.g. ACLineSegment, PowerTransformer). Qualified by the profile namespace at export time. The natural key referenced by cim_ref_attributes and cim_ref_profile_members.';
COMMENT ON COLUMN metadata.cim_ref_classes.parent_name IS 'Superclass name (rdfs:subClassOf). NULL for a root class (e.g. IdentifiedObject). Carries the CIM class hierarchy for the editor and for validity checks (e.g. is this a ConductingEquipment?).';
COMMENT ON COLUMN metadata.cim_ref_classes.namespace IS 'The schema namespace URI the class is defined in (e.g. http://iec.ch/TC57/2013/CIM-schema-cim16#), so classes from different CIM versions can coexist and be distinguished.';
COMMENT ON COLUMN metadata.cim_ref_classes.is_abstract IS 'True if the class is abstract in the schema (cannot be instantiated directly, e.g. ConductingEquipment) -- the editor should not offer it as a concrete mapping target.';
COMMENT ON COLUMN metadata.cim_ref_classes.source IS 'Provenance: the schema file (and version) this row was parsed from, e.g. schema/cim/IEC_CIM.ttl. Every reference row is traceable to a file, never hand-typed.';

-- cim_ref_attributes -- one row per property of a CIM class: datatype properties
-- (attributes, is_association=false, e.g. ACLineSegment.length) and object
-- properties (associations, is_association=true, e.g. Terminal.ConnectivityNode).
CREATE TABLE metadata.cim_ref_attributes (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    class_name     text NOT NULL REFERENCES metadata.cim_ref_classes(name) ON DELETE CASCADE,
    name           text NOT NULL,
    is_association boolean NOT NULL DEFAULT false,
    range          text,
    multiplicity   text,
    description    text,
    source         text NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (class_name, name)
);

COMMENT ON TABLE metadata.cim_ref_attributes IS 'Reference dictionary of CIM class properties (attributes and associations), parsed from schema/cim/. Read-only; lets the mapping editor offer the real attributes of a chosen CIM class and validate cim_attribute strings.';
COMMENT ON COLUMN metadata.cim_ref_attributes.class_name IS 'The owning CIM class (FK to cim_ref_classes.name). A ClassName.attribute mapping target is (class_name, name).';
COMMENT ON COLUMN metadata.cim_ref_attributes.is_association IS 'False = datatype property (a literal attribute, e.g. length:Float). True = object property (an association to another class, e.g. BaseVoltage). The export encoder synthesizes most associations behaviorally; this flag lets the editor distinguish them.';
COMMENT ON COLUMN metadata.cim_ref_attributes.range IS 'The property range: a CIM datatype (e.g. Float, String, Boolean) for an attribute, or the target class name for an association. NULL if the schema does not state it.';
COMMENT ON COLUMN metadata.cim_ref_attributes.multiplicity IS 'Cardinality as stated in the schema, e.g. 0..1, 1..1, 0..*. NULL if not available.';
COMMENT ON COLUMN metadata.cim_ref_attributes.source IS 'Provenance: the schema file this row was parsed from. Never hand-typed.';

-- cim_ref_profiles -- the profiles whose membership is recorded below. At least
-- one (CDPSM/CIM16) so the mapping editor can default to the in-profile subset.
CREATE TABLE metadata.cim_ref_profiles (
    code        text PRIMARY KEY,
    name        text NOT NULL,
    source      text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE metadata.cim_ref_profiles IS 'The CIM profiles whose class/attribute membership is recorded in cim_ref_profile_members (e.g. CDPSM_CIM16). Distinct from metadata.cim_profiles (which is the export TARGET config); this is the reference-side notion of "which classes does this profile include".';
COMMENT ON COLUMN metadata.cim_ref_profiles.code IS 'Profile code, ideally matching a metadata.cim_profiles.code (e.g. CDPSM_CIM16) so the editor can tie "in the profile you are exporting" to this membership set.';
COMMENT ON COLUMN metadata.cim_ref_profiles.source IS 'The profile schema/RDFS file the membership was parsed from (e.g. schema/cim/CPSM_2013_CIM_16.rdf).';

-- cim_ref_profile_members -- which reference classes/attributes belong to a
-- profile. A row with attribute_name NULL means the whole class is in-profile;
-- a row with attribute_name set marks one property as in-profile. Deliberately
-- NOT FK-constrained to cim_ref_classes/_attributes: the full-CIM reference is a
-- superset so membership should always resolve, but keeping it unconstrained lets
-- the two seeds load in either order and tolerates a profile referencing a class
-- the reference dump happens to omit (surfaced as a coverage gap rather than a
-- load failure).
CREATE TABLE metadata.cim_ref_profile_members (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    profile_code   text NOT NULL REFERENCES metadata.cim_ref_profiles(code) ON DELETE CASCADE,
    class_name     text NOT NULL,
    attribute_name text,
    created_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (profile_code, class_name, attribute_name)
);

COMMENT ON TABLE metadata.cim_ref_profile_members IS 'Marks which reference classes/attributes are in a given profile (e.g. CDPSM_CIM16), the "layered" half of the dictionary: full CIM in cim_ref_classes, the profile subset flagged here. The mapping editor defaults to in-profile targets and lets an admin opt into the wider CIM knowingly.';
COMMENT ON COLUMN metadata.cim_ref_profile_members.class_name IS 'The in-profile CIM class (matches cim_ref_classes.name). Always set.';
COMMENT ON COLUMN metadata.cim_ref_profile_members.attribute_name IS 'NULL = the whole class is in the profile. Set = a specific attribute of the class is in the profile (matches cim_ref_attributes.name for this class).';

CREATE INDEX cim_ref_attributes_class_idx      ON metadata.cim_ref_attributes (class_name);
CREATE INDEX cim_ref_classes_parent_idx        ON metadata.cim_ref_classes (parent_name);
CREATE INDEX cim_ref_profile_members_class_idx ON metadata.cim_ref_profile_members (profile_code, class_name);
