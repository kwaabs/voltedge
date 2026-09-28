import { Loader2, Printer } from "lucide-react"
import { useEffect, useRef, useState } from "react"

import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import {
  buildSheetSvg,
  printDiagramSheet,
  type PaperOrientation,
  type PaperSize,
} from "@/lib/diagram-print"
import { cn } from "@/lib/utils"

const PAPER_SIZES: PaperSize[] = ["A4", "A3", "A2", "A1", "A0"]
const ORIENTATIONS: { value: PaperOrientation; label: string }[] = [
  { value: "landscape", label: "Landscape" },
  { value: "portrait", label: "Portrait" },
]

export interface DiagramPrintDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  diagramName: string
  /** The live canvas <svg> (svgRef.current). Cloned per preview build. */
  svg: SVGSVGElement | null
  fitBox: { x: number; y: number; w: number; h: number }
}

/**
 * Print-to-PDF preview for a diagram. Shows the SAME vector sheet the download
 * emits -- `buildSheetSvg` produces a real DOM SVG, so the preview mounts that
 * exact node and only runs svg2pdf/download on confirm. Paper size + orientation
 * rebuild the preview live; Download hands a fresh build to `printDiagramSheet`.
 */
export function DiagramPrintDialog({
  open,
  onOpenChange,
  diagramName,
  svg,
  fitBox,
}: DiagramPrintDialogProps) {
  const [paper, setPaper] = useState<PaperSize>("A3")
  const [orientation, setOrientation] = useState<PaperOrientation>("landscape")
  const [busy, setBusy] = useState(false)
  const previewRef = useRef<HTMLDivElement>(null)

  // Rebuild the preview sheet whenever the dialog opens or a knob changes.
  // buildSheetSvg clones the live canvas, so this always reflects what's on
  // screen at the moment the dialog is (re)configured.
  useEffect(() => {
    const host = previewRef.current
    if (!open || !svg || !host) return
    let sheet: SVGSVGElement | null = null
    try {
      sheet = buildSheetSvg({ diagramName, svg, fitBox, paper, orientation })
      // The sheet is authored in mm (unitless width/height); let it scale to
      // the preview column while its viewBox holds the aspect ratio.
      sheet.setAttribute("style", "display:block; width:100%; height:auto;")
      host.replaceChildren(sheet)
    } catch (err) {
      console.error("diagram print preview failed", err)
      host.replaceChildren()
    }
    return () => {
      if (sheet && sheet.parentNode === host) host.removeChild(sheet)
    }
  }, [open, svg, fitBox, diagramName, paper, orientation])

  const handleDownload = () => {
    if (!svg || busy) return
    setBusy(true)
    printDiagramSheet({ diagramName, svg, fitBox, paper, orientation })
      .then(() => onOpenChange(false))
      .catch((err) => console.error("diagram print failed", err))
      .finally(() => setBusy(false))
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-3xl">
        <DialogHeader>
          <DialogTitle>Print drawing sheet</DialogTitle>
          <DialogDescription>
            Preview the framed vector sheet, then download it as a PDF. The plot
            matches what is on the canvas now.
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-wrap items-center gap-4">
          <div className="flex items-center gap-1.5">
            <span className="text-muted-foreground text-xs font-medium">Paper</span>
            <div className="flex overflow-hidden rounded-md border">
              {PAPER_SIZES.map((size) => (
                <button
                  key={size}
                  type="button"
                  onClick={() => setPaper(size)}
                  className={cn(
                    "px-2.5 py-1 text-xs transition-colors",
                    paper === size
                      ? "bg-primary text-primary-foreground"
                      : "hover:bg-accent",
                  )}
                >
                  {size}
                </button>
              ))}
            </div>
          </div>
          <div className="flex items-center gap-1.5">
            <span className="text-muted-foreground text-xs font-medium">Orientation</span>
            <div className="flex overflow-hidden rounded-md border">
              {ORIENTATIONS.map((o) => (
                <button
                  key={o.value}
                  type="button"
                  onClick={() => setOrientation(o.value)}
                  className={cn(
                    "px-2.5 py-1 text-xs transition-colors",
                    orientation === o.value
                      ? "bg-primary text-primary-foreground"
                      : "hover:bg-accent",
                  )}
                >
                  {o.label}
                </button>
              ))}
            </div>
          </div>
        </div>

        <div className="bg-muted/40 max-h-[60vh] overflow-auto rounded-md border p-4">
          <div ref={previewRef} className="mx-auto max-w-full" />
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={handleDownload} disabled={busy || !svg}>
            {busy ? (
              <Loader2 className="size-4 animate-spin" />
            ) : (
              <Printer className="size-4" />
            )}
            Download PDF
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
