-- 0123_rmu_template_cable.sql
-- Purpose: let an RMU fan-out template define the EXTERNAL cable conductor each
-- switched way gets on its cable side (T1), so a placed RMU comes with real
-- MV_CONDUCTOR cable-stub features -- not a bare, unwired T1 the operator must
-- remember to cable up by hand.
--
-- Background. The RMU fan-out (api/internal/rmu/fanout.go) reads
-- metadata.rmu_template_items (migration 0073) to know which switched ways to
-- create and how to wire each way's bus side (T2) into the internal bus. It has
-- never modelled the way's CABLE side (T1): that leg stayed a normal
-- post-placement connect action. These four nullable columns add the missing
-- half -- the conductor to create on T1 -- exactly mirroring how the way row
-- already names its bus terminal, only for the cable end.
--
-- All four columns are NULLABLE (cable_attributes carries a '{}' default so it
-- is never NULL to scan): a template item with cable_feature_class_id NULL means
-- "this way has no auto-created cable", so every existing row and any future RMU
-- config that omits a cable keeps the prior behaviour untouched. Only rows the
-- seed populates (seed/rmu_templates.sql) activate the new cable step.
--
-- No recompile: metadata.rmu_template_items is a fixed-schema metadata table read
-- by column at request time (like the rest of metadata.*), not a compiled gis.*
-- feature table. NOTE the SEPARATE prerequisite the cable target carries: the
-- MV_CONDUCTOR class must be geometry_optional (seed/mv_conductor_diagram_promote
-- .sql) AND recompiled (`task compile CODE=MV_CONDUCTOR`) so its gis.mv_conductor
-- .geom drops NOT NULL -- otherwise the geometry-less cable create the fan-out
-- issues is rejected. That prerequisite belongs to the conductor class, not this
-- table, so it is not part of this migration.
--
-- Idempotent: ADD COLUMN IF NOT EXISTS, so a re-apply is a no-op.

ALTER TABLE metadata.rmu_template_items
    ADD COLUMN IF NOT EXISTS cable_feature_class_id uuid REFERENCES metadata.feature_classes (id),
    ADD COLUMN IF NOT EXISTS cable_subtype_code integer,
    ADD COLUMN IF NOT EXISTS cable_attributes jsonb NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS cable_terminal text;

COMMENT ON COLUMN metadata.rmu_template_items.cable_feature_class_id IS
    'The conductor class (e.g. MV_CONDUCTOR) the fan-out creates on this way''s cable side. NULL = no auto-created cable for this way.';
COMMENT ON COLUMN metadata.rmu_template_items.cable_subtype_code IS
    'Subtype code for the auto-created cable conductor (e.g. 2 = underground). NULL when cable_feature_class_id is NULL.';
COMMENT ON COLUMN metadata.rmu_template_items.cable_attributes IS
    'Default coded attribute values (jsonb) the auto-created cable needs to pass create-time validation (e.g. voltage_class, condition_rating). ''{}'' when no cable.';
COMMENT ON COLUMN metadata.rmu_template_items.cable_terminal IS
    'The way terminal the cable conductor wires to (e.g. T1, the cable side). NULL when cable_feature_class_id is NULL; the fan-out defaults it to T1.';
