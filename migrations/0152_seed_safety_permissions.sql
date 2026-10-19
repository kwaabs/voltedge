-- 0152_seed_safety_permissions.sql
-- The safety:read / safety:write capability pair that gates the operational
-- safety API (api/internal/safety, mounted at /safety). Follows the operations
-- (0148) / faults (0107) / OMS (0088) pattern exactly: the permission catalog is
-- data (iam.permissions), enforced in Go by auth.RequirePermission via the
-- PermSafety{Read,Write} constants in api/internal/auth/rbac.go.
--
-- Why its own capability, not oms:*: placing a tag, applying a ground and issuing
-- a clearance is a SAFETY officer's authority, and -- crucially -- a blocking tag
-- bars operation of a device for EVERYONE, including an oms:write holder. Keeping
-- safety:write separate means the right to protect a work area (and to release it)
-- is granted deliberately, not folded into general switching rights. Reads are
-- broad (anyone running the network must see what is tagged/earthed); writes go to
-- the control-room editors + admins + the field crew who place earths on the ground.
--
-- The 0025 admin CROSS JOIN already ran, so admin/break-glass are added explicitly.

INSERT INTO iam.permissions (key, description) VALUES
    ('safety:read',  'View safety tags, applied grounds and clearances (permits-to-work).'),
    ('safety:write', 'Place/remove safety tags and grounds, and issue/surrender clearances; a blocking tag bars device operation.')
ON CONFLICT (key) DO NOTHING;

-- read: everyone who runs or watches the network must see what is protected.
INSERT INTO iam.role_permissions (role_id, permission_key)
SELECT r.id, 'safety:read'
FROM iam.roles r
WHERE r.key IN ('viewer', 'analyst', 'editor', 'admin', 'break-glass-admin', 'field-technician')
ON CONFLICT DO NOTHING;

-- write: control-room editors + admins place tags and issue clearances; the field
-- technician applies/removes earths and tags on the ground.
INSERT INTO iam.role_permissions (role_id, permission_key)
SELECT r.id, 'safety:write'
FROM iam.roles r
WHERE r.key IN ('editor', 'admin', 'break-glass-admin', 'field-technician')
ON CONFLICT DO NOTHING;
