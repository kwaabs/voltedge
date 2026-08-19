import LineString from "ol/geom/LineString"
import Polygon from "ol/geom/Polygon"
import { getArea, getLength } from "ol/sphere"

import type { FeatureGeometry } from "@/hooks/use-feature"
import type { Units } from "@/stores/units-store"

const M_TO_FT = 3.280839895
const M2_TO_FT2 = 10.76391042
const FT_PER_MILE = 5280
const M2_PER_ACRE = 4046.8564224

/**
 * Formats a length in meters for display, in whichever unit system the
 * user has chosen (units-store.ts). Metric rolls m up into km past a
 * kilometre; imperial rolls ft up into mi past a mile. Sub-100 values
 * keep one decimal (a 42.5 m span reads more usefully than "42 m" or
 * "43 m"); above that the decimal is noise.
 */
export function formatLength(meters: number, units: Units): string {
  if (units === "imperial") {
    const ft = meters * M_TO_FT
    return ft < FT_PER_MILE ? `${ft.toFixed(ft < 100 ? 1 : 0)} ft` : `${(ft / FT_PER_MILE).toFixed(2)} mi`
  }
  return meters < 1000 ? `${meters.toFixed(meters < 100 ? 1 : 0)} m` : `${(meters / 1000).toFixed(2)} km`
}

/**
 * Formats an area in square meters for display. Metric steps m² -> ha
 * (1 ha = 10,000 m²) -> km² as the value grows; imperial steps ft² ->
 * acres past an acre, the units a utility parcel/right-of-way is
 * actually described in.
 */
export function formatArea(m2: number, units: Units): string {
  if (units === "imperial") {
    if (m2 < M2_PER_ACRE) return `${(m2 * M2_TO_FT2).toFixed(0)} ft²`
    return `${(m2 / M2_PER_ACRE).toFixed(2)} ac`
  }
  if (m2 < 10_000) return `${m2.toFixed(0)} m²`
  if (m2 < 1_000_000) return `${(m2 / 10_000).toFixed(2)} ha`
  return `${(m2 / 1_000_000).toFixed(2)} km²`
}

// ol/sphere's getLength/getArea transform their input to EPSG:4326
// internally before the geodesic (haversine) calc, so a geometry whose
// coordinates are ALREADY lon/lat must declare that projection --
// otherwise ol would transform 4326 coords as if they were 3857 and get
// a wildly wrong answer. (The measure tool in voltedge-map.tsx passes
// EPSG:3857 geometry straight in and relies on the default projection
// instead -- see there.)
const GEOJSON_SPHERE_OPTS = { projection: "EPSG:4326" } as const

/**
 * Geodesic length, in meters, of a fetched feature's GeoJSON geometry
 * (EPSG:4326, from GET /features/{code}/{id}). A LineString's own
 * length, or a Polygon's outer-ring perimeter; null for anything else
 * (a Point has no length). Correct on the sphere regardless of the
 * class's storage SRID, unlike a naive planar length in the distorted
 * EPSG:3857 working projection.
 */
export function geoJSONLengthMeters(geometry: FeatureGeometry): number | null {
  if (geometry.type === "LineString") {
    return getLength(new LineString(geometry.coordinates as number[][]), GEOJSON_SPHERE_OPTS)
  }
  if (geometry.type === "Polygon") {
    const outerRing = (geometry.coordinates as number[][][])[0]
    return outerRing ? getLength(new LineString(outerRing), GEOJSON_SPHERE_OPTS) : null
  }
  return null
}

/**
 * Geodesic area, in square meters, of a fetched feature's GeoJSON
 * Polygon geometry (EPSG:4326); null for non-polygons.
 */
export function geoJSONAreaMeters(geometry: FeatureGeometry): number | null {
  if (geometry.type !== "Polygon") return null
  return getArea(new Polygon(geometry.coordinates as number[][][]), GEOJSON_SPHERE_OPTS)
}
