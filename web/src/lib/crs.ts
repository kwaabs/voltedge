import GeoJSON from "ol/format/GeoJSON"
import { register } from "ol/proj/proj4"
import proj4 from "proj4"

import type { GeoJSONFeatureCollection } from "@/stores/imported-overlays-store"

/**
 * The frontend reprojection engine (Phase 15 Slice 3). Imported files arrive in
 * arbitrary coordinate systems; everything is reprojected to the canonical
 * storage CRS (EPSG:4326) here, in the browser, before it's stored or drawn.
 * (Export reprojection is a separate concern handled server-side by PostGIS
 * `ST_Transform` — the DB owns that data; this engine only ever sees imported
 * geometry. The one thing the two share is `CRS_CATALOG`, the picker list.)
 *
 * proj4 ships only 4326/3857, so we register a curated catalog (WGS 84,
 * Web Mercator, and the WGS 84 UTM zones) with both proj4 and OpenLayers, and
 * accept a pasted proj4/WKT definition for anything outside it.
 */
export interface CrsDef {
  /** e.g. "EPSG:32630". A synthetic "IMPORT:*" code for pasted definitions. */
  code: string
  /** Human label for the picker. */
  name: string
  /** proj4 definition string (proj4 also parses raw WKT). */
  proj4: string
}

/** WGS 84 UTM zones 1–60, north and south (EPSG:326xx / 327xx). Generated
 * rather than hand-listed — 120 near-identical defs. */
function utmZones(): CrsDef[] {
  const zones: CrsDef[] = []
  for (let z = 1; z <= 60; z++) {
    zones.push({
      code: `EPSG:${32600 + z}`,
      name: `WGS 84 / UTM zone ${z}N`,
      proj4: `+proj=utm +zone=${z} +datum=WGS84 +units=m +no_defs`,
    })
    zones.push({
      code: `EPSG:${32700 + z}`,
      name: `WGS 84 / UTM zone ${z}S`,
      proj4: `+proj=utm +zone=${z} +south +datum=WGS84 +units=m +no_defs`,
    })
  }
  return zones
}

const GEOGRAPHIC: CrsDef[] = [
  { code: "EPSG:4326", name: "WGS 84 (lat/lon)", proj4: "+proj=longlat +datum=WGS84 +no_defs" },
  {
    code: "EPSG:3857",
    name: "Web Mercator",
    proj4:
      "+proj=merc +a=6378137 +b=6378137 +lat_ts=0 +lon_0=0 +x_0=0 +y_0=0 +k=1 +units=m +nadgrids=@null +wktext +no_defs",
  },
]

/** Every CRS the picker offers. Shared with the future export target-CRS
 * control (the backend validates the chosen SRID against PostGIS
 * `spatial_ref_sys`; this list is only the UI menu). */
export const CRS_CATALOG: CrsDef[] = [...GEOGRAPHIC, ...utmZones()]

/** Grouped for the picker's optgroups. */
export const CRS_GROUPS: { label: string; items: CrsDef[] }[] = [
  { label: "Geographic / Web", items: GEOGRAPHIC },
  { label: "UTM North (WGS 84)", items: CRS_CATALOG.filter((c) => /UTM zone \d+N$/.test(c.name)) },
  { label: "UTM South (WGS 84)", items: CRS_CATALOG.filter((c) => /UTM zone \d+S$/.test(c.name)) },
]

const catalogByCode = new Map(CRS_CATALOG.map((c) => [c.code, c]))
const customDefs = new Map<string, string>()
let catalogRegistered = false

/** Registers the whole catalog with proj4 + OpenLayers, once. */
function ensureCatalogRegistered() {
  if (catalogRegistered) return
  for (const crs of CRS_CATALOG) proj4.defs(crs.code, crs.proj4)
  register(proj4)
  catalogRegistered = true
}

/** A tiny deterministic hash (no Date/Math.random) for naming pasted defs. */
function hashString(s: string): string {
  let h = 0
  for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) | 0
  return (h >>> 0).toString(36)
}

/**
 * Resolves an input CRS to a registered OpenLayers projection code. Accepts a
 * catalog code (e.g. "EPSG:32630") or a raw proj4/WKT definition, which is
 * registered under a stable synthetic "IMPORT:*" code so proj4 can transform
 * with it. Call before any reprojection so the projection is known to OL.
 */
export function ensureProjection(input: string): string {
  ensureCatalogRegistered()
  const trimmed = input.trim()
  if (catalogByCode.has(trimmed)) return trimmed

  // Not a known code — treat it as a raw proj4/WKT definition.
  const code = `IMPORT:${hashString(trimmed)}`
  if (!customDefs.has(code)) {
    proj4.defs(code, trimmed)
    register(proj4)
    customDefs.set(code, trimmed)
  }
  return code
}

/**
 * Reprojects a GeoJSON FeatureCollection from `sourceDef` (a catalog code or a
 * raw proj4/WKT definition) into canonical EPSG:4326. A no-op when the source
 * is already 4326.
 */
export function reprojectGeoJSONTo4326(
  fc: GeoJSONFeatureCollection,
  sourceDef: string,
): GeoJSONFeatureCollection {
  const source = ensureProjection(sourceDef)
  if (source === "EPSG:4326") return fc
  const format = new GeoJSON()
  const features = format.readFeatures(fc, {
    dataProjection: source,
    featureProjection: "EPSG:4326",
  })
  return format.writeFeaturesObject(features, {
    dataProjection: "EPSG:4326",
    featureProjection: "EPSG:4326",
  }) as unknown as GeoJSONFeatureCollection
}

/**
 * Best-effort identification of a CRS from a `.prj`'s WKT. A well-formed WKT
 * ends with `AUTHORITY["EPSG","<code>"]` for the CRS itself (the last such tag);
 * we match that and prefer the catalog entry. When the code isn't in the
 * catalog we still return it, carrying the raw WKT as the proj4 def so
 * reprojection works via proj4's WKT parsing. With no AUTHORITY tag at all, the
 * raw WKT itself is the definition.
 */
export function detectCrsFromWkt(wkt: string): CrsDef | null {
  const text = wkt.trim()
  if (!text) return null
  const matches = [...text.matchAll(/AUTHORITY\s*\[\s*"EPSG"\s*,\s*"?(\d+)"?\s*\]/gi)]
  if (matches.length > 0) {
    const code = `EPSG:${matches[matches.length - 1][1]}`
    return catalogByCode.get(code) ?? { code, name: code, proj4: text }
  }
  return { code: "PRJ", name: "From .prj file", proj4: text }
}
