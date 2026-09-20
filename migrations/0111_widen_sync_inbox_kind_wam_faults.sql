-- 0111_widen_sync_inbox_kind_wam_faults.sql
-- Purpose: widen ops.sync_inbox's kind CHECK to admit the WAM and faults offline
-- sync ops. 0043 created the inbox with a fixed kind allowlist -- the feature-edit
-- vocabulary of the day (create / update_attrs / delete / establish_connection /
-- establish_relationship). The offline WAM module (defects / work orders /
-- inspections) and the offline Faults module then added their own op kinds to the
-- device outbox and the server's apply router (fieldpack/sync.go), but the DB
-- allowlist was never extended to match. So the moment POST /sync tries to CAPTURE
-- a WAM or fault op, the INSERT trips sync_inbox_kind_check
-- (SQLSTATE 23514) -- e.g. a field crew's "Update status" on a fault fails to sync.
--
-- This is the missing half of that work: the CHECK is the allowlist for the DATA,
-- exactly like 0075/0080/0082 widened relationship_classes.kind and 0047/0050/0084
-- widened diagram_elements.kind. The Go apply router (applyWAMOp / applyFaultsOp)
-- already knows these kinds; this lets them land in the inbox to be applied.
--
-- Postgres auto-named 0043's inline column CHECK `sync_inbox_kind_check` (confirmed
-- by the violation's constraint name). Drop it and re-add with the full set. The
-- new kinds are additive -- every previously-valid kind stays valid -- so no
-- existing row can violate the widened constraint.

ALTER TABLE ops.sync_inbox
    DROP CONSTRAINT IF EXISTS sync_inbox_kind_check,
    ADD CONSTRAINT sync_inbox_kind_check CHECK (kind IN (
        -- Feature edits (0043, the original set)
        'create', 'update_attrs', 'delete',
        'establish_connection', 'establish_relationship',
        -- Offline WAM: defects, work orders, inspections
        'wam_defect_create', 'wam_defect_status',
        'wam_workorder_update', 'wam_inspection_record',
        -- Offline Faults: raise, status change, book a defect from a fault
        'fault_create', 'fault_status', 'fault_book_defect'
    ));
