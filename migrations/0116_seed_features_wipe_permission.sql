-- 0116_seed_features_wipe_permission.sql
-- Purpose: the features:wipe capability that gates the bulk class-wipe API
-- (api/internal/crud, POST /features/{code}/wipe and /purge-deleted). It is a
-- deliberately narrow, admin-tier permission distinct from features:write.
--
-- WHY ITS OWN CAPABILITY, above features:write:
--   features:write authorizes editing one feature at a time -- create, update,
--   soft-delete, restore, purge a single row. Wiping an entire feature class in
--   one action (soft-delete every live row, then permanently purge every deleted
--   row with its topology/editing cascade) is an order of magnitude more
--   destructive and irreversible-at-scale. An ordinary editor should never be one
--   mis-click from erasing a whole class, so the bulk operation sits behind its
--   own permission granted only to the two admin tiers. Territory scope still
--   applies underneath: a scoped admin wipes only their territory's rows; an
--   unscoped admin (territories:unscoped) wipes the whole class.
--
-- Grants: admin and break-glass-admin ONLY. The 0025 admin CROSS JOIN already
-- ran, so these grants are added explicitly here (same as 0115 did for tags).
-- No field or analyst role receives it.

INSERT INTO iam.permissions (key, description) VALUES
    ('features:wipe', 'Bulk soft-delete and permanently purge every feature in a class (admin).')
ON CONFLICT (key) DO NOTHING;

INSERT INTO iam.role_permissions (role_id, permission_key)
SELECT r.id, 'features:wipe'
FROM iam.roles r
WHERE r.key IN ('admin', 'break-glass-admin')
ON CONFLICT DO NOTHING;
