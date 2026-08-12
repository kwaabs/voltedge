-- seed/relationship_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this
-- lives under /seed on purpose, same reasoning as the other seed files.
-- Depends on seed/pole_example.sql (POLE) and
-- seed/connectivity_example.sql (MV_CONDUCTOR) having already been
-- applied -- this file only adds a metadata.relationship_classes row
-- between two feature classes that already exist, it doesn't create any
-- feature classes of its own.
--
-- POLE supports MV_CONDUCTOR, per docs/metadata-core.md's POLE sample:
-- "Relates to a conductor only via feature_relationships (kind =
-- spatial_support), explicitly established." Cardinality is
-- many_to_many here -- deliberately, not one_to_many -- because a real
-- conductor span is physically supported by more than one pole (one at
-- each end), and a real pole supports more than one span; one_to_many
-- would wrongly cap a conductor to a single supporting pole. See
-- docs/phase-2-relationships.md's "Decisions made" for the fuller
-- reasoning and for how a genuinely one_to_many pairing is exercised
-- instead (a second, synthetic relationship_classes row below, used
-- only to demonstrate cardinality enforcement).
--
-- Apply with psql after seed/pole_example.sql and
-- seed/connectivity_example.sql, then nothing further needs compiling
-- -- relationship_classes has no physical footprint of its own, it's
-- read directly by api/internal/relationship.

INSERT INTO metadata.relationship_classes (name, origin_feature_class_id, destination_feature_class_id, kind, cardinality)
SELECT 'POLE supports MV_CONDUCTOR', pole.id, mv.id, 'spatial_support', 'many_to_many'
FROM metadata.feature_classes pole, metadata.feature_classes mv
WHERE pole.code = 'POLE' AND mv.code = 'MV_CONDUCTOR'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.relationship_classes
    WHERE origin_feature_class_id = pole.id AND destination_feature_class_id = mv.id AND kind = 'spatial_support'
  );

-- A second, synthetic relationship class over the same two feature
-- classes, deliberately one_to_many, so api/internal/relationship's
-- cardinality enforcement has something real to reject against without
-- needing a third feature class just for this. "composite" is a stand-in
-- kind chosen only to disambiguate from the spatial_support row above --
-- establish requests hit this one only when they explicitly say
-- kind = "composite".

INSERT INTO metadata.relationship_classes (name, origin_feature_class_id, destination_feature_class_id, kind, cardinality)
SELECT 'POLE test-fixture composite (cardinality demo, one_to_many)', pole.id, mv.id, 'composite', 'one_to_many'
FROM metadata.feature_classes pole, metadata.feature_classes mv
WHERE pole.code = 'POLE' AND mv.code = 'MV_CONDUCTOR'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.relationship_classes
    WHERE origin_feature_class_id = pole.id AND destination_feature_class_id = mv.id AND kind = 'composite'
  );
