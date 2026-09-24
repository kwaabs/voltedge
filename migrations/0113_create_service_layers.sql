-- 0113_create_service_layers.sql
-- Purpose: iam.service_layers -- the admin-managed catalog of external/3rd-party
-- map service layers the geographic map can overlay: OGC WMS/WMTS, XYZ raster or
-- vector tiles, and Esri MapServer/FeatureServer + OGC WFS. It is the operational
-- sibling of iam.basemaps (migration 0077): basemaps are the single raster tile
-- source *under* everything and are XYZ-only, whereas a service layer is a
-- toggleable *overlay* that stacks, and can be any of several service types.
--
-- Like basemaps this is app/UI configuration, not network reference data, so it
-- lives in `iam` alongside iam.system_settings and iam.basemaps -- not gis. An
-- admin curates it through the Map settings page (a CRUD API + admin UI); the
-- map reads the enabled set from GET /service-layers to build its layers panel.
--
-- A layer's `service_type` discriminates how it is consumed (basemaps needed no
-- such column -- they are all XYZ):
--   wms          OGC WMS      -- raster GetMap, `layer_ids` = LAYERS
--   wmts         OGC WMTS     -- raster tile pyramid, `layer_ids` = layer id
--   xyz          XYZ tiles    -- {z}/{x}/{y} raster, like a basemap overlay
--   vector_tile  MVT tiles    -- {z}/{x}/{y} pbf, optional `style_url`
--   esri_map     Esri MapServer   -- dynamic raster export, `layer_ids` = sublayers
--   esri_feature Esri FeatureServer -- vector features, `layer_ids` = layer id
--   wfs          OGC WFS      -- vector features, `layer_ids` = typeName
--
-- Auth is per-layer and OPTIONAL: many public services need none, others need an
-- API key / basic / bearer secret. The secret is encrypted at rest exactly like
-- metadata.external_systems (migration 0058): the admin enters it once, the
-- server AES-256-GCM-seals it (api/internal/secretbox) into credential_enc, and
-- the plaintext is never stored or returned -- a response says only whether one
-- is set (has_credential). Only the later proxy slice decrypts it, server-side,
-- to build an authenticated outbound request. `proxied` marks whether the browser
-- reaches the upstream through our same-origin proxy (mandatory for authenticated
-- or CORS-blocked services) or directly (public, CORS-friendly raster).
--
-- No built-ins are seeded: unlike basemaps (which ship a known-good default set)
-- every service layer is site-specific, so the table starts empty and an operator
-- adds their own. Hence no is_builtin column -- every row deletes freely.

CREATE TABLE iam.service_layers (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    -- Stable string id the client persists (visibility/opacity state) and looks a
    -- row up by. Lowercase kebab by convention, like a basemap code.
    code           text NOT NULL UNIQUE,
    label          text NOT NULL,
    description    text,
    service_type   text NOT NULL
                      CHECK (service_type IN ('wms','wmts','xyz','vector_tile','esri_map','esri_feature','wfs')),
    -- Service endpoint: the tile URL template (xyz/vector_tile carry {z}/{x}/{y}),
    -- the WMS/WMTS/WFS base, or the Esri MapServer/FeatureServer root.
    url            text NOT NULL,
    -- Type-specific layer selector: WMS LAYERS csv, WMTS/Esri layer id(s),
    -- WFS typeName. NULL for a single-layer xyz/vector_tile source.
    layer_ids      text,
    -- Style JSON URL for a vector-tile source (Mapbox/Esri VTS style). NULL otherwise.
    style_url      text,
    -- Requested raster image format (image/png, image/jpeg) for wms/esri_map.
    image_format   text,
    -- Service protocol version (WMS 1.3.0, WFS 2.0.0) when it must be pinned.
    version        text,
    -- The CRS the layer is requested in. The map works in EPSG:3857; a service
    -- offering only another grid gets reprojected (a later slice registers proj4).
    crs            text NOT NULL DEFAULT 'EPSG:3857',
    -- Coverage extent [minLon,minLat,maxLon,maxLat] in EPSG:4326, for zoom-to and
    -- to skip requests outside coverage. NULL = unknown/global.
    extent         jsonb,
    -- Provider zoom ceiling; NULL = no cap (OL over-zooms the last level).
    max_zoom       integer CHECK (max_zoom IS NULL OR (max_zoom >= 0 AND max_zoom <= 24)),
    min_zoom       integer NOT NULL DEFAULT 0 CHECK (min_zoom >= 0 AND min_zoom <= 24),
    -- Default overlay opacity 0..1; the layers panel lets a user override live.
    default_opacity real NOT NULL DEFAULT 1.0 CHECK (default_opacity >= 0 AND default_opacity <= 1),
    -- Whether the service supports click-to-identify (WMS GetFeatureInfo / Esri
    -- identify / native vector hit-test). Drives whether the map offers identify.
    is_queryable   boolean NOT NULL DEFAULT false,
    -- Required credit line(s) the map's status bar renders, as a JSON array of
    -- {label, href} -- same shape as iam.basemaps.attribution.
    attribution    jsonb NOT NULL DEFAULT '[]'::jsonb,
    -- Reach the upstream through our same-origin proxy (true, the default -- hides
    -- credentials, solves CORS) or let the browser hit it directly (false, only
    -- safe for public CORS-friendly raster tiles).
    proxied        boolean NOT NULL DEFAULT true,
    -- How the outbound request authenticates -- OPTIONAL per layer. 'none' needs
    -- no secret; 'api_key' puts the secret in a named header or query param;
    -- 'basic' is username + secret-as-password; 'bearer' is Authorization: Bearer.
    auth_type      text NOT NULL DEFAULT 'none'
                      CHECK (auth_type IN ('none','api_key','basic','bearer')),
    -- Non-secret auth parameters: header name and/or query-param name for
    -- 'api_key', username for 'basic'. NULL for the kinds that don't use them.
    auth_header      text,
    auth_query_param text,
    auth_username    text,
    -- nonce||ciphertext||tag from secretbox.Encrypt. NULL when auth_type is
    -- 'none' (or no secret set yet). Never selected into an API response.
    credential_enc bytea,
    sort_order     integer NOT NULL DEFAULT 0,
    is_enabled     boolean NOT NULL DEFAULT true,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    updated_by     uuid
);

-- The map lists enabled layers in this order; the admin page lists all.
CREATE INDEX service_layers_sort_idx ON iam.service_layers (sort_order, label);

COMMENT ON TABLE iam.service_layers IS 'Admin catalog of external/3rd-party map service layers (WMS/WMTS/XYZ/vector-tile/Esri MapServer/Esri FeatureServer/WFS) the map overlays. App/UI config, not gis reference data. Per-layer optional auth; credentials AES-256-GCM encrypted at rest in credential_enc, only the server-side proxy decrypts them.';
COMMENT ON COLUMN iam.service_layers.credential_enc IS 'nonce||ciphertext||tag from api/internal/secretbox (AES-256-GCM). The API key / basic password / bearer token, sealed. Never returned in an API response -- responses expose only has_credential.';
