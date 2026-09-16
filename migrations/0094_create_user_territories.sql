-- 0094_create_user_territories.sql
-- Purpose: Phase 58 Slice 1 -- the two links spatial access control needs that
-- didn't exist before, plus the admin-bypass permission:
--
--   iam.user_territories -- which territories a user is directly granted. The
--     spatial analogue of iam.user_roles (migration 0025): a revocable grant
--     join, PK (user_id, territory_id), where revoke = DELETE the row. A grant
--     names a NODE in the territory tree; the effective set expands it DOWN to
--     the node's whole subtree at read time (api/internal/access.EffectiveIDs
--     reuses the territory.DescendantIDs cascade), so you grant "this district"
--     once and every area/plot under it comes along.
--
--   iam.users.manager_id -- the org/people tree, a self-referential adjacency
--     list exactly like gis.territories.parent_id (migration 0027). It exists
--     so access cascades UP the org: a manager's effective territory set is the
--     union of every grant held by their transitive reports. Nullable (top of
--     the org has no manager); ON DELETE SET NULL so removing a manager account
--     doesn't cascade-delete their reports. The "a manager can't report into
--     their own report" cycle guard is enforced in Go (access.wouldManagerCycle),
--     matching this repo's convention of validating cross-row references in the
--     app, not with Postgres triggers (see territory.wouldCreateCycle).
--
--   territories:unscoped -- the admin bypass permission. A holder sees and edits
--     every feature regardless of territory (the scoping predicate is dropped
--     entirely, so admin/break-glass reads and tiles stay fast). Granted to the
--     admin and break-glass-admin roles here.
--
-- Nothing here changes behavior on its own: enforcement stays gated on the
-- existing territory_enforcement_enabled flag (migration 0029), which ships OFF.
-- This migration only lays the tables/column/permission the later slices read.

-- The user -> territory grant join. Mirrors iam.user_roles.
CREATE TABLE iam.user_territories (
    user_id      uuid NOT NULL REFERENCES iam.users(id) ON DELETE CASCADE,
    territory_id uuid NOT NULL REFERENCES gis.territories(id) ON DELETE CASCADE,
    granted_at   timestamptz NOT NULL DEFAULT now(),
    granted_by   uuid REFERENCES iam.users(id) ON DELETE SET NULL,
    PRIMARY KEY (user_id, territory_id)
);

CREATE INDEX user_territories_territory_idx ON iam.user_territories (territory_id);

COMMENT ON TABLE iam.user_territories IS 'Direct user->territory grants (Phase 58). The spatial analogue of iam.user_roles: a revocable grant join where revoke = DELETE the row. A grant names a node in the gis.territories tree; api/internal/access.EffectiveIDs expands it down to the node''s whole subtree, then unions in the grants of everyone who reports up to the user (iam.users.manager_id). Enforcement is gated on territory_enforcement_enabled.';
COMMENT ON COLUMN iam.user_territories.territory_id IS 'The granted node. Descendants are NOT stored -- they are expanded at read time (territory.DescendantIDs), so re-parenting or extending the tree needs no re-grant.';
COMMENT ON COLUMN iam.user_territories.granted_by IS 'The acting admin, or NULL for out-of-band/bootstrap grants. ON DELETE SET NULL so the grant survives the granter''s account.';

-- The org/people tree. A self-referential adjacency list on iam.users.
ALTER TABLE iam.users
    ADD COLUMN manager_id uuid REFERENCES iam.users(id) ON DELETE SET NULL;

CREATE INDEX users_manager_idx ON iam.users (manager_id);

COMMENT ON COLUMN iam.users.manager_id IS 'This user''s manager (Phase 58) -- a self-referential adjacency list like gis.territories.parent_id, forming the org/people tree that spatial access cascades UP: a manager''s effective territory set is the union of every grant held by their transitive reports. NULL at the top of the org. The no-cycle invariant (a user can''t transitively manage themselves) is enforced in api/internal/access, not here.';

-- The admin-bypass permission, plus its grants to the two full-access roles.
INSERT INTO iam.permissions (key, description) VALUES
    ('territories:unscoped', 'Bypass territory scoping -- see and edit every feature regardless of territory grants.')
ON CONFLICT (key) DO NOTHING;

INSERT INTO iam.role_permissions (role_id, permission_key)
SELECT r.id, 'territories:unscoped'
FROM iam.roles r
WHERE r.key IN ('admin', 'break-glass-admin')
ON CONFLICT DO NOTHING;
