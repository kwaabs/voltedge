-- 0162_seed_assemblies_permissions.sql
-- Purpose: a dedicated capability to AUTHOR assembly kits (the general
-- "place one anchor feature and get its whole pre-wired set of components"
-- recipe -- metadata.assembly_templates / assembly_items, migration 0093).
--
-- Until now a kit could only be defined by seed SQL. The in-app authoring
-- surface (api/internal/assembly authoring endpoints + /admin/assemblies) needs
-- its own gate. A kit is a reusable ARRANGEMENT of EXISTING feature classes and
-- terminals, not a change to the schema itself, so it deliberately sits BELOW
-- metadata:write (catalog/schema authoring) -- a senior editor may compose a kit
-- without being able to edit the data model. Hence a new permission rather than
-- reusing metadata:write, and the grant reaches editor (not just admin).
--
-- assemblies:read gates the catalog list/detail (and the admin page's load);
-- assemblies:write gates create/update/delete/validate. Placing a kit on the map
-- is unchanged -- that still rides features:write + relationships:commit +
-- connectivity:commit on POST /assemblies.

INSERT INTO iam.permissions (key, description) VALUES
    ('assemblies:read',  'View the assembly-kit catalog (place-one-get-a-wired-set recipes).'),
    ('assemblies:write', 'Author assembly kits: define a place-one-get-a-wired-set recipe and its components.')
ON CONFLICT (key) DO NOTHING;

-- Grant to editor, supervisor, admin and break-glass-admin. (admin/break-glass
-- get every permission at 0025, but that was a one-time grant -- a permission
-- added later must be granted explicitly, same as every *_permissions migration
-- since.) Viewer/analyst stay read-only on the rest of the app and get neither.
INSERT INTO iam.role_permissions (role_id, permission_key)
SELECT r.id, p.key
FROM iam.roles r
CROSS JOIN iam.permissions p
WHERE r.key IN ('editor', 'supervisor', 'admin', 'break-glass-admin')
  AND p.key IN ('assemblies:read', 'assemblies:write')
ON CONFLICT DO NOTHING;
