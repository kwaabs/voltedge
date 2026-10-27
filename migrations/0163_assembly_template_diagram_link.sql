-- 0163_assembly_template_diagram_link.sql
-- Purpose: make the kit -> single-line-template link DATA-DRIVEN. The general
-- assembly engine's auto single-line (api/internal/assembly/autodiagram.go) maps
-- a kit config code to a runtime.diagram_templates name through a HARDCODED Go map
-- (assemblyDiagramTemplates). That means an in-app-authored kit -- whose config
-- the Go binary has never heard of -- could never get a diagram. Move the link to
-- a column so a new kit can name (or auto-generate) its own template.
--
-- Nullable: a kit with no diagram_template_name simply gets no auto single-line
-- (not an error), exactly as an unknown config does today.

ALTER TABLE metadata.assembly_templates
    ADD COLUMN IF NOT EXISTS diagram_template_name text;

COMMENT ON COLUMN metadata.assembly_templates.diagram_template_name IS
    'The runtime.diagram_templates row (by name) whose typed slots match this kit''s components, used to auto-build the kit''s single-line on placement (api/internal/assembly/autodiagram.go). NULL = no auto single-line. Replaces the former hardcoded config->template Go map so an in-app-authored kit can link or generate its own.';

-- Backfill the built-in kits from the former hardcoded map so behaviour is
-- unchanged once the engine reads this column. config codes are globally distinct
-- here, so matching on code alone is safe. Idempotent (only fills NULLs).
UPDATE metadata.assembly_templates at
SET diagram_template_name = m.tmpl
FROM (VALUES
    ('POLE_MOUNT_STD',       'Pole-mount transformer (overhead)'),
    ('POLE_MOUNT_RECLOSER',  'Pole-mount recloser'),
    ('POLE_MOUNT_CAP_BANK',  'Pole-mount capacitor bank'),
    ('POLE_MOUNT_REGULATOR', 'Pole-mount voltage regulator'),
    ('POLE_MOUNT_BESS',      'Pole-mount battery storage'),
    ('PAD_MOUNT_URD',        'Pad-mount transformer + feeder pillar (URD)'),
    ('WAYS_4',               'Feeder pillar — 4-way'),
    ('WAYS_6',               'Feeder pillar — 6-way'),
    ('PACKED_RMU_FIXED',     'Packed station — RMU + transformer + pillar'),
    ('PACKED_RMU_MOBILE',    'Packed station — RMU + transformer + pillar'),
    ('PACKED_XFMR_FIXED',    'Packed station — transformer + pillar'),
    ('PACKED_XFMR_MOBILE',   'Packed station — transformer + pillar')
) AS m(code, tmpl)
WHERE at.code = m.code AND at.diagram_template_name IS NULL;
