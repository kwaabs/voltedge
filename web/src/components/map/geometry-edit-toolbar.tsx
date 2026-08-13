import { Move, Waypoints } from "lucide-react"

import { SnapToggleButton } from "@/components/map/snap-toggle-button"
import { Button } from "@/components/ui/button"
import { useFeatureClassSchema } from "@/hooks/use-feature-class-schema"
import { cn } from "@/lib/utils"
import { useEditStore } from "@/stores/edit-store"

/**
 * Floating, centered above the map (mirrors new-feature-menu.tsx's own
 * top row, just centered rather than right-aligned so it doesn't
 * compete with the Layers/New feature buttons) -- shown for as long as
 * edit-store.ts's `editing` is set and the feature class actually has
 * geometry. Deliberately reads `editing` itself and returns null rather
 * than being conditionally rendered by a parent, same self-contained
 * pattern as connect-confirm-panel.tsx.
 *
 * Which buttons render is geometry-type-specific, per the user's own
 * framing ("the drawing tool bar for line features should have certain
 * controls that point features should not have"): a point has nothing
 * to select between "move the feature" and "move a vertex" -- they're
 * the same action -- so only Move ever shows for one. Move and Edit
 * vertices are mutually exclusive map interactions (Translate vs.
 * Modify, see voltedge-map.tsx), clicking the active one again turns it
 * off rather than picking a different one being the only way out.
 */
export function GeometryEditToolbar() {
  const editing = useEditStore((s) => s.editing)
  const geometryTool = useEditStore((s) => s.geometryTool)
  const { data: schema } = useFeatureClassSchema(editing?.featureClassCode)

  if (!editing || !schema || schema.geometry_type === "none") return null

  const showVertexTool = schema.geometry_type === "line" || schema.geometry_type === "polygon"

  return (
    <div className="bg-popover text-popover-foreground fixed top-3 left-1/2 z-40 flex -translate-x-1/2 items-center gap-1 rounded-md border p-1 shadow-lg">
      <Button
        variant="ghost"
        size="sm"
        className={cn(geometryTool === "move" && "bg-accent text-accent-foreground")}
        onClick={() => useEditStore.getState().setGeometryTool(geometryTool === "move" ? null : "move")}
      >
        <Move className="size-4" />
        Move
      </Button>
      {showVertexTool && (
        <Button
          variant="ghost"
          size="sm"
          className={cn(geometryTool === "vertices" && "bg-accent text-accent-foreground")}
          onClick={() => useEditStore.getState().setGeometryTool(geometryTool === "vertices" ? null : "vertices")}
        >
          <Waypoints className="size-4" />
          Edit vertices
        </Button>
      )}
      <SnapToggleButton />
      {geometryTool === "vertices" && (
        <p className="text-muted-foreground border-l pl-2 text-xs whitespace-nowrap">
          Drag a vertex to move it, a segment to add one, Alt+click to delete one
        </p>
      )}
    </div>
  )
}
