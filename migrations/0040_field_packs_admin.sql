-- 0040_field_packs_admin.sql
-- Purpose: Phase 21 pack management -- let an administrator delete or disable
-- field-tool region packs from the web UI, so stale/abandoned packs don't crowd
-- a field tech's tablet (the device lists its owner's packs from cmd/fieldsync's
-- GET /packs). Two capabilities:
--
--   * disabled_at -- a soft "hide from the device" flag. A disabled pack is
--     filtered out of the device's pack list (fieldpack.listPacks) but its
--     artifacts stay in RustFS, so an admin can re-enable it. Reversible.
--   * (delete is not a schema change -- the admin endpoint reaps both RustFS
--     objects and hard-deletes the row.)
--
-- And a dedicated permission, admin:field-packs, following 0031's precedent
-- (territories:write): managing every user's packs is its own administrative
-- capability, distinct from features:read/write or metadata:write. Granted to
-- admin and break-glass-admin only -- 0025's CROSS JOIN ran before this row
-- existed, so the grant is added explicitly here.

ALTER TABLE ops.field_packs
ADD COLUMN IF NOT EXISTS disabled_at timestamptz;

COMMENT ON COLUMN ops.field_packs.disabled_at IS 'When set, this pack is hidden from the device pack list (cmd/fieldsync GET /packs) without deleting its RustFS artifacts -- a reversible admin "hide" (admin:field-packs). NULL = visible. See docs/phase-21-field-tool.md.';

INSERT INTO iam.permissions (key, description) VALUES
    ('admin:field-packs', 'List every user''s field-tool region packs, and disable (hide from the device) or delete them.')
ON CONFLICT (key) DO NOTHING;

INSERT INTO iam.role_permissions (role_id, permission_key)
SELECT r.id, 'admin:field-packs'
FROM iam.roles r
WHERE r.key IN ('admin', 'break-glass-admin')
ON CONFLICT DO NOTHING;
