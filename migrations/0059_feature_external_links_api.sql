-- 0059_feature_external_links_api.sql
-- Purpose: give runtime.feature_external_links the two columns a
-- kind='api' link needs (Phase 32 Slice 3, live API-fetch links). A
-- hyperlink resolves its address from `url`; an api link resolves it
-- from a catalogued external system (metadata.external_systems, migration
-- 0058) plus a path relative to that system's base_url. The kind
-- discriminator and the nullable url were already in place (0057), so
-- this only adds the api-only columns -- no reshape of existing rows.
--
--   system_id     -- which external system this link fetches from
--   resource_path -- the path appended to the system's base_url
--
-- ON DELETE SET NULL, not RESTRICT: deleting an external system leaves
-- its api links in place but orphaned (system_id NULL); the fetch proxy
-- then returns a clean "system no longer configured" error rather than
-- blocking the system delete. This matches the admin UI copy ("links
-- pointing at it will stop resolving"). No CHECK forcing api => system_id
-- NOT NULL precisely because SET NULL must be allowed to leave that state.

ALTER TABLE runtime.feature_external_links
    ADD COLUMN system_id     uuid REFERENCES metadata.external_systems(id) ON DELETE SET NULL,
    ADD COLUMN resource_path text;

COMMENT ON COLUMN runtime.feature_external_links.system_id IS 'For kind=''api'': the catalogued external system (metadata.external_systems) this link fetches live data from. Null for hyperlinks, and nulled if the referenced system is deleted (ON DELETE SET NULL) -- the fetch proxy then reports the system is no longer configured.';
COMMENT ON COLUMN runtime.feature_external_links.resource_path IS 'For kind=''api'': the path (and optional query) appended to the system''s base_url to form the request URL. Validated by the API to be relative -- no scheme or host -- so it cannot repoint the request at a different origin (SSRF hardening).';

CREATE INDEX feature_external_links_system_idx ON runtime.feature_external_links (system_id);
