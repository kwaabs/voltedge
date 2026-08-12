-- 0007_create_feature_classes.sql
-- Purpose: feature_classes -- the root of the metadata core. Every other
-- metadata table hangs off this one. A row here describes what a feature
-- class *is*; it has no physical footprint (no generated table, no tile
-- layer) until the schema compiler has published it.

CREATE TABLE metadata.feature_classes (
    id                     uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code                   text NOT NULL UNIQUE,
    name                   text NOT NULL,
    geometry_type          text NOT NULL CHECK (geometry_type IN ('point', 'line', 'polygon', 'none')),
    srid                   integer,
    network_role           text NOT NULL DEFAULT 'none' CHECK (network_role IN ('edge', 'junction', 'container', 'none')),
    physical_table         text UNIQUE,
    status                 text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'active', 'deprecated')),
    is_diagram_container   boolean NOT NULL DEFAULT false,
    metadata_version_id    uuid REFERENCES metadata.metadata_versions(id),
    created_at             timestamptz NOT NULL DEFAULT now(),
    CHECK (geometry_type = 'none' OR srid IS NOT NULL)
);

COMMENT ON TABLE metadata.feature_classes IS 'The root metadata table: one row per feature class (MV_CONDUCTOR, POLE, DIST_XFMR, SUBSTATION_AREA, ...). Drives everything -- physical schema generation, the generic CRUD engine, Martin''s tile config, the edit form. A feature class does not exist to any consumer until it has gone through compile + publish.';
COMMENT ON COLUMN metadata.feature_classes.code IS 'Stable machine identifier, e.g. MV_CONDUCTOR. Used everywhere as the human-readable foreign reference in this doc''s examples; the real FK is always the uuid id.';
COMMENT ON COLUMN metadata.feature_classes.geometry_type IS 'point/line/polygon drive real geometry columns on the generated table. none means no geom column at all -- the feature only ever appears via diagram_placements, never on the map.';
COMMENT ON COLUMN metadata.feature_classes.srid IS 'Spatial reference for the generated geometry column. Required unless geometry_type = none, enforced by the CHECK below.';
COMMENT ON COLUMN metadata.feature_classes.network_role IS 'edge/junction participate in network_rules and get traced. container hosts a diagram (see is_diagram_container) and/or spatial containment, but is not itself a traced node. none is fully outside the network model (e.g. a pole).';
COMMENT ON COLUMN metadata.feature_classes.physical_table IS 'Fully-qualified name of the generated physical table once compiled (e.g. gis.mv_conductor). Null until the first successful publish.';
COMMENT ON COLUMN metadata.feature_classes.status IS 'draft = authoring, not yet compiled. active = compiled and live. deprecated = physical table may still exist but the class should not accept new features.';
COMMENT ON COLUMN metadata.feature_classes.is_diagram_container IS 'True for feature classes that can host a one-line/schematic diagram of the features inside them (e.g. SUBSTATION_AREA). Unrelated to network_role.';
COMMENT ON COLUMN metadata.feature_classes.metadata_version_id IS 'The metadata_versions row this feature class was last compiled and published under. Null while still draft.';
