import { Scan } from "lucide-react"

import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"
import { useBoxZoomStore } from "@/stores/box-zoom-store"

/**
 * Arms the box-zoom tool: click to arm, then drag a rectangle on the map to
 * zoom the view to that area (voltedge-map.tsx owns the OL Draw interaction and
 * the fit, keyed off box-zoom-store's `active`; it disarms after one box).
 * Same active-state styling convention as snap-toggle-button.tsx (bg-accent
 * when on). Escape cancels while armed.
 */
export function BoxZoomButton() {
  const active = useBoxZoomStore((s) => s.active)

  return (
    <Button
      variant="secondary"
      size="sm"
      className={cn("shadow-sm", active && "bg-accent text-accent-foreground")}
      onClick={() => useBoxZoomStore.getState().toggle()}
      title={active ? "Draw a box to zoom (Esc to cancel)" : "Box zoom"}
    >
      <Scan className={cn("size-3.5", !active && "text-sky-600")} /> Box zoom
    </Button>
  )
}
