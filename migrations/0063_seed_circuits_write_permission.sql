-- 0063_seed_circuits_write_permission.sql
-- Purpose: Phase 37 Slice 1 -- add the `circuits:write` permission that gates
-- in-app circuit management (create/rename a circuit, set its voltage, point
-- it at a source feature, delete it). It mirrors territories:write (migration
-- 0031): the read side rides features:read (every viewer reads the circuit
-- catalog to filter a trace and to resolve a circuit's name for a map label),
-- while these mutating verbs need their own administrative capability.
--
-- Why a new permission rather than reusing metadata:write or features:write:
-- curating the circuit catalog is its own act, distinct from editing feature
-- data or the metadata schema. Keeping it separate means an operator can be
-- allowed to manage circuits without being handed the whole metadata catalog
-- -- authorization as data, on brand with 0025.
--
-- Grants: admin and break-glass-admin only, matching territories:write --
-- the circuit catalog is administrative reference data. (A later slice's
-- assign-by-trace, which stamps circuit membership onto features, is feature
-- data and will gate on features:write, not this.) Because 0025's
-- admin/break-glass CROSS JOIN already ran before this permission existed, the
-- grant is added explicitly here.

INSERT INTO iam.permissions (key, description) VALUES
    ('circuits:write', 'Create, rename, and delete circuits, and edit their voltage and source feature.')
ON CONFLICT (key) DO NOTHING;

INSERT INTO iam.role_permissions (role_id, permission_key)
SELECT r.id, 'circuits:write'
FROM iam.roles r
WHERE r.key IN ('admin', 'break-glass-admin')
ON CONFLICT DO NOTHING;
