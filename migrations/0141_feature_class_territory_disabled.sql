-- 0141_feature_class_territory_disabled.sql
--
-- Per-territory feature-class deactivation (Slice 2 of the deactivation feature;
-- Slice 1 was the global on/off via feature_classes.status). An OPT-OUT disable
-- list: a class is available in every territory UNLESS a row here turns it off
-- for one. A disable authored on a territory CASCADES to every territory beneath
-- it (the check in api/internal/crud/availability.go walks gis.territories up to
-- the root), so turning a class off for a region turns it off for all its
-- districts without a row per district.
--
-- Enforced at write time (crud.enforceClassAvailability): a new feature landing in
-- a disabled territory is refused. Existing features are grandfathered -- this
-- never touches rows captured while the class was on.
CREATE TABLE metadata.feature_class_territory_disabled (
    feature_class_id uuid NOT NULL REFERENCES metadata.feature_classes(id) ON DELETE CASCADE,
    territory_id     uuid NOT NULL REFERENCES gis.territories(id)          ON DELETE CASCADE,
    created_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (feature_class_id, territory_id)
);

-- Reverse lookup for the territory-side view ("which classes are off here")
-- and for the ON DELETE CASCADE from a territory.
CREATE INDEX feature_class_territory_disabled_territory_idx
    ON metadata.feature_class_territory_disabled (territory_id);

COMMENT ON TABLE metadata.feature_class_territory_disabled IS
    'Per-territory feature-class deactivation (opt-out). One row = this class is unavailable for new features in this territory and, by cascade, every territory beneath it. Existing features are grandfathered.';
