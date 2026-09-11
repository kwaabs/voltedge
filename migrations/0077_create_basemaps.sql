-- 0077_create_basemaps.sql
-- Purpose: admin-managed map basemaps. Before this, the set of basemaps the
-- geographic map could show was a hardcoded frontend constant
-- (web/src/stores/basemap-store.ts) -- adding one (a satellite layer, a dark
-- style, an in-house tile server) meant a code change and a rebuild, and an
-- operator could never do it. This table makes the basemap list data: an admin
-- adds/edits/reorders/disables basemaps through the Map settings admin page, and
-- the map reads the list from the server (GET /basemaps), the same shape the
-- default map view became admin-configurable in migration 0066.
--
-- It lives in `iam` alongside iam.system_settings (the default-view store) --
-- this is app/UI configuration, not network reference data, so it does not
-- belong in gis. Modelled loosely on gis.circuits (migration 0062): a flat
-- lookup entity curated in-app through a CRUD API + admin UI.
--
-- A basemap is a raster XYZ tile source: `url_template` carries the tile URL
-- with {x}/{y}/{z} placeholders (their ORDER can vary -- Esri World Imagery is
-- {z}/{y}/{x}, cartocdn/OSM are {z}/{x}/{y}; OpenLayers' XYZ source substitutes
-- whichever placeholders are present, so the admin bakes the order into the
-- template). `tms` flips the Y axis for the rarer TMS-scheme servers.
-- `attribution` is the required credit line(s) the map's status bar renders
-- (OpenLayers' own attribution control is disabled), as a JSON array of
-- {label, href}. The blank/no-basemap choice is NOT a row here -- it has no tile
-- source and is always offered by the client as a built-in option.
--
-- `is_builtin` marks the five basemaps this migration seeds (OSM, three Carto
-- styles, Esri satellite): an admin may disable, reorder or edit them, but the
-- API refuses to DELETE them so the shipped set can't be lost by accident (a
-- custom basemap an admin added has is_builtin = false and deletes freely).

CREATE TABLE iam.basemaps (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    -- Stable string id the client persists as the user's chosen basemap
    -- (localStorage) and looks a row up by -- so it must survive edits, unlike
    -- the display label. Lowercase kebab by convention.
    code         text NOT NULL UNIQUE,
    label        text NOT NULL,
    url_template text NOT NULL,
    attribution  jsonb NOT NULL DEFAULT '[]'::jsonb,
    -- Provider zoom ceiling; NULL = no cap. Past it OpenLayers over-zooms the
    -- last level rather than requesting tiles that 404.
    max_zoom     integer CHECK (max_zoom IS NULL OR (max_zoom >= 0 AND max_zoom <= 24)),
    min_zoom     integer NOT NULL DEFAULT 0 CHECK (min_zoom >= 0 AND min_zoom <= 24),
    -- Flipped-Y (TMS) tile scheme; false = standard XYZ (slippy) scheme.
    tms          boolean NOT NULL DEFAULT false,
    sort_order   integer NOT NULL DEFAULT 0,
    is_enabled   boolean NOT NULL DEFAULT true,
    is_builtin   boolean NOT NULL DEFAULT false,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    updated_by   uuid
);

-- The map lists enabled basemaps in this order; the admin page lists all.
CREATE INDEX basemaps_sort_idx ON iam.basemaps (sort_order, label);

-- Seed the five built-ins -- the exact set the frontend used to hardcode, so the
-- map is unchanged the moment the server starts reading this table. All keyless
-- raster sources needing no account. ON CONFLICT keeps a re-apply idempotent.
INSERT INTO iam.basemaps (code, label, url_template, attribution, max_zoom, sort_order, is_builtin) VALUES
    ('osm', 'OpenStreetMap',
     'https://tile.openstreetmap.org/{z}/{x}/{y}.png',
     '[{"label":"© OpenStreetMap contributors","href":"https://www.openstreetmap.org/copyright"}]'::jsonb,
     19, 1, true),
    ('carto-voyager', 'Carto Voyager',
     'https://basemaps.cartocdn.com/rastertiles/voyager/{z}/{x}/{y}.png',
     '[{"label":"© OpenStreetMap contributors","href":"https://www.openstreetmap.org/copyright"},{"label":"© CARTO","href":"https://carto.com/attributions"}]'::jsonb,
     20, 2, true),
    ('carto-positron', 'Carto Positron (light)',
     'https://basemaps.cartocdn.com/rastertiles/light_all/{z}/{x}/{y}.png',
     '[{"label":"© OpenStreetMap contributors","href":"https://www.openstreetmap.org/copyright"},{"label":"© CARTO","href":"https://carto.com/attributions"}]'::jsonb,
     20, 3, true),
    ('carto-dark', 'Carto Dark Matter',
     'https://basemaps.cartocdn.com/rastertiles/dark_all/{z}/{x}/{y}.png',
     '[{"label":"© OpenStreetMap contributors","href":"https://www.openstreetmap.org/copyright"},{"label":"© CARTO","href":"https://carto.com/attributions"}]'::jsonb,
     20, 4, true),
    ('esri-imagery', 'Satellite (Esri)',
     'https://server.arcgisonline.com/ArcGIS/rest/services/World_Imagery/MapServer/tile/{z}/{y}/{x}',
     '[{"label":"© Esri, Maxar, Earthstar Geographics","href":"https://www.esri.com/en-us/legal/copyright-trademarks"}]'::jsonb,
     19, 5, true)
ON CONFLICT (code) DO NOTHING;
