import { useEffect, useRef, useState } from "react"
import JsBarcode from "jsbarcode"
import * as QRCode from "qrcode"

import { cn } from "@/lib/utils"

// A single physical asset label -- the printed artifact the asset-tagging module
// (api/internal/assettag) exists to produce. It carries the same code three ways
// so any reader can resolve it: a QR that encodes the web deep-link
// `<origin>/t/<code>` (any phone camera -> the app opens the feature, see S4), a
// Code128 barcode of the raw code (warehouse/scanner-gun), and the code in
// human-readable mono text with the asset's class for the eyeball / manual-keying
// fallback. Rendering is client-side (qrcode + jsbarcode, no server round-trip);
// the label is sized in millimetres so it prints true-to-size, and TagPrintSheet
// tiles one or many of these onto a page for the browser's print-to-PDF.

/** deepLinkBase is the public origin the QR should point at. Defaults to the
 * running app's own origin, which is correct for a label scanned to open this
 * same web app; a server-rendered batch (no browser origin) would pass an
 * explicit configured base instead. */
export function tagDeepLink(code: string, deepLinkBase?: string): string {
  const base = (deepLinkBase ?? window.location.origin).replace(/\/+$/, "")
  return `${base}/t/${encodeURIComponent(code)}`
}

export interface TagLabelProps {
  code: string
  /** Second line under the code -- the asset's class, and anything else short
   * that helps a human place the label (a circuit, a name). */
  subtitle?: string
  deepLinkBase?: string
  className?: string
}

/** One 54x30mm-ish label: QR (top-left), human text (top-right), Code128 (full
 * width, bottom). Fixed physical size so print output is scannable. */
export function TagLabel({ code, subtitle, deepLinkBase, className }: TagLabelProps) {
  const link = tagDeepLink(code, deepLinkBase)
  const [qrSvg, setQrSvg] = useState("")
  const barRef = useRef<SVGSVGElement>(null)

  useEffect(() => {
    let alive = true
    QRCode.toString(link, { type: "svg", margin: 0, errorCorrectionLevel: "M" })
      .then((svg) => {
        if (alive) setQrSvg(svg)
      })
      .catch(() => {
        if (alive) setQrSvg("")
      })
    return () => {
      alive = false
    }
  }, [link])

  useEffect(() => {
    const el = barRef.current
    if (!el) return
    try {
      JsBarcode(el, code, {
        format: "CODE128",
        displayValue: false,
        margin: 0,
        height: 40,
        width: 1.4,
        background: "transparent",
      })
    } catch {
      // A code JsBarcode can't encode (shouldn't happen for our clean codes)
      // leaves the QR + text; don't crash the label.
    }
  }, [code])

  return (
    <div
      className={cn(
        "flex h-[30mm] w-[54mm] flex-col justify-between gap-[1mm] overflow-hidden rounded-[1mm] border border-black bg-white p-[2mm] text-black",
        className,
      )}
    >
      <div className="flex items-start gap-[2mm]">
        {/* qrcode emits an <svg> with its own width/height; let it fill the box */}
        <div
          className="size-[16mm] shrink-0 [&>svg]:size-full"
          aria-hidden
          dangerouslySetInnerHTML={{ __html: qrSvg }}
        />
        <div className="min-w-0 flex-1">
          <p className="truncate font-mono text-[3mm] leading-tight font-bold">{code}</p>
          {subtitle && <p className="mt-[0.5mm] line-clamp-2 text-[2.3mm] leading-tight">{subtitle}</p>}
          <p className="mt-[0.5mm] text-[1.8mm] leading-tight text-neutral-500">Scan to open in VoltEdge</p>
        </div>
      </div>
      <svg ref={barRef} className="h-[7mm] w-full" aria-label={`Barcode ${code}`} />
    </div>
  )
}
