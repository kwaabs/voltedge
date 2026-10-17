-- seed/consumer_leaf_load_fields.sql
-- Phase 62/63 follow-up -- make the UNMETERED consumer leaves count in the
-- service / consumption rollup.
--
-- Background. The service trace (api/internal/trace/service.go) rolls up the
-- LOAD LEAVES a walk reaches into "how much load, how many customers, of what
-- restoration priority, sits below here." It used to recognise a leaf by code
-- (SERVICE_POINT + PRIMARY METER only), so STREET_LIGHT and EV_CHARGING_STATION
-- -- which are functional_role = 'consumer' (seed/functional_role_backfill.sql),
-- i.e. genuine sinks -- contributed NOTHING, not even a P4 line. The engine now
-- recognises a load leaf by that metadata signal (isConsumerLeaf), so every
-- consumer-role class counts; but a class only contributes real figures once it
-- carries the load fields. This seed gives the unmetered leaves those fields:
--
--   * demand_kw / power_factor -- the Phase 8 load inputs (numeric), so their
--     load rolls up (load-flow's resolve.go already reads them where present).
--   * load_priority -- the P1..P4 restoration tier (the shared LOAD_PRIORITY
--     domain from seed/service_point_load_priority.sql), nullable; absence reads
--     as P4/Standard in the engine (resolveServicePriority) and in the create
--     form (feature-create-form.tsx defaults the picker to P4).
--
-- CUSTOMER counting is unchanged and lives in CODE, not here: only SERVICE_POINT
-- and the PRIMARY METER are CUSTOMERS (isCustomerLeaf); a street light / EV
-- station is load-only (0 customers), so "customers affected" stays honest and
-- OMS -- which pre-filters its cone to SERVICE_POINT/METER before the rollup --
-- is completely untouched.
--
-- Metadata-only. Apply, then recompile each class so the physical columns exist
-- (the compiler ADDs the nullable columns; existing rows read as P4/no-demand):
--   task seed-consumer-leaf-load-fields
--   task compile CODE=STREET_LIGHT
--   task compile CODE=EV_CHARGING_STATION
-- Idempotent (ON CONFLICT DO NOTHING). The LOAD_PRIORITY domain is re-asserted
-- so this stands alone; the 'electrical' field group is a global base seed.

-- ---------------------------------------------------------------------------
-- Re-assert the coded restoration-tier domain (owned by
-- service_point_load_priority.sql) so this seed does not depend on apply order.
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
-- STREET_LIGHT: an unmetered LV leaf with NO load fields today. Add the two
-- load-flow inputs (numeric, no domain) and the restoration tier (coded). All
-- nullable -- live rows read as no-demand / P4. In the 'electrical' group, the
-- group STREET_LIGHT's existing fields already use.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, true
FROM metadata.feature_classes fc
JOIN (VALUES
    ('demand_kw',     'numeric', NULL::text),
    ('power_factor',  'numeric', NULL::text),
    ('load_priority', 'text',    'LOAD_PRIORITY')
) AS v(name, data_type, domain_code) ON true
LEFT JOIN metadata.domains d  ON d.code  = v.domain_code
JOIN metadata.field_groups fg ON fg.code = 'electrical'
WHERE fc.code = 'STREET_LIGHT'
ON CONFLICT (feature_class_id, name) DO NOTHING;

-- ---------------------------------------------------------------------------
-- EV_CHARGING_STATION: already carries demand_kw + power_factor
-- (seed/ev_charging_example.sql); it only lacks the restoration tier. Add it.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, 'load_priority', 'text', d.id, fg.id, true
FROM metadata.feature_classes fc
JOIN metadata.domains d       ON d.code  = 'LOAD_PRIORITY'
JOIN metadata.field_groups fg ON fg.code = 'electrical'
WHERE fc.code = 'EV_CHARGING_STATION'
ON CONFLICT (feature_class_id, name) DO NOTHING;
