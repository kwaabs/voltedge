/**
 * Temporary one-line -> DXF export. Reuses the diagram exporter's R12 DxfBuilder
 * (glyph blocks, polylines, layers, XDATA) but consumes the one-line payload
 * (nodes at abstract x/y + connector polylines) rather than the diagram canvas's
 * render model. Pure + client-side, like diagram-dxf.ts: the browser holds the
 * resolved symbology the export needs. Symbols become BLOCK/INSERT glyphs,
 * conductors become polylines on the CONDUCTORS layer coloured by their own
 * symbology, and rating/demand labels become TEXT.
 */

import { blockNameFor, DxfBuilder, LAYER, nearestAci } from "@/lib/diagram-dxf"
import { circuitColor, type OneLineNode, type OneLinePayload } from "@/lib/one-line"
import { resolveSymbology, type SymbologyRuleSchema } from "@/lib/symbology"
import { resolveIconMarkup, type IconRegistry } from "@/lib/symbology-icons"
import { flattenSvg, type FlatSubpath } from "@/lib/svg-flatten"

// The abstract one-line units the layout + the SVG view use (components/map/
// one-line-view.tsx): the DXF is authored in the same space, Y-down, flipped to
// CAD's Y-up by DxfBuilder.emitXY.
const ICON = 30
const FONT = 11

function ratingLabel(n: OneLineNode): string | null {
  if (n.rated_kva != null) return `${Number.isInteger(n.rated_kva) ? n.rated_kva : n.rated_kva.toFixed(1)} kVA`
  if (n.demand_kw != null) return `${Number.isInteger(n.demand_kw) ? n.demand_kw : n.demand_kw.toFixed(1)} kW`
  return null
}

/** Builds the DXF text for one temporary one-line. Pure: same input -> same output. */
export function buildOneLineDxf(
  payload: OneLinePayload,
  rules: Record<string, SymbologyRuleSchema[]>,
  icons: IconRegistry,
): string {
  const b = new DxfBuilder()
  const glyphScale = ICON / 24
  const glyphCache = new Map<string, FlatSubpath[] | null>()

  const diagramStyle = (featureClass: string, subtype?: number, attrs?: Record<string, unknown>) =>
    resolveSymbology(rules[featureClass], subtype, attrs ?? {}, "diagram") ??
    resolveSymbology(rules[featureClass], subtype, attrs ?? {}, "map")

  // One glyph at `at`: its real shape as a BLOCK/INSERT when the icon SVG
  // flattens to geometry, else a marker circle (never nothing) -- the same
  // fallback diagram-dxf.ts uses.
  const emitGlyph = (
    iconRef: string | undefined,
    color: string | undefined,
    at: { x: number; y: number },
    xdata: { key: string; value: string }[],
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
      b.circle(at, ICON * 0.5, { layer: LAYER.devices, color: aci, xdata })
      return
    }
    const name = blockNameFor(ref)
    if (!b.hasBlock(name)) b.defineGlyphBlock(name, subs)
    b.insertGlyph(name, at, glyphScale, 0, { layer: LAYER.devices, color: aci, xdata })
  }

  // Conductors / links first, behind the symbols.
  for (const c of payload.connectors) {
    if (c.path.length < 2) continue
    const def = c.feature_class ? diagramStyle(c.feature_class, c.subtype, c.circuit_ref ? { circuit_ref: c.circuit_ref } : {}) : undefined
    const xdata = c.feature_class && c.feature_id ? [
      { key: "feature_class", value: c.feature_class },
      { key: "feature_id", value: c.feature_id },
    ] : undefined
    // Match the on-screen colour: circuit tint when the wire carries one, else the
    // conductor's own symbology colour.
    const color = circuitColor(c.circuit_ref) ?? def?.color
    b.polyline(c.path, { layer: LAYER.conductors, color: nearestAci(color), xdata })
  }

  // Point features: glyph + rating label.
  for (const n of payload.nodes) {
    const attrs: Record<string, unknown> = {}
    if (n.device_state != null) attrs.device_state = n.device_state
    if (n.circuit_ref != null) attrs.circuit_ref = n.circuit_ref
    const def = diagramStyle(n.feature_class, n.subtype, attrs)
    const xdata = [
      { key: "feature_class", value: n.feature_class },
      { key: "feature_id", value: n.feature_id },
    ]
    if (n.device_state) xdata.push({ key: "device_state", value: n.device_state })
    if (n.circuit_ref) xdata.push({ key: "circuit_ref", value: n.circuit_ref })
    emitGlyph(def?.icon_ref, def?.color, { x: n.x, y: n.y }, xdata)

    const label = ratingLabel(n)
    if (label) {
      b.text({ x: n.x, y: n.y + ICON / 2 + FONT * 1.2 }, label, {
        layer: LAYER.labels,
        height: FONT,
        align: "center",
      })
    }
  }

  return b.serialize(Math.max(FONT * 0.8, 4))
}

/** Triggers a browser download of the one-line as DXF. */
export function downloadOneLineDxf(
  payload: OneLinePayload,
  rules: Record<string, SymbologyRuleSchema[]>,
  icons: IconRegistry,
) {
  const dxf = buildOneLineDxf(payload, rules, icons)
  const blob = new Blob([dxf], { type: "application/dxf" })
  const url = URL.createObjectURL(blob)
  const a = document.createElement("a")
  a.href = url
  a.download = "one-line.dxf"
  document.body.appendChild(a)
  a.click()
  a.remove()
  URL.revokeObjectURL(url)
}
