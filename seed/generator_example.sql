-- seed/generator_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives
-- under /seed on purpose, same reasoning as the other seed files.
--
-- TIER A EMBEDDED GENERATION (Phase 51 follow-on). The first first-class
-- generation ASSET in the model. Until now the network had exactly two
-- indirect notions of generation and no inventoried generator:
--   * the SOURCE is an INSTANCE, not a class -- a slack DIST_XFMR carrying
--     source_r_ohm/source_x_ohm (a Thevenin grid-infeed stand-in); and
--   * a DER existed ONLY as an analysis knob -- hosting.go models a
--     candidate generator as negative demand injected at a node, inside the
--     Phase 50 hosting-capacity study, never stored.
-- functional_role_backfill.sql explicitly RESERVED the 'source' electrical
-- role for "a future generation / grid-infeed class" -- this is that class.
--
-- WHAT THIS MODELS (Tier A, the 80% case): grid-TIED, non-grid-forming
-- embedded generation that injects a fixed (P, Q) at its point of common
-- coupling during a normal grid-connected sweep -- rooftop / commercial
-- SOLAR_PV, an exporting standby DIESEL, a WIND turbine, small HYDRO, a
-- BESS discharging, a GAS_CHP set. The power-flow engine treats it as a
-- FIXED NEGATIVE-DEMAND INJECTION at its node (resolveGeneration in
-- powerflow/resolve.go, netted in resolveNodeStates): the backward sweep
-- carries the injection up the tree as reduced upstream flow and the
-- forward sweep reads it as a local voltage rise -- the exact machinery
-- hosting.go already used, now driven by the asset's nameplate/dispatch
-- instead of a bisection search knob.
--
-- WHAT THIS DELIBERATELY DOES NOT MODEL (Tier B, a separate, larger job):
-- grid-FORMING generation -- a backup diesel islanding a feeder, a
-- microgrid, PV voltage regulation -- is a PV bus or a SECOND slack, which
-- the radial forward-backward sweep structurally cannot do (one slack, one
-- source->node path). That folds into the meshed Tier 2 nodal (multi-slack
-- Y-bus/Newton) solver, not here. See docs/phase-51 and the
-- gap-embedded-generation memory. A leading (VAR-absorbing) inverter mode
-- is likewise Tier B; Tier A injects P and Q at the stated power factor.
--
-- Depends on seed/connectivity_example.sql (DIST_XFMR, MV_CONDUCTOR) and
-- seed/lv_conductors_example.sql (LV_CONDUCTOR) having been applied, since
-- the network_rules below reference those classes. The 'electrical'
-- field_group is re-asserted idempotently so this seed is self-sufficient
-- regardless of order.
--
-- Apply with psql, then compile the new class:
--   task seed-generator   (or the psql -f invocation directly)
--   task compile CODE=GENERATOR
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL
-- has been reviewed statically -- a live psql apply + task compile + a
-- load-flow re-check are still owed to call it verified.

INSERT INTO metadata.field_groups (code, name, sort_order) VALUES
    ('electrical', 'Electrical', 1)
ON CONFLICT (code) DO NOTHING;

-- GENERATOR -- a point junction with one terminal (T1), so it hangs off the
-- network as a leaf injector at its point of common coupling (an LV
-- conductor, a transformer LV bus, or an MV conductor -- the three
-- network_rules below), exactly like a SERVICE_POINT hangs as a leaf load.
-- network_role 'junction' (a graph node that carries a power contribution),
-- NOT 'edge' -- it is a device, not a conductor.

INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status, is_diagram_container)
VALUES ('GENERATOR', 'Generator', 'point', 4326, 'junction', 'draft', false)
ON CONFLICT (code) DO NOTHING;

-- generation_type subtypes -- the kind of prime mover / source. Modelled as
-- a subtype (a fixed, small enum) rather than a separate coded field, the
-- same choice SERVICE_POINT makes for customer class. The subtype does not
-- change the engine's treatment (all inject as negative demand); it is
-- inventory/reporting metadata and a hook for future per-type behaviour
-- (e.g. a PV dispatch profile, a BESS state-of-charge model) under Tier B.
INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, v.code, v.name
FROM metadata.feature_classes fc
JOIN (VALUES
    (1, 'diesel'),
    (2, 'solar_pv'),
    (3, 'wind'),
    (4, 'hydro'),
    (5, 'battery'),
    (6, 'gas_chp')
) AS v(code, name) ON true
WHERE fc.code = 'GENERATOR'
ON CONFLICT (feature_class_id, code) DO NOTHING;

-- Dispatch fields, all NULLABLE and no subtype rules -- same graceful-
-- degradation contract load_flow_fields.sql uses: the engine
-- (resolveGeneration) reads these NAMES and treats an absent/unset value as
-- "no injection" (returns 0), so an un-populated generator is an inert
-- junction, never an un-runnable study. Nullable also sidesteps the
-- additive-compile 23502 landmine (a required column added to an already-
-- populated gis table) -- see the compiler-additive-nullable-required note.
--   * rated_kw     -- nameplate real output (kW). The injection magnitude
--                     used when no dispatch override is set.
--   * dispatch_kw  -- optional actual-output override (kW): curtailment, a
--                     part-loaded diesel, a PV inverter clipping, a BESS at
--                     partial discharge. When PRESENT it is authoritative
--                     even at 0 (a curtailed/offline unit injects nothing);
--                     only its ABSENCE falls back to rated_kw.
--   * power_factor -- operating pf. Q injected = P*tan(acos(pf)); unset
--                     defaults to unity (Q = 0), same rule resolveDemand
--                     uses. Reused field name -- a generator carries
--                     power_factor exactly as a load does, and the two never
--                     collide (a node is a load or a generator by which kW
--                     field it carries: demand_kw vs rated_kw/dispatch_kw).
--
-- Fault-contribution fields -- a generator is a SECOND fault source, so the
-- fault-current study (powerflow/genfault.go) adds its infeed to the bolted
-- three-phase fault at each node by superposition. Also nullable / graceful:
-- an un-surveyed generator with none of these set contributes no fault current
-- (an inert leaf in the fault study, exactly as an un-populated one injects no
-- load). Precedence, mirroring dispatch_kw over rated_kw:
--   * fault_contribution_ka       -- explicit prospective contribution into a
--                                    bolted 3-phase fault at the generator's
--                                    terminals (kA). Authoritative when set --
--                                    the interconnection-study figure, and the
--                                    natural input for a current-limited inverter
--                                    (whose fault current is a limit, not a
--                                    voltage-behind-reactance).
--   * subtransient_reactance_pct  -- the synchronous machine's X''d as a percent
--                                    on its OWN rated-kVA base (rated_kw / pf).
--                                    Used when no explicit kA is set: the classic
--                                    voltage-behind-sub-transient-reactance
--                                    model, I_contribution = I_rated / (X''d/100).
-- With neither set, an inverter subtype (solar_pv / battery) falls back to
-- ~1.2x rated current (power-electronics current limit); a synchronous subtype
-- (diesel / hydro / gas_chp / wind) gets NO default and contributes nothing,
-- since assuming an X''d would badly misstate its far larger infeed. The
-- machine's rated current is derived from the NAMEPLATE rated_kw (not
-- dispatch_kw): a curtailed but still-connected machine contributes its full
-- sub-transient fault current.
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, NULL, fg.id, true
FROM metadata.feature_classes fc
JOIN (VALUES
    ('rated_kw',                   'numeric'),
    ('dispatch_kw',                'numeric'),
    ('power_factor',               'numeric'),
    ('fault_contribution_ka',      'numeric'),
    ('subtransient_reactance_pct', 'numeric')
) AS v(name, data_type) ON true
JOIN metadata.field_groups fg ON fg.code = 'electrical'
WHERE fc.code = 'GENERATOR'
ON CONFLICT (feature_class_id, name) DO NOTHING;

-- Single terminal T1 (a generator is always a network leaf, never inline on
-- a run) -- mirrors SERVICE_POINT.T1.
INSERT INTO metadata.terminals (feature_class_id, subtype_id, code, role, phase_capable)
SELECT fc.id, NULL, 'T1', 'primary', true
FROM metadata.feature_classes fc
WHERE fc.code = 'GENERATOR'
ON CONFLICT (feature_class_id, subtype_id, code) DO NOTHING;

-- network_rules -- the three points of common coupling a Tier A generator
-- can connect at. All to_terminal_id = GENERATOR.T1; from side is whatever
-- feeds it. phase_rule NULL (device hop, not a phase-matched splice), all
-- guarded WHERE NOT EXISTS so re-apply is idempotent.

-- 1. LV_CONDUCTOR -> GENERATOR T1 -- the common case: LV-connected DER
--    (rooftop/commercial PV, a small exporting diesel) tapping an LV feeder,
--    exactly as LV_CONDUCTOR -> METER T1 taps it for a load.
INSERT INTO metadata.network_rules (name, from_feature_class_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'LV_CONDUCTOR -> GENERATOR T1', lv.id, g.id, t1.id, NULL, true
FROM metadata.feature_classes lv CROSS JOIN metadata.feature_classes g
JOIN metadata.terminals t1 ON t1.feature_class_id = g.id AND t1.code = 'T1'
WHERE lv.code = 'LV_CONDUCTOR' AND g.code = 'GENERATOR'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = lv.id AND to_feature_class_id = g.id AND to_terminal_id = t1.id
  );

-- 2. DIST_XFMR T2 -> GENERATOR T1 -- generation connected directly at the
--    distribution transformer's LV bus (a genset paralleled at the
--    substation LV board).
INSERT INTO metadata.network_rules (name, from_feature_class_id, from_terminal_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'DIST_XFMR T2 -> GENERATOR T1', xf.id, t2.id, g.id, t1.id, NULL, true
FROM metadata.feature_classes xf CROSS JOIN metadata.feature_classes g
JOIN metadata.terminals t2 ON t2.feature_class_id = xf.id AND t2.code = 'T2'
JOIN metadata.terminals t1 ON t1.feature_class_id = g.id AND t1.code = 'T1'
WHERE xf.code = 'DIST_XFMR' AND g.code = 'GENERATOR'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = xf.id AND from_terminal_id = t2.id AND to_feature_class_id = g.id AND to_terminal_id = t1.id
  );

-- 3. MV_CONDUCTOR -> GENERATOR T1 -- larger MV-connected generation (a
--    diesel plant, a solar farm exporting onto an 11 kV feeder).
INSERT INTO metadata.network_rules (name, from_feature_class_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'MV_CONDUCTOR -> GENERATOR T1', mv.id, g.id, t1.id, NULL, true
FROM metadata.feature_classes mv CROSS JOIN metadata.feature_classes g
JOIN metadata.terminals t1 ON t1.feature_class_id = g.id AND t1.code = 'T1'
WHERE mv.code = 'MV_CONDUCTOR' AND g.code = 'GENERATOR'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = mv.id AND to_feature_class_id = g.id AND to_terminal_id = t1.id
  );
