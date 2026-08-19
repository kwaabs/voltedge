-- seed/load_flow_fields.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives
-- under /seed on purpose, same reasoning as the other seed files.
-- Phase 8 (load-flow engine, api/internal/powerflow). The five nullable
-- numeric fields the sweep reads for its own inputs. Committing them as a
-- seed rather than leaving them as live-only admin add-field edits: the
-- engine reads these field NAMES directly (resolve.go / sweep.go), so on
-- any database without them every load flow silently returns flat nominal
-- voltage, zero current, and no transformer loading -- the engine is
-- correct but inert. This is the reproducible, idempotent form of the same
-- "add a field to an existing class" capability the admin UI exposes
-- (docs/phase-6-admin-metadata.md), so a fresh DB / CI / teammate gets the
-- same inputs the live DB already has.
--
-- All five are NULLABLE and carry NO subtype rules on purpose: the engine
-- treats an absent-or-unset value as "no demand / no rating / ideal stiff
-- source" and degrades gracefully (resolveDemand, resolveTransformerRating,
-- resolveSourceImpedance all return ok=false rather than erroring). A
-- survey that hasn't populated demand yet must not be un-runnable.
--
--   * SERVICE_POINT.demand_kw / .power_factor -- real (P, pf) demand at a
--     point of supply. Q is derived P*tan(acos(pf)); pf unset defaults to
--     unity. This is the only real load input the sweep has.
--   * DIST_XFMR.source_r_ohm / .source_x_ohm -- the Thevenin source
--     impedance behind the transformer, read ONLY when a load flow is
--     started from it; absent = ideal source.
--   * DIST_XFMR.rated_kva -- nameplate apparent-power rating; the
--     transformer's loading % is its own swept through-power over this.
--     NOTE: for that % to be meaningful, demand_kw must be entered on the
--     same phase basis as this nameplate (a 3-phase kVA nameplate wants
--     3-phase-total demand) -- see docs/phase-8-load-flow.md.
--
-- Depends on seed/service_point_example.sql (SERVICE_POINT) and the
-- substation seed that creates DIST_XFMR having been applied. The
-- 'electrical' field_group is re-asserted idempotently so this seed is
-- self-sufficient regardless of order.
--
-- Apply with psql, then recompile the two touched classes:
--   task seed-load-flow-fields   (or the psql -f invocation directly)
--   task compile CODE=SERVICE_POINT
--   task compile CODE=DIST_XFMR
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL
-- has been reviewed statically -- a live psql apply + the two `task
-- compile` runs + a load-flow re-check are still owed to call it verified.

INSERT INTO metadata.field_groups (code, name, sort_order) VALUES
    ('electrical', 'Electrical', 1)
ON CONFLICT (code) DO NOTHING;

-- SERVICE_POINT: real demand at the point of supply.
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, NULL, fg.id, true
FROM metadata.feature_classes fc
JOIN (VALUES
    ('demand_kw',    'numeric'),
    ('power_factor', 'numeric')
) AS v(name, data_type) ON true
JOIN metadata.field_groups fg ON fg.code = 'electrical'
WHERE fc.code = 'SERVICE_POINT'
ON CONFLICT (feature_class_id, name) DO NOTHING;

-- DIST_XFMR: source impedance (start-only) + nameplate rating.
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, NULL, fg.id, true
FROM metadata.feature_classes fc
JOIN (VALUES
    ('source_r_ohm', 'numeric'),
    ('source_x_ohm', 'numeric'),
    ('rated_kva',    'numeric')
) AS v(name, data_type) ON true
JOIN metadata.field_groups fg ON fg.code = 'electrical'
WHERE fc.code = 'DIST_XFMR'
ON CONFLICT (feature_class_id, name) DO NOTHING;
