import type { Coordinate } from "ol/coordinate"
import type { Extent } from "ol/extent"
import type Layer from "ol/layer/Layer"
import type Projection from "ol/proj/Projection"
import TileWMS from "ol/source/TileWMS"

import type { ServiceLayerRecord } from "@/hooks/use-service-layers"
import type { IdentifyFeatureResult, IdentifyLayerGroup } from "@/stores/identify-store"
import { serviceProxyBase, usesProxy } from "@/lib/service-layers-source"

/**
 * The server-round-trip side of click-to-identify (Slice 4): building and parsing
 * a WMS GetFeatureInfo or an Esri MapServer identify request for one raster
 * service layer. The vector families (WFS / Esri-feature / vector-tile) are
 * identified client-side by hit-testing already-rendered features in
 * voltedge-map.tsx, so they don't come through here.
 *
 * Every request rides the same-origin proxy when the layer is proxied/
 * authenticated (so credentials stay server-side and there's no CORS), exactly
 * like the render path -- the proxy forwards these GETs unchanged and relays the
 * JSON back. A direct (non-proxied) layer is fetched at the third party and then
 * depends on that service's CORS headers.
 */

/** Attribute keys, in priority order, that make a good one-line label for a
 * feature when present -- checked case-insensitively. */
const LABEL_KEYS = ["name", "label", "title", "display_name", "description", "id"]

/** Picks a human label from a feature's attributes, or undefined if none of the
 * common name-ish keys are present/non-empty. */
export function deriveFeatureLabel(properties: Record<string, unknown>): string | undefined {
  const lowered = new Map<string, unknown>()
  for (const [k, v] of Object.entries(properties)) lowered.set(k.toLowerCase(), v)
  for (const key of LABEL_KEYS) {
    const v = lowered.get(key)
    if (v !== null && v !== undefined && String(v).trim() !== "") return String(v)
  }
  return undefined
}

/** The context one identify request needs, derived from the map view at click
 * time. */
export interface IdentifyContext {
  coordinate: Coordinate
  resolution: number
  projection: Projection
  extent: Extent
  /** The map viewport size in pixels [width, height]. */
  sizePx: [number, number]
}

/** GET a URL expecting JSON, with cookies for the same-origin proxy path. */
async function fetchJson(url: string): Promise<unknown> {
  const resp = await fetch(url, { credentials: "same-origin", headers: { Accept: "application/json" } })
  if (!resp.ok) throw new Error(`HTTP ${resp.status}`)
  return resp.json()
}

/** Turns a list of raw attribute objects into identify results with derived
 * labels, dropping any geometry-ish member so the panel shows attributes only. */
function toResults(list: Array<Record<string, unknown>>): IdentifyFeatureResult[] {
  return list.map((raw) => {
    const properties = { ...raw }
    delete properties.geometry
    delete properties.geometry_name
    return { label: deriveFeatureLabel(properties), properties }
  })
}

/** Extracts feature attributes from a WMS GetFeatureInfo JSON body, tolerating
 * both GeoJSON (`features[].properties`) and Esri FeatureSet (`features[].
 * attributes` / `results[]`) shapes -- WMS servers vary in what "application/
 * json" yields. */
function parseFeatureInfoJson(body: unknown): IdentifyFeatureResult[] {
  if (!body || typeof body !== "object") return []
  const obj = body as Record<string, unknown>
  const features = Array.isArray(obj.features) ? obj.features : Array.isArray(obj.results) ? obj.results : []
  const attrs = (features as Array<Record<string, unknown>>).map(
    (f) => (f.properties ?? f.attributes ?? {}) as Record<string, unknown>,
  )
  return toResults(attrs)
}

/** WMS GetFeatureInfo for one layer. Uses OL's own URL builder off the live
 * TileWMS source (so LAYERS/CRS/version match what's rendered), asking for JSON
 * and up to 10 features under the pixel. */
async function identifyWms(layer: Layer, ctx: IdentifyContext): Promise<IdentifyFeatureResult[]> {
  const source = layer.getSource()
  if (!(source instanceof TileWMS)) return []
  const url = source.getFeatureInfoUrl(ctx.coordinate, ctx.resolution, ctx.projection, {
    INFO_FORMAT: "application/json",
    FEATURE_COUNT: 10,
  })
  if (!url) return []
  return parseFeatureInfoJson(await fetchJson(url))
}

/** Esri MapServer identify for one layer: a point-geometry identify over the
 * current view extent, asking for JSON without geometry. Parses the standard
 * `results[]` (each `{ layerName, value, attributes }`). */
async function identifyEsriMap(record: ServiceLayerRecord, ctx: IdentifyContext): Promise<IdentifyFeatureResult[]> {
  const [xmin, ymin, xmax, ymax] = ctx.extent
  const params = new URLSearchParams({
    f: "json",
    geometry: `${ctx.coordinate[0]},${ctx.coordinate[1]}`,
    geometryType: "esriGeometryPoint",
    sr: "3857",
    tolerance: "5",
    mapExtent: `${xmin},${ymin},${xmax},${ymax}`,
    imageDisplay: `${Math.round(ctx.sizePx[0])},${Math.round(ctx.sizePx[1])},96`,
    layers: "all",
    returnGeometry: "false",
  })
  const base = usesProxy(record) ? serviceProxyBase(record.id) : record.url.replace(/\/+$/, "")
  const body = (await fetchJson(`${base}/identify?${params.toString()}`)) as Record<string, unknown>
  const results = Array.isArray(body.results) ? (body.results as Array<Record<string, unknown>>) : []
  return results.map((r) => {
    const properties = { ...((r.attributes ?? {}) as Record<string, unknown>) }
    delete properties.geometry
    const value = r.value != null && String(r.value).trim() !== "" ? String(r.value) : undefined
    return { label: value ?? deriveFeatureLabel(properties) ?? (r.layerName ? String(r.layerName) : undefined), properties }
  })
}

/**
 * Runs the server-side identify for one raster service layer (wms or esri_map)
 * and returns its result group. Never throws: a network/parse failure lands as a
 * group with `error` set so one unreachable layer doesn't sink the others in the
 * click's Promise.all.
 */
export async function identifyRasterLayer(
  record: ServiceLayerRecord,
  layer: Layer,
  ctx: IdentifyContext,
): Promise<IdentifyLayerGroup> {
  const base = { layerId: record.id, layerLabel: record.label, serviceType: record.service_type }
  try {
    const features =
      record.service_type === "wms" ? await identifyWms(layer, ctx) : await identifyEsriMap(record, ctx)
    return { ...base, features }
  } catch (err) {
    return { ...base, features: [], error: err instanceof Error ? err.message : "identify request failed" }
  }
}
