-- 0083_widen_relationship_kind_panel_bay.sql
-- Purpose: widen metadata.relationship_classes.kind's CHECK to admit the
-- panel<->bay relationship kind Phase 45 (substation control panels) introduces:
--   * panel_bay -- a control-building PANEL serves one or more circuit BAYs.
--                  Concretely:  SUBSTATION_PANEL serves BAY  (many_to_many)
--
-- This is the loose, associative coupling between the PHYSICAL enclosure axis
-- (SUBSTATION_PANEL -- the protection/control/metering panel you can inventory
-- and tag) and the LOGICAL circuit axis (BAY -- the incomer/feeder/coupler
-- circuit). It is deliberately its OWN kind rather than overloading:
--   * containment -- a panel does NOT contain a bay; a bay is a logical circuit,
--     not a device sitting inside the enclosure (the panel's actual contents --
--     relays, meters -- ARE wired with containment, separately). Reusing
--     containment here would also make BAY show up as diagram-placeable inside a
--     panel container, a side effect we do not want.
--   * composite -- a panel does not OWN a bay; either can exist without the
--     other, and the mapping is many-to-many (one shared protection panel serves
--     several bays; one bay's kit spans a protection + a control + a metering
--     panel).
--   * spatial_support / *_sensing / cable_routing -- all describe unrelated
--     physical couplings (structural support, instrument-transformer feeds,
--     cable routing).
-- The many_to_many mapping is exactly what decouples the physical panel from the
-- circuit it serves -- the point of modelling panels as first-class assets
-- rather than the old 1:1 "a bay IS its panel" simplification (Phase 34).
--
-- The CHECK was last set by migration 0082 (auto-named
-- relationship_classes_kind_check). Drop it and re-add the widened list; no data
-- rewrite (every existing row already satisfies the superset). The Go relationship
-- matcher (api/internal/relationship/rules.go matchClass) resolves kind
-- dynamically against the seeded classes and switches only on CARDINALITY, never
-- kind, so it needs no change here -- the DB CHECK is the allowlist for the DATA;
-- the admin-authoring allowlists (api/internal/admin/rules.go
-- validRelationshipKinds + web RELATIONSHIP_KINDS) are widened in step so a user
-- can also hand-author a panel_bay class.

ALTER TABLE metadata.relationship_classes
    DROP CONSTRAINT relationship_classes_kind_check;

ALTER TABLE metadata.relationship_classes
    ADD CONSTRAINT relationship_classes_kind_check
    CHECK (kind IN ('spatial_support', 'containment', 'composite', 'protection_trip', 'protection_sensing', 'metering_sensing', 'cable_routing', 'panel_bay'));

COMMENT ON COLUMN metadata.relationship_classes.kind IS 'spatial_support (e.g. pole holding up a conductor span), containment (e.g. substation area containing equipment), composite (a stronger, ownership-like relationship), protection_trip (a relay operates a switching device), protection_sensing (a relay is fed by an instrument transformer), metering_sensing (a meter is fed by an instrument transformer), cable_routing (a cable is routed through a duct, or a duct laid in a trench) or panel_bay (a control-building panel serves one or more circuit bays).';
