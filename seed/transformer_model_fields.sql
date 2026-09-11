-- seed/transformer_model_fields.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives
-- under /seed on purpose, same reasoning as seed/load_flow_fields.sql (which
-- this extends). Phase 28, Slice 1a: the three nullable numeric fields the
-- load-flow sweep needs to treat DIST_XFMR as a real two-winding,
-- voltage-transforming node instead of a zero-impedance pass-through
-- (docs/phase-28-mv-lv-load-flow.md).
--
-- Committed as a seed, not left as live admin add-field edits, for exactly the
-- reason load_flow_fields.sql gives: the engine (api/internal/powerflow
-- resolve.go / sweep.go) reads these field NAMES directly, so on any database
-- missing them a load flow that crosses a transformer silently keeps the old
-- single-voltage behaviour -- correct but inert. This is the reproducible,
-- idempotent form of the same "add a field to an existing class" the admin UI
-- exposes.
--
-- All three are NULLABLE with NO subtype rules on purpose (same as the five
-- Phase 8 load-flow fields): the engine degrades gracefully -- a transformer
-- with no secondary_voltage stays a pass-through (no ratio), one with a ratio
-- but no impedance_pct is an ideal transformer (voltage steps, zero series
-- drop), and one with impedance_pct but no x_r_ratio falls back to a documented
-- default X/R. A survey that hasn't captured nameplate impedance yet must not
-- make the network un-runnable.
--
--   * secondary_voltage -- nominal LV line-to-line volts (e.g. 400). With the
--     existing coded primary_voltage (VOLTAGE_CLASS, 11KV/33KV parsed to volts
--     by the engine) this gives the turns ratio secondary/primary. Stored on
--     the same basis as primary_voltage (line-to-line nominal) so the ratio is
--     basis-independent -- see the doc.
--   * impedance_pct -- nameplate %Z. Drives the series impedance referred to
--     the secondary: Z_base = secondary_voltage^2 / (rated_kva*1000),
--     Z_ohm = impedance_pct/100 * Z_base. Needs rated_kva (already seeded by
--     load_flow_fields.sql) to be meaningful; unset => ideal ratio, no drop.
--   * x_r_ratio -- X/R of that impedance, splitting %Z into R and X. Unset =>
--     the engine's documented default X/R.
--
-- Depends on the DIST_XFMR seed (seed/connectivity_example.sql) and reuses the
-- 'electrical' field_group (re-asserted idempotently so this seed stands alone).
--
-- Apply with psql, then recompile the touched class:
--   task seed-transformer-fields   (or the psql -f invocation directly)
--   task compile CODE=DIST_XFMR
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL has
-- been reviewed statically -- a live psql apply + the compile + a load-flow
-- re-check across a transformer are still owed to call it verified.

INSERT INTO metadata.field_groups (code, name, sort_order) VALUES
    ('electrical', 'Electrical', 1)
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, NULL, fg.id, true
FROM metadata.feature_classes fc
JOIN (VALUES
    ('secondary_voltage', 'numeric'),
    ('impedance_pct',     'numeric'),
    ('x_r_ratio',         'numeric')
) AS v(name, data_type) ON true
JOIN metadata.field_groups fg ON fg.code = 'electrical'
WHERE fc.code = 'DIST_XFMR'
ON CONFLICT (feature_class_id, name) DO NOTHING;
