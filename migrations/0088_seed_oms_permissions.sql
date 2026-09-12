-- 0088_seed_oms_permissions.sql
-- Purpose: Phase 49 Slice 1c -- the oms:read / oms:write capability pair that
-- gates the Outage Management API (api/internal/oms, mounted at /oms). Follows
-- the 0061 map-annotation pattern exactly: the permission catalog is data
-- (iam.permissions), enforced in Go by auth.RequirePermission via the
-- PermOMS{Read,Write} constants in api/internal/auth/rbac.go.
--
-- Why its own capability, not features:read/write: an outage is not feature
-- data -- it is an operational transaction over the network (a call logged, an
-- incident confirmed, a restoration recorded). Keeping it separate means a
-- control-room dispatcher can be allowed to run the outage board without also
-- holding feature-edit rights, and a GIS editor can edit assets without
-- touching live outages -- authorization as data, on brand with 0025.
--
-- Grants mirror map annotations (0061): everyone who can see the map reads the
-- outage board (viewer/analyst/editor + admins); whoever can edit features
-- writes it (editor + admins). The 0025 admin CROSS JOIN already ran, so the
-- admin/break-glass grants are added explicitly here.

INSERT INTO iam.permissions (key, description) VALUES
    ('oms:read',  'View the outage board: trouble calls, predicted and active incidents.'),
    ('oms:write', 'Log trouble calls, run prediction, and confirm/restore/cancel incidents.')
ON CONFLICT (key) DO NOTHING;

-- read: everyone who can see the map (viewer/analyst/editor + admins).
INSERT INTO iam.role_permissions (role_id, permission_key)
SELECT r.id, 'oms:read'
FROM iam.roles r
WHERE r.key IN ('viewer', 'analyst', 'editor', 'admin', 'break-glass-admin')
ON CONFLICT DO NOTHING;

-- write: whoever can edit features (editor + admins), mirroring features:write.
INSERT INTO iam.role_permissions (role_id, permission_key)
SELECT r.id, 'oms:write'
FROM iam.roles r
WHERE r.key IN ('editor', 'admin', 'break-glass-admin')
ON CONFLICT DO NOTHING;
