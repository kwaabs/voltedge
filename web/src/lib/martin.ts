/**
 * Base path for Martin's tile server. Proxied by Vite in dev (see
 * vite.config.ts) the same way /api is, so the browser never talks to
 * Martin's actual port directly and needs no CORS config on Martin's
 * side either.
 */
const TILES_BASE = "/tiles"

export interface CatalogEntry {
  content_type: string
  description?: string
}

export interface Catalog {
  tiles: Record<string, CatalogEntry>
}

/**
 * Martin auto-discovers every geometry table in the gis schema and
 * publishes it as a tile source named after the table (see
 * migrations/0015's gis schema comment) -- so the catalog *is* the
 * live list of compiled, tile-servable feature classes. Fetching it
 * rather than hardcoding feature class codes keeps the map in step with
 * whatever's actually been compiled, the same metadata-driven spirit as
 * the rest of this app, one level down: Martin's catalog reflects
 * compiled physical tables, which themselves come from metadata.
 */
export async function fetchCatalog(): Promise<Catalog> {
  const res = await fetch(`${TILES_BASE}/catalog`)
  if (!res.ok) throw new Error(`Martin catalog request failed: ${res.status}`)
  return res.json()
}

/**
 * `version` is a pure cache-buster, not a real API parameter -- Martin
 * ignores it. OL's VectorTileSource.refresh() doesn't clear its
 * renderer-level tile cache the way a raster TileImage source does (it
 * only fires a 'change' event), so a feature added after a tile was
 * already fetched would otherwise never be requested again without a
 * full page reload. Passing a new `version` on save changes the URL --
 * and therefore the source's internal key -- which is what actually
 * makes OL discard cached tiles and refetch, per
 * src/stores/map-refresh-store.ts.
 */
export function tileUrlTemplate(sourceId: string, version = 0): string {
  const versionSuffix = version > 0 ? `?v=${version}` : ""
  return `${TILES_BASE}/${sourceId}/{z}/{x}/{y}${versionSuffix}`
}
