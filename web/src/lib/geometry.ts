import { fromLonLat, toLonLat } from "ol/proj"

import type { FeatureGeometry } from "@/hooks/use-feature"

/** A GeoJSON coordinate nest: a bare position (`[lon, lat]`), or a list of
 * them (line), or a list of lists (polygon rings / multi-geometries). Mirrors
 * the deliberately-untyped `coordinates: unknown` on FeatureGeometry -- the one
 * place we actually walk that structure, so the recursion is typed here rather
 * than forcing every caller to. */
type CoordNest = number[] | CoordNest[]

function isPosition(coords: CoordNest): coords is number[] {
  return typeof coords[0] === "number"
}

function shift(coords: CoordNest, dx: number, dy: number): CoordNest {
  if (isPosition(coords)) {
    // Offset in EPSG:3857 metres, then back to the 4326 lon/lat GeoJSON
    // carries everywhere else. 3857 units aren't true ground metres away from
    // the equator (they stretch by 1/cos(lat)), but this is only ever a few
    // metres of visual separation, so the distortion is irrelevant.
    const [x, y] = fromLonLat(coords)
    return toLonLat([x + dx, y + dy])
  }
  return coords.map((c) => shift(c, dx, dy))
}

/**
 * Returns a copy of a GeoJSON geometry translated by (dx, dy) metres in Web
 * Mercator. Used to place a duplicated feature (Phase 20 -- see
 * feature-panel.tsx's Duplicate action) a short, visible distance off the
 * original so it isn't hidden directly underneath it, ready to be dragged into
 * final position. Pure -- the input geometry is left untouched.
 */
export function offsetGeometry(geometry: FeatureGeometry, dxMeters: number, dyMeters: number): FeatureGeometry {
  return {
    type: geometry.type,
    coordinates: shift(geometry.coordinates as CoordNest, dxMeters, dyMeters),
  }
}

/**
 * Splits a polyline's coordinate list at the point on it nearest `point`,
 * returning `[nearHalf, farHalf]` -- the near half runs from the start to the
 * split, the far half from the split to the end, and both share the split
 * coordinate so they meet exactly. Projection-agnostic: it does plain planar
 * math on whatever units the inputs carry (the map preview passes EPSG:3857
 * coordinates). This backs only the on-map *preview* of a line split (Phase
 * 20); the authoritative cut is PostGIS `ST_LineSubstring` around
 * `ST_LineLocatePoint` in the /split endpoint, so a hair of planar-vs-geodesic
 * disagreement here is cosmetic. A degenerate line (<2 points) is returned
 * unchanged in both halves.
 */
export function splitCoordinates(coords: number[][], point: number[]): [number[][], number[][]] {
  if (coords.length < 2) return [coords, coords]
  let best = { seg: 0, split: coords[0], dist2: Infinity }
  for (let i = 0; i < coords.length - 1; i++) {
    const a = coords[i]
    const b = coords[i + 1]
    const dx = b[0] - a[0]
    const dy = b[1] - a[1]
    const len2 = dx * dx + dy * dy
    // Parameter of the perpendicular foot on segment a->b, clamped to the
    // segment so the split never lands off the end of a segment.
    const t = len2 === 0 ? 0 : Math.max(0, Math.min(1, ((point[0] - a[0]) * dx + (point[1] - a[1]) * dy) / len2))
    const split = [a[0] + t * dx, a[1] + t * dy]
    const dist2 = (point[0] - split[0]) ** 2 + (point[1] - split[1]) ** 2
    if (dist2 < best.dist2) best = { seg: i, split, dist2 }
  }
  const near = [...coords.slice(0, best.seg + 1), best.split]
  const far = [best.split, ...coords.slice(best.seg + 1)]
  return [near, far]
}
