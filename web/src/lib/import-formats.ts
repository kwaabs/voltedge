import GeoJSON from "ol/format/GeoJSON"
import KML from "ol/format/KML"
import { Helper } from "dxf"
import JSZip from "jszip"
import shp from "shpjs"

import { type CrsDef, detectCrsFromWkt, reprojectGeoJSONTo4326 } from "@/lib/crs"
import type { GeoJSONFeatureCollection, ImportFormat } from "@/stores/imported-overlays-store"

/**
 * Client-side ingest for the import overlay (Phase 15). Every supported format
 * is normalized here into one canonical shape -- an EPSG:4326 GeoJSON
 * FeatureCollection -- so everything downstream (the store, the map's
 * population effect, the layers panel) is format-agnostic and only ever sees
 * GeoJSON. Parsing/reprojection stays in the browser; nothing is uploaded.
 */
export interface ParsedImport {
  format: ImportFormat
  geojson: GeoJSONFeatureCollection
  featureCount: number
}

/** Maps a file extension to its import format. `.kml` and `.kmz` share the KML
 * family (a KMZ is just a zipped KML), so both report as "kmz". Unknown
 * extensions return null and are rejected by the dialog. */
const EXT_FORMAT: Record<string, ImportFormat> = {
  geojson: "geojson",
  json: "geojson",
  kml: "kmz",
  kmz: "kmz",
  zip: "shapefile",
  dxf: "dxf",
}

/** File-picker `accept` list of every extension we can currently parse. */
export const IMPORT_ACCEPT = ".geojson,.json,.kml,.kmz,.zip,.dxf"

export function detectFormat(filename: string): ImportFormat | null {
  const ext = filename.toLowerCase().match(/\.([^.]+)$/)?.[1]
  if (!ext) return null
  return EXT_FORMAT[ext] ?? null
}

/** Per-format ingest options resolved by the dialog before parsing. */
export interface ParseOptions {
  /** Source CRS (catalog code or raw proj4/WKT) to reproject *from* into 4326.
   * Set only when the format carries no usable georeference (a shapefile with
   * no `.prj`, DXF) — see `inspectFile`. */
  reprojectFrom?: string
}

/**
 * What the dialog needs to know about a picked file *before* importing it:
 * which parser handles it, whether its CRS was auto-detected, and whether the
 * user must supply one (no georeference in the file). Cheap enough to run on
 * pick — for a shapefile it peeks at the `.prj` inside the zip.
 */
export interface InspectResult {
  format: ImportFormat
  /** Auto-detected source CRS (a shapefile's `.prj`); null if none. */
  detectedCrs: CrsDef | null
  /** True when no CRS could be detected and one is required to reproject. */
  requiresCrs: boolean
}

/**
 * Parses a picked file into a canonical 4326 GeoJSON FeatureCollection,
 * dispatching on its extension. Throws a user-facing Error (surfaced as a
 * toast) on an unsupported type, a parse failure, or an empty result.
 */
export async function parseFile(file: File, opts: ParseOptions = {}): Promise<ParsedImport> {
  const format = detectFormat(file.name)
  if (!format) throw new Error("Unsupported file type.")
  switch (format) {
    case "geojson":
      return parseGeoJSON(await file.text())
    case "kmz":
      return parseKmlOrKmz(file)
    case "shapefile":
      return parseShapefile(file, opts.reprojectFrom)
    case "dxf":
      return parseDxf(file, opts.reprojectFrom)
    default:
      // Every ImportFormat is handled above; this guards an unreachable case so
      // a mislabeled format can't silently fall through.
      throw new Error(`${format} import isn't available yet.`)
  }
}

/**
 * Inspects a picked file for its format and CRS situation without fully
 * importing it. GeoJSON and KMZ/KML are 4326 by spec (no CRS control needed);
 * a shapefile's CRS is read from its `.prj`, and if absent the user must pick
 * one. Errors are swallowed to a "no detection" result — parsing surfaces the
 * real failure with a clearer message.
 */
export async function inspectFile(file: File): Promise<InspectResult> {
  const format = detectFormat(file.name)
  if (!format) throw new Error("Unsupported file type.")
  if (format === "shapefile") {
    const detectedCrs = await inspectShapefileCrs(file).catch(() => null)
    return { format, detectedCrs, requiresCrs: detectedCrs === null }
  }
  // DXF carries no georeference at all -- a source CRS is always required.
  if (format === "dxf") {
    return { format, detectedCrs: null, requiresCrs: true }
  }
  return { format, detectedCrs: null, requiresCrs: false }
}

/** GeoJSON: parse the text, normalize any root (FeatureCollection, a lone
 * Feature, or a bare geometry) into a FeatureCollection. Assumed 4326 (the
 * reprojection engine arrives in Slice 3). */
function parseGeoJSON(text: string): ParsedImport {
  let raw: unknown
  try {
    raw = JSON.parse(text)
  } catch {
    throw new Error("File is not valid JSON.")
  }
  const geojson = toFeatureCollection(raw)
  if (!geojson) throw new Error("Not a GeoJSON FeatureCollection, Feature, or geometry.")
  if (geojson.features.length === 0) throw new Error("No features found in the file.")
  return { format: "geojson", geojson, featureCount: geojson.features.length }
}

/**
 * KML/KMZ: a KMZ is unzipped to its inner KML first; a bare .kml is read as
 * text directly. The KML is parsed with OpenLayers' bundled reader (styles
 * skipped -- imported overlays get our own amber/dashed style, not the file's)
 * and written back out as a 4326 GeoJSON FeatureCollection. KML is always
 * WGS84 by spec, so no reprojection is needed.
 */
async function parseKmlOrKmz(file: File): Promise<ParsedImport> {
  const kmlText = file.name.toLowerCase().endsWith(".kmz")
    ? await readKmzKml(file)
    : await file.text()

  const features = new KML({ extractStyles: false }).readFeatures(kmlText, {
    dataProjection: "EPSG:4326",
    featureProjection: "EPSG:4326",
  })
  if (features.length === 0) throw new Error("No features found in the file.")

  const geojson = new GeoJSON().writeFeaturesObject(features, {
    dataProjection: "EPSG:4326",
    featureProjection: "EPSG:4326",
  }) as unknown as GeoJSONFeatureCollection

  return { format: "kmz", geojson, featureCount: features.length }
}

/**
 * Unzips a KMZ and returns the text of its main KML entry. A KMZ is a ZIP whose
 * doc is conventionally `doc.kml`; we prefer that, then any root-level `.kml`,
 * then the first `.kml` anywhere (directories/icons are ignored).
 */
async function readKmzKml(file: File): Promise<string> {
  const zip = await JSZip.loadAsync(await file.arrayBuffer())
  const kmls = Object.values(zip.files).filter(
    (entry) => !entry.dir && entry.name.toLowerCase().endsWith(".kml"),
  )
  if (kmls.length === 0) throw new Error("No .kml found inside the KMZ.")
  const main =
    kmls.find((entry) => entry.name.toLowerCase() === "doc.kml") ??
    kmls.find((entry) => !entry.name.includes("/")) ??
    kmls[0]
  return main.async("string")
}

/**
 * Shapefile (delivered as a `.zip` of `.shp`/`.dbf`/`.prj`): parsed with
 * `shpjs`. When the zip has a readable `.prj`, shpjs reprojects to WGS 84
 * itself, so the result is already 4326 and `reprojectFrom` is left unset (see
 * `inspectFile`). When there's no `.prj`, shpjs returns the raw source
 * coordinates and the dialog passes the user-chosen `reprojectFrom`, which we
 * run through the proj4 engine to reach 4326. A zip may hold several
 * shapefiles; their features are merged.
 */
async function parseShapefile(file: File, reprojectFrom?: string): Promise<ParsedImport> {
  const parsed = await shp(await file.arrayBuffer())
  const collections = Array.isArray(parsed) ? parsed : [parsed]
  const features = collections.flatMap((c) => c.features)
  if (features.length === 0) throw new Error("No features found in the shapefile.")

  let geojson: GeoJSONFeatureCollection = { type: "FeatureCollection", features }
  if (reprojectFrom) geojson = reprojectGeoJSONTo4326(geojson, reprojectFrom)

  return { format: "shapefile", geojson, featureCount: features.length }
}

/** Reads a shapefile zip's `.prj` (if any) and identifies its CRS. Returns null
 * when the zip has no `.prj` — the caller then requires a user-supplied CRS. */
async function inspectShapefileCrs(file: File): Promise<CrsDef | null> {
  const zip = await JSZip.loadAsync(await file.arrayBuffer())
  const prj = Object.values(zip.files).find(
    (entry) => !entry.dir && entry.name.toLowerCase().endsWith(".prj"),
  )
  if (!prj) return null
  const wkt = (await prj.async("string")).trim()
  return wkt ? detectCrsFromWkt(wkt) : null
}

/**
 * DXF: parsed with the `dxf` package's `Helper.toPolylines()`, which
 * interpolates arcs/circles into segments and expands block INSERTs into flat
 * world-coordinate polylines -- everything the drawing shows, as line geometry.
 * DXF has no georeference, so `reprojectFrom` (the user-chosen source CRS) is
 * required and every feature is reprojected to 4326 through the proj4 engine.
 *
 * Limitations (v1): only polyline geometry is carried -- isolated text, hatch
 * fills, and per-entity attributes (layer, color) aren't rendered. A degenerate
 * single-vertex polyline becomes a Point.
 */
async function parseDxf(file: File, reprojectFrom?: string): Promise<ParsedImport> {
  if (!reprojectFrom) throw new Error("A source CRS is required for DXF import.")

  const { polylines } = new Helper(await file.text()).toPolylines()
  const features: unknown[] = []
  for (const pl of polylines) {
    if (pl.vertices.length === 0) continue
    const geometry =
      pl.vertices.length === 1
        ? { type: "Point", coordinates: pl.vertices[0] }
        : { type: "LineString", coordinates: pl.vertices }
    features.push({ type: "Feature", properties: {}, geometry })
  }
  if (features.length === 0) throw new Error("No drawable geometry found in the DXF.")

  const raw: GeoJSONFeatureCollection = { type: "FeatureCollection", features }
  const geojson = reprojectGeoJSONTo4326(raw, reprojectFrom)
  return { format: "dxf", geojson, featureCount: features.length }
}

/**
 * Normalizes any GeoJSON root -- a FeatureCollection, a single Feature, or a
 * bare geometry -- into a FeatureCollection, or null if it's none of those.
 * Keeps whatever properties each feature already carries; a bare geometry gets
 * an empty property bag.
 */
export function toFeatureCollection(raw: unknown): GeoJSONFeatureCollection | null {
  if (!raw || typeof raw !== "object") return null
  const obj = raw as Record<string, unknown>
  if (obj.type === "FeatureCollection" && Array.isArray(obj.features)) {
    return { type: "FeatureCollection", features: obj.features }
  }
  if (obj.type === "Feature") {
    return { type: "FeatureCollection", features: [obj] }
  }
  if (typeof obj.type === "string" && "coordinates" in obj) {
    return {
      type: "FeatureCollection",
      features: [{ type: "Feature", geometry: obj, properties: {} }],
    }
  }
  return null
}
