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

export function tileUrlTemplate(sourceId: string): string {
  return `${TILES_BASE}/${sourceId}/{z}/{x}/{y}`
}
