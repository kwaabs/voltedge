-- 0097_seed_wam_permissions.sql
-- Purpose: Phase 59 Slice 1 -- the wam:read / wam:write capability pair that
-- gates the Work & Asset Management API (api/internal/wam, mounted at /wam).
-- Follows the OMS pattern exactly (0088): the permission catalog is data
-- (iam.permissions), enforced in Go by auth.RequirePermission via the
-- PermWAM{Read,Write} constants in api/internal/auth/rbac.go.
--
-- Why its own capability, not features:read/write: a defect or work order is
-- not feature data -- it is an operational record about the network (a problem
-- logged, a job scheduled, a crew assigned, work completed). A separate
-- capability lets a maintenance planner run the work board without holding
-- feature-edit rights, and a GIS editor edit assets without touching live work
-- -- authorization as data, the same split OMS drew.
--
-- Grants mirror OMS (0088): everyone who can see the map reads the work board
-- (viewer/analyst/editor + admins); whoever can edit features writes it (editor
-- + admins). The 0025 admin CROSS JOIN already ran, so the admin/break-glass
-- grants are added explicitly here.

INSERT INTO iam.permissions (key, description) VALUES
    ('wam:read',  'View the work board: defects, work orders, inspections and maintenance.'),
    ('wam:write', 'Log defects, raise/schedule/assign work orders, and record completion.')
ON CONFLICT (key) DO NOTHING;

-- read: everyone who can see the map (viewer/analyst/editor + admins).
INSERT INTO iam.role_permissions (role_id, permission_key)
SELECT r.id, 'wam:read'
FROM iam.roles r
WHERE r.key IN ('viewer', 'analyst', 'editor', 'admin', 'break-glass-admin')
ON CONFLICT DO NOTHING;

-- write: whoever can edit features (editor + admins), mirroring features:write.
INSERT INTO iam.role_permissions (role_id, permission_key)
SELECT r.id, 'wam:write'
FROM iam.roles r
WHERE r.key IN ('editor', 'admin', 'break-glass-admin')
ON CONFLICT DO NOTHING;
