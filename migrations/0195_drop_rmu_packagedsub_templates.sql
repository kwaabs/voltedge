-- 0195_drop_rmu_packagedsub_templates.sql
-- Retire the RMU and packaged-substation template tables: both fan-out engines
-- were folded into the single generic assembly engine (their recipes re-expressed
-- as metadata.assembly_templates / _items via seed/consolidated_assembly_templates
-- .sql). Nothing reads these tables anymore -- the /rmus and /packaged-substations
-- routes were unmounted (api/cmd/server/main.go) and the engine packages retired.
-- Placed RMUs / packaged subs are unaffected: they are ordinary features +
-- containment relationships + connections and reference no template row.
--
-- APPLY LAST, only after confirming the consolidated assembly templates place an
-- RMU + a packaged substation identically to the old engines -- this is the point
-- of no return for the old recipe tables (the old seeds can't repopulate after a
-- drop). Idempotent. Items first (FK to their parent), then the parents.

DROP TABLE IF EXISTS metadata.rmu_template_items;
DROP TABLE IF EXISTS metadata.rmu_templates;
DROP TABLE IF EXISTS metadata.packaged_substation_items;
DROP TABLE IF EXISTS metadata.packaged_substation_templates;
