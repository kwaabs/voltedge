import WMSCapabilities from "ol/format/WMSCapabilities"
import WMTSCapabilities from "ol/format/WMTSCapabilities"

import type { ServiceType } from "@/hooks/use-service-layers"

/**
 * Capabilities discovery (Slice 5): building the GetCapabilities / ?f=json URL
 * for a service and parsing what it advertises into a normalized shape the admin
 * form can auto-fill from. The document is fetched server-side (SSRF-vetted) via
 * the /service-layers/discover endpoint and handed back here as raw text; the
 * network round-trip never happens in this module.
 *
 * Parsing reuses OpenLayers' own WMS/WMTS capabilities readers (the same code
 * that would consume these docs at render time), a plain JSON.parse for Esri, and
 * a namespace-tolerant DOMParser pass for WFS FeatureType listings.
 */

export interface DiscoveredLayer {
  /** The identifier the layer's request needs (WMS/WMTS layer name, WFS
   * typename, Esri numeric sublayer id). Goes into the form's layer_ids field. */
  id: string
  title: string
}

export interface Discovery {
  layers: DiscoveredLayer[]
  /** Protocol versions the service advertises (fills the form's version field). */
  versions: string[]
  /** image/* MIME types offered for GetMap/GetTile (fills image_format). */
  imageFormats: string[]
  /** WMTS TileMatrixSet identifiers (fills crs, used as the matrix set). */
  matrixSets: string[]
  /** CRS/SRS codes the service supports (fills crs for WMS/WFS). */
  crsOptions: string[]
  /** [minLon, minLat, maxLon, maxLat] in WGS84 when the doc gives one. */
  extent?: number[]
  /** A human note surfaced to the admin (e.g. why the extent wasn't filled). */
  note?: string
}

const EMPTY: Discovery = { layers: [], versions: [], imageFormats: [], matrixSets: [], crsOptions: [] }

/** Whether a service type has a discovery protocol at all. xyz and vector_tile
 * are bare tile templates with nothing to introspect. */
export function supportsDiscovery(type: ServiceType): boolean {
  return type === "wms" || type === "wmts" || type === "wfs" || type === "esri_map" || type === "esri_feature"
}

/** Builds the GetCapabilities / ?f=json URL from the base service URL the admin
 * typed, or null for types with no discovery protocol (or an unparseable base).
 * Params are merged through a URL object, OVERRIDING (case-insensitively) any the
 * base already carries: admins routinely paste a base that already has
 * `?service=wfs&version=2.0.0`, and naively appending `&service=WFS&request=...`
 * would yield a duplicate `service` param the server rejects ("Single value
 * expected for parameter service"), returning an exception doc with no layers. */
export function capabilitiesUrl(type: ServiceType, baseUrl: string, version?: string): string | null {
  const trimmed = baseUrl.trim()
  if (trimmed === "") return null
  let url: URL
  try {
    url = new URL(trimmed)
  } catch {
    return null
  }
  const set = (params: Record<string, string | undefined>) => {
    const keys = new Set(Object.keys(params).map((k) => k.toLowerCase()))
    for (const existing of [...url.searchParams.keys()]) {
      if (keys.has(existing.toLowerCase())) url.searchParams.delete(existing)
    }
    for (const [k, v] of Object.entries(params)) if (v !== undefined) url.searchParams.set(k, v)
  }
  switch (type) {
    case "wms":
      set({ service: "WMS", request: "GetCapabilities", version })
      break
    case "wmts":
      set({ service: "WMTS", request: "GetCapabilities" })
      break
    case "wfs":
      set({ service: "WFS", request: "GetCapabilities", version })
      break
    case "esri_map":
    case "esri_feature":
      set({ f: "json" })
      break
    default:
      return null
  }
  return url.toString()
}

/** Parses a raw capabilities document for the given service type. Throws on a
 * fundamentally unparseable document; a well-formed-but-empty doc yields an empty
 * Discovery (optionally with a note). */
export function parseCapabilities(type: ServiceType, body: string): Discovery {
  switch (type) {
    case "wms":
      return parseWms(body)
    case "wmts":
      return parseWmts(body)
    case "wfs":
      return parseWfs(body)
    case "esri_map":
    case "esri_feature":
      return parseEsri(body)
    default:
      return { ...EMPTY, note: "This service type has no capabilities document." }
  }
}

// --- small defensive accessors over OL's loosely-typed capability trees --------

function asRecord(v: unknown): Record<string, unknown> {
  return v && typeof v === "object" ? (v as Record<string, unknown>) : {}
}
function asArray(v: unknown): unknown[] {
  return Array.isArray(v) ? v : []
}
function asString(v: unknown): string | undefined {
  return typeof v === "string" && v.trim() !== "" ? v : undefined
}

function parseWms(body: string): Discovery {
  const caps = asRecord(new WMSCapabilities().read(body))
  const version = asString(caps.version)
  const capability = asRecord(caps.Capability)
  const getMap = asRecord(asRecord(capability.Request).GetMap)
  const formats = asArray(getMap.Format)
    .map((f) => asString(f))
    .filter((f): f is string => !!f && f.startsWith("image/"))

  const layers: DiscoveredLayer[] = []
  const crsSet = new Set<string>()
  let extent: number[] | undefined
  const walk = (nodeU: unknown) => {
    const node = asRecord(nodeU)
    const name = asString(node.Name)
    if (name) layers.push({ id: name, title: asString(node.Title) ?? name })
    for (const c of asArray(node.CRS)) {
      const s = asString(c)
      if (s) crsSet.add(s)
    }
    const bb = node.EX_GeographicBoundingBox
    if (!extent && Array.isArray(bb) && bb.length === 4 && bb.every((n) => typeof n === "number")) {
      extent = bb as number[] // OL yields [minLon, minLat, maxLon, maxLat]
    }
    for (const child of asArray(node.Layer)) walk(child)
  }
  walk(capability.Layer)

  return {
    layers,
    versions: version ? [version] : [],
    imageFormats: formats,
    matrixSets: [],
    crsOptions: [...crsSet],
    extent,
  }
}

function parseWmts(body: string): Discovery {
  const caps = asRecord(new WMTSCapabilities().read(body))
  const version = asString(caps.version)
  const contents = asRecord(caps.Contents)

  const layers: DiscoveredLayer[] = asArray(contents.Layer)
    .map((lU) => {
      const l = asRecord(lU)
      const id = asString(l.Identifier)
      return id ? { id, title: asString(l.Title) ?? id } : null
    })
    .filter((l): l is DiscoveredLayer => l !== null)

  const matrixSets = asArray(contents.TileMatrixSet)
    .map((mU) => asString(asRecord(mU).Identifier))
    .filter((s): s is string => !!s)

  const formats = new Set<string>()
  for (const lU of asArray(contents.Layer)) {
    for (const f of asArray(asRecord(lU).Format)) {
      const s = asString(f)
      if (s) formats.add(s)
    }
  }

  return {
    layers,
    versions: version ? [version] : [],
    imageFormats: [...formats],
    matrixSets,
    crsOptions: matrixSets, // WMTS render path uses crs as the TileMatrixSet id
  }
}

function parseWfs(body: string): Discovery {
  const doc = new DOMParser().parseFromString(body, "application/xml")
  if (doc.getElementsByTagName("parsererror").length > 0) {
    throw new Error("WFS capabilities document was not valid XML")
  }
  const version = doc.documentElement?.getAttribute("version") ?? undefined

  const layers: DiscoveredLayer[] = []
  const crsSet = new Set<string>()
  for (const ft of Array.from(doc.getElementsByTagNameNS("*", "FeatureType"))) {
    const name = ft.getElementsByTagNameNS("*", "Name")[0]?.textContent?.trim()
    if (!name) continue
    const title = ft.getElementsByTagNameNS("*", "Title")[0]?.textContent?.trim()
    layers.push({ id: name, title: title && title !== "" ? title : name })
    for (const tag of ["DefaultCRS", "DefaultSRS"]) {
      const crs = ft.getElementsByTagNameNS("*", tag)[0]?.textContent?.trim()
      if (crs) crsSet.add(crs)
    }
  }

  return {
    layers,
    versions: version ? [version] : [],
    imageFormats: [],
    matrixSets: [],
    crsOptions: [...crsSet],
  }
}

function parseEsri(body: string): Discovery {
  let json: Record<string, unknown>
  try {
    json = asRecord(JSON.parse(body))
  } catch {
    return { ...EMPTY, note: "Response was not JSON — is this an Esri MapServer/FeatureServer URL?" }
  }
  const err = asRecord(json.error)
  if (Object.keys(err).length > 0) {
    return { ...EMPTY, note: `Service error: ${asString(err.message) ?? "unknown"}` }
  }

  let layers: DiscoveredLayer[] = asArray(json.layers)
    .map((lU) => {
      const l = asRecord(lU)
      const id = l.id != null ? String(l.id) : undefined
      return id !== undefined ? { id, title: asString(l.name) ?? `Layer ${id}` } : null
    })
    .filter((l): l is DiscoveredLayer => l !== null)
  // A single FeatureServer/MapServer sublayer (…/{n}?f=json) has no `layers`
  // array — it *is* the layer, with a top-level id/name.
  if (layers.length === 0 && (json.name != null || json.id != null)) {
    const id = json.id != null ? String(json.id) : ""
    layers = [{ id, title: asString(json.name) ?? `Layer ${id}` }]
  }

  let extent: number[] | undefined
  let note: string | undefined
  const fe = asRecord(json.fullExtent)
  if (Object.keys(fe).length > 0) {
    const sr = asRecord(fe.spatialReference)
    const wkid = (sr.latestWkid ?? sr.wkid) as unknown
    if (wkid === 4326 || wkid === 4269) {
      extent = [Number(fe.xmin), Number(fe.ymin), Number(fe.xmax), Number(fe.ymax)]
    } else {
      note = "The service reports its extent in a projected CRS; it wasn't auto-filled."
    }
  }

  const imageFormats =
    typeof json.supportedImageFormatTypes === "string"
      ? json.supportedImageFormatTypes.split(",").map((s) => s.trim()).filter(Boolean)
      : []

  return {
    layers,
    versions: json.currentVersion != null ? [String(json.currentVersion)] : [],
    imageFormats,
    matrixSets: [],
    crsOptions: [],
    extent,
    note,
  }
}
