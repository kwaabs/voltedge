-- 0023_create_symbology_icons.sql
-- Purpose: symbology_icons -- the actual SVG markup behind a symbology
-- rule's style_def.icon_ref. Until now, icon_ref keyed into a hardcoded
-- frontend registry (web/src/lib/symbology-icons.ts); this moves that
-- markup into data, so an icon can be added or changed without a frontend
-- deploy. Symbology rules are unchanged -- they still reference an icon by
-- name via style_def.icon_ref (migrations/0012); this table is only where
-- that name resolves to real SVG, the same split docs/metadata-core.md
-- already draws (a rule is metadata; the pictogram is an asset).
--
-- Martin never reads this, same as symbology_rules -- it's served to the
-- frontend via GET /symbology/icons and resolved entirely client-side
-- (web/src/lib/symbology-icons.ts's iconDataUri). The frontend keeps a
-- small hardcoded fallback set, so a missing/typo'd icon_ref or a
-- not-yet-loaded fetch never renders a feature invisible.

CREATE TABLE metadata.symbology_icons (
    icon_ref   text PRIMARY KEY,
    svg        text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE metadata.symbology_icons IS 'The SVG markup a symbology_rules.style_def.icon_ref points at. Resolved client-side, never by Martin. Points get an icon; lines/polygons never reference one (they only ever carry color/width or fill/stroke in style_def).';
COMMENT ON COLUMN metadata.symbology_icons.icon_ref IS 'The name a symbology rule references (style_def.icon_ref). A rule names an icon; this row is the markup for that name.';
COMMENT ON COLUMN metadata.symbology_icons.svg IS 'Raw <svg> markup. If it contains the literal {{COLOR}} placeholder, the frontend substitutes a rule''s resolved color in (a single-color, tintable symbol -- e.g. a switch that is green when closed, red when open); if it has no placeholder it renders as-is (full-color artwork, the rule''s color no longer applies to it). MUST carry explicit width and height attributes, not just a viewBox -- Chromium resolves a missing intrinsic size to 150x150 for a detached Image(), which silently broke vector-tile icon rendering the one time it shipped without them (see docs/phase-3-symbology.md).';
