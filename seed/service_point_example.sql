-- seed/service_point_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this
-- lives under /seed on purpose, same reasoning as the other seed files.
-- A minimal meter/consumer feature class, the piece missing for any
-- trace that needs a real "customer" to start or end at (trace to
-- transformer, service connection trace, outage impact). A single
-- terminal (a service point is always a network leaf, never inline on
-- a run), connected directly to a distribution transformer's secondary
-- terminal -- there's no distinct LV/secondary conductor feature class
-- yet (see docs/open-items.md), so this deliberately simplifies the
-- real-world "meter fed by a short service drop off the transformer's
-- LV bus" into a direct terminal-to-terminal connection. Revisit if an
-- LV conductor class is ever added.
--
-- Also adds the network_rule letting a DIST_XFMR's secondary (T2) feed
-- an onward MV_CONDUCTOR, not just a service point -- needed so a
-- second DIST_XFMR instance can stand in for the power transformer at
-- a substation, feeding the existing feeder chain from upstream (see
-- docs/phase-4-utility-traces.md's "transformer to source" scenario).
-- The same network_rule works for both roles since network_rules key
-- off feature *class*, not instance -- nothing in the metadata model
-- distinguishes "distribution transformer" from "power transformer" as
-- separate classes; which role a given DIST_XFMR instance plays is
-- purely a function of how it's wired into the seeded topology.
--
-- Depends on seed/switching_device_example.sql having already been
-- applied (reuses its DIST_XFMR/MV_CONDUCTOR feature classes).
--
-- Apply with psql after seed/switching_device_example.sql, then compile:
--   task compile CODE=SERVICE_POINT

INSERT INTO metadata.field_groups (code, name, sort_order) VALUES
    ('service', 'Service', 1)
ON CONFLICT (code) DO NOTHING;

-- SERVICE_POINT -- junction, one terminal (T1), so it can be the leaf
-- end of a DIST_XFMR's secondary. Subtypes distinguish the kind of
-- customer, not a mounting/physical variant like most other classes'
-- subtypes -- deliberately kept this way rather than adding a separate
-- service_type field, since customer class is naturally a fixed,
-- small enum a subtype already models well.

INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status, is_diagram_container)
VALUES ('SERVICE_POINT', 'Service Point', 'point', 4326, 'junction', 'draft', false)
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, v.code, v.name
FROM metadata.feature_classes fc
JOIN (VALUES (1, 'residential'), (2, 'commercial'), (3, 'industrial')) AS v(code, name) ON true
WHERE fc.code = 'SERVICE_POINT'
ON CONFLICT (feature_class_id, code) DO NOTHING;

-- No attribute fields. A SERVICE_POINT deliberately carries NO meter_number:
-- the meter is a first-class feature (METER, seed/lv_metering_example.sql), and
-- the optional "this supply is metered" link is the composite
-- `SERVICE_POINT owns METER` relationship_class -- not a text attribute
-- duplicated on the service point. A meter_number column here was the old
-- single-representation-of-a-meter and is dropped: an SP may have zero, one, or
-- (one_to_many) several meters, none of which a scalar field could hold, and a
-- meter can exist with no SP at all. See docs/phase-9-materials.md.
--
-- For a database seeded before this change, the already-compiled column is
-- deprecated (not physically dropped) with:
--   UPDATE metadata.fields SET status = 'deprecated'
--    WHERE name = 'meter_number'
--      AND feature_class_id = (SELECT id FROM metadata.feature_classes
--                              WHERE code = 'SERVICE_POINT');
-- status <> 'active' makes it invisible to the compiler and CRUD (the physical
-- gis.service_point.meter_number column stays, per
-- api/internal/metadata/load.go's active-only filter). The deprecation MUST be
-- paired with relaxing the physical constraint --
--   ALTER TABLE gis.service_point ALTER COLUMN meter_number DROP NOT NULL;
-- -- otherwise, if that column was compiled NOT NULL, every new service_point
-- insert fails (CRUD stops supplying the deprecated field, and the compiler
-- never relaxes a deprecated column). Both steps live together in
-- seed/migrate_meter_number_to_meter.sql; run that on any pre-Phase-9 DB.

INSERT INTO metadata.terminals (feature_class_id, subtype_id, code, role, phase_capable)
SELECT fc.id, NULL, 'T1', 'primary', true
FROM metadata.feature_classes fc
WHERE fc.code = 'SERVICE_POINT'
ON CONFLICT (feature_class_id, subtype_id, code) DO NOTHING;

-- DIST_XFMR's secondary (T2) feeding a service point directly.

INSERT INTO metadata.network_rules (name, from_feature_class_id, from_terminal_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'DIST_XFMR T2 -> SERVICE_POINT T1', xf.id, t2.id, sp.id, t1.id, NULL, true
FROM metadata.feature_classes xf CROSS JOIN metadata.feature_classes sp
JOIN metadata.terminals t2 ON t2.feature_class_id = xf.id AND t2.code = 'T2'
JOIN metadata.terminals t1 ON t1.feature_class_id = sp.id AND t1.code = 'T1'
WHERE xf.code = 'DIST_XFMR' AND sp.code = 'SERVICE_POINT'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = xf.id AND from_terminal_id = t2.id AND to_feature_class_id = sp.id AND to_terminal_id = t1.id
  );

-- DIST_XFMR's secondary (T2) feeding an onward MV_CONDUCTOR -- the
-- "this instance is actually a substation power transformer, feeding a
-- feeder onward rather than a service point" role, same class, no new
-- network_rule needed per instance.

INSERT INTO metadata.network_rules (name, from_feature_class_id, from_terminal_id, to_feature_class_id, phase_rule, is_valid)
SELECT 'DIST_XFMR T2 -> MV_CONDUCTOR', xf.id, t2.id, mv.id, NULL, true
FROM metadata.feature_classes mv, metadata.feature_classes xf
JOIN metadata.terminals t2 ON t2.feature_class_id = xf.id AND t2.code = 'T2'
WHERE xf.code = 'DIST_XFMR' AND mv.code = 'MV_CONDUCTOR'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = xf.id AND from_terminal_id = t2.id AND to_feature_class_id = mv.id
  );
