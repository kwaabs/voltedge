-- seed/service_point_critical_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this
-- lives under /seed on purpose, same reasoning as the other seed files.
-- Adds is_critical to SERVICE_POINT -- a plain boolean, no domain
-- needed, so an isolation result (api/internal/trace/isolate.go) can
-- flag whether any de-energized service point is a critical facility
-- (hospital, water treatment, etc.) instead of a human having to
-- cross-reference a separate list by hand.
--
-- Depends on seed/service_point_example.sql having already been
-- applied and SERVICE_POINT compiled.
--
-- Deliberately nullable, unlike most of this class's other fields --
-- SERVICE_POINT already has at least one real row by the time this
-- gets seeded and compiled (Meter 4471, see docs/phase-4-utility-traces.md),
-- and a NOT NULL ALTER TABLE ADD COLUMN with no default fails outright
-- against a table that already has rows. Optional and defaulting to
-- "not critical" (NULL/absent, same as false) is also the more honest
-- semantics anyway -- nothing should silently assume a service point
-- is a hospital or water treatment plant just because a value wasn't
-- set for it.
--
-- Apply with psql after seed/service_point_example.sql, then recompile:
--   task compile CODE=SERVICE_POINT

INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, NULL, fg.id, v.nullable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('is_critical', 'boolean', 'service', true)
) AS v(name, data_type, group_code, nullable) ON true
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code = 'SERVICE_POINT'
ON CONFLICT (feature_class_id, name) DO NOTHING;
