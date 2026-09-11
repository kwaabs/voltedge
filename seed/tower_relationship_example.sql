-- seed/tower_relationship_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives under
-- /seed on purpose, same reasoning as the other seed files.
-- Depends on seed/tower_example.sql (TOWER) and seed/connectivity_example.sql
-- (MV_CONDUCTOR) having already been applied -- this file only adds a
-- metadata.relationship_classes row between two feature classes that already
-- exist, it creates no feature classes of its own.
--
-- TOWER supports MV_CONDUCTOR -- the tower sibling of the POLE relationship in
-- seed/relationship_example.sql. This is what lets a tower carry a DOUBLE
-- CIRCUIT (docs/phase-35-double-circuit.md): a double circuit is two MV_CONDUCTOR
-- runs (Feeder 1 / Feeder 2, tagged via circuit_ref) sharing one TOWER, each run
-- linked to the tower by a spatial_support relationship. Cardinality is
-- many_to_many -- deliberately, not one_to_many -- because a conductor span is
-- supported by more than one tower (one at each end) and a tower supports more
-- than one span; one_to_many would wrongly cap a conductor to a single tower.
-- Because TOWER is network_role='none' it is not a traced node, so sharing the
-- structure never connects the two feeders electrically -- they stay
-- topologically separate.
--
-- Apply with psql after seed/tower_example.sql and seed/connectivity_example.sql,
-- then nothing further needs compiling -- relationship_classes has no physical
-- footprint of its own, it is read directly by api/internal/relationship.

INSERT INTO metadata.relationship_classes (name, origin_feature_class_id, destination_feature_class_id, kind, cardinality)
SELECT 'TOWER supports MV_CONDUCTOR', tower.id, mv.id, 'spatial_support', 'many_to_many'
FROM metadata.feature_classes tower, metadata.feature_classes mv
WHERE tower.code = 'TOWER' AND mv.code = 'MV_CONDUCTOR'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.relationship_classes
    WHERE origin_feature_class_id = tower.id AND destination_feature_class_id = mv.id AND kind = 'spatial_support'
  );
