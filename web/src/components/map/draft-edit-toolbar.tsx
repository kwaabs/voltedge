import { useEffect } from "react"
import { Move, Spline } from "lucide-react"

import { SnapToggleButton } from "@/components/map/snap-toggle-button"
import { Button } from "@/components/ui/button"
import { useDraggableToolbar } from "@/hooks/use-draggable-toolbar"
import { cn } from "@/lib/utils"
import { useDrawStore } from "@/stores/draw-store"

/**
 * The placed-draft toolbar: while an unsaved draft with geometry sits on the map
 * -- a just-drawn feature OR a Duplicate/copy -- this lets the user touch up its
 * geometry before Save. Move drags the whole shape; Reshape edits its vertices
 * (line/polygon only -- a point has none); Snap bends either onto nearby
 * features. voltedge-map.tsx owns the Translate/Modify + Snap interactions off
 * draw-store's draftGeometryTool and the snap flag; this only arms them. Floating
 * and draggable, the same treatment as the geometry-/territory-edit toolbars.
 */
export function DraftEditToolbar() {
  const draft = useDrawStore((s) => s.draft)
  const draftToken = useDrawStore((s) => s.draftToken)
  const tool = useDrawStore((s) => s.draftGeometryTool)
  const setTool = useDrawStore((s) => s.setDraftGeometryTool)
  const drag = useDraggableToolbar()

  // Reset to Move for each new draft, so a Reshape left armed on the last one
  // doesn't carry into a fresh copy.
  useEffect(() => {
    useDrawStore.getState().setDraftGeometryTool("move")
  }, [draftToken])

  const geomType = draft?.geometry?.type
  if (!draft?.geometry || !geomType) return null
  const canReshape = geomType !== "Point" && geomType !== "MultiPoint"

  return (
    <div
      ref={drag.ref}
      style={drag.style}
      className={cn(
        "bg-popover text-popover-foreground absolute z-40 flex items-center gap-1 rounded-md border p-1 shadow-lg",
        // Until dragged, stay centered near the top of the map; a drag pins an
        // explicit left/top and drops this anchor (same as the edit toolbars).
        !drag.positioned && "top-8 left-1/2 -translate-x-1/2",
      )}
    >
      {drag.grip}
      <Button
        variant="ghost"
        size="sm"
        className={cn("gap-1.5", tool === "move" && "bg-accent text-accent-foreground")}
        onClick={() => setTool("move")}
        title="Move the whole shape"
      >
        <Move className="size-4" />
        Move
      </Button>
      {canReshape && (
        <Button
          variant="ghost"
          size="sm"
          className={cn("gap-1.5", tool === "reshape" && "bg-accent text-accent-foreground")}
          onClick={() => setTool("reshape")}
          title="Reshape — drag, add or delete vertices"
        >
          <Spline className="size-4" />
          Reshape
        </Button>
      )}
      <SnapToggleButton />
    </div>
  )
}
