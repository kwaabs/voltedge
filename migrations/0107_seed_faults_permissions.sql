-- 0107_seed_faults_permissions.sql
-- Purpose: the faults:read / faults:write capability pair that gates the Faults
-- API (api/internal/faults, mounted at /faults). Follows the OMS (0088) and WAM
-- (0097) pattern exactly: the permission catalog is data (iam.permissions),
-- enforced in Go by auth.RequirePermission via the PermFaults{Read,Write}
-- constants in api/internal/auth/rbac.go.
--
-- Why its own capability, not oms:*/wam:*: a fault event straddles both
-- subsystems (it explains an outage and books a defect) but is neither an outage
-- record nor an asset-condition record; a control engineer may log and analyse
-- faults without holding outage-dispatch or work-order rights. A separate
-- capability keeps that boundary clean -- authorization as data, the same split
-- OMS and WAM drew.
--
-- Grants mirror OMS/WAM: everyone who can see the map reads the fault board
-- (viewer/analyst/editor + admins); whoever can edit features writes it (editor
-- + admins). The 0025 admin CROSS JOIN already ran, so the admin/break-glass
-- grants are added explicitly here.

INSERT INTO iam.permissions (key, description) VALUES
    ('faults:read',  'View the fault board: electrical fault events and their outage/defect links.'),
    ('faults:write', 'Log fault events, link them to outages, and raise defects from them.')
ON CONFLICT (key) DO NOTHING;

-- read: everyone who can see the map (viewer/analyst/editor + admins).
INSERT INTO iam.role_permissions (role_id, permission_key)
SELECT r.id, 'faults:read'
FROM iam.roles r
WHERE r.key IN ('viewer', 'analyst', 'editor', 'admin', 'break-glass-admin')
ON CONFLICT DO NOTHING;

-- write: whoever can edit features (editor + admins), mirroring features:write.
INSERT INTO iam.role_permissions (role_id, permission_key)
SELECT r.id, 'faults:write'
FROM iam.roles r
WHERE r.key IN ('editor', 'admin', 'break-glass-admin')
ON CONFLICT DO NOTHING;
