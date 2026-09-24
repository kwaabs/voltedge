-- seed/service_point_load_priority.sql
-- Phase 62 (service / consumption traces), Slice 1 -- the metadata foundation.
--
-- Adds a graded `load_priority` field to SERVICE_POINT: a four-tier restoration
-- priority that SUPERSEDES the old boolean `is_critical`
-- (seed/service_point_critical_example.sql). A single "critical: yes/no" flag
-- can't tell a life-support home from an ordinary priority customer; a real OMS
-- ranks load so restoration and shed decisions can walk the tiers in order.
--
--   * P1 -- Life support   (home ventilators, dialysis; must be restored first)
--   * P2 -- Essential       (hospitals, water/telecoms; critical infrastructure)
--   * P3 -- Priority        (schools, commercial anchors; restore before general)
--   * P4 -- Standard        (ordinary residential/commercial; the default)
--
-- "Critical" is now DERIVED, not stored: critical = rank <= P2 (P1 or P2). The
-- trace/OMS engines compute it from load_priority instead of reading is_critical
-- (api/internal/trace/isolate.go and the OMS customer rollups). is_critical is
-- kept as a physical column for back-compat and read during the transition, but
-- is HIDDEN from the create/edit forms below so operators only ever set the tier.
--
-- Modelled exactly on operational_state (seed/operational_state_example.sql) and
-- normal_state (seed/switch_normal_state_fields.sql): a coded domain + a
-- metadata.fields row the compiler turns into a `load_priority text CHECK (...)`
-- column that generic CRUD + the forms pick up with NO API/compiler change.
--
-- Metadata-only here. Apply, recompile SERVICE_POINT so the physical column
-- exists, then run the SEPARATE backfill (it UPDATEs the new column, which only
-- exists after the recompile):
--   task seed-service-point-load-priority
--   task compile CODE=SERVICE_POINT
--   task seed-service-point-load-priority-backfill
--
-- The field is NULLABLE on purpose (SERVICE_POINT already has live rows; a NOT
-- NULL ADD COLUMN with no default fails). Absence reads as P4/Standard both in
-- the engine (the service rollup coalesces NULL -> P4) and in the create form,
-- which defaults its picker to P4 (feature-create-form.tsx). Idempotent
-- (ON CONFLICT DO NOTHING throughout; the is_critical hide is guarded).
--
-- Depends on seed/service_point_example.sql (the class + the `service` field
-- group) and, for the hide, seed/service_point_critical_example.sql having
-- created is_critical; both re-asserted / guarded below so this stands alone.

-- ---------------------------------------------------------------------------
-- Re-assert the `service` field group idempotently (owned by
-- service_point_example.sql) so this seed does not depend on apply order.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.field_groups (code, name, sort_order) VALUES
    ('service', 'Service', 1)
ON CONFLICT (code) DO NOTHING;

-- ---------------------------------------------------------------------------
-- The coded domain + its four tiers. sort_order runs P1..P4 so the create
-- form's picker renders them best-first (forms order coded options by
-- sort_order); the default landing tier (P4) is set by the form, not here.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.domains (code, name, domain_type) VALUES
    ('LOAD_PRIORITY', 'Load priority', 'coded')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domain_values (domain_id, code, label, sort_order)
SELECT d.id, v.code, v.label, v.sort_order
FROM metadata.domains d
JOIN (VALUES
    ('LOAD_PRIORITY', 'P1', 'P1 -- Life support', 1),
    ('LOAD_PRIORITY', 'P2', 'P2 -- Essential',    2),
    ('LOAD_PRIORITY', 'P3', 'P3 -- Priority',      3),
    ('LOAD_PRIORITY', 'P4', 'P4 -- Standard',      4)
) AS v(domain_code, code, label, sort_order) ON v.domain_code = d.code
ON CONFLICT (domain_id, code) DO NOTHING;

-- ---------------------------------------------------------------------------
-- The field itself, on SERVICE_POINT. Nullable, no per-subtype requirement (a
-- required tier would block create until picked -- the form default covers
-- "born P4/Standard"). Lives in the `service` group beside is_critical.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, 'load_priority', 'text', d.id, fg.id, true
FROM metadata.feature_classes fc
JOIN metadata.domains d       ON d.code  = 'LOAD_PRIORITY'
JOIN metadata.field_groups fg ON fg.code = 'service'
WHERE fc.code = 'SERVICE_POINT'
ON CONFLICT (feature_class_id, name) DO NOTHING;

-- ---------------------------------------------------------------------------
-- Retire is_critical from the forms. load_priority supersedes it, so operators
-- set the tier and never the boolean. The column and its stored values stay
-- (still readable by legacy code during the transition, and the backfill reads
-- it); it just disappears from create/edit. Metadata-only, no recompile needed
-- (visible_* flags never touch DDL). Guarded so re-running is a no-op.
-- ---------------------------------------------------------------------------
UPDATE metadata.fields f
SET visible_on_create = false,
    visible_on_update = false
FROM metadata.feature_classes fc
WHERE f.feature_class_id = fc.id
  AND fc.code = 'SERVICE_POINT'
  AND f.name = 'is_critical'
  AND (f.visible_on_create = true OR f.visible_on_update = true);
