-- 0019_create_diagram_tables.sql
-- Purpose: runtime.diagram_instances and runtime.diagram_placements --
-- the last of the three "generic runtime infrastructure, not compiled
-- per feature class" families migration 0016's own comment already
-- named ahead of time (feature_connectivity/feature_relationships,
-- attachments, diagram tables). Lives in the runtime schema reserved
-- there, alongside those two, same reasoning: from/to (here,
-- container/placed) feature ids are polymorphic, the target table
-- varies by feature class, and Postgres has no cross-table FK for that.
--
-- docs/metadata-core.md's "Diagram containers" section is the design
-- this implements: a diagram_instance belongs to one container feature
-- (metadata.feature_classes.is_diagram_container = true, e.g. a
-- SUBSTATION_YARD), typed by a diagram_type domain value (diagram_type
-- is "just another row in domains, not a new lookup table", same
-- pattern as trace direction and snapping target_type -- see migration
-- 0004's own comment on domain_values). A diagram_placement puts any
-- feature -- one with real geometry (a second, independent coordinate
-- system from its geom column, same feature_id) or one with
-- geometry_type = 'none' (its only visual representation at all) --
-- at an x/y/rotation inside one diagram_instance.
--
-- No is_valid/legality gate here unlike network_rules -- placement
-- inside a diagram a user is actively editing has no "is this pairing
-- allowed" question the way establishing a real electrical connection
-- does; the only enforced legality is is_diagram_container itself (the
-- API validates a diagram_instance's container feature class has that
-- flag set before ever writing here).

CREATE TABLE runtime.diagram_instances (
    id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    container_feature_class_id uuid NOT NULL REFERENCES metadata.feature_classes(id),
    container_feature_id   uuid NOT NULL,
    diagram_type_value_id  uuid NOT NULL REFERENCES metadata.domain_values(id),
    name                   text,
    created_at             timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE runtime.diagram_instances IS 'One diagram (e.g. a one-line schematic) belonging to a single container feature. The API enforces container_feature_class_id has is_diagram_container = true before writing here -- absence of that flag is the rejection, same zero-trust-by-omission discipline as network_rules, just gating a feature class rather than a connection pairing.';
COMMENT ON COLUMN runtime.diagram_instances.container_feature_id IS 'No FK -- same polymorphic reasoning as feature_connectivity.from_feature_id: the target table depends on container_feature_class_id and varies per feature class. The API validates existence against the right physical table before writing here.';
COMMENT ON COLUMN runtime.diagram_instances.diagram_type_value_id IS 'A domain_values row under a DIAGRAM_TYPE domain (e.g. ONE_LINE) -- not a new lookup table, matching domain_values'' own doubling-up comment (migration 0004).';
COMMENT ON COLUMN runtime.diagram_instances.name IS 'Optional human label for this specific diagram -- a container could plausibly host more than one (e.g. a one-line and a control-scheme diagram for the same substation) even though nothing seeds more than one today.';

CREATE INDEX diagram_instances_container_idx ON runtime.diagram_instances (container_feature_id);

CREATE TABLE runtime.diagram_placements (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    diagram_instance_id uuid NOT NULL REFERENCES runtime.diagram_instances(id) ON DELETE CASCADE,
    feature_class_id    uuid NOT NULL REFERENCES metadata.feature_classes(id),
    feature_id          uuid NOT NULL,
    x                   numeric NOT NULL,
    y                   numeric NOT NULL,
    rotation            numeric NOT NULL DEFAULT 0,
    created_at          timestamptz NOT NULL DEFAULT now(),
    -- Explicitly named rather than left to Postgres's own derived
    -- default -- the natural default name here (table + all three
    -- columns + "_key") runs past Postgres's 63-byte identifier limit
    -- and would get silently truncated, making a later `COMMENT ON
    -- CONSTRAINT` (or any other reference to it by name) fragile.
    CONSTRAINT diagram_placements_one_per_diagram UNIQUE (diagram_instance_id, feature_class_id, feature_id)
);

COMMENT ON TABLE runtime.diagram_placements IS 'Where one feature sits on one diagram -- x/y/rotation in the diagram''s own local coordinate space, entirely independent of any real geom column the same feature might also have (docs/metadata-core.md: "two independent coordinate systems, same feature_id"). A feature with geometry_type = none has no other visual representation at all; one with real geometry can be placed on any number of diagrams while still living at one real location on the map.';
COMMENT ON COLUMN runtime.diagram_placements.feature_id IS 'No FK -- same polymorphic reasoning as diagram_instances.container_feature_id. The API validates existence against the right physical table before writing here.';
COMMENT ON COLUMN runtime.diagram_placements.rotation IS 'Degrees, diagram-local -- purely a rendering hint (e.g. a switch symbol drawn horizontally vs. vertically), never fed into any real-world calculation.';
COMMENT ON CONSTRAINT diagram_placements_one_per_diagram ON runtime.diagram_placements IS 'A feature appears at most once per diagram -- placing it again moves it (an UPDATE), not a second placement.';

CREATE INDEX diagram_placements_instance_idx ON runtime.diagram_placements (diagram_instance_id);
CREATE INDEX diagram_placements_feature_idx ON runtime.diagram_placements (feature_id);
