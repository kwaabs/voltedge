/**
 * Flattens an SVG glyph's drawing primitives to plain polylines in a
 * normalised 0..SIZE coordinate frame -- the geometry the DXF exporter
 * (diagram-dxf.ts, Phase 33 Slice 2) turns into a CAD BLOCK. Symbology
 * glyphs are authored as small `0 0 24 24` SVGs whose whole silhouette the
 * canvas draws as an opaque `<image>`; a faithful DXF needs the actual
 * outlines, so this parses `<path>` data (M/L/H/V/C/S/Q/T/A/Z, absolute and
 * relative) plus `line`/`polyline`/`polygon`/`rect`/`circle`/`ellipse`,
 * samples curves and arcs to line segments, applies element/group
 * `transform` attributes, and maps every point through the root `viewBox`
 * to a fixed 0..SIZE box -- so a glyph authored at any viewBox exports at
 * the same scale the browser renders it (the `<image>` maps viewBox ->
 * icon box identically). Outlines only: fill vs stroke is irrelevant to a
 * DXF, which carries geometry, not paint.
 */

/** One flattened subpath -- an ordered run of points and whether it closes
 * back to its start. A filled shape exports as its outline (closed); an
 * open stroke stays open. */
export interface FlatSubpath {
  pts: [number, number][]
  closed: boolean
}

/** A 2-D affine transform [a, b, c, d, e, f], SVG's own column-vector
 * convention: x' = a*x + c*y + e, y' = b*x + d*y + f. */
type Matrix = [number, number, number, number, number, number]

const IDENTITY: Matrix = [1, 0, 0, 1, 0, 0]

/** Segments per full curve/arc span -- 16 keeps a 24px glyph smooth without
 * bloating the DXF; arcs subdivide further by sweep below. */
const CURVE_SEGMENTS = 16

function mul(a: Matrix, b: Matrix): Matrix {
  return [
    a[0] * b[0] + a[2] * b[1],
    a[1] * b[0] + a[3] * b[1],
    a[0] * b[2] + a[2] * b[3],
    a[1] * b[2] + a[3] * b[3],
    a[0] * b[4] + a[2] * b[5] + a[4],
    a[1] * b[4] + a[3] * b[5] + a[5],
  ]
}

function apply(m: Matrix, x: number, y: number): [number, number] {
  return [m[0] * x + m[2] * y + m[4], m[1] * x + m[3] * y + m[5]]
}

/** Parses an SVG `transform` attribute (translate/scale/rotate/matrix/skew)
 * into a single composed matrix, applied left-to-right as SVG specifies. */
function parseTransform(s: string | null): Matrix {
  let m: Matrix = IDENTITY
  if (!s) return m
  const re = /(matrix|translate|scale|rotate|skewX|skewY)\s*\(([^)]*)\)/g
  let match: RegExpExecArray | null
  while ((match = re.exec(s)) !== null) {
    const name = match[1]
    const args = match[2].split(/[\s,]+/).map(Number).filter((n) => !Number.isNaN(n))
    let t: Matrix = IDENTITY
    if (name === "matrix" && args.length === 6) {
      t = [args[0], args[1], args[2], args[3], args[4], args[5]]
    } else if (name === "translate") {
      t = [1, 0, 0, 1, args[0] || 0, args[1] || 0]
    } else if (name === "scale") {
      const sx = args[0] ?? 1
      t = [sx, 0, 0, args[1] ?? sx, 0, 0]
    } else if (name === "rotate") {
      const a = ((args[0] || 0) * Math.PI) / 180
      const c = Math.cos(a)
      const sn = Math.sin(a)
      const rot: Matrix = [c, sn, -sn, c, 0, 0]
      if (args.length >= 3) {
        t = mul([1, 0, 0, 1, args[1], args[2]], mul(rot, [1, 0, 0, 1, -args[1], -args[2]]))
      } else {
        t = rot
      }
    } else if (name === "skewX") {
      t = [1, 0, Math.tan(((args[0] || 0) * Math.PI) / 180), 1, 0, 0]
    } else if (name === "skewY") {
      t = [1, Math.tan(((args[0] || 0) * Math.PI) / 180), 0, 1, 0, 0]
    }
    m = mul(m, t)
  }
  return m
}

function sampleCubic(p0: [number, number], p1: [number, number], p2: [number, number], p3: [number, number]): [number, number][] {
  const out: [number, number][] = []
  for (let k = 1; k <= CURVE_SEGMENTS; k++) {
    const t = k / CURVE_SEGMENTS
    const u = 1 - t
    const x = u * u * u * p0[0] + 3 * u * u * t * p1[0] + 3 * u * t * t * p2[0] + t * t * t * p3[0]
    const y = u * u * u * p0[1] + 3 * u * u * t * p1[1] + 3 * u * t * t * p2[1] + t * t * t * p3[1]
    out.push([x, y])
  }
  return out
}

function sampleQuad(p0: [number, number], p1: [number, number], p2: [number, number]): [number, number][] {
  const out: [number, number][] = []
  for (let k = 1; k <= CURVE_SEGMENTS; k++) {
    const t = k / CURVE_SEGMENTS
    const u = 1 - t
    const x = u * u * p0[0] + 2 * u * t * p1[0] + t * t * p2[0]
    const y = u * u * p0[1] + 2 * u * t * p1[1] + t * t * p2[1]
    out.push([x, y])
  }
  return out
}

/** SVG elliptical-arc endpoint parameterisation (spec F.6.5), sampled to
 * line segments. Returns the points after the start (the start is already
 * on the subpath). */
function sampleArc(
  x1: number,
  y1: number,
  rxIn: number,
  ryIn: number,
  phiDeg: number,
  largeArc: boolean,
  sweep: boolean,
  x2: number,
  y2: number,
): [number, number][] {
  if (rxIn === 0 || ryIn === 0) return [[x2, y2]]
  let rx = Math.abs(rxIn)
  let ry = Math.abs(ryIn)
  const phi = (phiDeg * Math.PI) / 180
  const cosP = Math.cos(phi)
  const sinP = Math.sin(phi)
  const dx = (x1 - x2) / 2
  const dy = (y1 - y2) / 2
  const x1p = cosP * dx + sinP * dy
  const y1p = -sinP * dx + cosP * dy
  const lambda = (x1p * x1p) / (rx * rx) + (y1p * y1p) / (ry * ry)
  if (lambda > 1) {
    const s = Math.sqrt(lambda)
    rx *= s
    ry *= s
  }
  const denom = rx * rx * y1p * y1p + ry * ry * x1p * x1p
  const numer = Math.max(rx * rx * ry * ry - rx * rx * y1p * y1p - ry * ry * x1p * x1p, 0)
  const coef = (largeArc !== sweep ? 1 : -1) * Math.sqrt(denom === 0 ? 0 : numer / denom)
  const cxp = (coef * (rx * y1p)) / ry
  const cyp = (coef * -(ry * x1p)) / rx
  const cx = cosP * cxp - sinP * cyp + (x1 + x2) / 2
  const cy = sinP * cxp + cosP * cyp + (y1 + y2) / 2
  const angle = (ux: number, uy: number, vx: number, vy: number): number => {
    const dot = ux * vx + uy * vy
    const len = Math.sqrt((ux * ux + uy * uy) * (vx * vx + vy * vy))
    let a = Math.acos(Math.min(1, Math.max(-1, len === 0 ? 1 : dot / len)))
    if (ux * vy - uy * vx < 0) a = -a
    return a
  }
  const ux = (x1p - cxp) / rx
  const uy = (y1p - cyp) / ry
  const theta1 = angle(1, 0, ux, uy)
  let dtheta = angle(ux, uy, (-x1p - cxp) / rx, (-y1p - cyp) / ry)
  if (!sweep && dtheta > 0) dtheta -= 2 * Math.PI
  if (sweep && dtheta < 0) dtheta += 2 * Math.PI
  const segs = Math.max(2, Math.ceil((Math.abs(dtheta) / (Math.PI / 2)) * (CURVE_SEGMENTS / 4)))
  const out: [number, number][] = []
  for (let k = 1; k <= segs; k++) {
    const th = theta1 + (dtheta * k) / segs
    const ex = cosP * rx * Math.cos(th) - sinP * ry * Math.sin(th) + cx
    const ey = sinP * rx * Math.cos(th) + cosP * ry * Math.sin(th) + cy
    out.push([ex, ey])
  }
  return out
}

/** Parses a `<path>` `d` string to subpaths (local user coordinates,
 * pre-transform). Curves and arcs are sampled to segments. */
function parsePathData(d: string): FlatSubpath[] {
  const tokens = d.match(/[MmLlHhVvCcSsQqTtAaZz]|-?(?:\d*\.\d+|\d+\.?)(?:[eE][-+]?\d+)?/g) ?? []
  const subs: FlatSubpath[] = []
  let cur: [number, number][] = []
  let x = 0
  let y = 0
  let startX = 0
  let startY = 0
  let cmd = ""
  let prevCubicCtrl: [number, number] | null = null
  let prevQuadCtrl: [number, number] | null = null
  let i = 0
  const num = () => parseFloat(tokens[i++])
  const flush = (closed: boolean) => {
    if (cur.length > 1) subs.push({ pts: cur, closed })
    cur = []
  }
  const ensureStart = () => {
    if (cur.length === 0) cur.push([x, y])
  }
  while (i < tokens.length) {
    const tok = tokens[i]
    if (/[a-zA-Z]/.test(tok)) {
      cmd = tok
      i++
      if (cmd === "Z" || cmd === "z") {
        cur.push([startX, startY])
        flush(true)
        x = startX
        y = startY
        prevCubicCtrl = null
        prevQuadCtrl = null
        continue
      }
    } else if (cmd === "M") {
      cmd = "L"
    } else if (cmd === "m") {
      cmd = "l"
    }
    const rel = cmd === cmd.toLowerCase()
    const ox = rel ? x : 0
    const oy = rel ? y : 0
    switch (cmd.toUpperCase()) {
      case "M": {
        flush(false)
        x = ox + num()
        y = oy + num()
        startX = x
        startY = y
        cur = [[x, y]]
        prevCubicCtrl = null
        prevQuadCtrl = null
        break
      }
      case "L": {
        ensureStart()
        x = ox + num()
        y = oy + num()
        cur.push([x, y])
        prevCubicCtrl = null
        prevQuadCtrl = null
        break
      }
      case "H": {
        ensureStart()
        x = ox + num()
        cur.push([x, y])
        prevCubicCtrl = null
        prevQuadCtrl = null
        break
      }
      case "V": {
        ensureStart()
        y = oy + num()
        cur.push([x, y])
        prevCubicCtrl = null
        prevQuadCtrl = null
        break
      }
      case "C": {
        ensureStart()
        const c1: [number, number] = [ox + num(), oy + num()]
        const c2: [number, number] = [ox + num(), oy + num()]
        const end: [number, number] = [ox + num(), oy + num()]
        cur.push(...sampleCubic([x, y], c1, c2, end))
        x = end[0]
        y = end[1]
        prevCubicCtrl = c2
        prevQuadCtrl = null
        break
      }
      case "S": {
        ensureStart()
        const c1: [number, number] = prevCubicCtrl ? [2 * x - prevCubicCtrl[0], 2 * y - prevCubicCtrl[1]] : [x, y]
        const c2: [number, number] = [ox + num(), oy + num()]
        const end: [number, number] = [ox + num(), oy + num()]
        cur.push(...sampleCubic([x, y], c1, c2, end))
        x = end[0]
        y = end[1]
        prevCubicCtrl = c2
        prevQuadCtrl = null
        break
      }
      case "Q": {
        ensureStart()
        const c1: [number, number] = [ox + num(), oy + num()]
        const end: [number, number] = [ox + num(), oy + num()]
        cur.push(...sampleQuad([x, y], c1, end))
        x = end[0]
        y = end[1]
        prevQuadCtrl = c1
        prevCubicCtrl = null
        break
      }
      case "T": {
        ensureStart()
        const c1: [number, number] = prevQuadCtrl ? [2 * x - prevQuadCtrl[0], 2 * y - prevQuadCtrl[1]] : [x, y]
        const end: [number, number] = [ox + num(), oy + num()]
        cur.push(...sampleQuad([x, y], c1, end))
        x = end[0]
        y = end[1]
        prevQuadCtrl = c1
        prevCubicCtrl = null
        break
      }
      case "A": {
        ensureStart()
        const rx = num()
        const ry = num()
        const rot = num()
        const large = num() !== 0
        const sweep = num() !== 0
        const end: [number, number] = [ox + num(), oy + num()]
        cur.push(...sampleArc(x, y, rx, ry, rot, large, sweep, end[0], end[1]))
        x = end[0]
        y = end[1]
        prevCubicCtrl = null
        prevQuadCtrl = null
        break
      }
      default:
        // Unknown command letter -- consume nothing further to avoid a spin.
        i = tokens.length
        break
    }
  }
  flush(false)
  return subs
}

function parsePoints(attr: string | null): [number, number][] {
  if (!attr) return []
  const nums = attr.trim().split(/[\s,]+/).map(Number).filter((n) => !Number.isNaN(n))
  const pts: [number, number][] = []
  for (let k = 0; k + 1 < nums.length; k += 2) pts.push([nums[k], nums[k + 1]])
  return pts
}

function ellipsePoints(cx: number, cy: number, rx: number, ry: number): [number, number][] {
  const segs = CURVE_SEGMENTS * 2
  const pts: [number, number][] = []
  for (let k = 0; k < segs; k++) {
    const a = (k / segs) * 2 * Math.PI
    pts.push([cx + rx * Math.cos(a), cy + ry * Math.sin(a)])
  }
  return pts
}

const attrNum = (v: string | null, fallback = 0): number => {
  const n = parseFloat(v ?? "")
  return Number.isNaN(n) ? fallback : n
}

/** Collects one element's own subpaths in local coordinates (no transform
 * applied yet). Group/`<svg>` containers return none here -- their children
 * are walked separately. */
function elementSubpaths(el: Element): FlatSubpath[] {
  switch (el.localName) {
    case "path": {
      const d = el.getAttribute("d")
      return d ? parsePathData(d) : []
    }
    case "line":
      return [
        {
          pts: [
            [attrNum(el.getAttribute("x1")), attrNum(el.getAttribute("y1"))],
            [attrNum(el.getAttribute("x2")), attrNum(el.getAttribute("y2"))],
          ],
          closed: false,
        },
      ]
    case "polyline":
      return [{ pts: parsePoints(el.getAttribute("points")), closed: false }]
    case "polygon":
      return [{ pts: parsePoints(el.getAttribute("points")), closed: true }]
    case "rect": {
      const x = attrNum(el.getAttribute("x"))
      const y = attrNum(el.getAttribute("y"))
      const w = attrNum(el.getAttribute("width"))
      const h = attrNum(el.getAttribute("height"))
      if (w <= 0 || h <= 0) return []
      return [
        {
          pts: [
            [x, y],
            [x + w, y],
            [x + w, y + h],
            [x, y + h],
          ],
          closed: true,
        },
      ]
    }
    case "circle": {
      const r = attrNum(el.getAttribute("r"))
      if (r <= 0) return []
      return [{ pts: ellipsePoints(attrNum(el.getAttribute("cx")), attrNum(el.getAttribute("cy")), r, r), closed: true }]
    }
    case "ellipse": {
      const rx = attrNum(el.getAttribute("rx"))
      const ry = attrNum(el.getAttribute("ry"))
      if (rx <= 0 || ry <= 0) return []
      return [{ pts: ellipsePoints(attrNum(el.getAttribute("cx")), attrNum(el.getAttribute("cy")), rx, ry), closed: true }]
    }
    default:
      return []
  }
}

function walk(el: Element, parent: Matrix, out: FlatSubpath[]) {
  const m = mul(parent, parseTransform(el.getAttribute("transform")))
  for (const sub of elementSubpaths(el)) {
    out.push({ pts: sub.pts.map(([px, py]) => apply(m, px, py)), closed: sub.closed })
  }
  for (const child of Array.from(el.children)) walk(child, m, out)
}

/**
 * Flattens SVG markup to subpaths in a normalised 0..size frame. The root
 * `viewBox` (or width/height) is mapped onto the size box so glyphs authored
 * at any viewBox export at the same scale the canvas renders them. Returns
 * an empty array on a parse failure or an empty/non-drawing SVG -- the
 * caller falls back to a marker rather than emitting nothing.
 */
export function flattenSvg(markup: string, size = 24): FlatSubpath[] {
  let doc: Document
  try {
    doc = new DOMParser().parseFromString(markup, "image/svg+xml")
  } catch {
    return []
  }
  const svg = doc.documentElement
  if (!svg || svg.localName !== "svg" || doc.getElementsByTagName("parsererror").length > 0) return []

  const vbAttr = svg.getAttribute("viewBox")
  let minX = 0
  let minY = 0
  let vw = attrNum(svg.getAttribute("width"), size)
  let vh = attrNum(svg.getAttribute("height"), size)
  if (vbAttr) {
    const v = vbAttr.trim().split(/[\s,]+/).map(Number)
    if (v.length === 4 && v.every((n) => !Number.isNaN(n))) {
      minX = v[0]
      minY = v[1]
      vw = v[2]
      vh = v[3]
    }
  }
  if (vw <= 0) vw = size
  if (vh <= 0) vh = size

  const raw: FlatSubpath[] = []
  for (const child of Array.from(svg.children)) walk(child, IDENTITY, raw)

  const sx = size / vw
  const sy = size / vh
  return raw
    .map((sub) => ({
      pts: sub.pts.map(([px, py]): [number, number] => [(px - minX) * sx, (py - minY) * sy]),
      closed: sub.closed,
    }))
    .filter((sub) => sub.pts.length > 1)
}
