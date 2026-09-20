-- 0112_create_feature_pending_edits.sql
-- Purpose: Private drafts, Phase B -- the staging table that holds a scoped
-- user's PRIVATE, not-yet-published EDIT or DELETE of an ALREADY-PUBLISHED
-- feature. Phase A ([[phase-61-private-drafts]]) made a never-published feature
-- private to its author chain; Phase B lets an author revise or retire a feature
-- that is ALREADY public without the change becoming visible to anyone else until
-- they apply (publish) it. The published gis.<code> row stays untouched and
-- public to everyone; the author's pending change lives here and is overlaid onto
-- their OWN reads only (an actor-dependent read, not a filter).
--
-- ONE generic table, not a per-class gis.<code>_pending shadow: the overlay is
-- the same LEFT-JOIN/merge fragment for every feature class, so the compiler and
-- the per-class DDL are untouched (a per-class table would double the schema
-- surface for no gain). The trade-off -- overlaid values are applied for DISPLAY,
-- not made first-class in WHERE/ORDER BY -- is deliberate: an overlaid attribute
-- shows its new value but does not re-sort/re-filter a scoped list. See the
-- Phase B design notes on the topic memory.
--
-- Deliberately NO foreign key on feature_id (matches runtime.feature_registry /
-- gis.feature_history): feature_id spans every gis.<code> table and Postgres has
-- no cross-table FK. A pending edge whose base row is purged is cleaned up by the
-- apply/discard paths, not a constraint. author_id DOES FK iam.users: a pending
-- edit is that user's private workspace, so if the user is deleted their
-- unpublished edits go with them (ON DELETE CASCADE).

CREATE TABLE runtime.feature_pending_edits (
    id                 uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    feature_class_code text        NOT NULL,
    feature_id         uuid        NOT NULL,
    -- The author whose private overlay this is. A feature can carry one pending
    -- edit PER author (the unique constraint below), each private to that author.
    author_id          uuid        NOT NULL REFERENCES iam.users (id) ON DELETE CASCADE,
    -- The overlay envelope: only the changed pieces, shaped like the formatRow
    -- response so the read overlay can merge it key-by-key. Keys that may appear:
    -- "name", "subtype", "territory_id", and "attributes" (a partial field map).
    -- Geometry is kept OUT of here (see geom_json) so the tile SQL can feed it
    -- straight to ST_GeomFromGeoJSON without digging through jsonb.
    patch              jsonb       NOT NULL DEFAULT '{}'::jsonb,
    -- A privately changed geometry, as GeoJSON text (NULL = geometry unchanged).
    -- Separate column, not a patch key, so the tile overlay is a clean
    -- COALESCE(ST_GeomFromGeoJSON(pe.geom_json), t.geom).
    geom_json          text,
    -- true = the author privately DELETED this feature: their own reads hide it,
    -- everyone else still sees the live published row until they apply the delete.
    is_delete          boolean     NOT NULL DEFAULT false,
    -- The base row's updated_at at the moment this edit was first staged. On apply
    -- (publish), if the live row's updated_at has moved past this, someone else
    -- published a change first -> the apply is blocked as a conflict rather than
    -- silently clobbering them.
    base_version       timestamptz,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),

    -- One pending edit per (feature, author): a re-stage upserts the same row.
    UNIQUE (feature_class_code, feature_id, author_id)
);

COMMENT ON TABLE runtime.feature_pending_edits IS 'Private drafts Phase B: a scoped user''s unpublished private edit/delete of an already-published feature. One generic overlay table (not per-class); the published gis.<code> row is untouched and public, the pending change is overlaid onto the author''s own reads only. Applied (published) or discarded via the crud pending endpoints. No FK on feature_id (polymorphic); author_id cascades from iam.users.';
COMMENT ON COLUMN runtime.feature_pending_edits.patch IS 'Changed pieces only, shaped like the formatRow envelope (name/subtype/territory_id/attributes) for a key-by-key read merge. Geometry lives in geom_json, not here.';
COMMENT ON COLUMN runtime.feature_pending_edits.is_delete IS 'true = author privately retired the feature: hidden from their own reads, still live for everyone else until applied.';
COMMENT ON COLUMN runtime.feature_pending_edits.base_version IS 'The base row updated_at when first staged; a moved value at apply time means someone else published first -> conflict.';

-- The overlay access pattern: look up a feature''s pending edits by (class, id),
-- then narrow to the acting author. Covers both the single-row GET overlay and
-- the "which of these listed rows have a pending edit" batch.
CREATE INDEX feature_pending_edits_feature_idx
    ON runtime.feature_pending_edits (feature_class_code, feature_id);
-- The "my open private edits" pattern (a future dashboard / the author''s own
-- workspace), and the cascade lookup by author.
CREATE INDEX feature_pending_edits_author_idx
    ON runtime.feature_pending_edits (author_id);
