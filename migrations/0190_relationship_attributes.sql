-- 0190_relationship_attributes.sql
-- Purpose: give runtime.feature_relationships a generic, nullable attributes jsonb
-- column. The link table (migration 0017) was created bare -- class, origin,
-- destination, established_at/by -- with nowhere to record anything ABOUT a
-- specific link. The first concrete need is the tower/pole elevation: a
-- spatial_support link (STRUCTURE supports CONDUCTOR) wants to say WHERE on the
-- structure that circuit hangs -- its side and crossarm level -- so an asymmetric
-- arrangement (a quad-circuit tower, 2 circuits left / 2 right) renders faithfully
-- instead of being guessed from the circuit count. That is a property of the link
-- (this conductor, on THIS tower, at this position), not of the conductor (which
-- spans many towers and can sit differently on each), so it belongs here.
--
-- Generic jsonb rather than named columns so the column serves any relationship
-- kind later without another migration, mirroring how features and connectivity
-- already carry free-form attributes. NULLABLE and with no default: every existing
-- relationship is untouched and reads back as no-attributes, and the elevation
-- falls back to its inferred template whenever the hint is absent.

ALTER TABLE runtime.feature_relationships ADD COLUMN IF NOT EXISTS attributes jsonb;

COMMENT ON COLUMN runtime.feature_relationships.attributes IS 'Optional free-form metadata about this specific established relationship (NULL for most). First use: a spatial_support link records the carried circuit''s arrangement on the structure as {"side":"L|R|C","level":N} (level 1 = highest crossarm), read by the tower/pole elevation view. A property of the link, not of either endpoint.';
