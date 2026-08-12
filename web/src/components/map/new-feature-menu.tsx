import { Plus, X } from "lucide-react"

import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { useFeatureClasses } from "@/hooks/use-feature-classes"
import { useDrawStore } from "@/stores/draw-store"

/**
 * Entry point for placing a new feature -- one item per point feature
 * class (line/polygon drawing isn't built yet, see
 * docs/phase-3-map-loop.md's "What's still open"). Picking one starts a
 * draw session; src/components/map/voltedge-map.tsx owns the actual OL
 * Draw interaction, keyed off draw-store's drawingFeatureClassCode.
 */
export function NewFeatureMenu() {
  const { data: featureClasses } = useFeatureClasses()
  const startDrawing = useDrawStore((s) => s.startDrawing)
  const cancelDrawing = useDrawStore((s) => s.cancelDrawing)
  const drawing = useDrawStore((s) => s.drawingFeatureClassCode)
  const pointClasses = (featureClasses?.feature_classes ?? []).filter((fc) => fc.geometry_type === "point")

  if (drawing) {
    return (
      <div className="bg-background/90 flex items-center gap-2 rounded-md border py-1.5 pr-1.5 pl-3 text-sm shadow-sm">
        <span className="bg-primary/70 size-2 shrink-0 animate-pulse rounded-full" />
        Click the map to place the new feature
        <Button variant="ghost" size="icon" className="size-6 shrink-0" onClick={cancelDrawing}>
          <X className="size-3.5" />
          <span className="sr-only">Cancel</span>
        </Button>
      </div>
    )
  }

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="outline" size="sm" className="bg-background/90 gap-1.5 shadow-sm">
          <Plus className="size-4" />
          New feature
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-52">
        <DropdownMenuLabel>Feature class</DropdownMenuLabel>
        <DropdownMenuSeparator />
        {pointClasses.length === 0 ? (
          <p className="text-muted-foreground px-2 py-1.5 text-sm">No point feature classes compiled yet</p>
        ) : (
          pointClasses.map((fc) => (
            <DropdownMenuItem key={fc.code} onSelect={() => startDrawing(fc.code)}>
              {fc.name}
            </DropdownMenuItem>
          ))
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
