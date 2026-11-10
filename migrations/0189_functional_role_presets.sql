-- 0189_functional_role_presets.sql
-- Purpose: functional, job-family ROLE presets on top of the generic access tiers
-- (viewer / analyst / editor / admin). As VoltEdge grew from GIS+analysis into a
-- network-operations suite (OMS, WAM, operations, safety, faults, telemetry…), the
-- viewer→editor→admin ladder stopped matching how utilities actually divide work:
-- an operator, an engineer, a field crew and an asset manager need very different
-- slices. These presets bundle the permissions per discipline.
--
-- They are is_system = FALSE on purpose: unlike the enforcement-critical tiers,
-- these are STARTER roles an admin owns and tunes (or clones) in the role editor
-- (api/internal/auth roleadmin.go, web /admin/roles). Grants are matched by key
-- PATTERN where a whole domain belongs to a role, so the intent reads clearly and
-- a missed key is a tweak in the editor, not a broken role. Idempotent (ON CONFLICT
-- DO NOTHING); re-running never clobbers an admin's later edits.
--
-- admin / break-glass-admin already hold every permission; these presets sit
-- alongside them, not above. Territory scoping (Phase 58) is orthogonal — a role
-- says WHAT, a territory grant says WHERE; none of these presets get
-- territories:unscoped (that stays a deliberate per-user elevation).

INSERT INTO iam.roles (key, name, description, is_system) VALUES
    ('engineering',      'Engineering & Design', 'Design the network and run studies: edit features/connectivity/relationships/diagrams/assemblies, trace and analysis, circuits. No device operation or admin.', false),
    ('operations',       'Operations & Control', 'Run the grid: outage management, device operations, switching safety (tags/grounds/clearances), faults and live telemetry, with trace/analysis. Reads the model but does not edit it.', false),
    ('asset-management', 'Asset & Maintenance',  'Manage assets and work: defects, work orders, inspections, and asset tags. Reads the network.', false),
    ('planning',         'Planning',             'Study the network read-only: trace and analysis (load growth, hosting capacity) over the published model and diagrams.', false),
    ('data-gis',         'Data & GIS',           'Author the data and the map: the metadata catalog, feature edits, imports, symbology, basemaps, diagrams, circuits and tags.', false),
    ('system-admin',     'System Administrator', 'Platform administration: users, roles, auth settings, the database explorer, field packs, sync review and territory access — without the full network-edit surface.', false)
ON CONFLICT (key) DO NOTHING;

-- Engineering & Design --------------------------------------------------------
INSERT INTO iam.role_permissions (role_id, permission_key)
SELECT r.id, p.key FROM iam.roles r, iam.permissions p
WHERE r.key = 'engineering' AND (
    p.key IN ('features:read','features:write','trace:run','analysis:run','circuits:write','metadata:read','attachments:read','attachments:write','tags:read')
    OR p.key LIKE 'connectivity:%' OR p.key LIKE 'relationships:%' OR p.key LIKE 'diagrams:%'
    OR p.key LIKE 'assemblies:%' OR p.key LIKE 'annotations:%')
ON CONFLICT DO NOTHING;

-- Operations & Control --------------------------------------------------------
INSERT INTO iam.role_permissions (role_id, permission_key)
SELECT r.id, p.key FROM iam.roles r, iam.permissions p
WHERE r.key = 'operations' AND (
    p.key LIKE 'oms:%' OR p.key LIKE 'operations:%' OR p.key LIKE 'safety:%' OR p.key LIKE 'faults:%'
    OR p.key IN ('telemetry:read','trace:run','analysis:run','features:read','connectivity:read','relationships:read','wam:read','annotations:read','attachments:read'))
ON CONFLICT DO NOTHING;

-- Asset & Maintenance ---------------------------------------------------------
INSERT INTO iam.role_permissions (role_id, permission_key)
SELECT r.id, p.key FROM iam.roles r, iam.permissions p
WHERE r.key = 'asset-management' AND (
    p.key LIKE 'wam:%' OR p.key LIKE 'tags:%'
    OR p.key IN ('faults:read','features:read','metadata:read','attachments:read','attachments:write'))
ON CONFLICT DO NOTHING;

-- Planning --------------------------------------------------------------------
INSERT INTO iam.role_permissions (role_id, permission_key)
SELECT r.id, p.key FROM iam.roles r, iam.permissions p
WHERE r.key = 'planning' AND
    p.key IN ('features:read','connectivity:read','relationships:read','diagrams:read','annotations:read','metadata:read','trace:run','analysis:run')
ON CONFLICT DO NOTHING;

-- Data & GIS ------------------------------------------------------------------
INSERT INTO iam.role_permissions (role_id, permission_key)
SELECT r.id, p.key FROM iam.roles r, iam.permissions p
WHERE r.key = 'data-gis' AND (
    p.key IN ('metadata:read','metadata:write','features:read','features:write','circuits:write','tags:read','tags:write','attachments:read','attachments:write','connectivity:read','relationships:read')
    OR p.key LIKE 'diagrams:%' OR p.key LIKE 'annotations:%' OR p.key LIKE 'assemblies:%')
ON CONFLICT DO NOTHING;

-- System Administrator --------------------------------------------------------
INSERT INTO iam.role_permissions (role_id, permission_key)
SELECT r.id, p.key FROM iam.roles r, iam.permissions p
WHERE r.key = 'system-admin' AND (
    p.key LIKE 'admin:%'
    OR p.key IN ('territories:write','features:read','metadata:read','sync:review','sync:post'))
ON CONFLICT DO NOTHING;
