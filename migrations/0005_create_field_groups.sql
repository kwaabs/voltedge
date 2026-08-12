-- 0005_create_field_groups.sql
-- Purpose: field_groups -- the small, reusable set of attribute
-- categories (general, electrical, maintenance, asset) that fields
-- belong to. Global, not redefined per feature class; drives the edit
-- form's tab/section layout.

CREATE TABLE metadata.field_groups (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code       text NOT NULL UNIQUE,
    name       text NOT NULL,
    sort_order integer NOT NULL DEFAULT 0,
    icon       text,
    created_at timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE metadata.field_groups IS 'Reusable attribute categories (general, electrical, maintenance, asset) that metadata.fields.group_id points at. The edit form renders one section per group that has a populated field on a given feature class; an empty group is simply not rendered.';
COMMENT ON COLUMN metadata.field_groups.code IS 'Stable machine identifier, e.g. general, electrical, maintenance, asset.';
COMMENT ON COLUMN metadata.field_groups.name IS 'Display label for the form section, e.g. "Electrical".';
COMMENT ON COLUMN metadata.field_groups.sort_order IS 'Order groups appear in the edit form -- not enforced, a UI hint.';
COMMENT ON COLUMN metadata.field_groups.icon IS 'Optional icon identifier for the form UI. Free text, no enforced icon set yet.';
