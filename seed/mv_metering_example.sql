-- seed/mv_metering_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives
-- under /seed on purpose, same reasoning as the other seed files.
-- Phase 43, Slice 1 (MV metering point). See docs/phase-43-mv-metering.md.
--
-- MV metering -- the metered supply to an industrial / building-complex /
-- bulk customer taking supply at MV (11 kV and above). This is NOT LV
-- metering: at MV the primary current never flows through the meter, it reads
-- the CT/VT SECONDARIES. Two classes:
--   * MV_METERING_POINT -- the delivery-point LEAF, the commercial boundary
--     where the DNO's network hands off to the customer. junction, single
--     terminal T1, like SERVICE_POINT (a "trace to customer" lands here).
--   * MV_METER -- the meter DEVICE. network_role='none', NO terminals -- it
--     is not on the primary path; it is fed by the instrument transformers,
--     the same reason MV_VT/MV_CT are taps and a protective relay is off-path.
--     Reusing the inline LV METER class here would misrepresent the physics.
--
-- KEY MODELLING DECISIONS (user-locked 2026-09-08 via AskUserQuestion, all the
-- recommended option -- see docs/phase-43-mv-metering.md):
--
--   1. TWO classes, mirroring the LV SERVICE_POINT + METER split. The point
--      OWNS the meter (composite one_to_many) -- so an MV_METERING_POINT can
--      carry a main + check meter, or none yet. Not a scalar meter field.
--
--   2. MV_METER is a MEASUREMENT DEVICE, network_role='none', no terminals --
--      because at MV the primary current does not flow through the meter (it
--      reads CT 5 A / VT 110 V secondaries). Same off-path shape as a
--      protective relay. It is NOT a traced node.
--
--   3. The meter ties to its instrument transformers by RELATIONSHIP, not by
--      duplicated ratio fields: 'MV_METER measures via MV_CT' / 'measures via
--      MV_VT', kind 'metering_sensing' (migration 0080 -- the metering twin of
--      protection_sensing). Ratios are read from the linked real MV_CT/MV_VT
--      features; MV_METER carries NO ct_ratio/vt_ratio field.
--
-- MV_METER's measures-via relationships are many_to_many, deliberately: a
-- meter reads several CTs (one per phase) AND a CT/VT can feed more than one
-- meter (a main + check meter sharing a metering core), so one_to_many would
-- wrongly cap either side.
--
-- Depends on seed/mv_measuring_example.sql (MV_CT, MV_VT) and
-- seed/connectivity_example.sql (MV_CONDUCTOR) having been applied, and on
-- migration 0080 (the 'metering_sensing' relationship kind). The
-- MV_SWITCH-fed feed-in rule is naturally a no-op if the generic-switch seed
-- (MV_SWITCH) has not been applied -- its JOIN finds no class and inserts
-- nothing. Domains / field_groups are re-asserted idempotently so this seed is
-- self-sufficient regardless of order.
--
-- Apply with psql, then compile both new classes:
--   task seed-mv-metering   (or the psql -f invocation directly)
--   task compile CODE=MV_METERING_POINT
--   task compile CODE=MV_METER   (network_role='none' -> no terminals, like POLE/TOWER)
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL has
-- been reviewed statically -- a live psql apply, migration 0080, the two
-- compiles, and a trace-to-MV-customer + owns/measures-via resolution check are
-- still owed to call it verified.

INSERT INTO metadata.field_groups (code, name, sort_order) VALUES
    ('electrical', 'Electrical', 1),
    ('service', 'Service', 1),
    ('maintenance', 'Maintenance', 2)
ON CONFLICT (code) DO NOTHING;

-- MV_SUPPLY_VOLTAGE -- the customer's supply voltage. MV metering is always
-- 11 kV and above (never LV), so these are MV levels, mirroring the two
-- VOLTAGE_CLASS / VT_RATIO primaries already in the model. Coded, so trivially
-- extended (66 kV etc.) later. METERING_CLASS -- the meter's accuracy class
-- (IEC 62053): 0.2S / 0.5S for revenue metering at MV, 1.0 for general.
INSERT INTO metadata.domains (code, name, domain_type) VALUES
    ('MV_SUPPLY_VOLTAGE', 'MV supply voltage', 'coded'),
    ('METERING_CLASS', 'Metering accuracy class', 'coded')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domain_values (domain_id, code, label, sort_order)
SELECT d.id, v.code, v.label, v.sort_order
FROM metadata.domains d
JOIN (VALUES
    ('MV_SUPPLY_VOLTAGE', '11KV', '11 kV', 1),
    ('MV_SUPPLY_VOLTAGE', '33KV', '33 kV', 2),
    ('METERING_CLASS', '0.2S', 'Class 0.2S', 1),
    ('METERING_CLASS', '0.5S', 'Class 0.5S', 2),
    ('METERING_CLASS', '1.0',  'Class 1.0',  3)
) AS v(domain_code, code, label, sort_order) ON v.domain_code = d.code
ON CONFLICT (domain_id, code) DO NOTHING;

-- ------------------------------------------------- MV_METERING_POINT --
-- The delivery-point leaf. junction, single T1 -- the customer boundary a
-- trace ends at (never inline on a run). Subtypes are the customer class
-- (like SERVICE_POINT's), not a physical variant.
INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status, is_diagram_container, is_diagram_placeable, geometry_optional)
VALUES ('MV_METERING_POINT', 'MV Metering Point', 'point', 4326, 'junction', 'draft', false, true, true)
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, v.code, v.name
FROM metadata.feature_classes fc
JOIN (VALUES (1, 'industrial'), (2, 'commercial_complex'), (3, 'bulk_supply')) AS v(code, name) ON true
WHERE fc.code = 'MV_METERING_POINT'
ON CONFLICT (feature_class_id, code) DO NOTHING;

-- supply_voltage + contracted_demand_kva are the commercial terms of the
-- MV supply (required). tariff_ref / account_ref are the CIS/MDM join keys
-- (nullable -- a point can exist before it is billed, same as METER.account_ref).
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, v.nullable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('supply_voltage',        'text',    'MV_SUPPLY_VOLTAGE', 'electrical', false),
    ('contracted_demand_kva', 'numeric', NULL,               'service',    false),
    ('tariff_ref',            'text',    NULL,               'service',    true),
    ('account_ref',           'text',    NULL,               'service',    true)
) AS v(name, data_type, domain_code, group_code, nullable) ON true
LEFT JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code = 'MV_METERING_POINT'
ON CONFLICT (feature_class_id, name) DO NOTHING;

INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, true
FROM metadata.subtypes s
JOIN metadata.fields f ON f.feature_class_id = s.feature_class_id
WHERE s.feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'MV_METERING_POINT')
  AND f.name IN ('supply_voltage', 'contracted_demand_kva')
ON CONFLICT (subtype_id, field_id) DO NOTHING;

INSERT INTO metadata.terminals (feature_class_id, subtype_id, code, role, phase_capable)
SELECT fc.id, NULL, 'T1', 'primary', true
FROM metadata.feature_classes fc
WHERE fc.code = 'MV_METERING_POINT'
ON CONFLICT (feature_class_id, subtype_id, code) DO NOTHING;

-- --------------------------------------------------------- MV_METER --
-- The meter device. network_role='none' -> NOT a traced node, NO terminals
-- (like POLE/TOWER) -- it hangs off the CT/VT via the measures-via
-- relationships below, it is not on the primary path. is_diagram_placeable so
-- it can sit on a metering-cubicle schematic later (Phase 43 S2).
INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status, is_diagram_container, is_diagram_placeable, geometry_optional)
VALUES ('MV_METER', 'MV Meter', 'point', 4326, 'none', 'draft', false, true, true)
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, v.code, v.name
FROM metadata.feature_classes fc
JOIN (VALUES (1, 'main_meter'), (2, 'check_meter')) AS v(code, name) ON true
WHERE fc.code = 'MV_METER'
ON CONFLICT (feature_class_id, code) DO NOTHING;

-- meter_serial is the device identity (required); metering_class the accuracy
-- (required). demand_kva records the read max demand (nullable). account_ref
-- is the billing key (nullable). NO ct_ratio/vt_ratio -- read from the linked
-- MV_CT/MV_VT via the measures-via relationships (decision 3).
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, v.nullable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('meter_serial',   'text',    NULL,             'service',    false),
    ('metering_class', 'text',    'METERING_CLASS', 'electrical', false),
    ('demand_kva',     'numeric', NULL,             'service',    true),
    ('account_ref',    'text',    NULL,             'service',    true)
) AS v(name, data_type, domain_code, group_code, nullable) ON true
LEFT JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code = 'MV_METER'
ON CONFLICT (feature_class_id, name) DO NOTHING;

INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, true
FROM metadata.subtypes s
JOIN metadata.fields f ON f.feature_class_id = s.feature_class_id
WHERE s.feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'MV_METER')
  AND f.name IN ('meter_serial', 'metering_class')
ON CONFLICT (subtype_id, field_id) DO NOTHING;

-- No terminals for MV_METER: network_role='none'.

-- ------------------------------------------------------ network_rules --
-- The metering point is fed at MV -- from the customer's incoming cable
-- (MV_CONDUCTOR) or from a switch's load side (MV_SWITCH T2, e.g. an RMU way
-- or intake breaker). Leaf, so no onward rule. All phase_rule NULL (device
-- hops, not the phase-matched conductor splice). The MV_SWITCH rule is a
-- no-op if MV_SWITCH is not seeded (its JOIN yields no rows).
INSERT INTO metadata.network_rules (name, from_feature_class_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'MV_CONDUCTOR -> MV_METERING_POINT T1', mv.id, mp.id, t1.id, NULL, true
FROM metadata.feature_classes mv CROSS JOIN metadata.feature_classes mp
JOIN metadata.terminals t1 ON t1.feature_class_id = mp.id AND t1.code = 'T1'
WHERE mv.code = 'MV_CONDUCTOR' AND mp.code = 'MV_METERING_POINT'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = mv.id AND to_feature_class_id = mp.id AND to_terminal_id = t1.id
  );

INSERT INTO metadata.network_rules (name, from_feature_class_id, from_terminal_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'MV_SWITCH T2 -> MV_METERING_POINT T1', sw.id, t2.id, mp.id, t1.id, NULL, true
FROM metadata.feature_classes sw CROSS JOIN metadata.feature_classes mp
JOIN metadata.terminals t2 ON t2.feature_class_id = sw.id AND t2.code = 'T2'
JOIN metadata.terminals t1 ON t1.feature_class_id = mp.id AND t1.code = 'T1'
WHERE sw.code = 'MV_SWITCH' AND mp.code = 'MV_METERING_POINT'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = sw.id AND from_terminal_id = t2.id AND to_feature_class_id = mp.id AND to_terminal_id = t1.id
  );

-- -------------------------------------------------- relationship_classes --
-- Metadata only (no physical footprint, read by api/internal/relationship),
-- guarded on the UNIQUE name via NOT EXISTS.

-- The point OWNS its meter(s) -- composite (ownership), one_to_many: a point
-- may be metered by a main + check meter, or none yet; a meter belongs to one
-- point. Exact analogue of 'SERVICE_POINT owns METER (composite)'.
INSERT INTO metadata.relationship_classes (name, origin_feature_class_id, destination_feature_class_id, kind, cardinality)
SELECT 'MV_METERING_POINT owns MV_METER', mp.id, m.id, 'composite', 'one_to_many'
FROM metadata.feature_classes mp, metadata.feature_classes m
WHERE mp.code = 'MV_METERING_POINT' AND m.code = 'MV_METER'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.relationship_classes
    WHERE origin_feature_class_id = mp.id AND destination_feature_class_id = m.id AND kind = 'composite'
  );

-- The meter is fed by its instrument transformers -- metering_sensing
-- (migration 0080), many_to_many (a meter reads several CTs, one per phase; a
-- CT/VT can feed a main + check meter). Ratios are read from the linked
-- MV_CT/MV_VT, not duplicated on the meter.
INSERT INTO metadata.relationship_classes (name, origin_feature_class_id, destination_feature_class_id, kind, cardinality)
SELECT 'MV_METER measures via MV_CT', m.id, ct.id, 'metering_sensing', 'many_to_many'
FROM metadata.feature_classes m, metadata.feature_classes ct
WHERE m.code = 'MV_METER' AND ct.code = 'MV_CT'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.relationship_classes
    WHERE origin_feature_class_id = m.id AND destination_feature_class_id = ct.id AND kind = 'metering_sensing'
  );

INSERT INTO metadata.relationship_classes (name, origin_feature_class_id, destination_feature_class_id, kind, cardinality)
SELECT 'MV_METER measures via MV_VT', m.id, vt.id, 'metering_sensing', 'many_to_many'
FROM metadata.feature_classes m, metadata.feature_classes vt
WHERE m.code = 'MV_METER' AND vt.code = 'MV_VT'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.relationship_classes
    WHERE origin_feature_class_id = m.id AND destination_feature_class_id = vt.id AND kind = 'metering_sensing'
  );
