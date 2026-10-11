/**
 * Diagram -> DXF export (Phase 33). A pure, client-side converter that walks
 * the exact same resolved render model diagram-canvas.tsx draws to the SVG
 * canvas -- resolved symbology, connectivity-derived conductor endpoints,
 * icon/font sizing -- and emits a DXF a CAD tool can open. Client-side on
 * purpose (docs/phase-33-diagram-dxf-export.md): the browser already holds
 * every derived value a faithful export needs, and the server holds none of
 * it in resolved form (symbology matching is client-side, elbow routing is
 * computed here). Nothing on screen is re-derived; this consumes what the
 * canvas already computed.
 *
 * Target is DXF R12 (AC1009): the most portable interchange dialect -- no
 * handles, no object model, read by essentially every CAD/GIS tool -- at the
 * cost of true-colour (R12 is ACI-only, so symbology colours map to the
 * nearest of the seven standard indices, `nearestAci`). Geometry, layering
 * and text carry across exactly; the drawing is authored Y-down (screen
 * space) and flipped once to CAD's Y-up on emit (`emitXY`).
 *
 * Slice 1 covers all non-glyph geometry (conductors, buses/taps, boxes,
 * text, title blocks, legends, slots, and every name label). Slice 2 emits
 * feature devices and stencil SYMBOLS as their real glyph shapes: each
 * icon's `0 0 24 24` SVG is flattened to outlines (svg-flatten.ts) and
 * written once as a DXF BLOCK, then INSERTed per placement (scaled to
 * iconSize, centred, rotated) -- a glyph the exporter can't parse falls back
 * to the Slice 1 marker so nothing silently vanishes.
 */

import type { DiagramElement, DiagramPathPoint, DiagramPlacement } from "@/hooks/use-diagrams"
import type { FeatureRecord } from "@/hooks/use-feature"
import { resolveIconMarkup, type IconRegistry } from "@/lib/symbology-icons"
import { resolveSymbology, type StyleDef, type SymbologyRuleSchema } from "@/lib/symbology"
import { flattenSvg, type FlatSubpath } from "@/lib/svg-flatten"

/** A resolved conductor (a geometry_type = 'line' placement), exactly as
 * diagram-canvas.tsx's `conductorItems` computes it: its two real endpoints
 * already resolved from connectivity, plus the placement's own record for its
 * symbology + name. Structurally a subset of the canvas's ConductorRender. */
export interface DiagramExportConductor {
  placement: DiagramPlacement
  /** [end A, end B] in diagram-local space -- either real endpoint placements
   * (fullyConnected) or a fallback to the conductor's own x/y (loose end). */
  points: [{ x: number; y: number }, { x: number; y: number }]
  fullyConnected: boolean
  feature?: FeatureRecord
}

/** A resolved point placement (a device), as `pointItems` computes it. */
export interface DiagramExportPoint {
  placement: DiagramPlacement
  feature?: FeatureRecord
}

/** Everything the converter needs -- assembled by diagram-canvas.tsx from the
 * same memoised values it already renders from, so the export can never drift
 * from what's on screen. */
export interface DiagramDxfInput {
  diagramName: string
  elements: DiagramElement[]
  conductors: DiagramExportConductor[]
  points: DiagramExportPoint[]
  /** Per-class symbology rules (useAllSymbology) -- resolved here the same way
   * the canvas resolves each placement's colour. */
  rules: Record<string, SymbologyRuleSchema[]>
  /** icon_ref -> raw SVG markup (useSymbologyIcons) -- flattened to real
   * glyph outlines and emitted as BLOCK/INSERT per placement (Slice 2). */
  icons: IconRegistry
  /** The extent-relative sizes the canvas derives once per render, so exported
   * text/markers land at the same scale as the on-screen drawing. */
  iconSize: number
  fontSize: number
}

// --- DXF R12 primitives -----------------------------------------------------

const DEG2RAD = Math.PI / 180
/** DXF TEXT height is roughly cap height; SVG font-size is the em box. ~0.7
 * lands exported text at about the on-screen size rather than oversized. */
const TEXT_CAP = 0.7

/** The XDATA application id every feature's attributes are attached under --
 * registered in the APPID table (required in R12) so a CAD/GIS tool accepts
 * and can filter to our extended data. */
const APPID = "VOLTEDGE"

/** One extended-data key/value pair carried on a feature-backed entity. */
interface XDataPair {
  key: string
  value: string
}

/** The seven standard AutoCAD Color Index entries, as RGB, for nearest-colour
 * mapping (R12 has no true colour). 7 is black-on-white / white-on-black. */
const ACI_RGB: [number, number, number][] = [
  [255, 0, 0], // 1 red
  [255, 255, 0], // 2 yellow
  [0, 255, 0], // 3 green
  [0, 255, 255], // 4 cyan
  [0, 0, 255], // 5 blue
  [255, 0, 255], // 6 magenta
  [255, 255, 255], // 7 white/black
]

/** Parses a #rgb / #rrggbb colour to RGB; returns null for anything else (a
 * CSS var, a named colour, undefined) -- the caller then falls back to
 * ByLayer, which prints black. */
function parseHex(color: string | undefined): [number, number, number] | null {
  if (!color) return null
  const s = color.trim()
  const m3 = /^#([0-9a-f])([0-9a-f])([0-9a-f])$/i.exec(s)
  if (m3) return [parseInt(m3[1] + m3[1], 16), parseInt(m3[2] + m3[2], 16), parseInt(m3[3] + m3[3], 16)]
  const m6 = /^#([0-9a-f]{2})([0-9a-f]{2})([0-9a-f]{2})$/i.exec(s)
  if (m6) return [parseInt(m6[1], 16), parseInt(m6[2], 16), parseInt(m6[3], 16)]
  return null
}

/** The ACI (1-7) whose standard RGB is nearest a symbology colour, or 0
 * (ByLayer) when the colour isn't a resolvable hex. Keeps a green feeder green
 * and a red isolator red without R12 true colour. */
export function nearestAci(color: string | undefined): number {
  const rgb = parseHex(color)
  if (!rgb) return 0 // ByLayer
  let best = 7
  let bestDist = Infinity
  ACI_RGB.forEach(([r, g, b], i) => {
    const d = (r - rgb[0]) ** 2 + (g - rgb[1]) ** 2 + (b - rgb[2]) ** 2
    if (d < bestDist) {
      bestDist = d
      best = i + 1
    }
  })
  return best
}

interface TextOpts {
  layer: string
  height: number
  color?: number
  /** SVG textAnchor -> DXF horizontal justification. */
  align?: "left" | "center"
  /** Screen-space rotation in degrees (SVG, clockwise). Flipped to CAD's
   * CCW convention on emit. */
  rotationDeg?: number
}

/** Accumulates DXF entities and the layers they use, then serialises a
 * complete R12 document. Every coordinate is passed in diagram (Y-down) space
 * and flipped to CAD's Y-up exactly once, in `emitXY`. */
export class DxfBuilder {
  private entities: string[] = []
  private blocks: string[] = []
  /** Where pair()/emitXY write -- normally the ENTITIES buffer, temporarily
   * the BLOCKS buffer while a glyph block is being defined. */
  private cursor: string[] = this.entities
  private layers = new Set<string>(["0"])
  private blockNames = new Set<string>()

  private pair(code: number, value: string | number) {
    this.cursor.push(String(code), String(value))
  }

  /** The one place Y is negated -- diagram space is Y-down, CAD is Y-up. */
  private emitXY(x: number, y: number, xCode = 10, yCode = 20) {
    this.pair(xCode, round(x))
    this.pair(yCode, round(-y))
  }

  private color(aci: number | undefined) {
    if (aci && aci > 0) this.pair(62, aci)
  }

  /** Appends XDATA (feature attributes) under APPID -- valid on any entity,
   * and always the last group codes of that entity. Each pair is one 1000
   * string "key=value", newline-stripped and capped at DXF's 255-byte limit.
   * Emitted on the POLYLINE header (before its vertices), or at the tail of a
   * simple entity. */
  private emitXData(data?: XDataPair[]) {
    if (!data || data.length === 0) return
    this.pair(1001, APPID)
    for (const { key, value } of data) {
      this.pair(1000, `${key}=${value}`.replace(/[\r\n]+/g, " ").slice(0, 255))
    }
  }

  polyline(pts: DiagramPathPoint[], opts: { layer: string; closed?: boolean; color?: number; dashed?: boolean; xdata?: XDataPair[] }) {
    if (pts.length < 2) return
    this.layers.add(opts.layer)
    this.pair(0, "POLYLINE")
    this.pair(8, opts.layer)
    this.color(opts.color)
    if (opts.dashed) this.pair(6, "DASHED")
    this.pair(66, 1) // vertices-follow flag (required in R12)
    this.pair(70, opts.closed ? 1 : 0)
    // A dummy elevation point some strict R12 readers expect on the header.
    this.emitXY(0, 0)
    this.pair(30, 0)
    // XDATA rides the POLYLINE header, before the VERTEX entities follow.
    this.emitXData(opts.xdata)
    for (const [x, y] of pts) {
      this.pair(0, "VERTEX")
      this.pair(8, opts.layer)
      this.emitXY(x, y)
      this.pair(30, 0)
    }
    this.pair(0, "SEQEND")
    this.pair(8, opts.layer)
  }

  line(a: { x: number; y: number }, b: { x: number; y: number }, opts: { layer: string; color?: number; dashed?: boolean }) {
    this.layers.add(opts.layer)
    this.pair(0, "LINE")
    this.pair(8, opts.layer)
    this.color(opts.color)
    if (opts.dashed) this.pair(6, "DASHED")
    this.emitXY(a.x, a.y)
    this.pair(30, 0)
    this.emitXY(b.x, b.y, 11, 21)
    this.pair(31, 0)
  }

  circle(c: { x: number; y: number }, r: number, opts: { layer: string; color?: number; xdata?: XDataPair[] }) {
    if (r <= 0) return
    this.layers.add(opts.layer)
    this.pair(0, "CIRCLE")
    this.pair(8, opts.layer)
    this.color(opts.color)
    this.emitXY(c.x, c.y)
    this.pair(30, 0)
    this.pair(40, round(r))
    this.emitXData(opts.xdata)
  }

  text(pos: { x: number; y: number }, value: string, opts: TextOpts) {
    const str = value.replace(/[\r\n]+/g, " ").trim()
    if (!str || opts.height <= 0) return
    this.layers.add(opts.layer)
    this.pair(0, "TEXT")
    this.pair(8, opts.layer)
    this.color(opts.color)
    this.emitXY(pos.x, pos.y)
    this.pair(30, 0)
    this.pair(40, round(opts.height * TEXT_CAP))
    this.pair(1, str)
    // SVG rotate is clockwise in Y-down space; after the Y-flip that reads as
    // clockwise-negated in CAD's CCW group-50 convention.
    if (opts.rotationDeg) this.pair(50, round(-opts.rotationDeg))
    if (opts.align === "center") {
      this.pair(72, 1) // centre
      // With justification set, the alignment point is the second point.
      this.emitXY(pos.x, pos.y, 11, 21)
      this.pair(31, 0)
    }
  }

  /** True once a block of this name has been defined, so callers only build
   * each distinct glyph once and INSERT it per placement. */
  hasBlock(name: string): boolean {
    return this.blockNames.has(name)
  }

  /** Defines a glyph as an R12 BLOCK from flattened subpaths, once. The
   * subpaths are in the glyph's own 0..24 space; each is centred on (12,12)
   * so the block's base point is the glyph centre, and emitted pre-Y-flipped
   * (via emitXY) so an INSERT with a positive scale and group-50 rotation of
   * `-deg` reproduces the canvas's translate.rotate.scale exactly (see
   * insertGlyph). Entities are colour BYBLOCK (62 = 0) so each INSERT tints
   * the whole glyph from its own resolved symbology colour. */
  defineGlyphBlock(name: string, subpaths: FlatSubpath[]) {
    if (this.blockNames.has(name)) return
    this.blockNames.add(name)
    const prev = this.cursor
    this.cursor = this.blocks
    this.pair(0, "BLOCK")
    this.pair(8, "0")
    this.pair(2, name)
    this.pair(70, 0)
    this.pair(10, 0)
    this.pair(20, 0)
    this.pair(30, 0)
    this.pair(3, name)
    for (const sub of subpaths) {
      if (sub.pts.length < 2) continue
      this.pair(0, "POLYLINE")
      this.pair(8, "0")
      this.pair(62, 0) // BYBLOCK
      this.pair(66, 1)
      this.pair(70, sub.closed ? 1 : 0)
      this.emitXY(0, 0)
      this.pair(30, 0)
      for (const [gx, gy] of sub.pts) {
        this.pair(0, "VERTEX")
        this.pair(8, "0")
        this.pair(62, 0)
        this.emitXY(gx - 12, gy - 12)
        this.pair(30, 0)
      }
      this.pair(0, "SEQEND")
      this.pair(8, "0")
    }
    this.pair(0, "ENDBLK")
    this.pair(8, "0")
    this.cursor = prev
  }

  /** Places a defined glyph block. `at` is the placement centre in diagram
   * (Y-down) space -- Y-flipped by emitXY like every other coordinate.
   * `scale` maps the glyph's 24-unit box to iconSize; `rotationDeg` is the
   * placement's SVG (clockwise, Y-down) rotation, emitted as CAD's CCW
   * group-50 `-deg` -- the same negation text() uses, and the value that
   * makes the pre-flipped block geometry land upright and correctly turned. */
  insertGlyph(name: string, at: { x: number; y: number }, scale: number, rotationDeg: number, opts: { layer: string; color?: number; xdata?: XDataPair[] }) {
    this.layers.add(opts.layer)
    this.pair(0, "INSERT")
    this.pair(8, opts.layer)
    this.color(opts.color)
    this.pair(2, name)
    this.emitXY(at.x, at.y)
    this.pair(30, 0)
    this.pair(41, round(scale))
    this.pair(42, round(scale))
    this.pair(43, round(scale))
    if (rotationDeg) this.pair(50, round(-rotationDeg))
    this.emitXData(opts.xdata)
  }

  /** Serialises the full R12 document: HEADER, a TABLES section defining the
   * CONTINUOUS/DASHED linetypes and every layer used, a BLOCKS section
   * holding the glyph blocks, then ENTITIES and EOF. */
  serialize(dashLen: number): string {
    const out: string[] = []
    const p = (code: number, value: string | number) => {
      out.push(String(code), String(value))
    }

    // HEADER
    p(0, "SECTION")
    p(2, "HEADER")
    p(9, "$ACADVER")
    p(1, "AC1009")
    p(0, "ENDSEC")

    // TABLES: linetypes then layers.
    p(0, "SECTION")
    p(2, "TABLES")
    p(0, "TABLE")
    p(2, "LTYPE")
    p(70, 2)
    p(0, "LTYPE")
    p(2, "CONTINUOUS")
    p(70, 0)
    p(3, "Solid line")
    p(72, 65)
    p(73, 0)
    p(40, 0)
    // A simple equal dash/gap pattern in drawing units, scaled to the sheet.
    const dl = Math.max(dashLen, 1)
    p(0, "LTYPE")
    p(2, "DASHED")
    p(70, 0)
    p(3, "Dashed")
    p(72, 65)
    p(73, 2)
    p(40, round(dl * 2))
    p(49, round(dl))
    p(49, round(-dl))
    p(0, "ENDTAB")

    const layerNames = [...this.layers].sort()
    p(0, "TABLE")
    p(2, "LAYER")
    p(70, layerNames.length)
    for (const name of layerNames) {
      p(0, "LAYER")
      p(2, name)
      p(70, 0)
      p(62, 7)
      p(6, "CONTINUOUS")
    }
    p(0, "ENDTAB")

    // APPID: registers the XDATA application id so readers accept the
    // feature attributes attached to entities (required in R12).
    p(0, "TABLE")
    p(2, "APPID")
    p(70, 1)
    p(0, "APPID")
    p(2, APPID)
    p(70, 0)
    p(0, "ENDTAB")
    p(0, "ENDSEC")

    // BLOCKS -- one glyph block per distinct icon_ref actually placed.
    p(0, "SECTION")
    p(2, "BLOCKS")
    out.push(...this.blocks)
    p(0, "ENDSEC")

    // ENTITIES
    p(0, "SECTION")
    p(2, "ENTITIES")
    out.push(...this.entities)
    p(0, "ENDSEC")
    p(0, "EOF")
    return out.join("\r\n") + "\r\n"
  }
}

/** DXF is whitespace-sensitive per pair but tolerant of precision; trimming to
 * 4 dp keeps files small without visible drift at diagram scale. */
function round(n: number): string {
  if (!Number.isFinite(n)) return "0"
  return (Math.round(n * 1e4) / 1e4).toString()
}

/** Screen-space [x,y] of a diagram-local point taken through a
 * translate(tx,ty) . rotate(deg) chain (SVG's own matrix, clockwise in
 * Y-down space) -- the transform every rotated placement/label rides. */
function transformPoint(lx: number, ly: number, deg: number, tx: number, ty: number): { x: number; y: number } {
  const a = deg * DEG2RAD
  const cos = Math.cos(a)
  const sin = Math.sin(a)
  return { x: tx + lx * cos - ly * sin, y: ty + lx * sin + ly * cos }
}

// Layer names -- one per structural kind, so a CAD user can toggle conductors,
// devices, furniture and labels independently.
export const LAYER = {
  conductors: "CONDUCTORS",
  devices: "DEVICES",
  symbols: "SYMBOLS",
  buses: "BUSES",
  taps: "TAPS",
  annotations: "ANNOTATIONS",
  text: "TEXT",
  labels: "LABELS",
  titleBlock: "TITLEBLOCK",
  legend: "LEGEND",
  slots: "SLOTS",
} as const

/** An R12 block name for an icon_ref: uppercase, only [A-Z0-9_$], `SYM_`
 * prefixed so it never collides with a reserved name and reads in a CAD
 * layer/block list. Same ref -> same name, so each glyph is defined once. */
export function blockNameFor(iconRef: string): string {
  return "SYM_" + iconRef.toUpperCase().replace(/[^A-Z0-9_$]/g, "_")
}

/** The XDATA a feature-backed entity carries: its identity (class/id/name/
 * subtype) plus every non-null attribute, so a conductor or device selected
 * in CAD exposes the same data the feature panel shows. Scalars only --
 * nested objects/arrays are skipped (an attribute value is rendered with
 * String(), which would be unhelpful for those). */
function featureXData(placement: DiagramPlacement, feature: FeatureRecord | undefined): XDataPair[] {
  const out: XDataPair[] = [
    { key: "feature_class", value: placement.feature_class },
    { key: "feature_id", value: placement.feature_id },
  ]
  if (feature?.name) out.push({ key: "name", value: feature.name })
  if (feature?.subtype !== undefined) out.push({ key: "subtype", value: String(feature.subtype) })
  if (feature?.status) out.push({ key: "status", value: feature.status })
  for (const [key, value] of Object.entries(feature?.attributes ?? {})) {
    if (value === null || value === undefined || typeof value === "object") continue
    out.push({ key, value: String(value) })
  }
  return out
}

/** Builds the DXF text for one diagram. Pure: same input -> same output. */
export function buildDiagramDxf(input: DiagramDxfInput): string {
  const { elements, conductors, points, rules, icons, iconSize, fontSize } = input
  const b = new DxfBuilder()
  const dashLen = Math.max(fontSize * 0.8, 4)
  const glyphScale = iconSize / 24
  // Flatten each distinct icon_ref once; a ref that yields no geometry maps
  // to null so the caller draws the Slice 1 marker instead.
  const glyphCache = new Map<string, FlatSubpath[] | null>()

  const resolveStyle = (feature: FeatureRecord | undefined, featureClass: string): StyleDef | undefined => {
    const classRules = rules[featureClass]
    return (
      resolveSymbology(classRules, feature?.subtype, feature?.attributes ?? {}, "diagram") ??
      resolveSymbology(classRules, feature?.subtype, feature?.attributes ?? {}, "map")
    )
  }

  const styleColor = (feature: FeatureRecord | undefined, featureClass: string): number =>
    nearestAci(resolveStyle(feature, featureClass)?.color)

  /** Emits one glyph at `at`: its real shape as a BLOCK/INSERT when the SVG
   * flattens to geometry, else a marker circle (never nothing). `xdata` is
   * the feature's attributes, attached to whichever entity is emitted. */
  const emitGlyph = (
    iconRef: string | undefined,
    color: string | undefined,
    at: { x: number; y: number },
    rotationDeg: number,
    layer: string,
    xdata?: XDataPair[],
  ) => {
    const ref = iconRef ?? "circle"
    const aci = nearestAci(color)
    let subs = glyphCache.get(ref)
    if (subs === undefined) {
      const flat = flattenSvg(resolveIconMarkup(ref, icons))
      subs = flat.length > 0 ? flat : null
      glyphCache.set(ref, subs)
    }
    if (!subs) {
      b.circle(at, iconSize * 0.5, { layer, color: aci, xdata })
      return
    }
    const name = blockNameFor(ref)
    if (!b.hasBlock(name)) b.defineGlyphBlock(name, subs)
    b.insertGlyph(name, at, glyphScale, rotationDeg, { layer, color: aci, xdata })
  }

  // --- Elements (furniture): drawn first so they sit behind the network, the
  //     same z-order the canvas paints them in. ---
  for (const el of elements) {
    const style = (el.style ?? {}) as Record<string, unknown>
    const dashed = Boolean(style.dash)
    const w = el.width ?? 0
    const h = el.height ?? 0

    if (el.kind === "box") {
      b.polyline(
        [
          [el.x, el.y],
          [el.x + w, el.y],
          [el.x + w, el.y + h],
          [el.x, el.y + h],
        ],
        { layer: LAYER.annotations, closed: true, dashed },
      )
      if (el.text) {
        b.text({ x: el.x + w / 2, y: el.y - fontSize * 0.5 }, el.text, { layer: LAYER.annotations, height: fontSize, align: "center" })
      }
    } else if (el.kind === "line") {
      if (el.path && el.path.length >= 2) b.polyline(el.path, { layer: LAYER.annotations, dashed })
    } else if (el.kind === "control") {
      // A control/trip signal line (Phase 46): a hand-drawn 'line' that always
      // reads dashed. DXF has no arrowhead primitive here, so it exports as a
      // dashed polyline on the annotations layer -- the dash carries the
      // "signal, not power" semantics; the on-canvas arrowhead is cosmetic.
      if (el.path && el.path.length >= 2) b.polyline(el.path, { layer: LAYER.annotations, dashed: true })
    } else if (el.kind === "bus") {
      if (el.path && el.path.length >= 2) b.polyline(el.path, { layer: LAYER.buses })
    } else if (el.kind === "tap") {
      if (el.path && el.path.length >= 2) {
        b.polyline(el.path, { layer: LAYER.taps })
        const j = el.path[el.path.length - 1]
        b.circle({ x: j[0], y: j[1] }, Math.max(fontSize * 0.45, 2.5), { layer: LAYER.taps })
      }
    } else if (el.kind === "title_block") {
      const fields = Array.isArray(style.fields) ? (style.fields as { label: string; value: string }[]) : []
      const rowH = fields.length > 0 ? h / fields.length : h
      const labelColW = w * 0.42
      b.polyline(
        [
          [el.x, el.y],
          [el.x + w, el.y],
          [el.x + w, el.y + h],
          [el.x, el.y + h],
        ],
        { layer: LAYER.titleBlock, closed: true },
      )
      fields.forEach((f, i) => {
        const ry = el.y + i * rowH
        if (i > 0) b.line({ x: el.x, y: ry }, { x: el.x + w, y: ry }, { layer: LAYER.titleBlock })
        b.line({ x: el.x + labelColW, y: ry }, { x: el.x + labelColW, y: ry + rowH }, { layer: LAYER.titleBlock })
        b.text({ x: el.x + w * 0.03, y: ry + rowH * 0.62 }, f.label, { layer: LAYER.titleBlock, height: fontSize * 0.72 })
        b.text({ x: el.x + labelColW + w * 0.03, y: ry + rowH * 0.62 }, f.value || "-", { layer: LAYER.titleBlock, height: fontSize * 0.9 })
      })
    } else if (el.kind === "legend") {
      const title = typeof style.title === "string" ? style.title : "Legend"
      const headerH = Math.min(fontSize * 1.8, h * 0.3)
      b.polyline(
        [
          [el.x, el.y],
          [el.x + w, el.y],
          [el.x + w, el.y + h],
          [el.x, el.y + h],
        ],
        { layer: LAYER.legend, closed: true },
      )
      b.line({ x: el.x, y: el.y + headerH }, { x: el.x + w, y: el.y + headerH }, { layer: LAYER.legend })
      b.text({ x: el.x + w * 0.05, y: el.y + headerH * 0.66 }, title, { layer: LAYER.legend, height: fontSize * 0.95 })
    } else if (el.kind === "slot") {
      const label = el.slot_class ?? "slot"
      const r = iconSize * 0.5
      b.circle({ x: el.x, y: el.y }, r, { layer: LAYER.slots })
      b.text({ x: el.x, y: el.y + fontSize * 0.4 }, "+", { layer: LAYER.slots, height: fontSize * 1.3, align: "center" })
      b.text({ x: el.x, y: el.y + r + fontSize * 1.1 }, label, { layer: LAYER.slots, height: fontSize * 0.8, align: "center" })
    } else if (el.kind === "symbol") {
      // A stencil glyph: its real shape, centred on (x,y) and rotated like the
      // canvas draws it (translate.rotate.image). style.color tints it.
      const iconRef = typeof style.iconRef === "string" ? style.iconRef : undefined
      const color = typeof style.color === "string" ? style.color : undefined
      emitGlyph(iconRef, color, { x: el.x, y: el.y }, el.rotation, LAYER.symbols)
    } else {
      // text (and any future kind falls through to a plain label)
      const size = typeof style.fontSize === "number" ? (style.fontSize as number) : fontSize * 1.2
      b.text({ x: el.x, y: el.y }, el.text || "Label", { layer: LAYER.text, height: size })
    }
  }

  // --- Conductors: a drawn path if it has one, else the connectivity-derived
  //     elbow between its two endpoints (elbowPath). Loose ends (no geometry)
  //     are skipped, matching the canvas. ---
  for (const c of conductors) {
    const color = styleColor(c.feature, c.placement.feature_class)
    const drawnPath = c.placement.path && c.placement.path.length >= 2 ? c.placement.path : null
    const hasGeometry = drawnPath !== null || c.fullyConnected
    if (!hasGeometry) continue
    const [p1, p2] = c.points
    const path: DiagramPathPoint[] = drawnPath ?? elbowPoints(p1, p2)
    b.polyline(path, { layer: LAYER.conductors, color, xdata: featureXData(c.placement, c.feature) })
    if (!c.placement.label_hidden) {
      const mid = drawnPath ? drawnPath[Math.floor(drawnPath.length / 2)] : [(p1.x + p2.x) / 2, (p1.y + p2.y) / 2]
      const ldx = c.placement.label_dx ?? 0
      const ldy = c.placement.label_dy ?? 0
      const lrot = c.placement.label_rotation ?? 0
      const lscale = c.placement.label_scale ?? 1
      const anchorY = -fontSize * 0.6
      b.text({ x: mid[0] + ldx, y: mid[1] + anchorY + ldy }, c.feature?.name || c.placement.feature_class, {
        layer: LAYER.labels,
        height: fontSize * lscale,
        align: "center",
        rotationDeg: lrot,
      })
    }
  }

  // --- Point placements (devices): the resolved glyph (BLOCK/INSERT) at the
  //     placement centre, rotated like the canvas, plus the name label. ---
  for (const pt of points) {
    const p = pt.placement
    const def = resolveStyle(pt.feature, p.feature_class)
    emitGlyph(def?.icon_ref, def?.color, { x: p.x, y: p.y }, p.rotation, LAYER.devices, featureXData(p, pt.feature))
    if (!p.label_hidden) {
      const ldx = p.label_dx ?? 0
      const ldy = p.label_dy ?? 0
      const lrot = p.label_rotation ?? 0
      const lscale = p.label_scale ?? 1
      const anchorY = iconSize / 2 + fontSize * 1.4
      // Label rides inside the glyph's rotate(): its anchor is the glyph-local
      // offset taken through the placement rotation, and its own rotation is
      // the glyph rotation plus the label rotation.
      const anchor = transformPoint(ldx, anchorY + ldy, p.rotation, p.x, p.y)
      b.text(anchor, pt.feature?.name || p.feature_class, {
        layer: LAYER.labels,
        height: fontSize * lscale,
        align: "center",
        rotationDeg: p.rotation + lrot,
      })
    }
  }

  return b.serialize(dashLen)
}

/** The vertices of elbowPath(p1, p2) -- a single mid-span right-angle bend,
 * matching diagram-canvas.tsx's connectivity-derived conductor route. */
function elbowPoints(p1: { x: number; y: number }, p2: { x: number; y: number }): DiagramPathPoint[] {
  const midX = (p1.x + p2.x) / 2
  return [
    [p1.x, p1.y],
    [midX, p1.y],
    [midX, p2.y],
    [p2.x, p2.y],
  ]
}

/** Triggers a browser download of the built DXF. A sanitised diagram name
 * gives the file a recognisable stem; falls back to "diagram". */
export function downloadDiagramDxf(input: DiagramDxfInput) {
  const dxf = buildDiagramDxf(input)
  const stem = (input.diagramName || "diagram").replace(/[^\w.-]+/g, "_").replace(/^_+|_+$/g, "") || "diagram"
  const blob = new Blob([dxf], { type: "application/dxf" })
  const url = URL.createObjectURL(blob)
  const a = document.createElement("a")
  a.href = url
  a.download = `${stem}.dxf`
  document.body.appendChild(a)
  a.click()
  a.remove()
  URL.revokeObjectURL(url)
}
