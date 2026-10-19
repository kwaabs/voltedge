-- 0150_fields_default_value.sql
-- A field-level DEFAULT value: the value a feature gets for this field when a
-- create omits it. Complements the existing per-subtype override
-- (metadata.subtype_field_rules.default_override): the effective default is the
-- subtype override when present, else this field-level default.
--
-- Stored as text and cast to the field's data_type at write time, exactly like
-- default_override (a coded/domain field stores the domain VALUE CODE here, e.g.
-- 'CLOSED'; a numeric field a number as text; a boolean 'true'/'false'). Null =
-- no default.
--
-- PURE UI/metadata, read live by the create path (api/internal/crud
-- validate.go injectDefaults) and the schema endpoint -- NOT load-bearing for
-- the compiler or the physical table, so NO recompile is needed. Create-only: an
-- update is a partial write and never resurrects a default for a field the user
-- cleared.

ALTER TABLE metadata.fields ADD COLUMN default_value text;

COMMENT ON COLUMN metadata.fields.default_value IS
    'Optional field-level default value, as text (cast to the field''s data_type at write time; a coded/domain field stores the domain value code). Applied on create when the field is omitted. Overridden per subtype by subtype_field_rules.default_override. Read live -- no recompile.';
