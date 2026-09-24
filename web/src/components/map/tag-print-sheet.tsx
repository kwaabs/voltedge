import { useEffect } from "react"
import { Printer, X } from "lucide-react"

import { Button } from "@/components/ui/button"
import { TagLabel } from "@/components/map/tag-label"

// A full-screen print surface: it tiles one or many TagLabels onto a page and
// hands them to the browser's own print-to-PDF (there is no server-side PDF
// worker yet -- see the S3 note in the asset-tagging memory). The toolbar is
// print:hidden and the labels sit in a `.print-sheet` container that the global
// @media print rules (index.css) reveal while hiding the rest of the app, so a
// print produces a clean sheet of labels with no app chrome.

export interface PrintTag {
  code: string
  subtitle?: string
}

export function TagPrintSheet({
  tags,
  deepLinkBase,
  note,
  onClose,
  onPrinted,
}: {
  tags: PrintTag[]
  deepLinkBase?: string
  /** Optional line shown in the (print-hidden) toolbar -- e.g. a bulk print's
   * "N features had no tag and were skipped" shortfall. */
  note?: string
  onClose: () => void
  /** Fired once when the user sends the sheet to print -- the caller bumps
   * print_count. Best-effort; the print dialog outcome isn't observable. */
  onPrinted?: () => void
}) {
  // Escape closes, matching the map's other transient overlays.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose()
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [onClose])

  const print = () => {
    onPrinted?.()
    window.print()
  }

  return (
    <div className="bg-background/95 fixed inset-0 z-[60] flex flex-col overflow-auto">
      <div className="bg-background sticky top-0 flex items-center justify-between gap-2 border-b px-4 py-2 print:hidden">
        <div className="flex min-w-0 flex-col">
          <span className="text-sm font-medium">
            Print {tags.length === 1 ? "label" : `${tags.length} labels`}
          </span>
          {note && <span className="text-muted-foreground truncate text-xs">{note}</span>}
        </div>
        <div className="flex items-center gap-2">
          <Button size="sm" onClick={print}>
            <Printer className="size-4" />
            Print
          </Button>
          <Button variant="ghost" size="sm" onClick={onClose}>
            <X className="size-4" />
            Close
          </Button>
        </div>
      </div>
      <div className="flex-1 p-6 print:p-0">
        <div className="print-sheet flex flex-wrap content-start gap-[4mm]">
          {tags.map((t) => (
            <TagLabel key={t.code} code={t.code} subtitle={t.subtitle} deepLinkBase={deepLinkBase} />
          ))}
        </div>
      </div>
    </div>
  )
}
