-- 0078_add_diagrams_delete_permission.sql
-- Purpose: split "delete an entire diagram" out of diagrams:write into its own
-- diagrams:delete permission, and add a Supervisor role that holds it. Deleting
-- a whole schematic (cascading to all its placements) is more destructive than
-- the everyday create/rename/place-and-remove an editor does, so it gets a
-- dedicated permission that editor and viewer do NOT receive. Supervisor is a
-- new tier above editor (an editor who can also delete diagrams); admin and
-- break-glass-admin get the new permission too, since they are full-access.
--
-- Enforcement: the DELETE /diagrams/{id} route in api/internal/diagram is gated
-- on diagrams:delete (auth.PermDiagramsDelete); every other diagram route stays
-- on diagrams:read / diagrams:write as before.

-- New permission.
INSERT INTO iam.permissions (key, description) VALUES
    ('diagrams:delete', 'Delete an entire schematic diagram, including all of its placements.')
ON CONFLICT (key) DO NOTHING;

-- diagrams:write no longer implies deleting the whole diagram -- narrow its
-- catalog description to match (deleting is now diagrams:delete). The 0025 seed
-- text said "Create, rename, delete diagrams and place/remove features."
UPDATE iam.permissions
   SET description = 'Create and rename diagrams and place or remove features on them.'
 WHERE key = 'diagrams:write';

-- New Supervisor role: an editor who can also delete diagrams. Sits above
-- editor, below admin. is_system so the user-admin UI treats it as
-- enforcement-critical (won't offer to delete it), same as the other baseline
-- roles seeded in migration 0025.
INSERT INTO iam.roles (key, name, description, is_system) VALUES
    ('supervisor', 'Supervisor', 'Everything an editor can do, plus deleting diagrams.', true)
ON CONFLICT (key) DO NOTHING;

-- Supervisor gets every permission the editor role currently grants ...
INSERT INTO iam.role_permissions (role_id, permission_key)
SELECT sup.id, rp.permission_key
FROM iam.roles sup
JOIN iam.roles ed ON ed.key = 'editor'
JOIN iam.role_permissions rp ON rp.role_id = ed.id
WHERE sup.key = 'supervisor'
ON CONFLICT DO NOTHING;

-- ... plus the new diagrams:delete.
INSERT INTO iam.role_permissions (role_id, permission_key)
SELECT r.id, 'diagrams:delete'
FROM iam.roles r
WHERE r.key = 'supervisor'
ON CONFLICT DO NOTHING;

-- admin and break-glass-admin were granted "every permission" at migration 0025
-- time via a CROSS JOIN over the then-existing catalog; a permission added
-- later is NOT retroactively granted, so grant the new one to them explicitly.
INSERT INTO iam.role_permissions (role_id, permission_key)
SELECT r.id, 'diagrams:delete'
FROM iam.roles r
WHERE r.key IN ('admin', 'break-glass-admin')
ON CONFLICT DO NOTHING;
