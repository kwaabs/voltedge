-- seed/meter_load_leaf.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives
-- under /seed on purpose, same reasoning as the other seed files.
--
-- Phase 63 ("the network ends at the meter"). Decision, locked with the user:
-- a consumer is a LEAF, not a pass-through. Current is consumed at the meter;
-- it does not flow THROUGH the meter to a node behind it. So the metered supply
-- ends at the METER (its T1), and the load records live on the METER itself --
-- there is no separate SERVICE_POINT node behind a meter. SERVICE_POINT remains
-- the leaf ONLY for genuinely UNMETERED supplies (street lighting, allowances,
-- unmetered CT), which have no meter to carry the load.
--
-- This seed is the ADDITIVE, non-destructive half of that change: it gives the
-- METER class the same load attributes SERVICE_POINT already carries, plus a
-- PRIMARY/CHECK role so a supply's billing check-meter never double-counts the
-- load its primary meter already records. It does NOT drop METER.T2 or retire
-- the METER->SERVICE_POINT rule (that, plus lifting existing SP load onto the
-- meter and retiring the now-redundant SPs, is the destructive migration in
-- seed/meter_load_leaf_migrate.sql -- run that knowingly, after this).
--
-- THE PRIMARY CONTRACT (read by api/internal/trace/service.go collectService):
--   * A METER contributes to the consumption rollup / customer count ONLY when
--     meter_role = 'PRIMARY'. An unset role (every legacy/device meter) or
--     'CHECK' contributes NOTHING. This is deliberate and is what makes this
--     seed safe to apply BEFORE the data migration: an existing meter->SP chain
--     (load on the SP, the meter empty and role-unset) still counts once, via
--     the SP -- the meter stays inert until you explicitly move the load onto
--     it and mark it PRIMARY. Load lives in exactly ONE place per supply.
--   * SERVICE_POINT is unchanged: it always counts (the unmetered leaf).
--
-- Powerflow needs NO change: resolveDemand (api/internal/powerflow/resolve.go)
-- already reads demand_kw/power_factor off ANY node ("or any future load-bearing
-- junction"), so a PRIMARY meter injects its load into a load flow the moment
-- these fields are compiled onto gis.meter. (A CHECK meter must simply be left
-- with demand_kw NULL so it injects nothing -- it measures the same load.)
--
-- Depends on seed/lv_metering_example.sql (METER) and, for the LOAD_PRIORITY
-- domain, seed/service_point_load_priority.sql -- both re-asserted idempotently
-- below so this seed is self-sufficient regardless of order.
--
-- Apply with psql, then recompile the touched class:
--   task seed-meter-load-leaf   (or the psql -f invocation directly)
--   task compile CODE=METER
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL has
-- been reviewed statically -- a live psql apply + the `task compile CODE=METER`
-- run + a service-trace-through-a-meter check are still owed to call it verified.

-- Field groups (re-asserted idempotently; 'service' + 'electrical' already exist
-- from earlier phases). 'service' groups the consumption/billing attributes.
INSERT INTO metadata.field_groups (code, name, sort_order) VALUES
    ('electrical', 'Electrical', 1),
    ('service',    'Service',    1)
ON CONFLICT (code) DO NOTHING;

-- LOAD_PRIORITY -- the graded restoration tier shared with SERVICE_POINT
-- (Phase 62). Re-asserted idempotently so a meter's load_priority always has a
-- domain even if seed/service_point_load_priority.sql has not been applied yet.
INSERT INTO metadata.domains (code, name, domain_type) VALUES
    ('LOAD_PRIORITY', 'Load priority', 'coded')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domain_values (domain_id, code, label, sort_order)
SELECT d.id, v.code, v.label, v.sort_order
FROM metadata.domains d
JOIN (VALUES
    ('LOAD_PRIORITY', 'P1', 'P1 -- Life support', 1),
    ('LOAD_PRIORITY', 'P2', 'P2 -- Essential',    2),
    ('LOAD_PRIORITY', 'P3', 'P3 -- Priority',     3),
    ('LOAD_PRIORITY', 'P4', 'P4 -- Standard',     4)
) AS v(domain_code, code, label, sort_order) ON v.domain_code = d.code
ON CONFLICT (domain_id, code) DO NOTHING;

-- METER_ROLE -- PRIMARY carries the supply's load records; CHECK is a second
-- meter on the same supply (dual-tariff / check metering) that measures the
-- same load and must NOT be counted. Only these two values; more can be added
-- later without a schema change (it is coded, not a boolean).
INSERT INTO metadata.domains (code, name, domain_type) VALUES
    ('METER_ROLE', 'Meter role', 'coded')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domain_values (domain_id, code, label, sort_order)
SELECT d.id, v.code, v.label, v.sort_order
FROM metadata.domains d
JOIN (VALUES
    ('METER_ROLE', 'PRIMARY', 'Primary (carries load)', 1),
    ('METER_ROLE', 'CHECK',   'Check meter',            2)
) AS v(domain_code, code, label, sort_order) ON v.domain_code = d.code
ON CONFLICT (domain_id, code) DO NOTHING;

-- The load attributes on METER -- the SAME names SERVICE_POINT carries, so the
-- class-agnostic readers (trace rollup, powerflow resolveDemand) pick them up
-- with no per-class special-casing. All NULLABLE per the additive-compile rule
-- (gis.meter already has rows; a NOT NULL ADD COLUMN would fail 23502), and none
-- carry subtype_field_rules -- an unknown demand / customer count is fine.
--   demand_kw / power_factor -> 'electrical' (the load-flow inputs, uniform with
--     SERVICE_POINT's own two from seed/load_flow_fields.sql).
--   customers_served / load_priority / meter_role -> 'service'.
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, true
FROM metadata.feature_classes fc
JOIN (VALUES
    ('demand_kw',        'numeric', NULL,            'electrical'),
    ('power_factor',     'numeric', NULL,            'electrical'),
    ('customers_served', 'integer', NULL,            'service'),
    ('load_priority',    'text',    'LOAD_PRIORITY', 'service'),
    ('meter_role',       'text',    'METER_ROLE',    'service')
) AS v(name, data_type, domain_code, group_code) ON true
LEFT JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code = 'METER'
ON CONFLICT (feature_class_id, name) DO NOTHING;
