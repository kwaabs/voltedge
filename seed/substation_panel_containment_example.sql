-- ===========================================================================
-- Phase 45 Slice 1 -- SUBSTATION_PANEL containment + panel<->bay mapping
-- (metadata-only).
--
-- Wires the control-building panel into the model on TWO independent axes:
--
--   1. PHYSICAL containment (kind 'containment', one_to_many):
--        SUBSTATION       contains SUBSTATION_PANEL      (panels parked under
--                                                         the yard -- the user
--                                                         chose no separate
--                                                         CONTROL_BUILDING node)
--        SUBSTATION_PANEL contains {PROTECTIVE_RELAY, METER}
--                                                        (the enclosure's actual
--                                                         secondary contents)
--
--   2. LOGICAL association (kind 'panel_bay', many_to_many -- migration 0083):
--        SUBSTATION_PANEL serves BAY
--
-- The second axis is the whole point of modelling panels as assets: it DECOUPLES
-- the physical panel from the circuit it serves. A PROTECTIVE_RELAY can now be
-- contained by BOTH a BAY (which circuit it protects -- Phase 34) AND a
-- SUBSTATION_PANEL (which enclosure it physically sits in), because those are
-- two separate containment relationship_classes -- independent, optional, and
-- both one_to_many (a relay has at most one bay and at most one panel). The
-- panel<->bay mapping is many_to_many because a shared protection panel serves
-- several bays and a bay's kit spans several panels; it is deliberately NOT
-- containment (a panel does not hold a bay -- a bay is logical) nor composite
-- (neither owns the other). See migration 0083 and docs/phase-45-substation-panels.md.
--
-- Like every seed in this repo, this file defines legal pairings only
-- (metadata.relationship_classes). It seeds NO instances: actual panel<->relay,
-- yard<->panel and panel<->bay links live in runtime.feature_relationships and
-- are established explicitly through the app/API.
--
-- Depends on: substation_panel_example.sql (SUBSTATION_PANEL), plus
-- substation_yard_example.sql, bay_example.sql, protective_relay_example.sql and
-- the metering seed (METER). Any destination class not yet seeded is simply
-- skipped by the JOIN. Requires migration 0083 (the 'panel_bay' kind); without
-- it the panel<->bay INSERT fails the CHECK loudly, which is the intended signal
-- to run `task migrate` first.
--
-- Apply:  task seed-substation-panel-containment
-- (relationship_classes have no physical footprint -- no compile step needed,
--  same as bay_containment_example.sql.)
-- ===========================================================================

-- ---------------------------------------------------------------------------
-- SUBSTATION contains SUBSTATION_PANEL (containment, one_to_many). Guarded
-- on (origin, destination, kind). The panel can be parked directly under the
-- substation -- the fallback home when a site has no building. Phase 47 Slice 1
-- (seed/control_building_example.sql) adds CONTROL_BUILDING contains
-- SUBSTATION_PANEL as the honest physical home; this row is KEPT alongside it, so
-- a panel has two legal parents and picks one at runtime (building when present,
-- substation directly when not -- not all substations have a building).
-- ---------------------------------------------------------------------------
INSERT INTO metadata.relationship_classes (name, origin_feature_class_id, destination_feature_class_id, kind, cardinality)
SELECT 'SUBSTATION contains SUBSTATION_PANEL', yard.id, panel.id, 'containment', 'one_to_many'
FROM metadata.feature_classes yard, metadata.feature_classes panel
WHERE yard.code = 'SUBSTATION' AND panel.code = 'SUBSTATION_PANEL'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.relationship_classes
    WHERE origin_feature_class_id = yard.id AND destination_feature_class_id = panel.id AND kind = 'containment'
  );

-- ---------------------------------------------------------------------------
-- SUBSTATION_PANEL contains its secondary devices (containment, one_to_many).
-- One set-generating INSERT over the destination classes; guarded on
-- (origin, destination, kind). Classes not yet seeded are skipped.
--   * PROTECTIVE_RELAY -- protection/control panels hold the relays. This is the
--     relay's PHYSICAL parent, orthogonal to its BAY parent (the circuit).
--   * METER -- a metering panel holds the tariff/revenue meter(s).
-- CTs/VTs are deliberately absent: they live in the yard on the primary plant
-- (already BAY-contained) and are only WIRED to the panel, not housed in it.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.relationship_classes (name, origin_feature_class_id, destination_feature_class_id, kind, cardinality)
SELECT 'SUBSTATION_PANEL contains ' || dst.code, panel.id, dst.id, 'containment', 'one_to_many'
FROM metadata.feature_classes panel
JOIN metadata.feature_classes dst ON dst.code IN (
    'PROTECTIVE_RELAY', 'METER'
)
WHERE panel.code = 'SUBSTATION_PANEL'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.relationship_classes
    WHERE origin_feature_class_id = panel.id AND destination_feature_class_id = dst.id AND kind = 'containment'
  );

-- ---------------------------------------------------------------------------
-- SUBSTATION_PANEL serves BAY (panel_bay, many_to_many). The associative
-- mapping that decouples the physical panel from the logical circuit -- one
-- panel serves many bays, one bay spans many panels. Guarded on
-- (origin, destination, kind). Requires migration 0083.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.relationship_classes (name, origin_feature_class_id, destination_feature_class_id, kind, cardinality)
SELECT 'SUBSTATION_PANEL serves BAY', panel.id, bay.id, 'panel_bay', 'many_to_many'
FROM metadata.feature_classes panel, metadata.feature_classes bay
WHERE panel.code = 'SUBSTATION_PANEL' AND bay.code = 'BAY'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.relationship_classes
    WHERE origin_feature_class_id = panel.id AND destination_feature_class_id = bay.id AND kind = 'panel_bay'
  );
