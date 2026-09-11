import { useEffect, useMemo, useRef, useState } from "react"
import { CircuitBoard } from "lucide-react"

import { Button } from "@/components/ui/button"
import { useDiagramStencils, type DiagramStencil } from "@/hooks/use-diagram-stencils"
import { useSymbologyIcons } from "@/hooks/use-symbology-icons"
import { iconDataUri } from "@/lib/symbology-icons"
import { cn } from "@/lib/utils"

// A neutral mid-grey the glyph thumbnails tint to (via {{COLOR}}) -- reads on
// both the light and dark popover backgrounds. The stamped symbol on the
// canvas gets its own colour from its element style, independent of this.
const THUMB_COLOR = "#64748b"

/**
 * The Phase 24 Slice 2 symbol stencil library, as a header palette beside
 * AddAnnotationMenu. Where that menu adds generic furniture (box/text/line),
 * this stamps a curated ANSI/IEEE schematic glyph (ground, junction node,
 * CT/VT, delta/wye winding, flow arrow, ...) as a non-feature `symbol`
 * element. The catalog comes from GET /symbology/stencils; each glyph's SVG
 * is the same metadata.symbology_icons asset feature symbology already
 * resolves, rendered here as a tinted thumbnail via iconDataUri. Picking one
 * calls onPick(icon_ref) and closes -- the canvas drops it at the view centre,
 * ready to drag/rotate into place.
 */
export function StencilPalette({ onPick }: { onPick: (iconRef: string) => void }) {
  const [open, setOpen] = useState(false)
  const containerRef = useRef<HTMLDivElement>(null)
  const stencils = useDiagramStencils()
  const symbologyIcons = useSymbologyIcons()

  useEffect(() => {
    if (!open) return
    function handlePointerDown(e: PointerEvent) {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) setOpen(false)
    }
    window.addEventListener("pointerdown", handlePointerDown)
    return () => window.removeEventListener("pointerdown", handlePointerDown)
  }, [open])

  // Group into ordered category sections. The server already sorts by
  // category then sort_order, so a simple stable grouping preserves both.
  const groups = useMemo(() => {
    const byCategory = new Map<string, DiagramStencil[]>()
    for (const s of stencils) {
      const list = byCategory.get(s.category)
      if (list) list.push(s)
      else byCategory.set(s.category, [s])
    }
    return [...byCategory.entries()]
  }, [stencils])

  function pick(iconRef: string) {
    setOpen(false)
    onPick(iconRef)
  }

  return (
    <div ref={containerRef} className="relative shrink-0">
      <Button
        variant="ghost"
        size="icon"
        className={cn("size-7", open && "bg-accent text-accent-foreground")}
        title="Add schematic symbol"
        onClick={() => setOpen((o) => !o)}
      >
        <CircuitBoard className="size-4" />
        <span className="sr-only">Add schematic symbol</span>
      </Button>
      {open && (
        <div className="bg-popover text-popover-foreground absolute top-full right-0 z-10 mt-1 max-h-96 w-64 overflow-y-auto rounded-md border py-1 shadow-lg">
          {stencils.length === 0 ? (
            <p className="text-muted-foreground p-3 text-sm">No symbols available yet.</p>
          ) : (
            groups.map(([category, items]) => (
              <div key={category} className="px-1 py-0.5">
                <p className="text-muted-foreground px-2 py-1 text-xs font-medium">{category}</p>
                <div className="grid grid-cols-4 gap-1">
                  {items.map((s) => (
                    <button
                      key={s.icon_ref}
                      type="button"
                      title={s.label}
                      onClick={() => pick(s.icon_ref)}
                      className="hover:bg-accent hover:text-accent-foreground flex aspect-square items-center justify-center rounded-md p-1.5 transition-colors"
                    >
                      <img
                        src={iconDataUri(s.icon_ref, THUMB_COLOR, symbologyIcons)}
                        alt={s.label}
                        className="size-full"
                        draggable={false}
                      />
                    </button>
                  ))}
                </div>
              </div>
            ))
          )}
        </div>
      )}
    </div>
  )
}
