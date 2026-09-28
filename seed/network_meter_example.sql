-- seed/network_meter_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives
-- under /seed on purpose, same reasoning as the other seed files.
--
-- NETWORK_METER -- a network energy meter placed for ENERGY ACCOUNTING, not for
-- customer service. It is the non-commercial sibling of the Phase 43 metering
-- stack (MV_METERING_POINT + MV_METER), which is bound to a customer delivery
-- point with tariff / account. A NETWORK_METER instead meters a point of the
-- NETWORK -- a substation incomer, a feeder head, the output of a distribution
-- transformer, a bus section, or a tie between territories -- so the utility
-- can account for energy: how much a distribution transformer gave out to the
-- services downstream, how much was exchanged across a territory boundary, etc.
--
-- KEY MODELLING DECISIONS (user-locked 2026-09-26 via discussion):
--
--   1. ASSET REGISTRY ONLY -- no readings in VoltEdge. The meter is registered
--      as an identifiable asset on the geographical map; kWh / kVArh / demand
--      readings are NOT stored here. They live in an external MDM / head-end and
--      are fetched TEMPORARILY at view time, keyed by external_ref. So this
--      class carries meter identity + accuracy + instrument-transformer ratios,
--      never energy values or a time series. (Storing live readings as feature
--      attributes would misuse a GIS asset model.)
--
--   2. network_role='none', NO terminals -- like MV_METER (and POLE / TOWER):
--      the meter is an accounting overlay, not a topology node. The trace never
--      walks THROUGH it; "what is downstream of this meter" is answered by
--      tracing the element it meters (e.g. from the DIST_XFMR). No network_rules,
--      no trace barrier (no device_state).
--
--   3. VOLTAGE IS NOT STORED -- it is read from the element the meter is
--      attached to (the conductor / busbar / transformer carries the voltage),
--      the same way MV_METER reads its ratios from the linked MV_CT / MV_VT
--      rather than duplicating them. Mostly HV / MV, occasionally LV -- hence the
--      LV_CONDUCTOR target below -- so a fixed meter_voltage field would be both
--      redundant and a second source of truth.
--
--   4. It METERS the network element by RELATIONSHIP, kind 'metering_accounting'
--      (migration 0121 -- the accounting twin of metering_sensing, which is the
--      CT/VT feed). many_to_many, deliberately: an element may carry a main +
--      check meter, and a boundary meter accounts across a tie -- so neither
--      one_to_many nor composite (the element is not "owned" by the meter).
--
-- Depends on migration 0121 (the 'metering_accounting' relationship kind). The
-- METERING_CLASS / CT_RATIO / VT_RATIO domains + field_groups are re-asserted
-- idempotently so this seed is self-sufficient regardless of whether the MV
-- metering / measuring seeds ran. Each 'meters' rule is naturally a no-op if its
-- target class is not seeded (the JOIN finds no class and inserts nothing), so
-- this seed is safe on a partial network.
--
-- Apply with psql, then compile the class:
--   task seed-network-meter   (or the psql -f invocation directly)
--   task compile CODE=NETWORK_METER   (network_role='none' -> no terminals, like POLE/TOWER)
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL has
-- been reviewed statically -- a live psql apply, migration 0121, the compile,
-- and a 'meters' relationship-resolution check are still owed to call it verified.

INSERT INTO metadata.field_groups (code, name, sort_order) VALUES
    ('electrical', 'Electrical', 1),
    ('service', 'Service', 1),
    ('maintenance', 'Maintenance', 2)
ON CONFLICT (code) DO NOTHING;

-- METERING_CLASS re-asserted (seeded by seed/mv_metering_example.sql); harmless
-- if already present. CT_RATIO / VT_RATIO re-asserted (seed/mv_measuring_example.sql).
INSERT INTO metadata.domains (code, name, domain_type) VALUES
    ('METERING_CLASS', 'Metering accuracy class', 'coded'),
    ('CT_RATIO', 'CT ratio', 'coded'),
    ('VT_RATIO', 'VT ratio', 'coded')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domain_values (domain_id, code, label, sort_order)
SELECT d.id, v.code, v.label, v.sort_order
FROM metadata.domains d
JOIN (VALUES
    ('METERING_CLASS', '0.2S', 'Class 0.2S', 1),
    ('METERING_CLASS', '0.5S', 'Class 0.5S', 2),
    ('METERING_CLASS', '1.0',  'Class 1.0',  3),
    ('CT_RATIO', '100_5',  '100/5 A',  1),
    ('CT_RATIO', '200_5',  '200/5 A',  2),
    ('CT_RATIO', '400_5',  '400/5 A',  3),
    ('CT_RATIO', '800_5',  '800/5 A',  4),
    ('CT_RATIO', '1600_5', '1600/5 A', 5),
    ('VT_RATIO', '11KV_110', '11 kV / 110 V', 1),
    ('VT_RATIO', '33KV_110', '33 kV / 110 V', 2)
) AS v(domain_code, code, label, sort_order) ON v.domain_code = d.code
ON CONFLICT (domain_id, code) DO NOTHING;

-- ------------------------------------------------------ NETWORK_METER --
-- The meter DEVICE as a map-placeable asset. network_role='none' -> NOT a
-- traced node, NO terminals (like POLE/TOWER/MV_METER). geometry_optional +
-- is_diagram_placeable so it can also sit on a metering-cubicle / one-line
-- schematic later.
INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status, is_diagram_container, is_diagram_placeable, geometry_optional)
VALUES ('NETWORK_METER', 'Network Meter', 'point', 4326, 'none', 'draft', false, true, true)
ON CONFLICT (code) DO NOTHING;

-- Subtypes name the METERING PURPOSE (the accounting role), not a customer
-- class -- there is no customer here.
INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, v.code, v.name
FROM metadata.feature_classes fc
JOIN (VALUES
    (1, 'substation_incomer'),
    (2, 'feeder_head'),
    (3, 'transformer_metering'),
    (4, 'bus_section'),
    (5, 'boundary_exchange'),
    (6, 'interchange_tie'),
    (7, 'generation_export')
) AS v(code, name) ON true
WHERE fc.code = 'NETWORK_METER'
ON CONFLICT (feature_class_id, code) DO NOTHING;

-- meter_serial is the device identity (required); metering_class the accuracy
-- (required). ct_ratio / vt_ratio are the instrument-transformer ratios
-- (nullable -- not every accounting meter records them, and MV/HV feeds vary).
-- external_ref is the join key into the external MDM / head-end from which
-- readings are fetched at view time (nullable -- an asset can be registered
-- before it is wired to the readings system). NO voltage field (decision 3),
-- NO readings fields (decision 1).
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, v.nullable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('meter_serial',   'text', NULL,             'service',    false),
    ('metering_class', 'text', 'METERING_CLASS', 'electrical', false),
    ('ct_ratio',       'text', 'CT_RATIO',       'electrical', true),
    ('vt_ratio',       'text', 'VT_RATIO',       'electrical', true),
    ('external_ref',   'text', NULL,             'service',    true)
) AS v(name, data_type, domain_code, group_code, nullable) ON true
LEFT JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code = 'NETWORK_METER'
ON CONFLICT (feature_class_id, name) DO NOTHING;

-- meter_serial + metering_class required on every subtype.
INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, true
FROM metadata.subtypes s
JOIN metadata.fields f ON f.feature_class_id = s.feature_class_id
WHERE s.feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'NETWORK_METER')
  AND f.name IN ('meter_serial', 'metering_class')
ON CONFLICT (subtype_id, field_id) DO NOTHING;

-- No terminals for NETWORK_METER: network_role='none'.
-- No network_rules: it is off the primary path, placed and related, never
-- connected. No trace_barrier_rules: it carries no device_state.

-- -------------------------------------------------- relationship_classes --
-- NETWORK_METER meters <network element> -- kind 'metering_accounting'
-- (migration 0121), many_to_many. One class per target it can account for; each
-- is a no-op if that target class is not seeded (JOIN yields no rows). Voltage
-- and live readings are read THROUGH these links / the external system, never
-- stored on the meter (decisions 1 & 3).
INSERT INTO metadata.relationship_classes (name, origin_feature_class_id, destination_feature_class_id, kind, cardinality)
SELECT 'NETWORK_METER meters ' || tgt.code, nm.id, tgt.id, 'metering_accounting', 'many_to_many'
FROM metadata.feature_classes nm
JOIN metadata.feature_classes tgt ON tgt.code IN ('DIST_XFMR', 'POWER_XFMR', 'MV_BUSBAR', 'MV_CONDUCTOR', 'LV_CONDUCTOR')
WHERE nm.code = 'NETWORK_METER'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.relationship_classes rc
    WHERE rc.origin_feature_class_id = nm.id
      AND rc.destination_feature_class_id = tgt.id
      AND rc.kind = 'metering_accounting'
  );
