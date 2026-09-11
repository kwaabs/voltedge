-- 0075_widen_relationship_kind_protection.sql
-- Purpose: widen metadata.relationship_classes.kind's CHECK to admit the two
-- protection relationship kinds Phase (protection) Slice A introduces:
--   * protection_trip    -- a PROTECTIVE_RELAY trips a switching device
--                           (which breaker/recloser this relay operates).
--   * protection_sensing -- a PROTECTIVE_RELAY senses an instrument
--                           transformer (which CT/VT feeds this relay).
-- Both are feature_relationships-style associations, NOT network_rules:
-- protection trip/control wiring is a documented association between
-- features, never an electrically traced connection (the open item noted in
-- seed/protective_relay_example.sql's header). relationship_classes is
-- exactly the right home -- it says the pairing is *legal*; Phase 2's
-- feature_relationships records the specific established instances.
--
-- The original CHECK (migration 0010) is inline and unnamed, so Postgres
-- auto-named it relationship_classes_kind_check. Drop it and re-add the
-- widened list; no data rewrite (every existing row already satisfies the
-- superset). The Go layer (api/internal/relationship/rules.go matchClass)
-- matches kind dynamically against the seeded classes, so it needs no
-- change -- the DB CHECK is the only allowlist.

ALTER TABLE metadata.relationship_classes
    DROP CONSTRAINT relationship_classes_kind_check;

ALTER TABLE metadata.relationship_classes
    ADD CONSTRAINT relationship_classes_kind_check
    CHECK (kind IN ('spatial_support', 'containment', 'composite', 'protection_trip', 'protection_sensing'));

COMMENT ON COLUMN metadata.relationship_classes.kind IS 'spatial_support (e.g. pole holding up a conductor span), containment (e.g. substation area containing equipment), composite (a stronger, ownership-like relationship), protection_trip (a relay operates a switching device) or protection_sensing (a relay is fed by an instrument transformer).';
