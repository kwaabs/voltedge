-- 0060_create_map_annotations.sql
-- Purpose: runtime.map_annotations -- the geographic sibling of
-- runtime.diagram_elements (migration 0046). A diagram element is furniture in
-- the diagram's local, abstract x/y space; a map annotation is the same idea
-- geo-referenced: free-text notes, boxes/areas, lines, and arrows/leaders drawn
-- directly on the geographic map (SRID 4326) as markup/redlining. It points at
-- no feature and is never compiled -- it is NOT an asset, so it never appears in
-- a trace, in connectivity, or in the feature catalog; it is map decoration a
-- user draws and styles freely.
--
-- Why a bespoke table and not a feature class: the metadata-driven feature
-- system styles per CLASS (symbology_rules / label_def), not per instance, so a
-- feature class could give uniform notes but never "make THIS note red and that
-- one bigger." Redlining needs per-annotation styling, which is exactly what
-- diagram_elements.style already models -- so this mirrors that table's shape
-- (a typed structural core plus a heterogeneous `style` jsonb), swapping the
-- local x/y/path for a real PostGIS geometry.
--
-- Home: runtime, alongside diagram_elements and the other generic,
-- non-compiled runtime infrastructure -- despite carrying a 4326 geometry like
-- the gis.* feature tables, it is furniture, not a compiled feature class.
--
-- kind drives rendering/semantics; the geometry column holds whatever shape the
-- kind implies (text -> Point, box/area -> Polygon, line/arrow -> LineString).
-- A single geometry(Geometry, 4326) column carries all of them, the same way
-- diagram_elements uses one row shape for several kinds. Per-kind appearance
-- (colour, stroke width, dash, fill, fill opacity, font size, bold/italic,
-- arrowheads) lives in `style` jsonb rather than a wide fan of nullable
-- columns, exactly like diagram_elements.style -- and never carries any
-- real-world/network semantics.

CREATE TABLE runtime.map_annotations (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    kind         text NOT NULL CHECK (kind IN ('text', 'box', 'line', 'arrow')),
    geom         geometry(Geometry, 4326) NOT NULL,
    -- The note text: required in spirit for kind = 'text', an optional caption
    -- for the other kinds. Kept nullable so a bare box/line/arrow needs no
    -- label; the write path (Slice 2) enforces text presence for kind='text'.
    text         text,
    -- Per-annotation styling (stroke/fill/dash/colour/font/arrowheads). NOT
    -- NULL with a '{}' default, never nullzero -- the API always writes a
    -- non-empty object, same discipline as diagram_elements.style.
    style        jsonb NOT NULL DEFAULT '{}'::jsonb,
    -- Draw order within the annotation layer: lower is farther back. Boxes/
    -- areas typically sit behind lines and text.
    z_index      integer NOT NULL DEFAULT 0,
    -- Territory scoping, same plain-uuid (deliberately NOT a FK to
    -- gis.territories) convention as a feature's territory_id (migration 0028)
    -- and created_by/updated_by: spatial assignment and any "at least a
    -- district" invariant are enforced in the write path (Slice 2), not by the
    -- database. Nullable until that slice populates it.
    territory_id uuid,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz,
    -- Identity placeholders until auth lands, same as feature_connectivity/
    -- feature_relationships.established_by and a feature's created_by/updated_by.
    created_by   text,
    updated_by   text
);

COMMENT ON TABLE runtime.map_annotations IS 'Geo-referenced map furniture (the geographic sibling of runtime.diagram_elements): free-text notes, boxes/areas, lines, and arrows/leaders drawn as markup/redlining directly on the map. Points at no feature, never compiled, never an asset -- pure map decoration with per-annotation styling.';
COMMENT ON COLUMN runtime.map_annotations.kind IS 'text | box | line | arrow. Drives rendering/semantics; the geom column holds the matching shape (text->Point, box->Polygon, line/arrow->LineString).';
COMMENT ON COLUMN runtime.map_annotations.geom IS 'PostGIS geometry in SRID 4326 (lon/lat), generic subtype so one table holds points, lines, and polygons across the kinds. Read as GeoJSON (ST_AsGeoJSON), written from GeoJSON (ST_GeomFromGeoJSON), same wire convention as api/internal/crud.';
COMMENT ON COLUMN runtime.map_annotations.style IS 'Per-annotation rendering hints (stroke, dash, fill, fill_opacity, colour, font_size, bold, italic, arrowheads), never real-world semantics. Defaults to an empty object.';
COMMENT ON COLUMN runtime.map_annotations.z_index IS 'Draw order within the annotation layer: lower is farther back.';
COMMENT ON COLUMN runtime.map_annotations.territory_id IS 'Plain uuid, deliberately NOT a FK to gis.territories -- same no-enforced-join convention as a feature''s territory_id (migration 0028). Assigned in the write path, not by the database.';

-- Spatial index for the map''s bbox/territory-scoped reads (Slice 2/3).
CREATE INDEX map_annotations_geom_idx ON runtime.map_annotations USING GIST (geom);
-- Territory-scoped listing.
CREATE INDEX map_annotations_territory_idx ON runtime.map_annotations (territory_id);
