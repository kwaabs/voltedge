import { X } from "lucide-react"

import { OrthoToggleButton } from "@/components/map/ortho-toggle-button"
import { SnapToggleButton } from "@/components/map/snap-toggle-button"
import { Button } from "@/components/ui/button"
import { useDraggableToolbar } from "@/hooks/use-draggable-toolbar"
import { cn } from "@/lib/utils"
import { type DrawGeometryType, useDrawStore } from "@/stores/draw-store"

const BANNER_TEXT: Record<DrawGeometryType, string> = {
  point: "Click the map to place the new feature",
  line: "Click to add points, double-click to finish the line",
  polygon: "Click to add points, double-click to finish the polygon",
}

/**
 * The active-draw toolbar, shown while a new-feature (or "Place on map")
 * draw is in flight (draw-store's drawingFeatureClassCode). A floating,
 * DRAGGABLE toolbar over the map -- the same treatment the geometry-/territory-
 * edit toolbars get (useDraggableToolbar) -- rather than sitting inline in the
 * top control cluster where it crowded the buttons. Carries the "how to draw"
 * hint, the Ortho/Snap toggles (ortho only for line/polygon), and a cancel.
 * NewFeatureMenu renders nothing while drawing; this takes over.
 */
export function DrawBanner() {
  const drawing = useDrawStore((s) => s.drawingFeatureClassCode)
  const geometryType = useDrawStore((s) => s.drawingGeometryType)
  const cancelDrawing = useDrawStore((s) => s.cancelDrawing)
  const drag = useDraggableToolbar()

  if (!drawing) return null

  return (
    <div
      ref={drag.ref}
      style={drag.style}
      className={cn(
        "bg-popover text-popover-foreground absolute z-40 flex items-center gap-2 rounded-md border py-1.5 pr-1.5 pl-2 text-sm shadow-lg",
        // Until dragged, stay centered near the top of the map (clearing the top
        // control cluster); a drag pins an explicit left/top and drops this anchor.
        !drag.positioned && "top-8 left-1/2 -translate-x-1/2",
      )}
    >
      {drag.grip}
      <span className="bg-primary/70 size-2 shrink-0 animate-pulse rounded-full" />
      {geometryType ? BANNER_TEXT[geometryType] : BANNER_TEXT.point}
      {(geometryType === "line" || geometryType === "polygon") && <OrthoToggleButton />}
      <SnapToggleButton />
      <Button variant="ghost" size="icon" className="size-6 shrink-0" onClick={cancelDrawing}>
        <X className="size-3.5" />
        <span className="sr-only">Cancel</span>
      </Button>
    </div>
  )
}
