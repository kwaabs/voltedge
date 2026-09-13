-- seed/generator_pv_fields.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations; this lives under
-- /seed on purpose, same as every other field seed.
--
-- ADDS THE GRID-FORMING (PV bus) INPUT FIELDS to the GENERATOR class -- the
-- asset-driven half of the Phase 52 Tier 2 "Both" PV model. Until now a
-- GENERATOR carried only Tier A grid-FOLLOWING dispatch fields (rated_kw /
-- dispatch_kw / power_factor -- a fixed negative-demand PQ injection; see
-- seed/generator_example.sql). The nodal (Y-bus) solver (nodal.go, S3) can
-- additionally treat a generator as a PV bus: it holds its terminal voltage at
-- a per-unit setpoint, injects its scheduled real power, and the solver finds
-- the reactive power (optionally clamped to a symmetric limit, PV -> PQ
-- switching). resolvePVSpec reads the two NAMES this seed adds:
--
--   * voltage_setpoint_pu  -- the per-unit magnitude the generator regulates its
--                             bus to (e.g. 1.02 = 2 % above nominal). PRESENT and
--                             > 0 => this generator is a PV bus in a nodal solve
--                             (grid-forming). Absent/<=0 => grid-following, the
--                             unchanged Tier A negative-demand PQ behaviour.
--   * reactive_limit_kvar  -- optional symmetric (+/-) reactive bound (kvar) on
--                             the PV bus's Q. When the solve would need more than
--                             this to hold the setpoint, the bus reverts to PQ at
--                             the clamped Q for that pass. Absent => unbounded Q.
--
-- Both NULLABLE, no subtype rules, no domain -- the same graceful-degradation
-- contract load_flow_fields.sql / generator_example.sql use: the engine reads
-- the NAMES and an absent value simply means "not a PV bus", never an
-- un-runnable study. Nullable also sidesteps the additive-compile 23502 landmine
-- on the already-populated gis.generator table (see the
-- compiler-additive-nullable-required note). A PV designation can ALSO be forced
-- or overridden per-run via the request's regulated[] entries (nodal.go), so
-- this seed is not required to exercise PV -- it makes the ASSET-DRIVEN path real
-- (a generator that IS a PV bus by its own stored setpoint, no request needed).
--
-- Depends only on seed/generator_example.sql having created the GENERATOR class
-- and the 'electrical' field_group (re-asserted idempotently below so this seed
-- is self-sufficient regardless of order).
--
-- Apply with psql, then RECOMPILE the class so the two columns materialise:
--   task seed-generator-pv-fields   (or the psql -f invocation directly)
--   task compile CODE=GENERATOR
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL has
-- been reviewed statically -- a live psql apply + task compile CODE=GENERATOR +
-- a nodal-load-flow re-check that a seeded voltage_setpoint_pu makes the
-- generator's node report role "pv" are still owed to call it verified.

INSERT INTO metadata.field_groups (code, name, sort_order) VALUES
    ('electrical', 'Electrical', 1)
ON CONFLICT (code) DO NOTHING;

-- The two grid-forming inputs. Guarded ON CONFLICT so a re-apply is idempotent
-- and this seed can run before OR after generator_example.sql.
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, NULL, fg.id, true
FROM metadata.feature_classes fc
JOIN (VALUES
    ('voltage_setpoint_pu', 'numeric'),
    ('reactive_limit_kvar', 'numeric')
) AS v(name, data_type) ON true
JOIN metadata.field_groups fg ON fg.code = 'electrical'
WHERE fc.code = 'GENERATOR'
ON CONFLICT (feature_class_id, name) DO NOTHING;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM metadata.feature_classes WHERE code = 'GENERATOR') THEN
        RAISE EXCEPTION 'missing feature_class GENERATOR -- apply seed/generator_example.sql first (then re-apply this seed, then: task compile CODE=GENERATOR)';
    END IF;
    RAISE NOTICE 'seed/generator_pv_fields.sql applied: GENERATOR gained nullable voltage_setpoint_pu + reactive_limit_kvar (grid-forming PV inputs). Now run: task compile CODE=GENERATOR so the columns materialise on gis.generator.';
END $$;
