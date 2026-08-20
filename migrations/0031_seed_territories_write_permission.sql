-- 0031_seed_territories_write_permission.sql
-- Purpose: Phase 11 Slice F -- add the `territories:write` permission that
-- gates in-app territory management (create/rename/re-parent/delete a
-- territory and edit its boundary). This reverses the earlier "fixed
-- reference data, no authoring UI" decision: territories are now managed
-- by administrators through the app, so they need a dedicated write
-- capability distinct from features:read (which every viewer holds and
-- which still gates the read side -- the territory label and the boundary
-- overlay).
--
-- Why a new permission rather than reusing metadata:write or admin:users:
-- managing the administrative-area tree is its own capability. An operator
-- could reasonably be allowed to curate territories without also being
-- handed the metadata catalog or user administration. Keeping it separate
-- means that distinction stays a grant, not a code change -- on brand with
-- 0025's "authorization as data".
--
-- Grants: admin and break-glass-admin only. Editors edit *feature data*,
-- not the territory boundaries that classify it; territory curation is an
-- administrative act. Because 0025's admin/break-glass CROSS JOIN already
-- ran before this permission existed, the grant is added explicitly here
-- (the CROSS JOIN does not retroactively pick up new catalog rows).

INSERT INTO iam.permissions (key, description) VALUES
    ('territories:write', 'Create, rename, re-parent, and delete territories, and edit their boundaries.')
ON CONFLICT (key) DO NOTHING;

INSERT INTO iam.role_permissions (role_id, permission_key)
SELECT r.id, 'territories:write'
FROM iam.roles r
WHERE r.key IN ('admin', 'break-glass-admin')
ON CONFLICT DO NOTHING;
