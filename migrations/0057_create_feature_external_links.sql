-- 0057_create_feature_external_links.sql
-- Purpose: runtime.feature_external_links -- outbound links from a
-- feature to external systems (CIS/billing, maintenance/work-order,
-- document stores, ...). A feature can have as many links as it likes;
-- this is the "many outbound links per feature" table, not a field.
--
-- Slice 1 records plain hyperlinks only (kind='hyperlink', url set): the
-- panel renders them as anchors that open the external system in a new
-- tab. The `kind` column is here from the start so Slice 2's live
-- API-fetch links (kind='api', pointing at a catalogued external_systems
-- row) need no schema churn -- they just start populating the api-only
-- columns a later migration will add.
--
-- Lives in the runtime schema (migration 0016) alongside
-- feature_connectivity and feature_relationships -- same family of
-- generic runtime instance data, not compiled per feature class.

CREATE TABLE runtime.feature_external_links (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    feature_class_id   uuid NOT NULL REFERENCES metadata.feature_classes(id),
    feature_id         uuid NOT NULL,
    kind               text NOT NULL DEFAULT 'hyperlink' CHECK (kind IN ('hyperlink', 'api')),
    label              text NOT NULL,
    url                text,
    created_at         timestamptz NOT NULL DEFAULT now(),
    created_by         text
);

COMMENT ON TABLE runtime.feature_external_links IS 'Outbound links from a feature to external systems. Many per feature. Slice 1 is hyperlinks (kind=''hyperlink'', url set); kind=''api'' is reserved for Slice 2''s live-fetch links. Written only via api/internal/externallink, which validates the target feature exists and (for hyperlinks) that the url is http/https.';
COMMENT ON COLUMN runtime.feature_external_links.feature_id IS 'No FK -- same polymorphic reasoning as feature_relationships.origin_feature_id: the target table depends on feature_class_id and varies per feature class. The API validates existence against the right physical table before writing here.';
COMMENT ON COLUMN runtime.feature_external_links.url IS 'The external URL for kind=''hyperlink''. Validated http/https-only by the API (javascript:/data: rejected). Null-eligible so Slice 2''s kind=''api'' rows -- which resolve their address from a catalogued system + resource path instead -- fit the same table.';
COMMENT ON COLUMN runtime.feature_external_links.created_by IS 'The acting user''s id (auth.ActorID), or null on a session-less path -- same convention as feature_relationships.established_by.';

CREATE INDEX feature_external_links_feature_idx ON runtime.feature_external_links (feature_class_id, feature_id);
