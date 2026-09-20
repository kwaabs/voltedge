-- 0110_grant_field_technician_faults.sql
-- Purpose: extend the field-technician role with the faults:read / faults:write
-- capability pair, so a technician issued a field token can bring open faults
-- offline in a pack and log/clear them from the device -- the same way they
-- already run the WAM work board.
--
-- Why now: the offline Faults module (Faults module mobile -- pack read-context +
-- fault_create/fault_status/fault_book_defect sync ops) is gated exactly like
-- WAM. A pack ships faults ONLY when its requester holds faults:read (frozen into
-- the pack scope at enqueue), and the device may push fault ops ONLY with
-- faults:write. The three field roles seeded in 0099 stop at wam:read/write, so a
-- field-technician could see Works but never Faults -- the button simply never
-- appeared. This closes that gap.
--
-- Why field-technician specifically, not surveyor/enumerator: a fault is an
-- electrical event a works-capable technician responds to (log it, clear it, book
-- a defect from it) -- the same tier that already holds the work board. A
-- read-only surveyor or a feature-editing enumerator has no fault-response duty,
-- so their tiers are left unchanged, mirroring how 0099 kept WAM to the technician
-- alone.
--
-- No new permissions are introduced: faults:read / faults:write already exist
-- (seeded in 0107). This only bundles them into an existing role -- authorization
-- as data, a seed row, not a recompile. The 0025 admin CROSS JOIN matched only
-- key='admin', so this role gets exactly the two grants listed here.

INSERT INTO iam.role_permissions (role_id, permission_key)
SELECT r.id, p.key
FROM iam.roles r
CROSS JOIN iam.permissions p
WHERE r.key = 'field-technician'
  AND p.key IN ('faults:read', 'faults:write')
ON CONFLICT DO NOTHING;
