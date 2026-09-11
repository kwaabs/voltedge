-- ===========================================================================
-- Phase 34 Slice 2 -- BAY containment wiring (metadata-only).
--
-- Slots the BAY between the substation yard and its per-circuit plant, turning
-- the old flat containment (yard -> breaker/CT/VT/...) into two levels:
--
--     SUBSTATION -> BAY -> {breaker, isolator, sectionalizer, fuse,
--                                arrester, CT, VT, protective relay}
--
-- The shared MV busbar stays contained by the yard (see mv_busbar_example.sql)
-- because the bus spans every bay -- it is not owned by any one circuit.
--
-- Like every seed in this repo, this file defines legal pairings only
-- (metadata.relationship_classes). It seeds NO instances: actual
-- bay<->device links live in runtime.feature_relationships and are established
-- explicitly through the app/API.
--
-- Depends on: bay_example.sql (BAY class) and the MV plant seeds
-- (mv_busbar_example.sql etc.) and protective_relay_example.sql. Any
-- destination class not yet seeded is simply skipped by the JOIN.
--
-- Apply:  task seed-bay-containment
-- (relationship_classes have no physical footprint -- no compile step needed,
--  same as relationship_example.sql.)
-- ===========================================================================

-- ---------------------------------------------------------------------------
-- Retire the flat yard -> plant rows that BAY now owns. Guarded on
-- runtime.feature_relationships so a class that already has an established
-- instance is left untouched (the FK is RESTRICT anyway -- this keeps the
-- seed from erroring and never orphans real links). MV_BUSBAR is deliberately
-- absent: the shared bus stays yard-contained.
-- ---------------------------------------------------------------------------
DELETE FROM metadata.relationship_classes rc
USING metadata.feature_classes yard, metadata.feature_classes dst
WHERE rc.origin_feature_class_id = yard.id
  AND rc.destination_feature_class_id = dst.id
  AND rc.kind = 'containment'
  AND yard.code = 'SUBSTATION'
  AND dst.code IN (
    'MV_BREAKER', 'MV_ISOLATOR', 'MV_SECTIONALIZER',
    'MV_FUSE', 'MV_ARRESTER', 'MV_CT', 'MV_VT'
  )
  AND NOT EXISTS (
    SELECT 1 FROM runtime.feature_relationships fr
    WHERE fr.relationship_class_id = rc.id
  );

-- ---------------------------------------------------------------------------
-- SUBSTATION contains BAY (containment, one_to_many). Guarded on
-- (origin, destination, kind).
-- ---------------------------------------------------------------------------
INSERT INTO metadata.relationship_classes (name, origin_feature_class_id, destination_feature_class_id, kind, cardinality)
SELECT 'SUBSTATION contains BAY', yard.id, bay.id, 'containment', 'one_to_many'
FROM metadata.feature_classes yard, metadata.feature_classes bay
WHERE yard.code = 'SUBSTATION' AND bay.code = 'BAY'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.relationship_classes
    WHERE origin_feature_class_id = yard.id AND destination_feature_class_id = bay.id AND kind = 'containment'
  );

-- ---------------------------------------------------------------------------
-- BAY contains each per-circuit plant class (containment, one_to_many). One
-- set-generating INSERT over the destination classes; guarded on
-- (origin, destination, kind). Classes not yet seeded are skipped.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.relationship_classes (name, origin_feature_class_id, destination_feature_class_id, kind, cardinality)
SELECT 'BAY contains ' || dst.code, bay.id, dst.id, 'containment', 'one_to_many'
FROM metadata.feature_classes bay
JOIN metadata.feature_classes dst ON dst.code IN (
    'MV_BREAKER', 'MV_ISOLATOR', 'MV_SECTIONALIZER',
    'MV_FUSE', 'MV_ARRESTER', 'MV_CT', 'MV_VT', 'PROTECTIVE_RELAY'
)
WHERE bay.code = 'BAY'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.relationship_classes
    WHERE origin_feature_class_id = bay.id AND destination_feature_class_id = dst.id AND kind = 'containment'
  );
