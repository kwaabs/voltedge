-- 0080_widen_relationship_kind_metering.sql
-- Purpose: widen metadata.relationship_classes.kind's CHECK to admit the
-- metering relationship kind Phase 43 (MV metering point) introduces:
--   * metering_sensing -- an MV_METER is fed by an instrument transformer
--                          (which CT / VT feeds this meter).
-- This is the metering twin of protection_sensing (migration 0075): a meter,
-- like a protective relay, does NOT sit on the primary path -- at MV the
-- primary current never flows through the meter, it reads the CT/VT
-- SECONDARIES (5 A / 110 V). So "MV_METER measures via MV_CT / MV_VT" is a
-- documented association between features, NOT a network_rules connection --
-- exactly the shape protection_sensing already uses for a relay.
--
-- Kept as its OWN kind rather than overloading protection_sensing: the wiring
-- is identical but the purpose is not (metering is measurement, not
-- protection), and a shared kind would conflate a meter's CT/VT with a relay's
-- on the same instrument transformer. composite is wrong here too -- an MV_CT
-- can feed BOTH a meter and a relay, so it is not "owned" by the meter.
--
-- The CHECK was last set by migration 0075 (auto-named
-- relationship_classes_kind_check). Drop it and re-add the widened list; no
-- data rewrite (every existing row already satisfies the superset). The Go
-- layer (api/internal/relationship/rules.go matchClass) matches kind
-- dynamically against the seeded classes, so it needs no change -- the DB
-- CHECK is the only allowlist.

ALTER TABLE metadata.relationship_classes
    DROP CONSTRAINT relationship_classes_kind_check;

ALTER TABLE metadata.relationship_classes
    ADD CONSTRAINT relationship_classes_kind_check
    CHECK (kind IN ('spatial_support', 'containment', 'composite', 'protection_trip', 'protection_sensing', 'metering_sensing'));

COMMENT ON COLUMN metadata.relationship_classes.kind IS 'spatial_support (e.g. pole holding up a conductor span), containment (e.g. substation area containing equipment), composite (a stronger, ownership-like relationship), protection_trip (a relay operates a switching device), protection_sensing (a relay is fed by an instrument transformer) or metering_sensing (a meter is fed by an instrument transformer).';
