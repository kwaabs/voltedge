-- seed/mv_busbar_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives
-- under /seed on purpose, same reasoning as the other seed files.
-- Phase 27, Slice 4 (MV busbar + substation wiring). Adds the substation
-- bus and the connectivity that ties a yard together:
--   * MV_BUSBAR -- the substation bus. T1 incoming, T2 the equipotential
--     bar that fans out to every bay -- the exact LV_BUSBAR shape
--     (seed/lv_metering_example.sql, decision 2), scaled up to MV. A solid
--     bar, NOT a set of switchable ways (the bay switches ARE the Slice 1
--     devices), so it needs no N-terminal schema: many bays hang off the
--     one T2 tap, each a distinct feature_connectivity row.
--
-- SUBSTATION WIRING -- with the bus in place, four rule shapes make a whole
-- yard expressible (incomer -> bus -> bay switch -> feeder, plus bus
-- couplers and section switches):
--   A. MV_CONDUCTOR -> MV_BUSBAR T1     (a feeder / transformer tail onto
--                                        the bus incoming way)
--   B. MV_BUSBAR T2 -> MV_CONDUCTOR     (the bar fans out to a bay feeder)
--   C. MV_BUSBAR T2 -> {switch} T1      (the bar feeds a bay switch's line
--                                        side directly, no conductor between)
--   D. {switch} T2 -> MV_BUSBAR T1      (a switch's load side onto a bus --
--                                        an incomer breaker, a bus coupler,
--                                        or a bus-section switch)
-- where {switch} ranges over the Slice 1 devices (MV_BREAKER / MV_ISOLATOR /
-- MV_SECTIONALIZER). C and D together also express a bus coupler (two bus
-- instances joined through one breaker) and a bus-section switch, with no
-- new rule shapes. C/D are set-generating over the three device classes; if
-- Slice 1 hasn't been applied they simply insert zero rows (the join finds
-- no device), so this seed is order-tolerant.
--
-- SITE CONTAINMENT -- SUBSTATION (seed/substation_yard_example.sql) is
-- the site container; a single set-generating `containment` relationship_class
-- ties it to every MV plant class that exists, one_to_many -- the same
-- containment kind METER_CHAMBER uses for its busbar/meters. Rows for classes
-- not yet seeded are simply skipped.
--
-- MV_BUSBAR is geometry_optional = true / is_diagram_placeable = true (the
-- Phase 27 map-or-diagram-or-both model; see seed/mv_switching_example.sql).
--
-- Depends on seed/connectivity_example.sql (MV_CONDUCTOR) and
-- seed/substation_yard_example.sql (SUBSTATION). For the full wiring +
-- containment, apply after seed/mv_switching_example.sql,
-- seed/mv_protection_example.sql and seed/mv_measuring_example.sql -- but it
-- degrades gracefully if they're absent (fewer rows, no error). VOLTAGE_CLASS
-- / CONDITION_RATING + field_groups are re-asserted idempotently.
--
-- Apply with psql, then compile the new class:
--   task seed-mv-busbar   (or the psql -f invocation directly)
--   task compile CODE=MV_BUSBAR
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL
-- has been reviewed statically -- a live psql apply + the `task compile` run
-- + a downstream-trace-through-a-bus check are still owed to call it verified.

INSERT INTO metadata.field_groups (code, name, sort_order) VALUES
    ('electrical', 'Electrical', 1),
    ('maintenance', 'Maintenance', 2)
ON CONFLICT (code) DO NOTHING;

-- VOLTAGE_CLASS + CONDITION_RATING -- seeded upstream (connectivity example);
-- re-asserted idempotently so the fields below always have a domain.
INSERT INTO metadata.domains (code, name, domain_type) VALUES
    ('VOLTAGE_CLASS', 'Voltage class', 'coded'),
    ('CONDITION_RATING', 'Condition rating', 'coded')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domain_values (domain_id, code, label, sort_order)
SELECT d.id, v.code, v.label, v.sort_order
FROM metadata.domains d
JOIN (VALUES
    ('VOLTAGE_CLASS', '11KV', '11 kV', 1),
    ('VOLTAGE_CLASS', '33KV', '33 kV', 2),
    ('CONDITION_RATING', 'GOOD', 'Good', 1),
    ('CONDITION_RATING', 'FAIR', 'Fair', 2),
    ('CONDITION_RATING', 'POOR', 'Poor', 3)
) AS v(domain_code, code, label, sort_order) ON v.domain_code = d.code
ON CONFLICT (domain_id, code) DO NOTHING;

-- ---------------------------------------------------------------------------
-- MV_BUSBAR -- junction, T1 incoming + T2 the equipotential bar (fans out).
-- NO subtypes: an MV bus is classified by voltage (voltage_class), not by
-- switchgear construction. Leaving it subtype-less also lets the RMU fan-out
-- create an internal bus with an empty input (the RMU collapse, 2026-09-06 --
-- one MV bus class, the RMU just contains one). voltage_class + condition_rating
-- are NULLABLE for the same reason: a bare internal bus carries neither until a
-- crew fills them in.
-- ---------------------------------------------------------------------------
-- diagram_geometry_type = 'line' (migration 0053): MV_BUSBAR is a point
-- junction on the geographic map, but on a one-line/schematic it should
-- read as a spanning bar -- so the diagram canvas draws it as a wire while
-- the map keeps it a point. Pure metadata, no recompile.
INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status, is_diagram_container, is_diagram_placeable, geometry_optional, diagram_geometry_type)
VALUES ('MV_BUSBAR', 'MV Busbar', 'point', 4326, 'junction', 'draft', false, true, true, 'line')
ON CONFLICT (code) DO NOTHING;

-- Set it unconditionally too, so a MV_BUSBAR seeded before migration 0053
-- (ON CONFLICT DO NOTHING above skips the insert) still gets the override.
UPDATE metadata.feature_classes SET diagram_geometry_type = 'line' WHERE code = 'MV_BUSBAR';

-- No subtypes -- see the block comment above. voltage_class + condition_rating
-- are nullable (an internal RMU bus is created bare); a substation bus is
-- expected to carry them, but they are no longer enforced at create time.
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, v.nullable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('voltage_class', 'text', 'VOLTAGE_CLASS', 'electrical', true),
    ('condition_rating', 'text', 'CONDITION_RATING', 'maintenance', true)
) AS v(name, data_type, domain_code, group_code, nullable) ON true
LEFT JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code = 'MV_BUSBAR'
ON CONFLICT (feature_class_id, name) DO NOTHING;

INSERT INTO metadata.terminals (feature_class_id, subtype_id, code, role, phase_capable)
SELECT fc.id, NULL, v.code, v.role, v.phase_capable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('T1', 'primary', true),
    ('T2', 'other', true)
) AS v(code, role, phase_capable) ON true
WHERE fc.code = 'MV_BUSBAR'
ON CONFLICT (feature_class_id, subtype_id, code) DO NOTHING;

-- ---------------------------------------------------------------------------
-- network_rules -- the four substation-wiring shapes (see header A-D).
-- ---------------------------------------------------------------------------

-- A. Feeder / transformer tail onto the bus incoming way.
INSERT INTO metadata.network_rules (name, from_feature_class_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'MV_CONDUCTOR -> MV_BUSBAR T1', mv.id, b.id, t1.id, NULL, true
FROM metadata.feature_classes mv CROSS JOIN metadata.feature_classes b
JOIN metadata.terminals t1 ON t1.feature_class_id = b.id AND t1.code = 'T1'
WHERE mv.code = 'MV_CONDUCTOR' AND b.code = 'MV_BUSBAR'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = mv.id AND to_feature_class_id = b.id AND to_terminal_id = t1.id
  );

-- B. The bar fans out to a bay feeder -- one rule, any number of bays
--    (the LV_BUSBAR T2 -> METER analog).
INSERT INTO metadata.network_rules (name, from_feature_class_id, from_terminal_id, to_feature_class_id, phase_rule, is_valid)
SELECT 'MV_BUSBAR T2 -> MV_CONDUCTOR', b.id, t2.id, mv.id, NULL, true
FROM metadata.feature_classes b CROSS JOIN metadata.feature_classes mv
JOIN metadata.terminals t2 ON t2.feature_class_id = b.id AND t2.code = 'T2'
WHERE b.code = 'MV_BUSBAR' AND mv.code = 'MV_CONDUCTOR'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = b.id AND from_terminal_id = t2.id AND to_feature_class_id = mv.id
  );

-- C. The bar feeds a bay switch's line side directly (bus -> {switch} T1),
--    set-generating over the Slice 1 devices.
INSERT INTO metadata.network_rules (name, from_feature_class_id, from_terminal_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'MV_BUSBAR T2 -> ' || dev.code || ' T1', b.id, t2.id, dev.id, t1.id, NULL, true
FROM metadata.feature_classes b
JOIN metadata.terminals t2 ON t2.feature_class_id = b.id AND t2.code = 'T2'
JOIN metadata.feature_classes dev ON dev.code IN ('MV_BREAKER', 'MV_ISOLATOR', 'MV_SECTIONALIZER')
JOIN metadata.terminals t1 ON t1.feature_class_id = dev.id AND t1.code = 'T1'
WHERE b.code = 'MV_BUSBAR'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = b.id AND from_terminal_id = t2.id AND to_feature_class_id = dev.id AND to_terminal_id = t1.id
  );

-- D. A switch's load side onto a bus ({switch} T2 -> bus T1) -- incomer
--    breaker, bus coupler, or bus-section switch. Set-generating over the
--    Slice 1 devices; with C this expresses a coupler/section switch between
--    two bus instances with no extra rule shape.
INSERT INTO metadata.network_rules (name, from_feature_class_id, from_terminal_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT dev.code || ' T2 -> MV_BUSBAR T1', dev.id, t2.id, b.id, t1.id, NULL, true
FROM metadata.feature_classes b
JOIN metadata.terminals t1 ON t1.feature_class_id = b.id AND t1.code = 'T1'
JOIN metadata.feature_classes dev ON dev.code IN ('MV_BREAKER', 'MV_ISOLATOR', 'MV_SECTIONALIZER')
JOIN metadata.terminals t2 ON t2.feature_class_id = dev.id AND t2.code = 'T2'
WHERE b.code = 'MV_BUSBAR'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = dev.id AND from_terminal_id = t2.id AND to_feature_class_id = b.id AND to_terminal_id = t1.id
  );

-- ---------------------------------------------------------------------------
-- relationship_classes -- SUBSTATION contains the shared MV busbar
-- (containment, one_to_many), the same kind METER_CHAMBER uses. The bus is
-- station-level plant shared across every bay, so it hangs off the yard
-- directly. The per-circuit plant (breaker/isolator/sectionalizer/fuse/
-- arrester/CT/VT) used to be contained flat by the yard here too; Phase 34
-- re-parented it under a BAY (yard -> bay -> device), so those rows now live
-- in seed/bay_containment_example.sql, which also retires the old flat rows.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.relationship_classes (name, origin_feature_class_id, destination_feature_class_id, kind, cardinality)
SELECT 'SUBSTATION contains ' || dst.code, yard.id, dst.id, 'containment', 'one_to_many'
FROM metadata.feature_classes yard
JOIN metadata.feature_classes dst ON dst.code IN ('MV_BUSBAR')
WHERE yard.code = 'SUBSTATION'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.relationship_classes
    WHERE origin_feature_class_id = yard.id AND destination_feature_class_id = dst.id AND kind = 'containment'
  );
