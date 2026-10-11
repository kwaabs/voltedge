import type { OneLineConnector } from "@/lib/one-line"

// Mirrors the backend one-line router (api/internal/schematic/oneline.go) so the
// EDITABLE one-line can re-route wires live as symbols are dragged -- the server
// gives each connector its endpoint node ids, and we recompute its orthogonal
// path from the nodes' CURRENT (possibly moved) positions. Nothing persists; this
// is pure client geometry over the same shapes the server would have drawn.

const X_SPACING = 110 // matches oneLineXSpacing
const Y_SPACING = 90 // matches oneLineYSpacing

export type Pt = { x: number; y: number }
type Path = [number, number][]

/** An orthogonal elbow between two ends (a straight drop when they share x). */
function elbowPath(a: Pt, b: Pt): Path {
  if (a.x === b.x) return [[a.x, a.y], [b.x, b.y]]
  const ym = (a.y + b.y) / 2
  return [[a.x, a.y], [a.x, ym], [b.x, ym], [b.x, b.y]]
}

/** A 2-end wire bowed sideways by offset so parallel runs fan apart. */
function parallelPath(a: Pt, b: Pt, offset: number): Path {
  if (offset === 0) return elbowPath(a, b)
  const dx = b.x - a.x
  const dy = b.y - a.y
  const length = Math.hypot(dx, dy)
  if (length === 0) return [[a.x, a.y], [b.x, b.y]]
  const px = -dy / length
  const py = dx / length
  const mx = (a.x + b.x) / 2 + px * offset
  const my = (a.y + b.y) / 2 + py * offset
  return [[a.x, a.y], [mx, my], [b.x, b.y]]
}

/** An orthogonal staircase visiting every endpoint of a multi-tap conductor. */
function busPath(ends: Pt[]): Path {
  const sorted = [...ends].sort((p, q) => (p.y !== q.y ? p.y - q.y : p.x - q.x))
  const out: Path = [[sorted[0].x, sorted[0].y]]
  for (let i = 1; i < sorted.length; i++) {
    const prev = sorted[i - 1]
    const cur = sorted[i]
    if (prev.x !== cur.x && prev.y !== cur.y) out.push([prev.x, cur.y])
    out.push([cur.x, cur.y])
  }
  return out
}

function pairKey(a: Pt, b: Pt): string {
  if (a.x > b.x || (a.x === b.x && a.y > b.y)) [a, b] = [b, a]
  return `${a.x.toFixed(1)},${a.y.toFixed(1)}|${b.x.toFixed(1)},${b.y.toFixed(1)}`
}

/**
 * Re-route every connector from its endpoints' current positions. Connectors
 * whose endpoints are all known get a freshly computed orthogonal path; any
 * connector without usable endpoints keeps its original server path unchanged.
 * posOf returns null when a node id isn't drawn.
 */
export function routeConnectors(
  connectors: OneLineConnector[],
  posOf: (id: string) => Pt | null,
): OneLineConnector[] {
  // Parallel fan counts (two+ 2-end conductors between the same pair).
  const pairTotal = new Map<string, number>()
  for (const c of connectors) {
    const ends = endPositions(c, posOf)
    if (ends && ends.length === 2) {
      const k = pairKey(ends[0], ends[1])
      pairTotal.set(k, (pairTotal.get(k) ?? 0) + 1)
    }
  }
  const pairSeen = new Map<string, number>()

  return connectors.map((c) => {
    const ends = endPositions(c, posOf)
    if (!ends || ends.length === 0) return c // no live endpoints -- leave as-is
    let path: Path
    if (ends.length > 2) {
      path = busPath(ends)
    } else if (ends.length === 2) {
      const k = pairKey(ends[0], ends[1])
      const n = pairTotal.get(k) ?? 1
      if (n > 1) {
        const idx = pairSeen.get(k) ?? 0
        pairSeen.set(k, idx + 1)
        const offset = (idx - (n - 1) / 2) * X_SPACING * 0.35
        path = parallelPath(ends[0], ends[1], offset)
      } else {
        path = elbowPath(ends[0], ends[1])
      }
    } else {
      // One endpoint in the set -- a stub down from it (conductor leaves view).
      const e = ends[0]
      path = [[e.x, e.y], [e.x, e.y + Y_SPACING * 0.8]]
    }
    return { ...c, path }
  })
}

/** The point on segment a->b closest to p (clamped to the segment). */
export function projectToSegment(a: Pt, b: Pt, p: Pt): Pt {
  const dx = b.x - a.x
  const dy = b.y - a.y
  const len2 = dx * dx + dy * dy
  if (len2 === 0) return { x: a.x, y: a.y }
  let t = ((p.x - a.x) * dx + (p.y - a.y) * dy) / len2
  t = Math.max(0, Math.min(1, t))
  return { x: a.x + t * dx, y: a.y + t * dy }
}

/** The segment of a polyline nearest p: its start index and the projected point. */
export function nearestSegment(path: [number, number][], p: Pt): { index: number; point: Pt } {
  let index = 0
  let best = Infinity
  let point: Pt = path.length ? { x: path[0][0], y: path[0][1] } : { x: p.x, y: p.y }
  for (let i = 0; i < path.length - 1; i++) {
    const a = { x: path[i][0], y: path[i][1] }
    const b = { x: path[i + 1][0], y: path[i + 1][1] }
    const pr = projectToSegment(a, b, p)
    const d = Math.hypot(pr.x - p.x, pr.y - p.y)
    if (d < best) {
      best = d
      index = i
      point = pr
    }
  }
  return { index, point }
}

/** The known positions of a connector's endpoints, or null if it has none. */
function endPositions(c: OneLineConnector, posOf: (id: string) => Pt | null): Pt[] | null {
  if (!c.endpoints || c.endpoints.length === 0) return null
  const pts: Pt[] = []
  for (const id of c.endpoints) {
    const p = posOf(id)
    if (p) pts.push(p)
  }
  return pts.length > 0 ? pts : null
}
