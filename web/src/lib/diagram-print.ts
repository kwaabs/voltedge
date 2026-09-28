/**
 * Diagram -> print sheet (PDF). Slice 1 of the diagram print engine: takes the
 * LIVE on-screen canvas <svg> and lays it, scaled-to-fit, inside a professional
 * drawing frame on a fixed paper size (ISO A4-A0), then emits a vector PDF the
 * browser downloads.
 *
 * Client-side + clone-based on purpose, mirroring lib/diagram-dxf.ts's guiding
 * principle: the browser already holds every derived value a faithful output
 * needs (resolved symbology, connectivity-derived endpoints, icon glyphs, elbow
 * routing), and the server holds none of it in resolved form. But where the DXF
 * exporter re-EMITS from a resolved model, this simply CLONES what the canvas
 * already drew -- so it can never drift from the screen and needs no re-derivation
 * at all. We only strip the interactive-only chrome, resolve theme CSS variables
 * to a fixed light print palette, and drop the drawing under a fit transform.
 *
 * The intermediate is a paper-sized SVG authored in millimetres (viewBox and
 * jsPDF page share the same mm coordinate space, 1:1), converted by svg2pdf.js.
 * SVG is Y-down and so is the sheet, so -- unlike the DXF path's flip to CAD's
 * Y-up -- no axis flip is needed.
 *
 * Slice 1 furniture is the outer border + inner drawing frame + a thin footer
 * strip (name / scale / paper). The docked title block, zone grid, and auto
 * legend land in later slices.
 */

import { jsPDF } from "jspdf"
import { svg2pdf } from "svg2pdf.js"

const SVG_NS = "http://www.w3.org/2000/svg"

/** Supported ISO A paper sizes (jsPDF knows these named formats natively). */
export type PaperSize = "A4" | "A3" | "A2" | "A1" | "A0"
export type PaperOrientation = "portrait" | "landscape"

export interface DiagramPrintOptions {
  /** Used for the footer label and the download filename. */
  diagramName: string
  /** The live canvas <svg> node (svgRef.current). Cloned, never mutated. */
  svg: SVGSVGElement
  /** The content bounds in diagram-local user space the canvas fits to
   *  (DiagramCanvas's `fitBox`) -- what we scale into the drawing frame. */
  fitBox: { x: number; y: number; w: number; h: number }
  paper?: PaperSize
  orientation?: PaperOrientation
}

/** A fixed light palette for the handful of theme CSS variables the diagram
 *  subtree paints with, so a plot is always dark-on-white regardless of the
 *  app's current theme. Every variable enumerated appears in diagram-canvas.tsx;
 *  the interactive ones (--ring/--destructive*) are here only as a backstop --
 *  their nodes are stripped before this runs. */
const PRINT_PALETTE: Record<string, string> = {
  "--foreground": "#0f172a",
  "--background": "#ffffff",
  "--border": "#94a3b8",
  "--muted-foreground": "#475569",
  "--ring": "#94a3b8",
  "--destructive": "#dc2626",
  "--destructive-foreground": "#ffffff",
}

/** Portrait paper dimensions in millimetres [width, height]. Landscape swaps. */
const PAPER_MM: Record<PaperSize, [number, number]> = {
  A4: [210, 297],
  A3: [297, 420],
  A2: [420, 594],
  A1: [594, 841],
  A0: [841, 1189],
}

// Sheet margins (mm): a wider left margin is the drawing-office binding edge.
const MARGIN = { top: 10, right: 10, bottom: 10, left: 20 }
const OUTER_INSET = 5 // thin trim border, mm from the paper edge
const FOOTER_H = 10 // footer strip height inside the frame, mm

const VAR_REF = /var\((--[a-z-]+)(?:,[^)]*)?\)/g
const URL_REF = /url\(#([^)]+)\)/g

/** Replace every `var(--x)` token in a value string with its print-palette
 *  colour (leaving an unknown variable's original text untouched). */
function resolveVars(value: string): string {
  return value.replace(VAR_REF, (match, name: string) => PRINT_PALETTE[name] ?? match)
}

/** Deep-clone the live canvas SVG, strip interactive-only chrome, make its
 *  element ids unique (so they can't collide with the still-mounted live SVG --
 *  or a second sheet, like an on-screen preview -- in the same document), and
 *  resolve theme variables to concrete print colours. `token` namespaces the
 *  remapped ids so two independently built sheets never collide. Returns the
 *  cloned <svg> whose children are the print-ready drawing. */
function prepareContentClone(live: SVGSVGElement, token: string): SVGSVGElement {
  const clone = live.cloneNode(true) as SVGSVGElement

  // Drop interactive-only overlays: in-place edit inputs (foreignObject) and
  // every hover-revealed handle/remove-"x"/resize/rotate control -- all of
  // which consistently carry the `opacity-0 group-hover:opacity-100` classes.
  clone.querySelectorAll("foreignObject").forEach((n) => n.remove())
  clone.querySelectorAll('[class*="opacity-0"]').forEach((n) => n.remove())
  // The opt-in drawing grid paints a huge over-cover rect; never plot it.
  clone.querySelectorAll('rect[fill^="url(#diagram-grid"]').forEach((n) => n.remove())

  // Inline device/stencil glyphs (SVG-data-URI <image>s svg2pdf can't rasterize)
  // as real geometry, before the id/var passes below process them too.
  inlineGlyphImages(clone)

  // Remap ids so url(#..) references (arrowhead markers, clips) can't resolve
  // against the identical ids still live in the mounted canvas.
  const idMap = new Map<string, string>()
  clone.querySelectorAll<SVGElement>("[id]").forEach((n, i) => {
    const old = n.id
    const next = `vep-${token}-${i}-${old}`
    idMap.set(old, next)
    n.id = next
  })
  const remapUrls = (v: string): string => v.replace(URL_REF, (_m, id: string) => `url(#${idMap.get(id) ?? id})`)

  const REF_ATTRS = ["fill", "stroke", "marker-start", "marker-mid", "marker-end", "clip-path", "filter", "mask"]
  clone.querySelectorAll<SVGElement>("*").forEach((n) => {
    for (const attr of REF_ATTRS) {
      const v = n.getAttribute(attr)
      if (v == null) continue
      let next = v
      if (next.includes("url(#")) next = remapUrls(next)
      if (next.includes("var(")) next = resolveVars(next)
      if (next !== v) n.setAttribute(attr, next)
    }
    const style = n.getAttribute("style")
    if (style != null && (style.includes("var(") || style.includes("url(#"))) {
      n.setAttribute("style", resolveVars(remapUrls(style)))
    }
  })

  return clone
}

const XLINK_NS = "http://www.w3.org/1999/xlink"

/** Decode an SVG `data:` URI (utf8 or base64) to its raw markup, or null if the
 *  href isn't an inline SVG image. */
function decodeSvgDataUri(href: string): string | null {
  if (!href.startsWith("data:image/svg+xml")) return null
  const comma = href.indexOf(",")
  if (comma < 0) return null
  const meta = href.slice(0, comma)
  const data = href.slice(comma + 1)
  try {
    return meta.includes(";base64") ? atob(data) : decodeURIComponent(data)
  } catch {
    return null
  }
}

/** svg2pdf renders raster <image>s, not SVG-data-URI images -- so device/stencil
 *  glyphs (embedded as `<image href="data:image/svg+xml,...">`) would plot blank.
 *  Replace each with the real glyph geometry inlined as a <g>, scaled from the
 *  icon's own viewBox (0 0 24 24 by convention) to the image's x/y/width/height.
 *  A glyph that fails to parse is left as-is (worst case: one blank marker). */
function inlineGlyphImages(root: SVGSVGElement): void {
  const parser = new DOMParser()
  root.querySelectorAll("image").forEach((img) => {
    const href = img.getAttribute("href") ?? img.getAttributeNS(XLINK_NS, "href")
    if (!href) return
    const markup = decodeSvgDataUri(href)
    if (!markup) return
    const parsed = parser.parseFromString(markup, "image/svg+xml").documentElement
    if (!parsed || parsed.nodeName === "parsererror") return

    const x = Number(img.getAttribute("x") ?? 0)
    const y = Number(img.getAttribute("y") ?? 0)
    const w = Number(img.getAttribute("width") ?? 0)
    const h = Number(img.getAttribute("height") ?? 0)
    // Source coordinate box: prefer the icon's viewBox, else its width/height,
    // else the 24-unit stencil convention.
    const vb = (parsed.getAttribute("viewBox") ?? "").split(/[ ,]+/).map(Number)
    const vbW = vb.length === 4 && vb[2] > 0 ? vb[2] : Number(parsed.getAttribute("width")) || 24
    const vbH = vb.length === 4 && vb[3] > 0 ? vb[3] : Number(parsed.getAttribute("height")) || 24
    const vbX = vb.length === 4 ? vb[0] : 0
    const vbY = vb.length === 4 ? vb[1] : 0

    const g = document.createElementNS(SVG_NS, "g")
    g.setAttribute(
      "transform",
      `translate(${x} ${y}) scale(${w / vbW} ${h / vbH}) translate(${-vbX} ${-vbY})`,
    )
    Array.from(parsed.childNodes).forEach((child) => g.appendChild(document.importNode(child, true)))
    img.replaceWith(g)
  })
}

function el(tag: string, attrs: Record<string, string | number>): SVGElement {
  const node = document.createElementNS(SVG_NS, tag)
  for (const [k, v] of Object.entries(attrs)) node.setAttribute(k, String(v))
  return node
}

/** Oriented paper dimensions in millimetres. Landscape swaps the portrait pair.
 *  Matches jsPDF's own named-format dimensions for A0-A4, so the sheet SVG and
 *  the PDF page share one coordinate space. */
export function pageDimsMm(paper: PaperSize, orientation: PaperOrientation): { w: number; h: number } {
  const [pw, ph] = PAPER_MM[paper]
  return orientation === "landscape" ? { w: ph, h: pw } : { w: pw, h: ph }
}

/**
 * Build a paper-sized sheet SVG (mm coordinate space) with the drawing frame,
 * footer, and the scaled-to-fit diagram content -- unattached, so a caller can
 * either mount it for an on-screen preview or hand it to svg2pdf. Each build
 * namespaces its element ids (a random token), so a preview sheet and the sheet
 * actually converted to PDF can coexist in the document without colliding.
 */
export function buildSheetSvg(opts: DiagramPrintOptions): SVGSVGElement {
  const paper = opts.paper ?? "A3"
  const orientation = opts.orientation ?? "landscape"
  const token = Math.random().toString(36).slice(2, 8)
  const { w: pageW, h: pageH } = pageDimsMm(paper, orientation)

  // Inner drawing frame (inside the margins) and the drawable area above the
  // footer strip that the content is fitted into.
  const frameX = MARGIN.left
  const frameY = MARGIN.top
  const frameW = pageW - MARGIN.left - MARGIN.right
  const frameH = pageH - MARGIN.top - MARGIN.bottom
  const drawX = frameX
  const drawY = frameY
  const drawW = frameW
  const drawH = frameH - FOOTER_H

  // Fit the content bbox into the drawable area, preserving aspect and centring.
  const { x: cx, y: cy, w: cw, h: ch } = opts.fitBox
  const scale = Math.min(drawW / Math.max(cw, 1e-6), drawH / Math.max(ch, 1e-6))
  const tx = drawX + (drawW - cw * scale) / 2 - cx * scale
  const ty = drawY + (drawH - ch * scale) / 2 - cy * scale

  const sheet = el("svg", {
    xmlns: SVG_NS,
    viewBox: `0 0 ${pageW} ${pageH}`,
    width: pageW,
    height: pageH,
  }) as SVGSVGElement

  sheet.appendChild(el("rect", { x: 0, y: 0, width: pageW, height: pageH, fill: "#ffffff" }))
  // Thin trim border.
  sheet.appendChild(
    el("rect", {
      x: OUTER_INSET,
      y: OUTER_INSET,
      width: pageW - OUTER_INSET * 2,
      height: pageH - OUTER_INSET * 2,
      fill: "none",
      stroke: "#0f172a",
      "stroke-width": 0.3,
    }),
  )
  // Inner drawing frame.
  sheet.appendChild(
    el("rect", { x: frameX, y: frameY, width: frameW, height: frameH, fill: "none", stroke: "#0f172a", "stroke-width": 0.6 }),
  )

  // Clip the drawing to the drawable area so nothing bleeds over the frame.
  const clipId = `vep-${token}-draw-clip`
  const defs = el("defs", {})
  const clip = el("clipPath", { id: clipId })
  clip.appendChild(el("rect", { x: drawX, y: drawY, width: drawW, height: drawH }))
  defs.appendChild(clip)
  sheet.appendChild(defs)

  const content = el("g", { transform: `translate(${tx} ${ty}) scale(${scale})`, "clip-path": `url(#${clipId})` })
  const clone = prepareContentClone(opts.svg, token)
  // Move the cloned drawing (defs/markers + every drawn group) under the fit
  // transform. Children are live nodes; iterate a static copy as we re-parent.
  Array.from(clone.childNodes).forEach((child) => content.appendChild(child))
  sheet.appendChild(content)

  // Footer strip: separator line + name / scale / paper labels.
  const footerY = frameY + frameH - FOOTER_H
  sheet.appendChild(el("line", { x1: frameX, y1: footerY, x2: frameX + frameW, y2: footerY, stroke: "#0f172a", "stroke-width": 0.4 }))
  const textY = footerY + FOOTER_H * 0.66
  const name = el("text", { x: frameX + 3, y: textY, fill: "#0f172a", "font-family": "Helvetica, Arial, sans-serif", "font-size": 4 })
  name.textContent = opts.diagramName
  sheet.appendChild(name)
  const scaleLabel = el("text", { x: frameX + frameW / 2, y: textY, fill: "#475569", "font-family": "Helvetica, Arial, sans-serif", "font-size": 4, "text-anchor": "middle" })
  scaleLabel.textContent = "Scale: NTS"
  sheet.appendChild(scaleLabel)
  const meta = el("text", { x: frameX + frameW - 3, y: textY, fill: "#475569", "font-family": "Helvetica, Arial, sans-serif", "font-size": 4, "text-anchor": "end" })
  meta.textContent = `${new Date().toLocaleDateString()}  ·  ${paper} ${orientation}`
  sheet.appendChild(meta)

  return sheet
}

/**
 * Build the sheet, convert it to a vector PDF, and trigger a browser download.
 * Async because svg2pdf resolves a promise. A prebuilt sheet may be passed (e.g.
 * the one already mounted in the preview dialog) to avoid rebuilding.
 */
export async function printDiagramSheet(opts: DiagramPrintOptions, prebuilt?: SVGSVGElement): Promise<void> {
  const paper = opts.paper ?? "A3"
  const orientation = opts.orientation ?? "landscape"
  const { w: pageW, h: pageH } = pageDimsMm(paper, orientation)
  const doc = new jsPDF({ orientation, unit: "mm", format: paper.toLowerCase() })

  const sheet = prebuilt ?? buildSheetSvg(opts)
  // svg2pdf needs the node connected + laid out (getBBox/text metrics). If the
  // caller didn't already mount it, park it off-screen and clean up after.
  const wasConnected = sheet.isConnected
  if (!wasConnected) {
    sheet.setAttribute("style", "position:absolute; left:-99999px; top:0; pointer-events:none;")
    document.body.appendChild(sheet)
  }
  try {
    await svg2pdf(sheet, doc, { x: 0, y: 0, width: pageW, height: pageH })
    doc.save(`${sanitizeName(opts.diagramName)}.pdf`)
  } finally {
    if (!wasConnected) sheet.remove()
  }
}

function sanitizeName(name: string): string {
  const trimmed = name.trim().replace(/[^\w.-]+/g, "_").replace(/^_+|_+$/g, "")
  return trimmed.length > 0 ? trimmed : "diagram"
}
