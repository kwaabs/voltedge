-- seed/arc_flash_geometry_fields.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations; this lives under
-- /seed on purpose, same as every other field seed.
--
-- Phase 53 Slice S2 -- the ASSET-DRIVEN half of the arc-flash "Both" model. The
-- S1 engine (api/internal/powerflow/arcflash.go, POST /analysis/arc-flash) runs
-- IEEE 1584-2002 on IEEE TYPICAL geometry keyed only by voltage, so a study runs
-- before any equipment geometry is captured. This seed adds the DECLARED,
-- per-feature geometry fields the engine now reads OFF the faulted node's own
-- attributes, overriding the typical defaults where real data exists. The four
-- field NAMES match the constants arcflash.go reads (arcElectrodeConfigField etc.):
--
--   * arc_electrode_configuration (coded, ARC_ELECTRODE_CONFIG domain) -- the
--       equipment class / electrode arrangement. Sets enclosed-vs-open (the K
--       terms) and the distance exponent x, plus the class's typical gap &
--       working distance. Values: switchgear (default) / mcc_panelboard / cable /
--       open_air. Absent => the engine assumes enclosed switchgear (conservative).
--   * arc_conductor_gap_mm (numeric) -- electrode gap in mm; overrides the class
--       typical (LV switchgear 32, 5 kV 104, 15 kV 153).
--   * arc_working_distance_mm (numeric) -- worker working distance in mm; the
--       per-feature value that WINS over the request-level working_distance_mm.
--   * arc_system_grounded (boolean) -- true selects K2 = -0.113 (grounded, the
--       LOWER-energy case). Absent => false (ungrounded), the conservative default.
--
-- RESOLUTION PRECEDENCE (arcflash.go arcGeometryFor): declared per-feature value >
-- study-level request override (working distance only) > IEEE typical default. The
-- result flags geometry_assumed whenever any numeric fell back to a typical value,
-- so a study is never silently trusted.
--
-- TARGET CLASSES -- the energized primary equipment that is a plausible arc-flash
-- WORK LOCATION and appears as a fault node: DIST_XFMR, POWER_XFMR, MV_BUSBAR,
-- MV_SWITCHGEAR_PANEL. Set-generating over the four; a class that isn't seeded on
-- this DB is simply skipped (the join yields no row), the same tolerance
-- switch_normal_state_fields.sql uses. Conductors are deliberately excluded -- an
-- arc-flash geometry is a property of the enclosure a worker opens, not a span.
--
-- ALL FOUR NULLABLE, no subtype rules, no default_override. These columns are
-- added to ALREADY-COMPILED gis tables that hold live rows, so a NOT NULL column
-- would fail to add (SQLSTATE 23502 -- the compiler-additive-nullable-required
-- landmine); nullable also gives the graceful-degradation contract the whole
-- arc-flash engine is built on (an unset field just means "use the typical").
--
-- Depends only on the target classes existing (and being compiled). The domain +
-- field group are asserted idempotently below so this seed stands alone regardless
-- of apply order.
--
-- Apply with psql, then RECOMPILE each touched class so the columns materialise:
--   task seed-arc-flash-geometry-fields   (or the psql -f invocation directly)
--   task compile CODE=DIST_XFMR
--   task compile CODE=POWER_XFMR
--   task compile CODE=MV_BUSBAR
--   task compile CODE=MV_SWITCHGEAR_PANEL
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL has
-- been reviewed statically -- a live psql apply + the `task compile` runs + an
-- /analysis/arc-flash re-check that a seeded arc_electrode_configuration /
-- arc_conductor_gap_mm flips geometry_assumed to false and changes the reported
-- incident energy are still owed to call it verified.

-- Field group for the arc-flash geometry inputs (keeps them together in the
-- attribute panel). Idempotent so this seed is self-sufficient.
INSERT INTO metadata.field_groups (code, name, sort_order) VALUES
    ('arc_flash', 'Arc flash', 5)
ON CONFLICT (code) DO NOTHING;

-- The coded electrode-configuration domain + its four values.
INSERT INTO metadata.domains (code, name, domain_type) VALUES
    ('ARC_ELECTRODE_CONFIG', 'Arc-flash electrode configuration', 'coded')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domain_values (domain_id, code, label, sort_order)
SELECT d.id, v.code, v.label, v.sort_order
FROM metadata.domains d
JOIN (VALUES
    ('ARC_ELECTRODE_CONFIG', 'switchgear',     'Switchgear (enclosed)',        1),
    ('ARC_ELECTRODE_CONFIG', 'mcc_panelboard', 'MCC / panelboard (enclosed)',  2),
    ('ARC_ELECTRODE_CONFIG', 'cable',          'Cable termination',            3),
    ('ARC_ELECTRODE_CONFIG', 'open_air',       'Open air',                     4)
) AS v(domain_code, code, label, sort_order) ON v.domain_code = d.code
ON CONFLICT (domain_id, code) DO NOTHING;

-- The coded electrode-configuration field on the four target classes.
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, 'arc_electrode_configuration', 'text', d.id, fg.id, true
FROM metadata.feature_classes fc
JOIN metadata.domains d ON d.code = 'ARC_ELECTRODE_CONFIG'
JOIN metadata.field_groups fg ON fg.code = 'arc_flash'
WHERE fc.code IN ('DIST_XFMR', 'POWER_XFMR', 'MV_BUSBAR', 'MV_SWITCHGEAR_PANEL')
ON CONFLICT (feature_class_id, name) DO NOTHING;

-- The three non-coded (numeric / boolean) geometry fields, domain NULL.
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, NULL, fg.id, true
FROM metadata.feature_classes fc
JOIN (VALUES
    ('arc_conductor_gap_mm',    'numeric'),
    ('arc_working_distance_mm', 'numeric'),
    ('arc_system_grounded',     'boolean')
) AS v(name, data_type) ON true
JOIN metadata.field_groups fg ON fg.code = 'arc_flash'
WHERE fc.code IN ('DIST_XFMR', 'POWER_XFMR', 'MV_BUSBAR', 'MV_SWITCHGEAR_PANEL')
ON CONFLICT (feature_class_id, name) DO NOTHING;

DO $$
DECLARE
    n_classes integer;
BEGIN
    SELECT count(*) INTO n_classes
    FROM metadata.feature_classes
    WHERE code IN ('DIST_XFMR', 'POWER_XFMR', 'MV_BUSBAR', 'MV_SWITCHGEAR_PANEL');
    IF n_classes = 0 THEN
        RAISE EXCEPTION 'none of DIST_XFMR / POWER_XFMR / MV_BUSBAR / MV_SWITCHGEAR_PANEL exist -- seed & compile at least one target class first';
    END IF;
    RAISE NOTICE 'seed/arc_flash_geometry_fields.sql applied: added nullable arc_electrode_configuration / arc_conductor_gap_mm / arc_working_distance_mm / arc_system_grounded to % target class(es). Now recompile each touched class (task compile CODE=...) so the columns materialise.', n_classes;
END $$;
