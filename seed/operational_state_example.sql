-- seed/operational_state_example.sql
-- Phase 38 (operational lifecycle), Slice 1.
--
-- Adds a declared `operational_state` lifecycle field to every physical asset
-- class: PLANNED -> IN_SERVICE -> ISOLATED (tagged out) -> RETIRED. This is a
-- deliberately separate axis from two others (see docs/phase-38-operational-state.md):
--   * the `status` system column (draft/published) is a PUBLISH workflow, not a
--     lifecycle -- draft is NOT the first step of this chain;
--   * electrical energization is DERIVED (a downstream trace from a source
--     through closed switches -- what /trace/isolate computes), never stored.
-- Operational state is the one a human declares, one asset at a time, so it IS
-- a stored field.
--
-- Modelled exactly on the device_state coded field
-- (seed/switching_device_example.sql) and the circuit_ref field add
-- (seed/circuit_ref_example.sql): a coded domain + a metadata.fields row that
-- the compiler turns into `operational_state text CHECK (... IN ...)` and that
-- generic CRUD + the create/edit forms pick up with NO API or compiler change.
--
-- Metadata-only. Apply, then recompile every stamped class so the physical
-- tables gain the column, then run the backfill (a SEPARATE step -- it UPDATEs
-- the new column, which only exists after the recompile):
--   task seed-operational-state
--   task compile CODE=MV_CONDUCTOR   (…and every other class listed below)
--   task seed-operational-state-backfill
-- Adding a nullable text column is non-destructive (ALTER TABLE ADD COLUMN).
--
-- The field is NULLABLE on purpose: a non-nullable column compiles to `... NOT
-- NULL`, which the compiler adds to an already-populated table via ALTER TABLE
-- ADD COLUMN with no DEFAULT (custom columns never get one) -- Postgres rejects
-- that on a table that already has rows. Nullable + the backfill below is the
-- clean path. "New features start at PLANNED" is delivered by the web create
-- form defaulting the picker to PLANNED (feature-create-form.tsx), not a DB
-- default.

-- ---------------------------------------------------------------------------
-- A dedicated "Operational" field group. Not reusing `electrical`: this field
-- lives on structural classes too (POLE, BAY…) that have no electrical group,
-- and a lifecycle field bucketed under "Electrical" on a pole reads wrong. High
-- sort_order so it renders after the class's own type-specific groups.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.field_groups (code, name, sort_order) VALUES
    ('operational', 'Operational', 90)
ON CONFLICT (code) DO NOTHING;

-- ---------------------------------------------------------------------------
-- The coded domain + its four states. sort_order puts PLANNED first so the
-- create form's picker lands on it (forms render coded options in sort_order).
-- ---------------------------------------------------------------------------
INSERT INTO metadata.domains (code, name, domain_type) VALUES
    ('OPERATIONAL_STATE', 'Operational state', 'coded')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domain_values (domain_id, code, label, sort_order)
SELECT d.id, v.code, v.label, v.sort_order
FROM metadata.domains d
JOIN (VALUES
    ('OPERATIONAL_STATE', 'PLANNED',    'Planned',    1),
    ('OPERATIONAL_STATE', 'IN_SERVICE', 'In service', 2),
    ('OPERATIONAL_STATE', 'ISOLATED',   'Isolated',   3),
    ('OPERATIONAL_STATE', 'RETIRED',    'Retired',    4)
) AS v(domain_code, code, label, sort_order) ON v.domain_code = d.code
ON CONFLICT (domain_id, code) DO NOTHING;

-- ---------------------------------------------------------------------------
-- The field itself, on every physical asset class -- a broader set than Phase
-- 37's circuit_id: the electrical apparatus PLUS the structural classes
-- (POLE/BAY/SUBSTATION/METER_CHAMBER) that carry no circuit membership but
-- are still planned, commissioned, and retired. Nullable, no per-subtype
-- requirement (a required lifecycle would block create until picked -- the form
-- default covers "born PLANNED" instead). One set-generating INSERT; ON
-- CONFLICT keeps it idempotent.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, 'operational_state', 'text', d.id, fg.id, true
FROM metadata.feature_classes fc
JOIN metadata.domains d       ON d.code  = 'OPERATIONAL_STATE'
JOIN metadata.field_groups fg ON fg.code = 'operational'
WHERE fc.code IN (
    -- Electrical apparatus (the Phase 37 network-stamped set).
    'MV_CONDUCTOR', 'MV_BUSBAR', 'MV_BREAKER', 'MV_ISOLATOR', 'MV_SECTIONALIZER',
    'MV_FUSE', 'MV_ARRESTER', 'MV_CT', 'MV_VT',
    'DIST_XFMR', 'DIST_SWITCH', 'PROTECTIVE_RELAY',
    'LV_CONDUCTOR', 'LV_BUSBAR', 'LV_LINK_BOX',
    'SERVICE_POINT', 'SERVICE_CUTOUT', 'METER', 'STREET_LIGHT',
    -- Structural assets -- excluded from circuit_id, but still commissioned.
    'POLE', 'BAY', 'SUBSTATION', 'METER_CHAMBER'
)
ON CONFLICT (feature_class_id, name) DO NOTHING;
