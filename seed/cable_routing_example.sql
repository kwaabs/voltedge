-- seed/cable_routing_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives under
-- /seed on purpose, same reasoning as the other seed files.
-- Phase 44 Slice 3 (docs/phase-44-cable-management.md): the cable_routing
-- relationship classes that COUPLE the civil axis (CABLE_TRENCH / CABLE_DUCT,
-- Slice 1) to the electrical cables and to each other -- the loose coupling that
-- lets a duct/trench say "these cables run through me" WITHOUT joining them
-- electrically (both civil classes are network_role='none', never traced), the
-- same way seed/tower_relationship_example.sql couples a TOWER to a conductor.
--
-- Five relationship classes, all on the new `cable_routing` kind (migration 0082
-- widens the CHECK to admit it) and all many_to_many:
--   CABLE_DUCT   carries MV_CONDUCTOR   -- a cable pulled through a duct
--   CABLE_DUCT   carries LV_CONDUCTOR
--   CABLE_TRENCH carries CABLE_DUCT     -- a duct laid in a trench
--   CABLE_TRENCH carries MV_CONDUCTOR   -- direct-buried cable (no duct)
--   CABLE_TRENCH carries LV_CONDUCTOR
--
-- many_to_many throughout, deliberately: a duct carries several cables and a
-- cable passes through several ducts along its route; a trench holds several
-- ducts; direct-buried cable can share a trench. one_to_many would wrongly cap
-- one side.
--
-- The COARSE duct-capacity model (docs/phase-44) reads a duct's spare ways as
-- way_count - COUNT(cable_routing rows whose origin is THIS duct and whose
-- destination is a conductor class) -- i.e. it counts occupancy off the
-- CABLE_DUCT->conductor rows only (the CABLE_TRENCH->CABLE_DUCT link runs the
-- other direction and is not a way-consumer). That count is what the
-- GET /features/CABLE_DUCT/{id}/capacity endpoint (this slice) returns.
--
-- These are relationship_classes rows only -- no feature classes, no physical
-- table, nothing to compile. They are read directly by api/internal/relationship
-- (matchClass) so an establish_relationship / relate action can create the
-- instances. Guarded on (origin, destination, kind) via NOT EXISTS.
--
-- Depends on migration 0082 (the cable_routing kind), seed/cable_management_
-- example.sql (CABLE_TRENCH, CABLE_DUCT), seed/connectivity_example.sql
-- (MV_CONDUCTOR) and seed/lv_conductors_example.sql (LV_CONDUCTOR). Apply with
-- psql after those; nothing to compile. task seed-cable-routing.

-- CABLE_DUCT carries {MV_CONDUCTOR, LV_CONDUCTOR} -- the way-consuming links.
INSERT INTO metadata.relationship_classes (name, origin_feature_class_id, destination_feature_class_id, kind, cardinality)
SELECT 'CABLE_DUCT carries ' || cond.code, duct.id, cond.id, 'cable_routing', 'many_to_many'
FROM metadata.feature_classes duct
JOIN metadata.feature_classes cond ON cond.code IN ('MV_CONDUCTOR', 'LV_CONDUCTOR')
WHERE duct.code = 'CABLE_DUCT'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.relationship_classes
    WHERE origin_feature_class_id = duct.id AND destination_feature_class_id = cond.id AND kind = 'cable_routing'
  );

-- CABLE_TRENCH carries CABLE_DUCT -- a duct laid in a trench (NOT a way-consumer).
INSERT INTO metadata.relationship_classes (name, origin_feature_class_id, destination_feature_class_id, kind, cardinality)
SELECT 'CABLE_TRENCH carries CABLE_DUCT', tr.id, duct.id, 'cable_routing', 'many_to_many'
FROM metadata.feature_classes tr
JOIN metadata.feature_classes duct ON duct.code = 'CABLE_DUCT'
WHERE tr.code = 'CABLE_TRENCH'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.relationship_classes
    WHERE origin_feature_class_id = tr.id AND destination_feature_class_id = duct.id AND kind = 'cable_routing'
  );

-- CABLE_TRENCH carries {MV_CONDUCTOR, LV_CONDUCTOR} -- direct-buried cable.
INSERT INTO metadata.relationship_classes (name, origin_feature_class_id, destination_feature_class_id, kind, cardinality)
SELECT 'CABLE_TRENCH carries ' || cond.code, tr.id, cond.id, 'cable_routing', 'many_to_many'
FROM metadata.feature_classes tr
JOIN metadata.feature_classes cond ON cond.code IN ('MV_CONDUCTOR', 'LV_CONDUCTOR')
WHERE tr.code = 'CABLE_TRENCH'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.relationship_classes
    WHERE origin_feature_class_id = tr.id AND destination_feature_class_id = cond.id AND kind = 'cable_routing'
  );
