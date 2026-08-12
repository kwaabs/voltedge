-- 0008_create_subtypes_and_fields.sql
-- Purpose: subtypes, fields, and subtype_field_rules -- the shape of a
-- feature class. subtypes partitions a class (e.g. overhead vs
-- underground conductor); fields lists its attributes; subtype_field_rules
-- says which fields are required for which subtype, and can override a
-- field's default per subtype.

CREATE TABLE metadata.subtypes (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    feature_class_id  uuid NOT NULL REFERENCES metadata.feature_classes(id) ON DELETE CASCADE,
    code              integer NOT NULL,
    name              text NOT NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (feature_class_id, code)
);

COMMENT ON TABLE metadata.subtypes IS 'A schema-level partition of a feature class, e.g. MV_CONDUCTOR has overhead_mv_conductor and underground_mv_conductor. Drives which fields are required (subtype_field_rules) and which symbology/terminals apply.';
COMMENT ON COLUMN metadata.subtypes.code IS 'Small integer subtype code, matching the Esri/G-Tech convention of a numeric subtype key stored on the feature row itself.';
COMMENT ON COLUMN metadata.subtypes.name IS 'Machine-readable subtype name, e.g. overhead_mv_conductor.';

CREATE TABLE metadata.fields (
    id                       uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    feature_class_id         uuid NOT NULL REFERENCES metadata.feature_classes(id) ON DELETE CASCADE,
    name                     text NOT NULL,
    data_type                text NOT NULL CHECK (data_type IN ('text', 'numeric', 'integer', 'boolean', 'date', 'timestamp', 'uuid', 'jsonb')),
    domain_id                uuid REFERENCES metadata.domains(id),
    group_id                 uuid REFERENCES metadata.field_groups(id),
    nullable                 boolean NOT NULL DEFAULT true,
    is_computed              boolean NOT NULL DEFAULT false,
    status                   text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'deprecated')),
    visible_on_create        boolean NOT NULL DEFAULT true,
    visible_on_update        boolean NOT NULL DEFAULT true,
    editable_after_publish   boolean NOT NULL DEFAULT true,
    created_at               timestamptz NOT NULL DEFAULT now(),
    UNIQUE (feature_class_id, name)
);

COMMENT ON TABLE metadata.fields IS 'One row per attribute on a feature class. Adding a row here (then publishing) is what ALTER TABLE ADD COLUMNs the physical table -- this is the schema compiler''s real input, not hand-written DDL.';
COMMENT ON COLUMN metadata.fields.name IS 'Physical column name once compiled, e.g. voltage_class, phase_designation.';
COMMENT ON COLUMN metadata.fields.data_type IS 'Maps directly to the generated column''s Postgres type.';
COMMENT ON COLUMN metadata.fields.domain_id IS 'Optional: constrains legal values to a coded/range domain. Changing or clearing this on an existing field requires the publish gate to re-validate existing data first.';
COMMENT ON COLUMN metadata.fields.group_id IS 'Which field_groups section this field appears under in the edit form. Null means it renders outside any group (rare, discouraged).';
COMMENT ON COLUMN metadata.fields.nullable IS 'Whether the generated column allows NULL. Requiredness *by subtype* is a separate, finer-grained question -- see subtype_field_rules.';
COMMENT ON COLUMN metadata.fields.is_computed IS 'True for derived/read-only fields (e.g. length_m via ST_Length). Never accepted on create/update from the API regardless of visible_on_create/update.';
COMMENT ON COLUMN metadata.fields.status IS 'active = in use. deprecated = hidden from forms/API, data retained, physical column NOT dropped automatically -- that is a separate, deliberate action.';
COMMENT ON COLUMN metadata.fields.visible_on_create IS 'Whether this field appears on the create form. Independent of visible_on_update and of subtype_field_rules.is_required.';
COMMENT ON COLUMN metadata.fields.visible_on_update IS 'Whether this field appears on the update/edit form.';
COMMENT ON COLUMN metadata.fields.editable_after_publish IS 'Whether a user can still change this field once the feature row has left draft state. False for things like conductor_material that should lock after commissioning.';

CREATE TABLE metadata.subtype_field_rules (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    subtype_id        uuid NOT NULL REFERENCES metadata.subtypes(id) ON DELETE CASCADE,
    field_id          uuid NOT NULL REFERENCES metadata.fields(id) ON DELETE CASCADE,
    is_required       boolean NOT NULL DEFAULT false,
    default_override  text,
    created_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (subtype_id, field_id)
);

COMMENT ON TABLE metadata.subtype_field_rules IS 'Per-subtype override of a field''s requiredness (and optionally its default). E.g. duct_bank_id is required only for the underground_mv_conductor subtype of MV_CONDUCTOR. A field with no row here for a given subtype is optional by default.';
COMMENT ON COLUMN metadata.subtype_field_rules.is_required IS 'Whether this field is required specifically when the feature is this subtype.';
COMMENT ON COLUMN metadata.subtype_field_rules.default_override IS 'Optional default value for this field, specific to this subtype, as text (cast at write time to the field''s data_type). Null means no override.';
