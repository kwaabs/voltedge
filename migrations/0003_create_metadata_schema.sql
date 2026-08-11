-- 0003_create_metadata_schema.sql
-- Purpose: reserve the 'metadata' schema for Phase 1 -- feature_classes,
-- fields, domains, network_rules, and the rest of the metadata core that
-- compiles into physical schema. Left empty on purpose; this migration
-- only claims the schema so Phase 1's migrations can assume it exists.

CREATE SCHEMA IF NOT EXISTS metadata;

COMMENT ON SCHEMA metadata IS 'The metadata core: feature_classes, fields, domains, network_rules, and every other table that defines and compiles the rest of the system. Populated starting Phase 1 -- empty as of this migration.';
