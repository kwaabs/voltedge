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
 * Martin auto-publishes *every* geometry table in the gis schema, and
 * gis.territories has a geom column -- so it shows up in the catalog like a
 * feature class even though it isn't one. Territories render through their
 * own dedicated boundary overlay (Phase 11 Slice H, fed by
 * GET /territories/boundaries), toggled independently; drawing them as an
 * always-on feature-class tile layer too is wrong (they'd show even with the
 * overlay off). This is the one source id filtered out of the feature-class
 * catalog everywhere it's consumed, leaving the overlay as the single path
 * territories are ever drawn by. It matches the table name (gis.territories).
 */
export const TERRITORY_TILE_SOURCE_ID = "territories"

/** The catalog's source ids minus the non-feature-class ones (see
 * TERRITORY_TILE_SOURCE_ID) -- what actually belongs in the feature-class
 * layer list and the map's per-class tile layers. */
export function featureClassSourceIds(catalog: Catalog | undefined): string[] {
  if (!catalog) return []
  return Object.keys(catalog.tiles).filter((id) => id !== TERRITORY_TILE_SOURCE_ID)
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

/**
 * The feature-attribute name ol/format/MVT stores each feature's source-layer under
 * (its `layerName` option). In a COMPOSITE tile (/{a,b,c}/{z}/{x}/{y}) each source is
 * a named sub-layer, so this is how the style function and hit-detection recover which
 * feature class a feature belongs to -- `feature.get(MVT_LAYER_PROP)` is the source id
 * (the lowercased class code), the same string codeBySourceId is keyed by. A
 * non-default name avoids colliding with any real feature column called "layer".
 */
export const MVT_LAYER_PROP = "mvtLayer"

/**
 * A composite tile URL. The /tiles origin (the Go API's Phase 58 tile router, which
 * replaced Martin -- see api/internal/tiles/tiles.go) serves `/{a,b,c}/{z}/{x}/{y}` as
 * ONE tile: each listed source is rendered and the single-layer MVTs are concatenated
 * into one tile carrying each as its own named sub-layer, collapsing N per-class
 * requests into one.
 */
export function compositeUrlTemplate(sourceIds: string[], version = 0): string {
  const versionSuffix = version > 0 ? `?v=${version}` : ""
  return `${TILES_BASE}/${sourceIds.join(",")}/{z}/{x}/{y}${versionSuffix}`
}

/** One zoom band: the feature-class sources fetched together as a single composite
 * tile, gated to a minimum view zoom. */
export interface TileBand {
  key: "overview" | "feeder" | "street"
  sourceIds: string[]
  /** OL layer minZoom (exclusive) -- the earliest-showing member's display-zoom min,
   * nudged a hair below so the layer fetches exactly where its first class appears. */
  minZoom: number
}

/**
 * Buckets feature-class sources into 3 zoom bands by their display-zoom MIN (migration
 * 0090), so each band is one composite tile fetched only from the zoom its classes
 * appear at (keeps the per-class zoom-gating while collapsing ~60 requests to ~3). A
 * class with no configured min is treated as 0 (shows from the start -> overview band).
 * minZoomByClass is keyed by UPPERCASE class code. Thresholds 13 / 16 split
 * overview / feeder / street; empty bands are omitted.
 */
export function bandSources(sourceIds: string[], minZoomByClass: Record<string, number | undefined>): TileBand[] {
  const MID = 13
  const STREET = 16
  const buckets: Record<TileBand["key"], string[]> = { overview: [], feeder: [], street: [] }
  for (const id of sourceIds) {
    const min = minZoomByClass[id.toUpperCase()] ?? 0
    if (min < MID) buckets.overview.push(id)
    else if (min < STREET) buckets.feeder.push(id)
    else buckets.street.push(id)
  }
  const bands: TileBand[] = []
  for (const key of ["overview", "feeder", "street"] as const) {
    const ids = buckets[key]
    if (ids.length === 0) continue
    const mins = ids.map((id) => minZoomByClass[id.toUpperCase()] ?? 0)
    bands.push({ key, sourceIds: ids, minZoom: Math.min(...mins) - 0.001 })
  }
  return bands
}
