-- 0182_work_order_mirror.sql
-- Purpose: consume an AUTHORITATIVE external WAM as a READ-ONLY, PUSH-ONLY mirror
-- (external-mirror-integration design, the WAM sibling of the OMS incident mirror
-- in 0181). An external WAM that OWNS its work orders pushes them to VoltEdge, which
-- reflects them beside its own locally-raised ones. Two columns tag a mirrored order:
--
--   origin_system — NULL = a locally-owned work order (editable; assignable to a
--                   crew/subcontractor; fans out to field packs; pushed out on
--                   change). A system_code = MIRRORED from that external WAM:
--                   read-only here (its lifecycle is theirs), no crew/field-pack
--                   machinery, never re-emitted outbound.
--   external_id   — the external WAM's own id for the order, the upsert key.
--
-- The asset it references resolves to a VoltEdge feature through external_ref (0145)
-- so a mirrored order still appears on the asset's Works tab.

ALTER TABLE wam.work_orders ADD COLUMN IF NOT EXISTS origin_system text;
ALTER TABLE wam.work_orders ADD COLUMN IF NOT EXISTS external_id   text;

COMMENT ON COLUMN wam.work_orders.origin_system IS 'Read-only mirror origin (0182): NULL = local; a system_code = mirrored from that external WAM (read-only, not re-emitted, no crew/fan-out).';
COMMENT ON COLUMN wam.work_orders.external_id IS 'The external WAM''s own work-order id (0182), the upsert key for a mirrored order.';

CREATE UNIQUE INDEX IF NOT EXISTS work_orders_origin_external_idx
    ON wam.work_orders (origin_system, external_id)
    WHERE origin_system IS NOT NULL AND external_id IS NOT NULL;
