-- 0099_seed_field_roles.sql
-- Purpose: Phase 59 -- seed three system roles for field staff, so a person
-- issued an API token for the field tool gets exactly the capability tier their
-- job needs. The field tool authenticates via an iam.api_tokens bearer and runs
-- under the token owner's RBAC (api/internal/fieldpack), so a "field worker" is
-- just a user who holds one of these roles plus a token -- no separate mobile
-- identity, no per-user boolean flags. Adding a tier is a seed row, not a
-- recompile: authorization as data, the same model the 0025 roles use.
--
-- Three tiers (the user asked for enumerators, and separately for read-only
-- capture and edit-capable capture -- two different people -- plus a works-
-- capable technician):
--   field-surveyor   -- read-only capture: walk the network, photograph assets,
--                       see connectivity. Cannot edit features. (features:read,
--                       attachments:read/write, connectivity:read)
--   field-enumerator -- the above plus creating/editing features in the field
--                       (features:write).
--   field-technician -- the above plus running the work board on a device:
--                       log defects, perform and close work orders, record
--                       inspections (wam:read/write).
--
-- No new permissions are introduced -- these roles only bundle existing ones.
-- The 0025 admin CROSS JOIN already ran and only matched key='admin', so these
-- new roles get exactly the grants listed here and nothing more.

INSERT INTO iam.roles (key, name, description, is_system) VALUES
    ('field-surveyor',   'Field surveyor',   'Read-only field capture: view features and connectivity, take photos and attachments. Cannot edit features.', true),
    ('field-enumerator', 'Field enumerator', 'Field capture with editing: everything a surveyor can do, plus creating and editing features in the field.', true),
    ('field-technician', 'Field technician', 'Field works: everything an enumerator can do, plus the work board -- log defects, perform and close work orders, record inspections.', true)
ON CONFLICT (key) DO NOTHING;

-- field-surveyor: read-only capture.
INSERT INTO iam.role_permissions (role_id, permission_key)
SELECT r.id, p.key
FROM iam.roles r
CROSS JOIN iam.permissions p
WHERE r.key = 'field-surveyor'
  AND p.key IN ('features:read', 'attachments:read', 'attachments:write', 'connectivity:read')
ON CONFLICT DO NOTHING;

-- field-enumerator: surveyor + edit features.
INSERT INTO iam.role_permissions (role_id, permission_key)
SELECT r.id, p.key
FROM iam.roles r
CROSS JOIN iam.permissions p
WHERE r.key = 'field-enumerator'
  AND p.key IN ('features:read', 'features:write', 'attachments:read', 'attachments:write', 'connectivity:read')
ON CONFLICT DO NOTHING;

-- field-technician: enumerator + the work board.
INSERT INTO iam.role_permissions (role_id, permission_key)
SELECT r.id, p.key
FROM iam.roles r
CROSS JOIN iam.permissions p
WHERE r.key = 'field-technician'
  AND p.key IN ('features:read', 'features:write', 'attachments:read', 'attachments:write', 'connectivity:read', 'wam:read', 'wam:write')
ON CONFLICT DO NOTHING;
