-- seed/circuit_membership_example.sql
-- Phase 37 (circuits), Slice 2 -- circuit membership on electrical apparatus.
--
-- Two metadata-only moves, both idempotent, no API/UI code:
--
--   1. Stamp a nominal voltage `network` ('MV' or 'LV') on every electrical
--      apparatus class (migration 0064 added the column). This is what scopes
--      the circuit_id picker: an MV class offers MV circuits, an LV class LV
--      ones. It is NOT the same as network_role (edge/junction/container/none),
--      which is the class's role in the connectivity graph -- orthogonal to
--      voltage. The code prefix (MV_/LV_) is only a hint and unreliable, so the
--      network is set here explicitly per class. Structural classes (POLE, BAY,
--      SUBSTATION, METER_CHAMBER) are deliberately left NULL -- they carry
--      no circuit_id and belong to no single feeder.
--
--   2. Add a nullable `circuit_id` field (data_type uuid, no domain, existing
--      'electrical' group) to those same classes. It references a gis.circuits
--      row (migration 0062); like territory_id it is a soft reference, not a DB
--      foreign key (the catalog can't FK into a per-class physical table). The
--      metadata-driven create/edit form renders it through a network-scoped
--      circuit picker (feature-field-input.tsx special-cases the field name);
--      generic CRUD flows the value exactly like circuit_ref, no engine change.
--
-- Membership is normally derived by Slice 3's assign-by-trace (bulk-stamp from a
-- circuit's source), with this field also editable by hand as the override.
--
-- The DIST_XFMR / DIST_SWITCH / PROTECTIVE_RELAY straddlers are assigned MV:
-- a distribution transformer's primary identity is the MV feeder it hangs off
-- (it is the MV circuit's downstream boundary -- exactly where Slice 4's
-- stop-at-circuit-boundary trace will halt), and the substation switching /
-- relaying that protects a feeder is MV-side too.
--
-- Recompile after applying so the physical tables gain the circuit_id column
-- (the network stamp needs no recompile -- it is pure metadata the DDL never
-- reads). Adding a nullable uuid column is non-destructive (ALTER TABLE ADD
-- COLUMN); the network UPDATE touches only metadata.feature_classes:
--   task seed-circuit-membership
--   task compile CODE=MV_CONDUCTOR   # ...and each class listed below
-- Depends on the classes' own seed files (which declare the 'electrical'
-- field_group this field reuses) and migrations 0062+0064 having been applied.

-- ---------------------------------------------------------------------------
-- 1. Nominal voltage network per electrical apparatus class.
--    MV: the medium-voltage feeder apparatus + the boundary transformer and its
--    substation switching/relaying. LV: the low-voltage network and services.
-- ---------------------------------------------------------------------------
UPDATE metadata.feature_classes
SET network = 'MV'
WHERE code IN (
    'MV_CONDUCTOR', 'MV_BUSBAR', 'MV_BREAKER', 'MV_ISOLATOR', 'MV_SECTIONALIZER',
    'MV_FUSE', 'MV_ARRESTER', 'MV_CT', 'MV_VT',
    'DIST_XFMR', 'DIST_SWITCH', 'PROTECTIVE_RELAY'
);

UPDATE metadata.feature_classes
SET network = 'LV'
WHERE code IN (
    'LV_CONDUCTOR', 'LV_BUSBAR', 'LV_LINK_BOX',
    'SERVICE_POINT', 'SERVICE_CUTOUT', 'METER', 'STREET_LIGHT'
);

-- ---------------------------------------------------------------------------
-- 2. circuit_id field (nullable uuid, no domain, existing 'electrical' group)
--    on every class stamped with a network above. One set-generating INSERT;
--    ON CONFLICT keeps it idempotent. No subtype_field_rules row -- membership
--    is optional for every subtype (a feature need not belong to a circuit).
-- ---------------------------------------------------------------------------
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, 'circuit_id', 'uuid', NULL, fg.id, true
FROM metadata.feature_classes fc
JOIN metadata.field_groups fg ON fg.code = 'electrical'
WHERE fc.network IS NOT NULL
ON CONFLICT (feature_class_id, name) DO NOTHING;
