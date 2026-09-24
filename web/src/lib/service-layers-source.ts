import type { Extent } from "ol/extent"
import { getTopLeft, getWidth } from "ol/extent"
import GeoJSON from "ol/format/GeoJSON"
import MVT from "ol/format/MVT"
import type Layer from "ol/layer/Layer"
import TileLayer from "ol/layer/Tile"
import VectorLayer from "ol/layer/Vector"
import VectorTileLayer from "ol/layer/VectorTile"
import { bbox as bboxStrategy } from "ol/loadingstrategy"
import { get as getProjection, transformExtent } from "ol/proj"
import type TileSource from "ol/source/Tile"
import TileArcGISRest from "ol/source/TileArcGISRest"
import TileWMS from "ol/source/TileWMS"
import VectorSource from "ol/source/Vector"
import VectorTileSource from "ol/source/VectorTile"
import WMTS from "ol/source/WMTS"
import XYZ from "ol/source/XYZ"
import CircleStyle from "ol/style/Circle"
import Fill from "ol/style/Fill"
import Stroke from "ol/style/Stroke"
import Style from "ol/style/Style"
import WMTSTileGrid from "ol/tilegrid/WMTS"

import type { ServiceLayerRecord, ServiceType } from "@/hooks/use-service-layers"

/** Same-origin API prefix (Vite proxies /api -> the Go API; see lib/api.ts). The
 * proxy endpoints are plain GETs, so browser tile/image requests to them ride the
 * session cookie automatically -- no header injection needed on the OL side. */
const API_BASE = "/api"

/**
 * Service types the map can render. Slice 2 added the raster family
 * (wms/wmts/xyz/esri_map); Slice 3 adds the vector family (vector_tile, wfs,
 * esri_feature), so all seven types now build a layer. The set (and the panel's
 * "soon" greying keyed off it) stays in place as the single source of truth for
 * "is this renderable yet" should a future type land unimplemented.
 */
export const RENDERABLE_SERVICE_TYPES = new Set<ServiceType>([
  "wms",
  "wmts",
  "xyz",
  "esri_map",
  "vector_tile",
  "wfs",
  "esri_feature",
])

export function isRenderableServiceType(type: ServiceType): boolean {
  return RENDERABLE_SERVICE_TYPES.has(type)
}

/** The generic KVP/Esri proxy endpoint for a layer (WMS GetMap, WMTS GetTile,
 * Esri export). OL appends its own query string (and, for Esri, an /export path
 * segment) to this base. */
function proxyBase(id: string): string {
  return `${API_BASE}/service-layers/${id}/proxy`
}

/** The tile-template proxy endpoint for an xyz layer -- the server substitutes
 * {z}/{x}/{y} into the stored template and forwards. */
function tileTemplate(id: string): string {
  return `${API_BASE}/service-layers/${id}/tiles/{z}/{x}/{y}`
}

/**
 * Whether to route this layer through our same-origin proxy rather than straight
 * at the third-party URL. The admin's `proxied` flag decides for public layers;
 * an authenticated layer is ALWAYS proxied regardless, since the browser can't
 * (and must never) inject the server-held credential -- going direct would just
 * fail auth. Proxying also means every tile is same-origin, so no CORS and no
 * canvas taint.
 */
export function usesProxy(record: ServiceLayerRecord): boolean {
  return record.proxied || record.auth_type !== "none"
}

/** The same-origin proxy base for a layer, exported for the identify path
 * (service-layers-identify.ts) which builds its own GetFeatureInfo/identify URLs
 * onto it. */
export function serviceProxyBase(id: string): string {
  return proxyBase(id)
}

/** OL wants `crossOrigin: "anonymous"` only for a direct cross-origin source (so
 * a later pixel read for click-to-identify isn't tainted); a proxied source is
 * same-origin and needs none. */
function crossOriginFor(record: ServiceLayerRecord): string | undefined {
  return usesProxy(record) ? undefined : "anonymous"
}

/** Builds a standard Web-Mercator (EPSG:3857) WMTS tile grid -- the
 * GoogleMapsCompatible-style matrix set most public WMTS services publish.
 * Without a GetCapabilities fetch (Slice 5) we can't know a service's real matrix
 * set, so this is a best-effort default; the matrixSet identifier is passed
 * separately by the caller. */
function webMercatorWMTSGrid(maxZoom: number): WMTSTileGrid {
  const projection = getProjection("EPSG:3857")
  const extent = projection?.getExtent() ?? [-20037508.342789244, -20037508.342789244, 20037508.342789244, 20037508.342789244]
  const size = getWidth(extent) / 256
  const resolutions: number[] = []
  const matrixIds: string[] = []
  for (let z = 0; z <= maxZoom; z++) {
    resolutions.push(size / 2 ** z)
    matrixIds.push(String(z))
  }
  return new WMTSTileGrid({ origin: getTopLeft(extent), resolutions, matrixIds })
}

/** Builds the OL raster source for one service-layer record, or null for a type
 * this slice doesn't render yet (vector families). */
function createServiceSource(record: ServiceLayerRecord): TileSource | null {
  const crossOrigin = crossOriginFor(record)
  const proxied = usesProxy(record)

  switch (record.service_type) {
    case "xyz":
      return new XYZ({
        url: proxied ? tileTemplate(record.id) : record.url,
        maxZoom: record.max_zoom ?? undefined,
        minZoom: record.min_zoom || undefined,
        crossOrigin,
      })

    case "wms":
      return new TileWMS({
        url: proxied ? proxyBase(record.id) : record.url,
        params: {
          LAYERS: record.layer_ids ?? "",
          FORMAT: record.image_format || "image/png",
          TRANSPARENT: true,
          VERSION: record.version || "1.3.0",
          TILED: true,
        },
        crossOrigin,
      })

    case "esri_map":
      // TileArcGISRest appends "/export" to the url, so the proxy's /proxy/* route
      // forwards it onto the MapServer root. LAYERS is optional (an admin can pin
      // sublayers as "show:1,2"); FORMAT is Esri's bare token, not a MIME type.
      return new TileArcGISRest({
        url: proxied ? proxyBase(record.id) : record.url,
        params: {
          ...(record.layer_ids ? { LAYERS: `show:${record.layer_ids}` } : {}),
          FORMAT: (record.image_format || "png").replace(/^image\//, ""),
          TRANSPARENT: true,
        },
        crossOrigin,
      })

    case "wmts":
      // Best-effort standard Web-Mercator grid; the matrixSet is a guess (the
      // record's CRS) until Slice 5 reads it from GetCapabilities.
      return new WMTS({
        url: proxied ? proxyBase(record.id) : record.url,
        layer: record.layer_ids ?? "",
        matrixSet: record.crs || "EPSG:3857",
        format: record.image_format || "image/png",
        projection: getProjection("EPSG:3857") ?? undefined,
        tileGrid: webMercatorWMTSGrid(record.max_zoom ?? 20),
        style: "default",
        requestEncoding: "KVP",
        crossOrigin,
      })

    default:
      // vector_tile / wfs / esri_feature are vector layers, built below.
      return null
  }
}

/** One flat default style for every vector feature (WFS / Esri-feature / vector
 * tile) so a layer is at least legibly visible the moment it's turned on: a blue
 * stroke, a faint fill for polygons, and a small dot for points. Per-attribute
 * theming and Mapbox-GL `style_url` interpretation are a later slice; this is the
 * "render something correct" baseline. Shared (styles are stateless) across all
 * vector layers. */
const defaultVectorStyle = new Style({
  stroke: new Stroke({ color: "#2563eb", width: 1.5 }),
  fill: new Fill({ color: "rgba(37, 99, 235, 0.12)" }),
  image: new CircleStyle({
    radius: 4,
    fill: new Fill({ color: "#2563eb" }),
    stroke: new Stroke({ color: "#ffffff", width: 1 }),
  }),
})

/** The base URL a vector request targets: the same-origin proxy when the layer is
 * proxied/authenticated, else the third-party url directly (which then relies on
 * that service's own CORS headers). An optional path suffix (e.g. Esri's "query")
 * is appended to reach a sub-resource of the endpoint. */
function vectorEndpoint(record: ServiceLayerRecord, suffix?: string): string {
  const base = usesProxy(record) ? proxyBase(record.id) : record.url.replace(/\/+$/, "")
  return suffix ? `${base}/${suffix}` : base
}

/** Merges KVP request params onto a base URL, OVERRIDING (case-insensitively) any
 * of the same keys the base already carries. Service endpoints are frequently
 * catalogued with a whole sample request baked into the URL -- e.g. a pasted
 * `.../ows?service=wfs&version=2.0.0&request=GetCapabilities` -- and naively
 * appending our own `?service=WFS&request=GetFeature&...` then yields a
 * double-`?` URL with duplicate `request`/`service`/`version` params that the
 * server rejects ("Single value expected for parameter request"). Building
 * through a URL object folds our params in and drops the stale duplicates.
 * Relative proxy bases resolve against the app origin and come back root-relative
 * so they stay same-origin; direct third-party bases return absolute. */
function withQuery(base: string, params: Record<string, string>): string {
  const url = new URL(base, window.location.origin)
  const ours = new Set(Object.keys(params).map((k) => k.toLowerCase()))
  for (const existing of [...url.searchParams.keys()]) {
    if (ours.has(existing.toLowerCase())) url.searchParams.delete(existing)
  }
  for (const [k, v] of Object.entries(params)) url.searchParams.append(k, v)
  return url.origin === window.location.origin ? `${url.pathname}${url.search}` : url.toString()
}

/** Clamps a lon/lat extent to valid WGS84 bounds. When the map is zoomed out or
 * panned so the world wraps, transforming the 3857 view extent to 4326 yields
 * longitudes past ±180 (and latitudes past ±90) -- ArcGIS/WFS reject such a bbox
 * as "invalid input parameters", so we clip to the real world before requesting.
 * The result still covers the visible portion of the layer's coverage. */
function clampExtent4326(extent: Extent): [number, number, number, number] {
  const [minLon, minLat, maxLon, maxLat] = extent
  return [
    Math.max(minLon, -180),
    Math.max(minLat, -90),
    Math.min(maxLon, 180),
    Math.min(maxLat, 90),
  ]
}

/** WFS GetFeature URL builder (bbox strategy). The strategy hands us the view
 * extent in EPSG:3857; we transform it to 4326 for the request because WFS
 * GeoJSON output is WGS84 lon/lat regardless of srsName. Version drives the
 * spelling of the type/count params (2.x = typenames/count, 1.x =
 * typename/maxFeatures). Axis-order / GetCapabilities-driven refinement is
 * Slice 5; this is a best-effort common-case request. */
function wfsUrlFn(record: ServiceLayerRecord): (extent: Extent) => string {
  const version = record.version || "2.0.0"
  const typeParam = version.startsWith("2") ? "typenames" : "typename"
  const countParam = version.startsWith("2") ? "count" : "maxFeatures"
  return (extent) => {
    const [minLon, minLat, maxLon, maxLat] = clampExtent4326(transformExtent(extent, "EPSG:3857", "EPSG:4326"))
    return withQuery(vectorEndpoint(record), {
      service: "WFS",
      version,
      request: "GetFeature",
      [typeParam]: record.layer_ids ?? "",
      outputFormat: "application/json",
      srsName: "EPSG:4326",
      bbox: `${minLon},${minLat},${maxLon},${maxLat},EPSG:4326`,
      [countParam]: "5000",
    })
  }
}

/** The path suffix that reaches an Esri FeatureServer layer's /query. A query
 * lives at `.../FeatureServer/{layerId}/query`, so when the layer id is supplied
 * in the Layer(s) field rather than baked into the URL (which still ends at the
 * FeatureServer root), we splice it in. If the URL already ends in a numeric
 * layer id (e.g. `.../FeatureServer/2`) we don't double it. */
function esriFeatureQuerySuffix(record: ServiceLayerRecord): string {
  const urlHasLayerId = /\/\d+$/.test(record.url.replace(/\/+$/, ""))
  const layerId = record.layer_ids?.trim()
  return !urlHasLayerId && layerId ? `${layerId}/query` : "query"
}

/** Esri FeatureServer /query URL builder (bbox strategy), asking for GeoJSON in
 * 4326 with an envelope filter so we page in only what's on screen. `f=geojson`
 * needs ArcGIS 10.4+; older servers would need EsriJSON, deferred to Slice 5. */
function esriFeatureUrlFn(record: ServiceLayerRecord): (extent: Extent) => string {
  const suffix = esriFeatureQuerySuffix(record)
  return (extent) => {
    const [xmin, ymin, xmax, ymax] = clampExtent4326(transformExtent(extent, "EPSG:3857", "EPSG:4326"))
    return withQuery(vectorEndpoint(record, suffix), {
      f: "geojson",
      where: "1=1",
      outFields: "*",
      returnGeometry: "true",
      spatialRel: "esriSpatialRelIntersects",
      geometryType: "esriGeometryEnvelope",
      inSR: "4326",
      outSR: "4326",
      geometry: JSON.stringify({ xmin, ymin, xmax, ymax, spatialReference: { wkid: 4326 } }),
    })
  }
}

/** Builds the OL vector layer for a WFS or Esri FeatureServer record: a
 * bbox-strategy VectorSource reading GeoJSON (WGS84), reprojected into the view
 * by the source automatically. */
function createFeatureVectorLayer(record: ServiceLayerRecord): Layer {
  const urlFn = record.service_type === "wfs" ? wfsUrlFn(record) : esriFeatureUrlFn(record)
  return new VectorLayer({
    source: new VectorSource({ format: new GeoJSON(), url: urlFn, strategy: bboxStrategy }),
    style: defaultVectorStyle,
    visible: false,
    opacity: record.default_opacity,
    extent: extentFor(record),
    properties: { serviceLayerId: record.id },
  })
}

/** Builds the OL vector-tile layer for a vector_tile record: MVT tiles fetched
 * from the same-origin tile proxy (or directly), in Web Mercator. A Mapbox-GL
 * `style_url` isn't applied yet (Slice 5) -- the default style renders geometry
 * so the layer is visible in the meantime. */
function createVectorTileLayer(record: ServiceLayerRecord): Layer {
  return new VectorTileLayer({
    source: new VectorTileSource({
      format: new MVT(),
      url: usesProxy(record) ? tileTemplate(record.id) : record.url,
      maxZoom: record.max_zoom ?? undefined,
      minZoom: record.min_zoom || undefined,
    }),
    style: defaultVectorStyle,
    visible: false,
    opacity: record.default_opacity,
    extent: extentFor(record),
    properties: { serviceLayerId: record.id },
  })
}

/** The record's coverage extent transformed from 4326 to the view's 3857, or
 * undefined when unknown (global) -- clips tile/feature requests to where the
 * service has data. */
function extentFor(record: ServiceLayerRecord): Extent | undefined {
  return record.extent ? transformExtent(record.extent, "EPSG:4326", "EPSG:3857") : undefined
}

/**
 * Builds the managed overlay layer for one service-layer record, or null when the
 * type isn't renderable. Dispatches raster types to a TileLayer and vector types
 * to a Vector/VectorTile layer. The layer starts hidden (overlays are opt-in)
 * with the record's default opacity; the map's visibility/opacity effects then
 * drive it from the store. It tags itself with `serviceLayerId` so those effects
 * can find it, and clips to the record's coverage extent.
 */
export function createServiceLayerObject(record: ServiceLayerRecord): Layer | null {
  switch (record.service_type) {
    case "vector_tile":
      return createVectorTileLayer(record)
    case "wfs":
    case "esri_feature":
      return createFeatureVectorLayer(record)
    default: {
      const source = createServiceSource(record)
      if (!source) return null
      return new TileLayer<TileSource>({
        source,
        visible: false,
        opacity: record.default_opacity,
        extent: extentFor(record),
        properties: { serviceLayerId: record.id },
      })
    }
  }
}
