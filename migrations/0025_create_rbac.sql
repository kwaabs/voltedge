-- 0025_create_rbac.sql
-- Purpose: Phase 10 Slice C RBAC -- the permission catalog, the roles that
-- bundle permissions, and the two join tables (role_permissions,
-- user_roles). This is the *data* half of authorization; enforcement lives
-- in the Go layer (auth.RequirePermission gates every route). "Authorization
-- as data" means a new distinction is a seed row, not a recompile -- on
-- brand with the rest of this metadata-driven system.
--
-- No user_roles are seeded here: at migrate time there are no application
-- users (the very first break-glass admin is created out-of-band by
-- api/cmd/authadmin, which also assigns it the admin role). See
-- docs/phase-10-auth.md.

-- iam.permissions -- the catalog. The key is a stable "resource:action"
-- string that the Go route gates compare against verbatim; it IS the
-- primary key, so a grant can never reference a permission that does not
-- exist.
CREATE TABLE iam.permissions (
    key         text PRIMARY KEY,
    description text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE iam.permissions IS 'The RBAC permission catalog. key is a stable resource:action string enforced by auth.RequirePermission in the Go layer. Extend by inserting a row, not by editing code.';

-- iam.roles -- named bundles of permissions. key is the machine name the
-- code and CLI reference; name is the human label. is_system marks the
-- seeded roles that enforcement assumes, so a future admin UI (Slice D) can
-- refuse to delete them.
CREATE TABLE iam.roles (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    key         text NOT NULL UNIQUE,
    name        text NOT NULL,
    description text NOT NULL DEFAULT '',
    is_system   boolean NOT NULL DEFAULT false,
    created_at  timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE iam.roles IS 'Named bundles of permissions. A user''s effective permission set is the union across the roles they hold. is_system roles are seeded and enforcement-critical.';

-- iam.role_permissions -- which permissions a role grants (many-to-many).
CREATE TABLE iam.role_permissions (
    role_id        uuid NOT NULL REFERENCES iam.roles(id) ON DELETE CASCADE,
    permission_key text NOT NULL REFERENCES iam.permissions(key) ON DELETE CASCADE,
    PRIMARY KEY (role_id, permission_key)
);

-- iam.user_roles -- which roles a user holds (many-to-many). granted_by is
-- the acting admin (nullable for out-of-band/bootstrap grants), ON DELETE
-- SET NULL so the grant record survives the granter's account.
CREATE TABLE iam.user_roles (
    user_id    uuid NOT NULL REFERENCES iam.users(id) ON DELETE CASCADE,
    role_id    uuid NOT NULL REFERENCES iam.roles(id) ON DELETE CASCADE,
    granted_at timestamptz NOT NULL DEFAULT now(),
    granted_by uuid REFERENCES iam.users(id) ON DELETE SET NULL,
    PRIMARY KEY (user_id, role_id)
);

CREATE INDEX user_roles_role_idx ON iam.user_roles (role_id);

COMMENT ON TABLE iam.user_roles IS 'Role assignments. The session middleware unions these into the effective permission set carried on each request''s context.';

-- ---------------------------------------------------------------------------
-- Seed: the permission catalog. Each key is enforced by a route gate in
-- api/cmd/server/main.go (read vs. write chosen by HTTP method) or, for the
-- pure-action mounts, a single permission. metadata:write covers editing AND
-- compiling the catalog and symbology (one cohesive admin capability, gated
-- at the /admin mount). admin:users is seeded now for the Slice D user-admin
-- API and granted to admin, though its enforcement point arrives with that
-- slice.
-- ---------------------------------------------------------------------------
INSERT INTO iam.permissions (key, description) VALUES
    ('features:read',        'View feature data: map layers, attributes, and feature-class schema.'),
    ('features:write',       'Create, edit, publish, unpublish, delete, and restore features.'),
    ('connectivity:read',    'View committed electrical connectivity.'),
    ('connectivity:commit',  'Establish or revoke electrical connectivity.'),
    ('relationships:read',   'View committed structural/spatial relationships.'),
    ('relationships:commit', 'Establish or revoke structural/spatial relationships.'),
    ('diagrams:read',        'View schematic diagrams and their placements.'),
    ('diagrams:write',       'Create, rename, delete diagrams and place/remove features.'),
    ('trace:run',            'Run network traces (isolation, feeder, section).'),
    ('analysis:run',         'Run power-flow, fault-current, and ampacity analysis.'),
    ('metadata:read',        'View the metadata catalog: classes, domains, field groups, symbology, materials.'),
    ('metadata:write',       'Edit and compile the metadata catalog and symbology.'),
    ('admin:auth-settings',  'Change auth settings, including the local-auth break-glass switch.'),
    ('admin:users',          'Manage user accounts and role assignments.')
ON CONFLICT (key) DO NOTHING;

-- Seed: the baseline roles.
INSERT INTO iam.roles (key, name, description, is_system) VALUES
    ('viewer',            'Viewer',            'Read-only: features, connectivity, relationships, diagrams, and the metadata catalog.', true),
    ('analyst',           'Analyst',           'Everything a viewer can do, plus running traces and network analysis.', true),
    ('editor',            'Editor',            'Everything an analyst can do, plus editing features, connectivity, relationships, and diagrams.', true),
    ('admin',             'Administrator',     'Full access, including the metadata catalog, user management, and auth settings.', true),
    ('break-glass-admin', 'Break-glass admin', 'Full access; the role given to emergency break-glass accounts.', true)
ON CONFLICT (key) DO NOTHING;

-- Seed: role -> permission grants.

-- viewer: pure reads.
INSERT INTO iam.role_permissions (role_id, permission_key)
SELECT r.id, p.key
FROM iam.roles r
CROSS JOIN iam.permissions p
WHERE r.key = 'viewer'
  AND p.key IN ('features:read', 'connectivity:read', 'relationships:read', 'diagrams:read', 'metadata:read')
ON CONFLICT DO NOTHING;

-- analyst: viewer's reads + run traces/analysis.
INSERT INTO iam.role_permissions (role_id, permission_key)
SELECT r.id, p.key
FROM iam.roles r
CROSS JOIN iam.permissions p
WHERE r.key = 'analyst'
  AND p.key IN ('features:read', 'connectivity:read', 'relationships:read', 'diagrams:read', 'metadata:read', 'trace:run', 'analysis:run')
ON CONFLICT DO NOTHING;

-- editor: analyst + all domain writes/commits. NOT metadata:write (catalog
-- editing) and NOT admin:* -- an editor edits data, not the schema or users.
INSERT INTO iam.role_permissions (role_id, permission_key)
SELECT r.id, p.key
FROM iam.roles r
CROSS JOIN iam.permissions p
WHERE r.key = 'editor'
  AND p.key IN ('features:read', 'features:write', 'connectivity:read', 'connectivity:commit',
                'relationships:read', 'relationships:commit', 'diagrams:read', 'diagrams:write',
                'trace:run', 'analysis:run', 'metadata:read')
ON CONFLICT DO NOTHING;

-- admin and break-glass-admin: every permission in the catalog.
INSERT INTO iam.role_permissions (role_id, permission_key)
SELECT r.id, p.key
FROM iam.roles r
CROSS JOIN iam.permissions p
WHERE r.key IN ('admin', 'break-glass-admin')
ON CONFLICT DO NOTHING;
