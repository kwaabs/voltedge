-- 0066_seed_map_default_view.sql
-- Purpose: Phase 39 -- seed the admin-configurable default map view (the
-- center lon/lat and zoom the geographic map opens on when the app starts).
--
-- Until now this was hardcoded in the web client (voltedge-map.tsx's
-- DEFAULT_CENTER/DEFAULT_ZOOM = a London placeholder centered on the seed
-- data, never a real service territory). An admin now sets it from the
-- Map settings page (GET/PUT /map-settings) so a deployment opens on its
-- own network without a rebuild.
--
-- Three scalar keys rather than one JSON blob: they mirror the client's
-- three primitives (lon, lat, zoom), each independently parsed with a
-- fallback, and reuse iam.system_settings -- the one global key/value store
-- every runtime toggle already lives in (local_auth_enabled 0024,
-- territory_enforcement_enabled 0029). Values are stored as text and parsed
-- server-side; the seed keeps the exact placeholder the client shipped so a
-- fresh install behaves identically until an admin changes it. ON CONFLICT
-- DO NOTHING keeps this idempotent and preserves an operator's chosen value.

INSERT INTO iam.system_settings (key, value) VALUES
  ('map_default_center_lng', '-0.127'),
  ('map_default_center_lat', '51.507'),
  ('map_default_zoom', '15')
ON CONFLICT (key) DO NOTHING;
