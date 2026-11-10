-- 0188_db_explorer.sql
-- Purpose: a new permission, admin:db, gating the in-app DATABASE EXPLORER — a
-- schema browser + SQL runner for the highest-privilege operators (see
-- api/internal/dbexplorer, web /admin/db). It is STRICTLY READ ONLY (every query
-- runs in a read-only transaction — it cannot change data), but it can still read
-- anything in the database, so the permission is granted ONLY to the admin and
-- break-glass-admin roles out of the box — never to a supervisor/field role. Like
-- every other permission, it is enforced in the Go API layer
-- (auth.RequirePermission), and every query the tool runs is written to the
-- server's structured audit log with the acting user.
--
-- No tables: the explorer reads the live catalog (pg_catalog / information_schema)
-- and runs queries directly; it owns no schema of its own.

INSERT INTO iam.permissions (key, description) VALUES
    ('admin:db', 'Use the database explorer: browse schema objects and run read-only SQL (admin / break-glass only).')
ON CONFLICT (key) DO NOTHING;

INSERT INTO iam.role_permissions (role_id, permission_key)
SELECT r.id, p.key
FROM iam.roles r
CROSS JOIN iam.permissions p
WHERE r.key IN ('admin', 'break-glass-admin')
  AND p.key = 'admin:db'
ON CONFLICT DO NOTHING;
