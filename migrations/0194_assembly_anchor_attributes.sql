-- 0194_assembly_anchor_attributes.sql
-- Template-level default attributes for an assembly's ANCHOR feature.
--
-- Why: the generic assembly engine (migration 0093) treats the kit `code` as a
-- pure template selector that is NOT stored on the placed anchor. The RMU and
-- packaged-substation engines it is meant to generalise instead store a required,
-- domain-coded config field on the placed unit (rmu_config / package_config,
-- nullable=false) that doubles as their template selector. To let those recipes
-- place through the generic engine WITHOUT the user re-entering that config -- and
-- to remove the double-encoding -- a kit can now carry default attributes that are
-- applied to the anchor at placement (the caller's own attributes still win).
--
-- Applied in api/internal/assembly/fanout.go (merged UNDER CreateAssemblyInput.
-- Attributes before crud.CreateFeature) and authored via api/internal/assembly/
-- authoring.go. Default '{}' so every existing template is a no-op.

ALTER TABLE metadata.assembly_templates
    ADD COLUMN IF NOT EXISTS anchor_attributes jsonb NOT NULL DEFAULT '{}'::jsonb;
