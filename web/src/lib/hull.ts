// Convex hull (Andrew's monotone chain) over 2D points, used to draw the OMS
// outage-area polygon that encloses an incident's affected service points -- the
// filled "this whole neighbourhood is dark" blob beneath the point markers.
//
// It operates in lon/lat space; at an outage's city-block scale the planar
// approximation is visually indistinguishable from a geodesic hull, and the
// result is purely a map visualisation (not authoritative data), so no
// projection round-trip or turf dependency is warranted. Returns the hull
// vertices in counter-clockwise order WITHOUT repeating the first point (the
// caller closes the ring). Fewer than three distinct points has no polygon and
// returns an empty array -- the caller falls back to drawing the bare points.

export type LonLat = [number, number]

/** 2D cross product of OA×OB -- >0 left turn, <0 right turn, 0 collinear. */
function cross(o: LonLat, a: LonLat, b: LonLat): number {
  return (a[0] - o[0]) * (b[1] - o[1]) - (a[1] - o[1]) * (b[0] - o[0])
}

export function convexHull(points: LonLat[]): LonLat[] {
  // Dedupe, then require at least a triangle's worth of distinct points.
  const seen = new Set<string>()
  const pts: LonLat[] = []
  for (const p of points) {
    const key = `${p[0]},${p[1]}`
    if (seen.has(key)) continue
    seen.add(key)
    pts.push(p)
  }
  if (pts.length < 3) return []

  pts.sort((a, b) => (a[0] === b[0] ? a[1] - b[1] : a[0] - b[0]))

  const lower: LonLat[] = []
  for (const p of pts) {
    while (lower.length >= 2 && cross(lower[lower.length - 2], lower[lower.length - 1], p) <= 0) {
      lower.pop()
    }
    lower.push(p)
  }

  const upper: LonLat[] = []
  for (let i = pts.length - 1; i >= 0; i--) {
    const p = pts[i]
    while (upper.length >= 2 && cross(upper[upper.length - 2], upper[upper.length - 1], p) <= 0) {
      upper.pop()
    }
    upper.push(p)
  }

  // Drop each list's last point (shared with the other list's first), then join.
  lower.pop()
  upper.pop()
  const hull = lower.concat(upper)
  // All points collinear -> degenerate (no area); treat as "no polygon".
  return hull.length >= 3 ? hull : []
}
