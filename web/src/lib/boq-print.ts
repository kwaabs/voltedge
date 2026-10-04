/**
 * Bill of Quantities -> print sheet (PDF). The on-screen schedule and the CSV
 * both already exist; this gives the third output the user asked for: a tidy,
 * self-contained PDF of the priced schedule they can file, email, or print.
 *
 * Client-side and dependency-light on purpose, in the spirit of lib/diagram-print.ts
 * (the diagram print engine). But where a diagram is a vector CANVAS -- so that
 * engine clones the live <svg> and runs it through svg2pdf -- a BoQ is a TABULAR
 * document, so there's no SVG to clone: we lay the grouped line-item table out
 * directly with jsPDF's text/line primitives (jspdf is already a dependency; no
 * svg2pdf, no jspdf-autotable, no new package). The data is the same BoqResult
 * the panel renders and the CSV serialises, so the sheet can't drift from either.
 *
 * Layout mirrors the on-screen schedule: priced lines grouped by class with a
 * per-class subtotal, a grand total, then the "Unpriced" buckets. A4 portrait,
 * millimetre units, with page-breaking that re-draws the column header on each
 * new page.
 */

import { jsPDF } from "jspdf"

import type { BoqResult } from "@/hooks/use-boq"

export interface BoqPrintOptions {
  /** A human-readable description of what was priced (e.g. "Whole network",
   *  "Trace result — 42 features"), shown under the title. Optional. */
  scopeLabel?: string
}

// A4 portrait, millimetres -- jsPDF's own "a4" format shares this space.
const PAGE_W = 210
const PAGE_H = 297
const M = { left: 15, right: 15, top: 16, bottom: 15 }
const CONTENT_RIGHT = PAGE_W - M.right // 195
const BOTTOM_LIMIT = PAGE_H - M.bottom // 282

// Column anchors (mm). Numeric columns are right-aligned to their edge; the Item
// and Unit columns are left-aligned to their x.
const COL = {
  item: M.left, // left edge of the item text
  itemWidth: 92, // wrap/clip width for the item label
  qtyRight: 120, // Qty right edge
  unitLeft: 124, // Unit left edge
  rateRight: 162, // Rate right edge
  totalRight: CONTENT_RIGHT, // Total right edge
}

const ROW_H = 5.6 // base line-item row height
const HEADER_H = 6.5

const COLOR = {
  ink: [15, 23, 42] as const, // slate-900
  muted: [71, 85, 105] as const, // slate-600
  rule: [148, 163, 184] as const, // slate-400
  bandFill: [241, 245, 249] as const, // slate-100 (class header band)
  headFill: [226, 232, 240] as const, // slate-200 (column header)
  amber: [180, 83, 9] as const, // amber-700 (unpriced heading)
}

const money = (n: number) =>
  n.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })

const qty = (n: number) => n.toLocaleString(undefined, { maximumFractionDigits: 3 })

/** The material/subtype label for a priced line, matching the on-screen table:
 *  "subtype · material", or "(all)" when neither is set. */
function lineLabel(l: BoqResult["lines"][number]): string {
  const parts = [l.subtype_name, l.material_name || l.material_code].filter(Boolean)
  return parts.length > 0 ? parts.join(" · ") : "(all)"
}

/** Group priced lines by class with a running subtotal (same shape the panel's
 *  BoqSchedule builds), preserving first-seen order. */
function groupByClass(result: BoqResult) {
  const byClass = new Map<string, { name: string; lines: BoqResult["lines"]; subtotal: number }>()
  for (const l of result.lines) {
    const g = byClass.get(l.feature_class) ?? { name: l.feature_class_name, lines: [], subtotal: 0 }
    g.lines.push(l)
    g.subtotal += l.line_total
    byClass.set(l.feature_class, g)
  }
  return [...byClass.values()]
}

/** The suggested download filename for a priced schedule. */
export function boqPdfFilename(result: BoqResult): string {
  return `bill-of-quantities-${result.currency}.pdf`
}

/**
 * Build the BoQ PDF and return the jsPDF document. The caller decides how to
 * present it -- the panel previews it in an in-app dialog (embedding
 * doc.output("bloburl") in an <iframe>, with its own Print and Download controls)
 * rather than saving straight to disk. Synchronous -- jsPDF's text layout needs
 * no async step (unlike the diagram engine's svg2pdf).
 */
export function buildBoqPdf(result: BoqResult, opts: BoqPrintOptions = {}): jsPDF {
  const doc = new jsPDF({ orientation: "portrait", unit: "mm", format: "a4" })

  // --- Cursor + page helpers ------------------------------------------------
  let y = M.top

  const setInk = (c: readonly [number, number, number]) => doc.setTextColor(c[0], c[1], c[2])
  const setDraw = (c: readonly [number, number, number]) => doc.setDrawColor(c[0], c[1], c[2])
  const setFill = (c: readonly [number, number, number]) => doc.setFillColor(c[0], c[1], c[2])

  // Right-aligned cell text.
  const right = (text: string, x: number, yy: number) => doc.text(text, x, yy, { align: "right" })

  // The column header row (re-drawn atop every page of the table).
  const drawColumnHeader = () => {
    setFill(COLOR.headFill)
    doc.rect(M.left, y, CONTENT_RIGHT - M.left, HEADER_H, "F")
    doc.setFont("helvetica", "bold")
    doc.setFontSize(8)
    setInk(COLOR.muted)
    const ty = y + HEADER_H - 2
    doc.text("Item", COL.item + 1, ty)
    right("Qty", COL.qtyRight, ty)
    doc.text("Unit", COL.unitLeft, ty)
    right("Rate", COL.rateRight, ty)
    right("Total", COL.totalRight, ty)
    y += HEADER_H
  }

  // Ensure `need` mm of vertical space remains; otherwise start a new page. When
  // `repeatHeader` is set, the column header is re-drawn at the top of the page.
  const ensureSpace = (need: number, repeatHeader: boolean) => {
    if (y + need <= BOTTOM_LIMIT) return
    doc.addPage()
    y = M.top
    if (repeatHeader) drawColumnHeader()
  }

  // --- Title block ----------------------------------------------------------
  doc.setFont("helvetica", "bold")
  doc.setFontSize(16)
  setInk(COLOR.ink)
  doc.text("Bill of Quantities", M.left, y + 4)
  y += 8

  doc.setFont("helvetica", "normal")
  doc.setFontSize(9)
  setInk(COLOR.muted)
  if (opts.scopeLabel) {
    doc.text(opts.scopeLabel, M.left, y)
    y += 4.5
  }
  const subtitle = [
    `Totals in ${result.currency}${result.currency_name ? ` — ${result.currency_name}` : ""}`,
    result.currency !== result.base_currency ? `base ${result.base_currency}` : null,
    new Date().toLocaleString(),
  ]
    .filter(Boolean)
    .join("  ·  ")
  doc.text(subtitle, M.left, y)
  y += 3
  setDraw(COLOR.rule)
  doc.setLineWidth(0.4)
  doc.line(M.left, y, CONTENT_RIGHT, y)
  y += 5

  // --- Priced schedule ------------------------------------------------------
  if (result.lines.length === 0) {
    doc.setFont("helvetica", "normal")
    doc.setFontSize(9)
    setInk(COLOR.muted)
    doc.text("No priced quantities in this scope.", M.left, y + 2)
    y += 8
  } else {
    drawColumnHeader()

    for (const g of groupByClass(result)) {
      // Class band (keep the band with at least its first line).
      ensureSpace(HEADER_H + ROW_H, true)
      setFill(COLOR.bandFill)
      doc.rect(M.left, y, CONTENT_RIGHT - M.left, ROW_H, "F")
      doc.setFont("helvetica", "bold")
      doc.setFontSize(8.5)
      setInk(COLOR.ink)
      doc.text(g.name, COL.item + 1, y + ROW_H - 1.7)
      y += ROW_H

      // Line items.
      doc.setFont("helvetica", "normal")
      doc.setFontSize(8.5)
      for (const l of g.lines) {
        const label = doc.splitTextToSize(lineLabel(l), COL.itemWidth) as string[]
        const rowH = Math.max(ROW_H, label.length * 4 + 1.6)
        ensureSpace(rowH, true)
        const baseY = y + 4
        setInk(COLOR.ink)
        label.forEach((ln, i) => doc.text(ln, COL.item + 2, baseY + i * 4))
        setInk(COLOR.muted)
        right(qty(l.quantity), COL.qtyRight, baseY)
        doc.text(l.unit, COL.unitLeft, baseY)
        right(money(l.unit_rate), COL.rateRight, baseY)
        setInk(COLOR.ink)
        right(money(l.line_total), COL.totalRight, baseY)
        y += rowH
        setDraw(COLOR.rule)
        doc.setLineWidth(0.1)
        doc.line(M.left, y, CONTENT_RIGHT, y)
      }

      // Per-class subtotal.
      ensureSpace(ROW_H, true)
      doc.setFont("helvetica", "bold")
      doc.setFontSize(8.5)
      setInk(COLOR.muted)
      right("Subtotal", COL.rateRight, y + 4)
      setInk(COLOR.ink)
      right(money(g.subtotal), COL.totalRight, y + 4)
      y += ROW_H + 1
    }

    // Grand total.
    ensureSpace(HEADER_H, false)
    setDraw(COLOR.ink)
    doc.setLineWidth(0.5)
    doc.line(M.left, y, CONTENT_RIGHT, y)
    y += 1.5
    doc.setFont("helvetica", "bold")
    doc.setFontSize(10)
    setInk(COLOR.ink)
    doc.text(`Grand total (${result.currency})`, COL.item + 1, y + 4)
    right(money(result.total), COL.totalRight, y + 4)
    y += HEADER_H + 2
  }

  // Priced-count note.
  doc.setFont("helvetica", "normal")
  doc.setFontSize(8)
  setInk(COLOR.muted)
  ensureSpace(5, false)
  doc.text(
    `${result.priced_count} priced line${result.priced_count === 1 ? "" : "s"}`,
    M.left,
    y + 2,
  )
  y += 7

  // --- Unpriced -------------------------------------------------------------
  if (result.unpriced.length > 0) {
    ensureSpace(12, false)
    doc.setFont("helvetica", "bold")
    doc.setFontSize(10)
    setInk(COLOR.amber)
    doc.text(`Unpriced (${result.unpriced.length})`, M.left, y + 2)
    y += 5
    doc.setFont("helvetica", "normal")
    doc.setFontSize(8)
    setInk(COLOR.muted)
    doc.text("Counted but no catalog rate applied — set a rate to include these.", M.left, y)
    y += 5

    for (const u of result.unpriced) {
      const label = [u.feature_class_name, u.subtype_name, u.material_code]
        .filter(Boolean)
        .join(" · ")
      const reason = doc.splitTextToSize(u.reason, COL.itemWidth) as string[]
      const rowH = Math.max(ROW_H, 4 + reason.length * 3.4)
      ensureSpace(rowH, false)
      doc.setFont("helvetica", "normal")
      doc.setFontSize(8.5)
      setInk(COLOR.ink)
      doc.text(label, COL.item + 1, y + 4)
      setInk(COLOR.muted)
      right(`${qty(u.quantity)} ${u.unit}`, COL.totalRight, y + 4)
      doc.setFontSize(7.5)
      reason.forEach((ln, i) => doc.text(ln, COL.item + 3, y + 7.4 + i * 3.4))
      y += rowH
    }
  }

  // --- Footer (page x of n) -------------------------------------------------
  const pages = doc.getNumberOfPages()
  for (let p = 1; p <= pages; p++) {
    doc.setPage(p)
    doc.setFont("helvetica", "normal")
    doc.setFontSize(7.5)
    setInk(COLOR.muted)
    doc.text("VoltEdge — Bill of Quantities", M.left, PAGE_H - 7)
    doc.text(`Page ${p} of ${pages}`, CONTENT_RIGHT, PAGE_H - 7, { align: "right" })
  }

  return doc
}
