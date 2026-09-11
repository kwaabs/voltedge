-- 0061_seed_map_annotation_permissions.sql
-- Purpose: Phase 36 Slice 2 -- the annotations:read / annotations:write
-- capability pair that gates the map-annotation (redlining) API added in this
-- slice (api/internal/mapannotation, mounted at /map-annotations). Follows the
-- 0025/0041 pattern exactly: the permission catalog is data (iam.permissions),
-- enforced in Go by auth.RequirePermission via the PermAnnotations{Read,Write}
-- constants in api/internal/auth/rbac.go.
--
-- Why its own capability, not features:read/write: an annotation points at no
-- feature and is never compiled -- it is map furniture (the geographic sibling
-- of runtime.diagram_elements), so redlining is a distinct act from editing
-- asset data. Keeping it separate means an operator could be allowed to mark up
-- the map without also holding feature-edit rights, and vice versa -- a grant,
-- not a code change, on brand with 0025's "authorization as data".
--
-- Grants mirror attachments (0041): everyone who can see the map reads the
-- redline layer; whoever can edit features writes it. The 0025 admin CROSS JOIN
-- already ran, so the admin/break-glass grants are added explicitly here.

INSERT INTO iam.permissions (key, description) VALUES
    ('annotations:read',  'View map annotations (the redlining/markup layer).'),
    ('annotations:write', 'Create, edit, and delete map annotations (redlining/markup).')
ON CONFLICT (key) DO NOTHING;

-- read: everyone who can see the map (viewer/analyst/editor + admins).
INSERT INTO iam.role_permissions (role_id, permission_key)
SELECT r.id, 'annotations:read'
FROM iam.roles r
WHERE r.key IN ('viewer', 'analyst', 'editor', 'admin', 'break-glass-admin')
ON CONFLICT DO NOTHING;

-- write: whoever can edit features (editor + admins), mirroring features:write.
INSERT INTO iam.role_permissions (role_id, permission_key)
SELECT r.id, 'annotations:write'
FROM iam.roles r
WHERE r.key IN ('editor', 'admin', 'break-glass-admin')
ON CONFLICT DO NOTHING;
