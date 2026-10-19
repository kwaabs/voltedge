-- 0148_seed_operations_permissions.sql
-- Purpose: the operations:read / operations:write capability pair that gates the
-- device-operation event log API (api/internal/deviceops, mounted at
-- /operations). Follows the faults (0107) / OMS (0088) / WAM (0097) pattern
-- exactly: the permission catalog is data (iam.permissions), enforced in Go by
-- auth.RequirePermission via the PermOperations{Read,Write} constants in
-- api/internal/auth/rbac.go.
--
-- Why its own capability, not faults:*/oms:*: a device operation is neither a
-- fault investigation nor a customer outage -- it's the raw electrical event
-- (a trip/lockout/reclose/open) that may underlie either, or neither (a manual
-- switching or a test). A control engineer may log/analyse operations without
-- holding fault-closure or outage-dispatch rights. A separate capability keeps
-- that boundary clean -- authorization as data, the same split faults/OMS/WAM drew.
--
-- Grants mirror faults: everyone who can see the map reads the operations log
-- (viewer/analyst/editor + admins); whoever can edit features writes it (editor
-- + admins). The 0025 admin CROSS JOIN already ran, so admin/break-glass are
-- added explicitly here.

INSERT INTO iam.permissions (key, description) VALUES
    ('operations:read',  'View the device-operation log: protective/switching device trips, lockouts, recloses and open/close events.'),
    ('operations:write', 'Record device operations and correlate them to faults, outages and switching steps.')
ON CONFLICT (key) DO NOTHING;

-- read: everyone who can see the map (viewer/analyst/editor + admins), plus the
-- field technician (crews are a primary source -- they log operations in the field).
INSERT INTO iam.role_permissions (role_id, permission_key)
SELECT r.id, 'operations:read'
FROM iam.roles r
WHERE r.key IN ('viewer', 'analyst', 'editor', 'admin', 'break-glass-admin', 'field-technician')
ON CONFLICT DO NOTHING;

-- write: whoever can edit features (editor + admins), mirroring features:write,
-- plus the field technician (crew entry is a day-one source).
INSERT INTO iam.role_permissions (role_id, permission_key)
SELECT r.id, 'operations:write'
FROM iam.roles r
WHERE r.key IN ('editor', 'admin', 'break-glass-admin', 'field-technician')
ON CONFLICT DO NOTHING;
