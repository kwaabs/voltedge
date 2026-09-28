-- 0121_widen_relationship_kind_metering_accounting.sql
-- Purpose: widen metadata.relationship_classes.kind's CHECK to admit the
-- energy-accounting relationship kind the NETWORK_METER class introduces:
--   * metering_accounting -- a NETWORK_METER accounts for the energy through a
--                            network element (which conductor / busbar /
--                            transformer this meter meters).
--
-- This is deliberately its OWN kind, distinct from metering_sensing
-- (migration 0080). metering_sensing means "a meter is fed by an instrument
-- transformer" (an MV_METER reads its MV_CT / MV_VT secondaries) -- a wiring
-- association. metering_accounting means "this meter is the energy-accounting
-- point for that element" (a bulk / boundary / transformer-output meter placed
-- for energy balance between territories or downstream of a distribution
-- transformer) -- an accounting association, not a CT/VT feed. Overloading
-- metering_sensing would conflate the meter's instrument-transformer wiring
-- with what it is accounting for.
--
-- Unlike the customer-facing MV_METER / MV_METERING_POINT (Phase 43), a
-- NETWORK_METER is not owned by a commercial delivery point and carries no
-- tariff / account / readings -- readings live in an external MDM / head-end,
-- fetched at view time via external_ref. So the coupling to the metered element
-- is many_to_many and associative, never composite (the element is not "owned"
-- by the meter, and an element may carry a main + check meter).
--
-- The CHECK was last set by migration 0083 (auto-named
-- relationship_classes_kind_check). Drop it and re-add the widened list; no data
-- rewrite (every existing row already satisfies the superset). The Go
-- relationship matcher (api/internal/relationship/rules.go matchClass) resolves
-- kind dynamically against the seeded classes and switches only on CARDINALITY,
-- never kind, so it needs no change here -- the DB CHECK is the allowlist for
-- the DATA; the admin-authoring allowlists (api/internal/admin/rules.go
-- validRelationshipKinds + web RELATIONSHIP_KINDS in
-- admin-feature-class-detail.tsx) are widened in step so a user can also
-- hand-author a metering_accounting class.

ALTER TABLE metadata.relationship_classes
    DROP CONSTRAINT relationship_classes_kind_check;

ALTER TABLE metadata.relationship_classes
    ADD CONSTRAINT relationship_classes_kind_check
    CHECK (kind IN ('spatial_support', 'containment', 'composite', 'protection_trip', 'protection_sensing', 'metering_sensing', 'cable_routing', 'panel_bay', 'metering_accounting'));

COMMENT ON COLUMN metadata.relationship_classes.kind IS 'spatial_support (e.g. pole holding up a conductor span), containment (e.g. substation area containing equipment), composite (a stronger, ownership-like relationship), protection_trip (a relay operates a switching device), protection_sensing (a relay is fed by an instrument transformer), metering_sensing (a meter is fed by an instrument transformer), cable_routing (a cable is routed through a duct, or a duct laid in a trench), panel_bay (a control-building panel serves one or more circuit bays) or metering_accounting (a network meter accounts for the energy through a conductor, busbar or transformer).';
