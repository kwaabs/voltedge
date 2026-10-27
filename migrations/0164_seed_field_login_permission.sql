-- 0164_seed_field_login_permission.sql
-- Purpose: gate the OFFLINE FIELD APP behind a dedicated capability. Until now
-- POST /auth/field-login (api/internal/fieldpack) minted a bearer for ANY valid
-- account -- it verified the credential under the web-login policy but checked no
-- field-specific permission, so an office viewer/analyst/editor could sign into the
-- field tool exactly like a field crew. This adds `field:login`: field-login now
-- refuses (403) a user who lacks it, so field-app access is an EXPLICIT grant.
--
-- Granted to the three field roles (0099) -- the people whose job is field work --
-- plus admin + break-glass-admin (support/emergency). Office-only roles (viewer,
-- analyst, editor) do NOT get it; a person who does both office and field work is
-- given an office role AND a field role (roles stack). No field access for anyone
-- else until explicitly granted.

INSERT INTO iam.permissions (key, description) VALUES
    ('field:login', 'Sign in to the offline field app (obtain a field-app bearer token). Required by POST /auth/field-login.')
ON CONFLICT (key) DO NOTHING;

INSERT INTO iam.role_permissions (role_id, permission_key)
SELECT r.id, p.key
FROM iam.roles r
CROSS JOIN iam.permissions p
WHERE r.key IN ('field-surveyor', 'field-enumerator', 'field-technician', 'admin', 'break-glass-admin')
  AND p.key = 'field:login'
ON CONFLICT DO NOTHING;
