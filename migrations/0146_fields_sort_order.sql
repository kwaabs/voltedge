-- 0146_fields_sort_order.sql
-- Give each field an explicit per-class DISPLAY order so an admin can reorder
-- the attribute form, instead of the hard-coded alphabetical-by-name order
-- (metadata.Load's ORDER BY name) every form has used until now.
--
-- This is PURE UI metadata: the schema endpoint (GET /feature-classes/{code}/
-- schema) reads it live and the create/edit forms render in this order, but the
-- compiler never looks at field order (physical column order in the generated
-- gis.* table is irrelevant to every reader). So adding/using this column
-- requires NO recompile of any feature class -- same "read live, no recompile"
-- nature as subtype_field_rules.is_required.
--
-- Ordering is per feature class (one order for the class's form), ties broken by
-- name. Admins reorder via PATCH-equivalent PUT /admin/feature-classes/{code}/
-- fields/order.

ALTER TABLE metadata.fields ADD COLUMN sort_order integer NOT NULL DEFAULT 0;

-- Backfill each class's CURRENT (alphabetical-by-name) order into sort_order, so
-- nothing visibly reorders until an admin explicitly drags a field: a 1-based
-- ordinal per feature class, matching exactly what the forms show today.
WITH ordered AS (
    SELECT id, row_number() OVER (PARTITION BY feature_class_id ORDER BY name) AS rn
    FROM metadata.fields
)
UPDATE metadata.fields f
SET sort_order = o.rn
FROM ordered o
WHERE o.id = f.id;

COMMENT ON COLUMN metadata.fields.sort_order IS
    'Display order of the field within its feature class''s form (ascending, ties broken by name). Pure UI metadata read live by GET /feature-classes/{code}/schema -- NOT load-bearing for the compiler or the physical table, so changing it needs no recompile. Admin reorders via PUT /admin/feature-classes/{code}/fields/order.';
