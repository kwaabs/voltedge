-- 0082_widen_relationship_kind_cable_routing.sql
-- Purpose: widen metadata.relationship_classes.kind's CHECK to admit the
-- cable-routing relationship kind Phase 44 (cable management) introduces:
--   * cable_routing -- a cable is routed THROUGH a civil conduit, and a duct is
--                       laid IN a trench. Concretely:
--                         CABLE_DUCT   carries {MV_CONDUCTOR, LV_CONDUCTOR}
--                         CABLE_TRENCH carries CABLE_DUCT
--                         CABLE_TRENCH carries {MV_CONDUCTOR, LV_CONDUCTOR} (direct-buried)
--
-- This is the loose coupling between the CIVIL axis (CABLE_TRENCH / CABLE_DUCT,
-- both network_role='none') and the electrical cables -- the same shape
-- spatial_support uses for POLE/TOWER holding up a conductor span, but for
-- underground conduit rather than overhead support. Kept as its OWN kind rather
-- than overloading spatial_support (that is overhead structural support -- a
-- pole holding a wire up), containment (a duct does not CONTAIN a cable as an
-- enclosure the way a chamber contains a joint; a cable is pulled THROUGH and
-- passes on), or composite (a cable is not OWNED by the duct -- it enters and
-- leaves, and a duct can be emptied and re-pulled). The docs/phase-44 coarse
-- duct-capacity model reads occupancy by COUNTing cable_routing rows whose
-- origin is a duct and destination is a conductor, so the kind is what makes
-- "spare ways" computable.
--
-- The CHECK was last set by migration 0080 (auto-named
-- relationship_classes_kind_check). Drop it and re-add the widened list; no data
-- rewrite (every existing row already satisfies the superset). The Go relationship
-- matcher (api/internal/relationship/rules.go matchClass) resolves kind
-- dynamically against the seeded classes, so it needs no change here -- the DB
-- CHECK is the allowlist for the DATA; the admin-authoring allowlists
-- (api/internal/admin/rules.go validRelationshipKinds + web RELATIONSHIP_KINDS)
-- are widened in step so a user can also hand-author a cable_routing class.

ALTER TABLE metadata.relationship_classes
    DROP CONSTRAINT relationship_classes_kind_check;

ALTER TABLE metadata.relationship_classes
    ADD CONSTRAINT relationship_classes_kind_check
    CHECK (kind IN ('spatial_support', 'containment', 'composite', 'protection_trip', 'protection_sensing', 'metering_sensing', 'cable_routing'));

COMMENT ON COLUMN metadata.relationship_classes.kind IS 'spatial_support (e.g. pole holding up a conductor span), containment (e.g. substation area containing equipment), composite (a stronger, ownership-like relationship), protection_trip (a relay operates a switching device), protection_sensing (a relay is fed by an instrument transformer), metering_sensing (a meter is fed by an instrument transformer) or cable_routing (a cable is routed through a duct, or a duct laid in a trench).';
