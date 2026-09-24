-- 0115_seed_asset_tag_permissions.sql
-- Purpose: the tags:read / tags:write capability pair that gates the asset-tag
-- API (api/internal/assettag, mounted at /tags). Follows the OMS (0088), WAM
-- (0097) and faults (0107) pattern exactly: the permission catalog is data
-- (iam.permissions), enforced in Go by auth.RequirePermission via the
-- PermTags{Read,Write} constants in api/internal/auth/rbac.go.
--
-- WHY ITS OWN CAPABILITY, not features:*:
--   Resolving a scanned code is a read anyone in the field needs -- a technician
--   with no feature-edit rights still scans an asset to see what it is -- so
--   tags:read sits at the viewer tier, alongside faults:read/oms:read/wam:read.
--   Registering a legacy number, voiding a damaged tag, promoting a primary, or
--   reissuing are tag-lifecycle acts, not feature edits, so tags:write is its own
--   capability at the editor tier rather than being folded into features:write.
--   Authorization stays data, the same split the operational subsystems drew.
--
-- Grants mirror faults/oms/wam: everyone who can see the map reads/scans tags
-- (viewer/analyst/editor + admins); whoever can edit features manages them
-- (editor + admins). The 0025 admin CROSS JOIN already ran, so the admin/
-- break-glass grants are added explicitly here. The field-technician role
-- (migration 0099) gets tags:read too -- scanning in the field is its whole
-- point -- matching the faults:read grant it received in 0110.

INSERT INTO iam.permissions (key, description) VALUES
    ('tags:read',  'Resolve scanned asset tags and view the tags on a feature.'),
    ('tags:write', 'Register legacy tags, set the primary, void, and reissue asset tags.')
ON CONFLICT (key) DO NOTHING;

-- read/scan: everyone who can see the map (viewer/analyst/editor + admins) and
-- the field-technician role.
INSERT INTO iam.role_permissions (role_id, permission_key)
SELECT r.id, 'tags:read'
FROM iam.roles r
WHERE r.key IN ('viewer', 'analyst', 'editor', 'admin', 'break-glass-admin', 'field-technician')
ON CONFLICT DO NOTHING;

-- write/manage: whoever can edit features (editor + admins), mirroring
-- features:write.
INSERT INTO iam.role_permissions (role_id, permission_key)
SELECT r.id, 'tags:write'
FROM iam.roles r
WHERE r.key IN ('editor', 'admin', 'break-glass-admin')
ON CONFLICT DO NOTHING;
